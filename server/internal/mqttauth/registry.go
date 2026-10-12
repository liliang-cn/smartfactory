package mqttauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Kinds of credential a device holds.
const (
	Token = "token"
	Cert  = "cert"
)

// Device is one registered device as the console lists it.
type Device struct {
	ID string `json:"id"`
	// Name is its MQTT client id and user name, and its certificate's CN.
	Name string `json:"name"`
	Scope
	// Kind: token | cert
	Kind      string    `json:"kind"`
	Created   time.Time `json:"created"`
	CreatedBy string    `json:"createdBy"`
	// Expires is when its certificate lapses (cert).
	Expires   *time.Time `json:"expires,omitempty"`
	Revoked   *time.Time `json:"revoked,omitempty"`
	RevokedBy string     `json:"revokedBy,omitempty"`
	// What the broker saw of it: connected now, its last accepted message, and how often it
	// was refused (a connection with a wrong credential, a topic not its own) and the last time.
	Connected   bool       `json:"connected"`
	LastSeen    *time.Time `json:"lastSeen,omitempty"`
	Refused     int64      `json:"refused"`
	LastRefusal string     `json:"lastRefusal,omitempty"`
	RefusedAt   *time.Time `json:"refusedAt,omitempty"`

	// the credential in force: the token's SHA-256, or the certificate's serial
	sum, serial string
}

// Credential is what a device is given once: its token, or its certificate, key and the CA.
type Credential struct {
	Token string `json:"token,omitempty"`
	Cert  string `json:"cert,omitempty"`
	Key   string `json:"key,omitempty"`
	CA    string `json:"ca,omitempty"`
}

// Invalid is a registration that cannot be made as asked.
type Invalid struct{ Msg string }

func (e *Invalid) Error() string { return e.Msg }

var ErrNotFound = errors.New("no such device")

// Registry keeps the devices in {data}/devices.db (tokens only as hashes) and the CA in
// {data}/ca. The broker asks it who a client is and tells it what it saw.
type Registry struct {
	dir string
	db  *sql.DB
	// Now is the clock (tests set it).
	Now func() time.Time

	mu       sync.Mutex
	active   map[string]*Device // by name
	live     map[string]int
	dirty    map[string]bool
	reserved map[string]bool
	ca       *CA
	onAdd    func()
	onDrop   func(name string)
}

func Open(dir string) (*Registry, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "devices.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS devices (id TEXT PRIMARY KEY, name TEXT NOT NULL, company TEXT NOT NULL,
		plant TEXT NOT NULL, assets TEXT NOT NULL, kind TEXT NOT NULL, sha256 TEXT NOT NULL DEFAULT '', serial TEXT NOT NULL DEFAULT '',
		expires INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, created_by TEXT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0,
		revoked_by TEXT NOT NULL DEFAULT '', last_seen INTEGER NOT NULL DEFAULT 0, refused INTEGER NOT NULL DEFAULT 0,
		last_refusal TEXT NOT NULL DEFAULT '', refused_at INTEGER NOT NULL DEFAULT 0);
		CREATE UNIQUE INDEX IF NOT EXISTS devices_name ON devices (name) WHERE revoked = 0;
		CREATE INDEX IF NOT EXISTS devices_company ON devices (company)`); err != nil {
		db.Close()
		return nil, err
	}
	// hashes of tokens, but still nobody else's business
	os.Chmod(path, 0o600)
	r := &Registry{dir: dir, db: db, Now: time.Now, active: map[string]*Device{}, live: map[string]int{}, dirty: map[string]bool{}, reserved: map[string]bool{}}
	list, err := r.query(`WHERE revoked = 0`)
	if err != nil {
		db.Close()
		return nil, err
	}
	for i := range list {
		r.active[list[i].Name] = &list[i]
	}
	return r, nil
}

func (r *Registry) Close() error {
	r.flush()
	return r.db.Close()
}

// Run writes what the broker saw to disk every half minute.
func (r *Registry) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.flush()
		}
	}
}

// Watch tells the broker when a device is added (it may then listen on the network) and when
// one must be cut off (revoked, or given a new credential).
func (r *Registry) Watch(added func(), dropped func(name string)) {
	r.mu.Lock()
	r.onAdd, r.onDrop = added, dropped
	r.mu.Unlock()
}

// Reserve keeps names no device may take: the broker's shared user names.
func (r *Registry) Reserve(names ...string) {
	r.mu.Lock()
	for _, n := range names {
		r.reserved[n] = true
	}
	r.mu.Unlock()
}

// CA is the server's CA, made the first time it is asked for.
func (r *Registry) CA() (*CA, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadCA()
}

func (r *Registry) loadCA() (*CA, error) {
	if r.ca == nil {
		ca, err := LoadCA(filepath.Join(r.dir, "ca"))
		if err != nil {
			return nil, fmt.Errorf("device CA: %w", err)
		}
		r.ca = ca
	}
	return r.ca, nil
}

// Count is how many devices are registered (not revoked).
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

// Add registers a device and gives it its credential, shown this once.
func (r *Registry) Add(by, name, kind string, sc Scope) (Device, *Credential, error) {
	name = strings.TrimSpace(name)
	if err := CheckName(name); err != nil {
		return Device{}, nil, &Invalid{err.Error()}
	}
	if err := sc.Check(); err != nil {
		return Device{}, nil, &Invalid{err.Error()}
	}
	if kind != Token && kind != Cert {
		return Device{}, nil, &Invalid{"凭据是 token（令牌）或 cert（证书）"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reserved[name] {
		return Device{}, nil, &Invalid{fmt.Sprintf("%s 是配置里的共用账号，换个设备名", name)}
	}
	if _, ok := r.active[name]; ok {
		return Device{}, nil, &Invalid{fmt.Sprintf("已经有叫 %s 的设备了（设备名在整个服务器上不能重）", name)}
	}
	d := Device{ID: randomHex(4), Name: name, Scope: sc, Kind: kind, Created: r.Now().Truncate(time.Millisecond), CreatedBy: by}
	d.Assets = append([]string(nil), sc.Assets...)
	cred, err := r.credential(&d)
	if err != nil {
		return Device{}, nil, err
	}
	if _, err := r.db.Exec(`INSERT INTO devices (id, name, company, plant, assets, kind, sha256, serial, expires, created, created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.Company, d.Plant, strings.Join(d.Assets, ","), d.Kind, d.sum, d.serial, millis(d.Expires), d.Created.UnixMilli(), by); err != nil {
		return Device{}, nil, err
	}
	r.active[name] = &d
	if r.onAdd != nil {
		go r.onAdd()
	}
	return d, cred, nil
}

// credential makes d a new token or certificate.
func (r *Registry) credential(d *Device) (*Credential, error) {
	if d.Kind == Token {
		b := make([]byte, 24)
		rand.Read(b)
		token := "sfd_" + base64.RawURLEncoding.EncodeToString(b)
		d.sum = hashToken(token)
		return &Credential{Token: token}, nil
	}
	ca, err := r.loadCA()
	if err != nil {
		return nil, err
	}
	is, err := ca.IssueDevice(d.Name)
	if err != nil {
		return nil, err
	}
	d.serial = is.Serial
	exp := is.Expires.Truncate(time.Millisecond)
	d.Expires = &exp
	return &Credential{Cert: string(is.Cert), Key: string(is.Key), CA: string(ca.PEM())}, nil
}

// Renew gives a device a new credential of its kind; the old one stops working and its
// connection is closed.
func (r *Registry) Renew(company, id string) (Device, *Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.byID(company, id)
	if d == nil {
		return Device{}, nil, ErrNotFound
	}
	next := *d
	cred, err := r.credential(&next)
	if err != nil {
		return Device{}, nil, err
	}
	if _, err := r.db.Exec(`UPDATE devices SET sha256 = ?, serial = ?, expires = ? WHERE id = ?`, next.sum, next.serial, millis(next.Expires), id); err != nil {
		return Device{}, nil, err
	}
	*d = next
	r.drop(d.Name)
	return r.view(d), cred, nil
}

// Revoke stops a device at once: its connection is closed and its credential refused.
func (r *Registry) Revoke(company, id, by string) (Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.byID(company, id)
	if d == nil {
		return Device{}, ErrNotFound
	}
	r.flushLocked()
	now := r.Now().Truncate(time.Millisecond)
	if _, err := r.db.Exec(`UPDATE devices SET revoked = ?, revoked_by = ? WHERE id = ?`, now.UnixMilli(), by, id); err != nil {
		return Device{}, err
	}
	delete(r.active, d.Name)
	out := r.view(d)
	out.Revoked, out.RevokedBy, out.Connected = &now, by, false
	r.drop(d.Name)
	return out, nil
}

func (r *Registry) drop(name string) {
	if r.onDrop != nil {
		go r.onDrop(name)
	}
}

func (r *Registry) byID(company, id string) *Device {
	for _, d := range r.active {
		if d.ID == id && d.Company == company {
			return d
		}
	}
	return nil
}

// List is a company's devices: registered ones by name, then the revoked, newest first.
func (r *Registry) List(company string) ([]Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list, err := r.query(`WHERE company = ? ORDER BY revoked != 0, revoked DESC, name LIMIT 500`, company)
	if err != nil {
		return nil, err
	}
	for i, d := range list {
		if a, ok := r.active[d.Name]; ok && a.ID == d.ID {
			list[i] = r.view(a)
		}
	}
	return list, nil
}

// view is d with what the broker sees now.
func (r *Registry) view(d *Device) Device {
	out := *d
	out.Assets = append([]string(nil), d.Assets...)
	out.Connected = r.live[d.Name] > 0
	return out
}

// Login is the device a client names with its user name and token, if the token is its own.
// cred identifies the credential, for Current.
func (r *Registry) Login(name, token string) (d Device, cred string, ok bool) {
	sum := hashToken(token)
	r.mu.Lock()
	defer r.mu.Unlock()
	a, found := r.active[name]
	if !found || a.Kind != Token || subtle.ConstantTimeCompare([]byte(sum), []byte(a.sum)) != 1 {
		return Device{}, "", false
	}
	return r.view(a), a.sum, true
}

// Known: a registered device has this name.
func (r *Registry) Known(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.active[name]
	return ok
}

// LoginCert is the device a client certificate (already verified against the CA) names, if
// it is the certificate in force for it.
func (r *Registry) LoginCert(c *x509.Certificate) (d Device, cred string, ok bool) {
	serial := hex.EncodeToString(c.SerialNumber.Bytes())
	r.mu.Lock()
	defer r.mu.Unlock()
	a, found := r.active[c.Subject.CommonName]
	if !found || a.Kind != Cert || a.serial != serial || r.Now().After(c.NotAfter) {
		return Device{}, "", false
	}
	return r.view(a), a.serial, true
}

// Current is the device as registered, while cred is still its credential.
func (r *Registry) Current(name, cred string) (Scope, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.active[name]
	if !ok || (cred != a.sum && cred != a.serial) || cred == "" {
		return Scope{}, false
	}
	return a.Scope, true
}

// Connected counts a device's connections up or down.
func (r *Registry) Connected(name string, delta int) {
	r.mu.Lock()
	r.live[name] += delta
	if r.live[name] <= 0 {
		delete(r.live, name)
	}
	r.mu.Unlock()
}

// Seen: the broker took a message from the device.
func (r *Registry) Seen(name string) {
	now := r.Now()
	r.mu.Lock()
	if a, ok := r.active[name]; ok {
		a.LastSeen = &now
		r.dirty[name] = true
	}
	r.mu.Unlock()
}

// Refuse counts something the device was refused; what says what (never a credential).
func (r *Registry) Refuse(name, what string) {
	now := r.Now()
	r.mu.Lock()
	if a, ok := r.active[name]; ok {
		a.Refused++
		a.LastRefusal = what
		a.RefusedAt = &now
		r.dirty[name] = true
	}
	r.mu.Unlock()
}

func (r *Registry) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushLocked()
}

func (r *Registry) flushLocked() {
	for name := range r.dirty {
		if a, ok := r.active[name]; ok {
			r.db.Exec(`UPDATE devices SET last_seen = ?, refused = ?, last_refusal = ?, refused_at = ? WHERE id = ?`,
				millis(a.LastSeen), a.Refused, a.LastRefusal, millis(a.RefusedAt), a.ID)
		}
		delete(r.dirty, name)
	}
}

func (r *Registry) query(where string, args ...any) ([]Device, error) {
	rows, err := r.db.Query(`SELECT id, name, company, plant, assets, kind, sha256, serial, expires, created, created_by, revoked, revoked_by,
		last_seen, refused, last_refusal, refused_at FROM devices `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		var assets string
		var expires, created, revoked, seen, refusedAt int64
		if err := rows.Scan(&d.ID, &d.Name, &d.Company, &d.Plant, &assets, &d.Kind, &d.sum, &d.serial, &expires, &created, &d.CreatedBy,
			&revoked, &d.RevokedBy, &seen, &d.Refused, &d.LastRefusal, &refusedAt); err != nil {
			return nil, err
		}
		d.Assets = strings.Split(assets, ",")
		d.Created = time.UnixMilli(created)
		d.Expires, d.Revoked, d.LastSeen, d.RefusedAt = at(expires), at(revoked), at(seen), at(refusedAt)
		out = append(out, d)
	}
	return out, rows.Err()
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func millis(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}

func at(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms)
	return &t
}
