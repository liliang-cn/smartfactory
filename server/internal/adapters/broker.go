// Package adapters connects industrial protocols to the tag hub.
package adapters

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"smartfactory/internal/config"
	"smartfactory/internal/mapping"
	"smartfactory/internal/mqttauth"
	"smartfactory/internal/tags"
)

// Broker is the embedded MQTT broker. Devices publish to it using the topic convention, and
// operator commands are published back on {prefix}/{company}/{plant}/{asset}/cmd.
type Broker struct {
	*mqtt.Server
	cfg config.Broker
	log *slog.Logger
	// listen is where the plain listener is: the configured address, or this machine only
	// while no client could prove who it is.
	mu     sync.Mutex
	listen string
}

// StartBroker runs the broker. Clients are the server's own (inline), holders of a shared
// user's password, devices registered in reg (nil: none), and — when there are no shared
// users — clients on this machine.
func StartBroker(cfg config.Broker, hub *tags.Hub, reg *mqttauth.Registry, log *slog.Logger) (*Broker, error) {
	const name = "broker"
	s := mqtt.New(&mqtt.Options{InlineClient: true, Logger: log.With("adapter", name)})
	b := &Broker{Server: s, cfg: cfg, log: log, listen: cfg.Listen}
	acc := &access{srv: s, users: cfg.Users, reg: reg, prefix: cfg.Prefix, log: log, who: map[*mqtt.Client]*ident{}, warned: map[string]*warning{}}
	if err := s.AddHook(acc, nil); err != nil {
		return nil, err
	}
	// a broker anyone on the network could publish to: only this machine, until a device can
	// prove who it is
	if len(cfg.Users) == 0 && (reg == nil || reg.Count() == 0) {
		if host, port, err := net.SplitHostPort(cfg.Listen); err == nil && !loopbackHost(host) {
			b.listen = net.JoinHostPort("127.0.0.1", port)
			log.Warn("the MQTT broker takes connections from this machine only until a device is registered (console 接入, or smartfactory device add) or mqtt.users has a password", "listen", b.listen)
		}
	}
	if err := s.AddListener(listeners.NewTCP(listeners.Config{ID: "tcp", Address: b.listen})); err != nil {
		return nil, err
	}
	if cfg.TLS != nil && cfg.TLS.Listen != "" {
		tc, err := tlsConfig(*cfg.TLS, reg)
		if err != nil {
			return nil, fmt.Errorf("mqtt tls: %w", err)
		}
		if err := s.AddListener(listeners.NewTCP(listeners.Config{ID: "tls", Address: cfg.TLS.Listen, TLSConfig: tc})); err != nil {
			return nil, err
		}
	}
	if reg != nil {
		names := []string{"inline"}
		for u := range cfg.Users {
			names = append(names, u)
		}
		reg.Reserve(names...)
		reg.Watch(b.open, acc.drop)
	}
	go func() {
		if err := s.Serve(); err != nil {
			log.Error("mqtt broker stopped", "err", err)
		}
	}()

	// Every publishing client is its own data source, named by its client id; the hook sees the
	// publisher (an inline subscription would only see itself).
	if err := s.AddHook(&clientStatus{hub: hub, prefix: cfg.Prefix, url: "mqtt://" + cfg.Listen, log: log, public: cfg.Public}, nil); err != nil {
		return nil, err
	}
	hub.OnCommand(func(c tags.Command) {
		if !Forward(cfg.Public, c) {
			return
		}
		payload, _ := json.Marshal(commandPayload(c))
		if err := s.Publish(mapping.CommandTopic(cfg.Prefix, c), payload, false, 1); err != nil {
			log.Warn("publish command", "err", err)
		}
	})
	attrs := []any{"addr", b.listen, "convention", cfg.Prefix + "/{company}/{plant}/{asset}/{field}"}
	if cfg.TLS != nil && cfg.TLS.Listen != "" {
		attrs = append(attrs, "tls", cfg.TLS.Listen)
	}
	log.Info("mqtt broker listening", attrs...)
	return b, nil
}

// Listening is where the plain listener is now.
func (b *Broker) Listening() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.listen
}

// open moves the plain listener from this machine to the configured address once a device
// is registered: it can now tell who connects. Clients already connected stay.
func (b *Broker) open() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.listen == b.cfg.Listen {
		return
	}
	b.Listeners.Close("tcp", func(string) {})
	b.Listeners.Delete("tcp")
	l := listeners.NewTCP(listeners.Config{ID: "tcp", Address: b.cfg.Listen})
	if err := b.AddListener(l); err != nil {
		b.log.Error("mqtt broker: listen on the network", "addr", b.cfg.Listen, "err", err)
		// back to this machine, as before
		l = listeners.NewTCP(listeners.Config{ID: "tcp", Address: b.listen})
		if err := b.AddListener(l); err != nil {
			b.log.Error("mqtt broker: listen", "addr", b.listen, "err", err)
			return
		}
		b.Listeners.Serve("tcp", b.EstablishConnection)
		return
	}
	b.Listeners.Serve("tcp", b.EstablishConnection)
	b.listen = b.cfg.Listen
	b.log.Info("mqtt broker listening on the network: a device is registered", "addr", b.listen)
}

// tlsConfig is the TLS listener's: the configured certificate or one from the server's CA, and
// client certificates checked against the CA when a device offers one.
func tlsConfig(c config.BrokerTLS, reg *mqttauth.Registry) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	var ca *mqttauth.CA
	if reg != nil {
		var err error
		if ca, err = reg.CA(); err != nil {
			return nil, err
		}
		tc.ClientCAs = ca.Pool()
		tc.ClientAuth = tls.VerifyClientCertIfGiven
	}
	switch {
	case c.Cert != "" || c.Key != "":
		pair, err := tls.LoadX509KeyPair(c.Cert, c.Key)
		if err != nil {
			return nil, err
		}
		tc.Certificates = []tls.Certificate{pair}
	case ca != nil:
		pair, err := ca.ServerCert(mqttauth.Hosts(c.Hosts...))
		if err != nil {
			return nil, err
		}
		tc.Certificates = []tls.Certificate{pair}
	default:
		return nil, errors.New("give mqtt.tls a cert and key, or the server a data directory for its CA")
	}
	return tc, nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func commandPayload(c tags.Command) map[string]any {
	m := map[string]any{"cmd": c.Cmd, "by": c.By, "t": time.Now().UnixMilli()}
	if c.Arg != nil {
		m["arg"] = *c.Arg
	}
	return m
}

// clientStatus turns each client's convention messages into tags under its own source, and
// marks that source offline when the client disconnects.
type clientStatus struct {
	mqtt.HookBase
	hub    *tags.Hub
	prefix string
	url    string
	log    *slog.Logger
	// public: the broker's devices are emulated
	public bool
}

func (h *clientStatus) ID() string { return "smartfactory-client-status" }

func (h *clientStatus) Provides(b byte) bool {
	return b == mqtt.OnPublish || b == mqtt.OnSessionEstablished || b == mqtt.OnDisconnect
}

func (h *clientStatus) OnPublish(cl *mqtt.Client, pk packets.Packet) (packets.Packet, error) {
	if cl == nil || cl.Net.Inline {
		return pk, nil
	}
	ts, err := mapping.Convention(h.prefix, pk.TopicName, pk.Payload, cl.ID)
	if err != nil {
		h.log.Debug("ignored message", "topic", pk.TopicName, "err", err)
		return pk, nil
	}
	if len(ts) > 0 {
		if !h.hub.HasSource(cl.ID) {
			h.hub.Source(cl.ID, "mqtt", h.url, 0)
			h.hub.SetPublic(cl.ID, h.public)
		}
		h.hub.SetConnected(cl.ID, true, nil)
	}
	for _, t := range ts {
		h.hub.Publish(t)
	}
	return pk, nil
}

func (h *clientStatus) OnSessionEstablished(cl *mqtt.Client, _ packets.Packet) {
	if h.hub.HasSource(cl.ID) {
		h.hub.SetConnected(cl.ID, true, nil)
	}
}

func (h *clientStatus) OnDisconnect(cl *mqtt.Client, err error, _ bool) {
	if h.hub.HasSource(cl.ID) {
		if err == nil {
			err = errors.New("disconnected")
		}
		h.hub.SetConnected(cl.ID, false, err)
	}
}
