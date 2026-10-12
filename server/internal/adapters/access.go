package adapters

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"sync"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"

	"smartfactory/internal/mqttauth"
)

// Who a broker client is.
const (
	// local: a client on this machine, when the broker has no shared users (as it always had)
	local = iota
	// shared: holds a shared user's password, may use any topic
	shared
	// device: a registered device, its own assets' topics only
	device
)

type ident struct {
	kind int
	// device: its name and the credential it logged in with
	name, cred  string
	established bool
}

type warning struct {
	at     time.Time
	missed int
}

// access decides who may connect and what each client may publish and hear. It replaces
// mochi's auth hooks, which let any authenticated client use any topic: a device is held to
// its own assets (mqttauth.Scope), the server's own client and shared users are not.
type access struct {
	mqtt.HookBase
	srv    *mqtt.Server
	users  map[string]string
	reg    *mqttauth.Registry
	prefix string
	log    *slog.Logger

	mu     sync.Mutex
	who    map[*mqtt.Client]*ident
	warned map[string]*warning
}

func (a *access) ID() string { return "smartfactory-access" }

func (a *access) Provides(b byte) bool {
	return b == mqtt.OnConnectAuthenticate || b == mqtt.OnACLCheck || b == mqtt.OnSessionEstablished || b == mqtt.OnDisconnect
}

func (a *access) OnConnectAuthenticate(cl *mqtt.Client, pk packets.Packet) bool {
	if cl.Net.Inline {
		return true
	}
	user, pass := string(pk.Connect.Username), pk.Connect.Password
	// a client certificate the TLS listener verified against the CA: the device it names, no password
	if c := peerCert(cl); c != nil {
		d, cred, ok := mqttauth.Device{}, "", false
		if a.reg != nil {
			d, cred, ok = a.reg.LoginCert(c)
		}
		if !ok {
			a.refuseConn(c.Subject.CommonName, cl, "证书已撤销、已换新或已过期")
			return false
		}
		return a.device(cl, d, cred)
	}
	if p, ok := a.users[user]; ok && user != "" {
		if subtle.ConstantTimeCompare([]byte(p), pass) == 1 {
			a.set(cl, &ident{kind: shared})
			return true
		}
		a.warn("user:"+user, "mqtt: wrong password for a shared user", "user", user, "remote", cl.Net.Remote)
		return false
	}
	if a.reg != nil && a.reg.Known(user) {
		d, cred, ok := a.reg.Login(user, string(pass))
		if !ok {
			a.refuseConn(user, cl, "令牌不对")
			return false
		}
		return a.device(cl, d, cred)
	}
	// a client on this machine, unless it takes a device's name as its client id: it would
	// take over that device's session
	if len(a.users) == 0 && loopbackRemote(cl.Net.Remote) && (a.reg == nil || !a.reg.Known(cl.ID)) {
		a.set(cl, &ident{kind: local})
		return true
	}
	host, _, _ := net.SplitHostPort(cl.Net.Remote)
	// the user name is not logged: it may be a token typed in the wrong field
	a.warn("remote:"+host, "mqtt: refused a client that is neither a registered device nor a shared user", "remote", cl.Net.Remote, "client", cl.ID, "user given", user != "")
	return false
}

// device lets a device in under its own name: the client id is the device's name, so one device
// cannot take over another's session.
func (a *access) device(cl *mqtt.Client, d mqttauth.Device, cred string) bool {
	if cl.ID != d.Name {
		a.refuseConn(d.Name, cl, "client id 要和设备名一样，这次是 "+clip(cl.ID, 64))
		return false
	}
	a.set(cl, &ident{kind: device, name: d.Name, cred: cred})
	return true
}

func (a *access) refuseConn(name string, cl *mqtt.Client, why string) {
	if a.reg != nil {
		a.reg.Refuse(name, "连接："+why)
	}
	a.warn("device:"+name, "mqtt: refused a device's connection", "device", name, "why", why, "remote", cl.Net.Remote)
}

func (a *access) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {
	if cl.Net.Inline {
		return true
	}
	a.mu.Lock()
	id := a.who[cl]
	a.mu.Unlock()
	if id == nil {
		return false
	}
	if id.kind != device {
		return true
	}
	verb := "订阅 "
	if write {
		verb = "发 "
	}
	sc, ok := a.reg.Current(id.name, id.cred)
	if !ok {
		a.reg.Refuse(id.name, verb+topic+"：凭据已失效")
		return false
	}
	if write && sc.MayPublish(a.prefix, topic) {
		a.reg.Seen(id.name)
		return true
	}
	if !write && sc.MayRead(a.prefix, topic) {
		return true
	}
	a.reg.Refuse(id.name, verb+clip(topic, 200))
	a.warn("device:"+id.name, "mqtt: refused a device a topic that is not its own", "device", id.name, "topic", clip(topic, 200), "publish", write)
	return false
}

func (a *access) OnSessionEstablished(cl *mqtt.Client, _ packets.Packet) {
	a.mu.Lock()
	id := a.who[cl]
	if id != nil && id.kind == device {
		id.established = true
	}
	a.mu.Unlock()
	if id != nil && id.kind == device {
		a.reg.Connected(id.name, 1)
		a.reg.Seen(id.name)
	}
}

func (a *access) OnDisconnect(cl *mqtt.Client, _ error, _ bool) {
	a.mu.Lock()
	id := a.who[cl]
	delete(a.who, cl)
	a.mu.Unlock()
	if id != nil && id.kind == device && id.established {
		a.reg.Connected(id.name, -1)
	}
}

func (a *access) set(cl *mqtt.Client, id *ident) {
	a.mu.Lock()
	a.who[cl] = id
	a.mu.Unlock()
}

// drop disconnects a device at once: it was revoked, or given a new credential.
func (a *access) drop(name string) {
	var cls []*mqtt.Client
	a.mu.Lock()
	for cl, id := range a.who {
		if id.kind == device && id.name == name {
			cls = append(cls, cl)
		}
	}
	a.mu.Unlock()
	for _, cl := range cls {
		a.srv.DisconnectClient(cl, packets.ErrNotAuthorized)
	}
	if len(cls) > 0 {
		a.log.Info("mqtt: disconnected a device whose credential no longer stands", "device", name)
	}
}

// warn logs at most once a minute for each key, saying how many it held back.
func (a *access) warn(key, msg string, args ...any) {
	now := time.Now()
	a.mu.Lock()
	w := a.warned[key]
	if w != nil && now.Sub(w.at) < time.Minute {
		w.missed++
		a.mu.Unlock()
		return
	}
	missed := 0
	if w != nil {
		missed = w.missed
	}
	a.warned[key] = &warning{at: now}
	if len(a.warned) > 10000 {
		for k, v := range a.warned {
			if now.Sub(v.at) > time.Minute {
				delete(a.warned, k)
			}
		}
	}
	a.mu.Unlock()
	if missed > 0 {
		args = append(args, "more since last logged", missed)
	}
	a.log.Warn(msg, args...)
}

// peerCert is the client certificate a TLS client gave and the listener verified.
func peerCert(cl *mqtt.Client) *x509.Certificate {
	tc, ok := cl.Net.Conn.(*tls.Conn)
	if !ok {
		return nil
	}
	st := tc.ConnectionState()
	if len(st.VerifiedChains) == 0 || len(st.PeerCertificates) == 0 {
		return nil
	}
	return st.PeerCertificates[0]
}

func loopbackRemote(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
