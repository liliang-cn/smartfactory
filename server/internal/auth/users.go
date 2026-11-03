package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// People sign in with an account (a login and a password) and keep a session in a cookie;
// tokens are for systems: equipment, the ERP, MCP clients, table reporters.

// SessionCookie is the cookie a signed-in person's browser carries.
const SessionCookie = "sf_session"

// SessionDays is how long a session lasts since it was last used.
const SessionDays = 30

// MinPassword is the shortest password accepted.
const MinPassword = 8

const bcryptCost = 12

// User is a person's account as the console lists it.
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Login string `json:"login"`
	Role  Role   `json:"role"`
	// Companies it covers; empty is every company.
	Companies []string   `json:"companies"`
	Disabled  bool       `json:"disabled,omitempty"`
	Created   time.Time  `json:"created"`
	CreatedBy string     `json:"createdBy,omitempty"`
	LastIn    *time.Time `json:"lastIn,omitempty"`
	// MustChange: the password was set by an administrator; the person picks their own next.
	MustChange bool `json:"mustChange,omitempty"`
}

func (u User) who() Who {
	var list []string
	if len(u.Companies) > 0 {
		list = u.Companies
	}
	return Who{Name: u.Name, Role: u.Role, Companies: list, Key: "u:" + u.ID, User: u.ID}
}

func (s *Store) migrateUsers() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, login TEXT NOT NULL UNIQUE COLLATE NOCASE, role TEXT NOT NULL,
		companies TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL, disabled INTEGER NOT NULL DEFAULT 0,
		must_change INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, created_by TEXT NOT NULL DEFAULT '',
		last_in INTEGER NOT NULL DEFAULT 0);
	CREATE TABLE IF NOT EXISTS sessions (
		sum TEXT PRIMARY KEY, user TEXT NOT NULL, created INTEGER NOT NULL, expires INTEGER NOT NULL,
		seen INTEGER NOT NULL, ip TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL DEFAULT '');
	CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user);
	CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`)
	return err
}

// Setting reads a server setting kept with the accounts; ok is false when it was never set.
func (s *Store) Setting(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

// SetSetting keeps a server setting.
func (s *Store) SetSetting(key, value string) error {
	if s == nil {
		return ErrNoStore
	}
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

var loginRe = regexp.MustCompile(`^[\p{L}\p{N}._@+-]{2,40}$`)

func checkPassword(p string) error {
	if utf8.RuneCountInString(p) < MinPassword {
		return fmt.Errorf("密码至少 %d 位", MinPassword)
	}
	if len(p) > 72 {
		return errors.New("密码太长了，72 个字节以内")
	}
	return nil
}

func hashPassword(p string) (string, error) {
	if err := checkPassword(p); err != nil {
		return "", err
	}
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcryptCost)
	return string(b), err
}

func newID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// HasUsers reports whether anyone has an account yet.
func (s *Store) HasUsers() bool {
	if s == nil {
		return false
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0
}

// NewUser is an account to create.
type NewUser struct {
	Name      string   `json:"name"`
	Login     string   `json:"login"`
	Role      string   `json:"role"`
	Companies []string `json:"companies"`
	Password  string   `json:"password"`
}

// CreateUser makes an account. by is who creates it ("" for the first administrator, made at
// setup); mustChange asks the person to pick their own password when they first sign in.
func (s *Store) CreateUser(by string, in NewUser, mustChange bool) (User, error) {
	if s == nil {
		return User{}, ErrNoStore
	}
	in.Name, in.Login = strings.TrimSpace(in.Name), strings.TrimSpace(in.Login)
	if in.Name == "" {
		return User{}, errors.New("填上姓名")
	}
	if !loginRe.MatchString(in.Login) {
		return User{}, errors.New("登录名用手机号或工号：2～40 个字母、数字，不带空格")
	}
	role, err := ParseRole(in.Role)
	if err != nil || role.Narrow() {
		return User{}, errors.New("角色要是成员、质检员、操作员或管理员")
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	var companies []string
	for _, c := range in.Companies {
		if c = strings.TrimSpace(c); c != "" && c != "*" {
			companies = append(companies, c)
		}
	}
	u := User{ID: newID(9), Name: in.Name, Login: in.Login, Role: role, Companies: orEmpty(companies), Created: time.Now(), CreatedBy: by, MustChange: mustChange}
	if _, err := s.db.Exec(`INSERT INTO users (id, name, login, role, companies, hash, must_change, created, created_by) VALUES (?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Name, u.Login, role.String(), strings.Join(companies, ","), hash, boolInt(mustChange), u.Created.UnixMilli(), by); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, fmt.Errorf("登录名 %s 已经有人用了", in.Login)
		}
		return User{}, err
	}
	return u, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const userCols = `id, name, login, role, companies, disabled, must_change, created, created_by, last_in`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var role, companies string
	var disabled, must int
	var created, lastIn int64
	if err := sc.Scan(&u.ID, &u.Name, &u.Login, &role, &companies, &disabled, &must, &created, &u.CreatedBy, &lastIn); err != nil {
		return u, err
	}
	u.Role, _ = ParseRole(role)
	u.Companies = []string{}
	if companies != "" {
		u.Companies = strings.Split(companies, ",")
	}
	u.Disabled, u.MustChange = disabled == 1, must == 1
	u.Created = time.UnixMilli(created)
	if lastIn > 0 {
		t := time.UnixMilli(lastIn)
		u.LastIn = &t
	}
	return u, nil
}

// Users lists the accounts by may manage, newest first.
func (s *Store) Users(by Who) ([]User, error) {
	out := []User{}
	if s == nil {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		var cover []string
		if len(u.Companies) > 0 {
			cover = u.Companies
		}
		if by.Manages(cover) {
			out = append(out, u)
		}
	}
	return out, rows.Err()
}

// User is one account.
func (s *Store) User(id string) (User, error) {
	if s == nil {
		return User{}, ErrNoStore
	}
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, errors.New("没有这个账号")
	}
	return u, err
}

// managed is the account id when by may manage it.
func (s *Store) managed(by Who, id string) (User, error) {
	u, err := s.User(id)
	if err != nil {
		return u, err
	}
	var cover []string
	if len(u.Companies) > 0 {
		cover = u.Companies
	}
	if !by.Manages(cover) {
		return u, errors.New("没有这个账号，或者它不归你管")
	}
	return u, nil
}

// UserChange is what an administrator changes on an account; nil fields stay as they are.
type UserChange struct {
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
	// Password resets it; the person picks their own when they next sign in.
	Password *string `json:"password"`
}

// UpdateUser changes an account. Disabling it or resetting its password ends its sessions.
func (s *Store) UpdateUser(by Who, id string, c UserChange) (User, error) {
	u, err := s.managed(by, id)
	if err != nil {
		return u, err
	}
	self := by.User == id
	if c.Name != nil {
		n := strings.TrimSpace(*c.Name)
		if n == "" {
			return u, errors.New("填上姓名")
		}
		if _, err := s.db.Exec(`UPDATE users SET name = ? WHERE id = ?`, n, id); err != nil {
			return u, err
		}
	}
	if c.Role != nil {
		r, err := ParseRole(*c.Role)
		if err != nil || r.Narrow() || r == Visitor {
			return u, errors.New("角色要是成员、质检员、操作员或管理员")
		}
		if self && r != Admin {
			return u, errors.New("不能降低你自己的角色：请另一位管理员来改")
		}
		if _, err := s.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, r.String(), id); err != nil {
			return u, err
		}
	}
	end := false
	if c.Disabled != nil {
		if self && *c.Disabled {
			return u, errors.New("不能停用你自己的账号")
		}
		if _, err := s.db.Exec(`UPDATE users SET disabled = ? WHERE id = ?`, boolInt(*c.Disabled), id); err != nil {
			return u, err
		}
		end = end || *c.Disabled
	}
	if c.Password != nil {
		hash, err := hashPassword(*c.Password)
		if err != nil {
			return u, err
		}
		if _, err := s.db.Exec(`UPDATE users SET hash = ?, must_change = ? WHERE id = ?`, hash, boolInt(!self), id); err != nil {
			return u, err
		}
		end = end || !self
	}
	if end {
		s.EndSessions(id, "")
	}
	return s.User(id)
}

// ChangePassword is a person changing their own password; their other sessions end.
func (s *Store) ChangePassword(who Who, old, next, keepSession string) error {
	if who.User == "" {
		return errors.New("用令牌登录的不能改密码")
	}
	var hash string
	if err := s.db.QueryRow(`SELECT hash FROM users WHERE id = ?`, who.User).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(old)) != nil {
		return errors.New("原来的密码不对")
	}
	if old == next {
		return errors.New("新密码要和原来的不一样")
	}
	h, err := hashPassword(next)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE users SET hash = ?, must_change = 0 WHERE id = ?`, h, who.User); err != nil {
		return err
	}
	s.EndSessions(who.User, keepSession)
	return nil
}

// ErrSignIn is the one answer to a wrong login or password: it does not say which was wrong.
var ErrSignIn = errors.New("登录名或密码不对")

// ErrLocked is the answer while a login or an address is held back after too many wrong tries.
var ErrLocked = errors.New("错得太多了，过 15 分钟再试")

// throttle holds back a login or an address after repeated failures.
type throttle struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

const (
	lockWindow   = 15 * time.Minute
	lockPerLogin = 5
	lockPerIP    = 20
)

func (t *throttle) recent(k string, now time.Time) []time.Time {
	var keep []time.Time
	for _, at := range t.fail[k] {
		if now.Sub(at) < lockWindow {
			keep = append(keep, at)
		}
	}
	if keep == nil {
		delete(t.fail, k)
	} else {
		t.fail[k] = keep
	}
	return keep
}

func (t *throttle) locked(login, ip string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fail == nil {
		t.fail = map[string][]time.Time{}
	}
	return len(t.recent("l:"+strings.ToLower(login), now)) >= lockPerLogin || len(t.recent("i:"+ip, now)) >= lockPerIP
}

func (t *throttle) failed(login, ip string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fail == nil {
		t.fail = map[string][]time.Time{}
	}
	for _, k := range []string{"l:" + strings.ToLower(login), "i:" + ip} {
		t.fail[k] = append(t.fail[k], now)
	}
}

func (t *throttle) cleared(login string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fail, "l:"+strings.ToLower(login))
}

// dummyHash keeps a sign-in for an unknown login as slow as one for a known login.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("smartfactory-not-a-password"), bcryptCost)

// SignIn checks a login and password and starts a session; the returned id goes in the cookie.
func (k *Keyring) SignIn(login, password, ip, agent string) (string, User, error) {
	s := k.storeOf()
	if s == nil {
		return "", User{}, ErrNoStore
	}
	now := time.Now()
	login = strings.TrimSpace(login)
	if k.throttle.locked(login, ip, now) {
		return "", User{}, ErrLocked
	}
	var hash string
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE login = ?`, login))
	if err == nil {
		err = s.db.QueryRow(`SELECT hash FROM users WHERE id = ?`, u.ID).Scan(&hash)
	}
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		k.throttle.failed(login, ip, now)
		return "", User{}, ErrSignIn
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		k.throttle.failed(login, ip, now)
		return "", User{}, ErrSignIn
	}
	if u.Disabled {
		return "", User{}, errors.New("这个账号已经停用了，找管理员")
	}
	k.throttle.cleared(login)
	id := "ss_" + newID(32)
	sum := sha256.Sum256([]byte(id))
	exp := now.AddDate(0, 0, SessionDays)
	if len(agent) > 200 {
		agent = agent[:200]
	}
	if _, err := s.db.Exec(`INSERT INTO sessions (sum, user, created, expires, seen, ip, agent) VALUES (?,?,?,?,?,?,?)`,
		hex.EncodeToString(sum[:]), u.ID, now.UnixMilli(), exp.UnixMilli(), now.UnixMilli(), ip, agent); err != nil {
		return "", User{}, err
	}
	s.db.Exec(`UPDATE users SET last_in = ? WHERE id = ?`, now.UnixMilli(), u.ID)
	u.LastIn = &now
	return id, u, nil
}

// Session finds the person a session id belongs to, and keeps the session going another
// SessionDays from now (at most once an hour).
func (k *Keyring) Session(id string) (Who, bool) {
	s := k.storeOf()
	if s == nil || !strings.HasPrefix(id, "ss_") {
		return Anonymous, false
	}
	sum := sha256.Sum256([]byte(id))
	key := hex.EncodeToString(sum[:])
	var user string
	var exp, seen int64
	if err := s.db.QueryRow(`SELECT user, expires, seen FROM sessions WHERE sum = ?`, key).Scan(&user, &exp, &seen); err != nil {
		return Anonymous, false
	}
	now := time.Now()
	if now.UnixMilli() > exp {
		s.db.Exec(`DELETE FROM sessions WHERE sum = ?`, key)
		return Anonymous, false
	}
	u, err := s.User(user)
	if err != nil || u.Disabled {
		return Anonymous, false
	}
	if now.Sub(time.UnixMilli(seen)) > time.Hour {
		s.db.Exec(`UPDATE sessions SET seen = ?, expires = ? WHERE sum = ?`, now.UnixMilli(), now.AddDate(0, 0, SessionDays).UnixMilli(), key)
	}
	return u.who(), true
}

// SignOut ends one session.
func (k *Keyring) SignOut(id string) {
	if s := k.storeOf(); s != nil && id != "" {
		sum := sha256.Sum256([]byte(id))
		s.db.Exec(`DELETE FROM sessions WHERE sum = ?`, hex.EncodeToString(sum[:]))
	}
}

// EndSessions ends every session of a person except keep (a session id, "" for none).
func (s *Store) EndSessions(user, keep string) {
	if s == nil {
		return
	}
	k := ""
	if keep != "" {
		sum := sha256.Sum256([]byte(keep))
		k = hex.EncodeToString(sum[:])
	}
	s.db.Exec(`DELETE FROM sessions WHERE user = ? AND sum != ?`, user, k)
}

func (k *Keyring) storeOf() *Store {
	if k == nil {
		return nil
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.store
}

// Store is where the keyring keeps issued tokens, accounts and sessions (nil without a data
// directory).
func (k *Keyring) Store() *Store { return k.storeOf() }

// SessionOf is the session id a request's cookie carries.
func SessionOf(r *http.Request) string {
	if c, err := r.Cookie(SessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// SetSession puts a session in the browser's cookie: httpOnly, Lax, Secure over HTTPS.
func SetSession(w http.ResponseWriter, r *http.Request, id string) {
	c := &http.Cookie{Name: SessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: Secure(r)}
	if id == "" {
		c.MaxAge = -1
	} else {
		c.Expires = time.Now().AddDate(0, 0, SessionDays)
	}
	http.SetCookie(w, c)
}

// Secure reports whether the browser reached the server over HTTPS (directly or through a proxy).
func Secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ClientIP is the address a request came from (the proxy's X-Forwarded-For first hop only when
// the request reached us from loopback, so a client cannot claim an address).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if f := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); f != "" {
			return f
		}
	}
	return host
}

// Loopback reports whether the request came from this machine itself, not through a proxy.
func Loopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("X-Real-IP") == ""
}

// RevokeBootstrap retires the token a new installation made for its first administrator, once
// that person has an account.
func (k *Keyring) RevokeBootstrap(by string) {
	s := k.storeOf()
	if s == nil {
		return
	}
	rows, err := s.db.Query(`SELECT key FROM tokens WHERE created_by = '安装' AND revoked = 0`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		s.db.Exec(`UPDATE tokens SET revoked = ?, revoked_by = ? WHERE key = ?`, time.Now().UnixMilli(), by, id)
		k.mu.Lock()
		kept := k.keys[:0]
		for _, c := range k.keys {
			if !(c.from == "issued" && c.who.Key == id) {
				kept = append(kept, c)
			}
		}
		k.keys = kept
		k.mu.Unlock()
	}
}
