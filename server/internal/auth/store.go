package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store keeps the tokens issued in the console: a hash of each, never the token itself.
type Store struct{ db *sql.DB }

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "accounts.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tokens (
		key TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL, companies TEXT NOT NULL, sha256 TEXT NOT NULL,
		expires TEXT NOT NULL, created INTEGER NOT NULL, created_by TEXT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0,
		revoked_by TEXT NOT NULL DEFAULT '', used INTEGER NOT NULL DEFAULT 0)`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.db.Close()
}

// Account is a token as the console lists it.
type Account struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Role Role   `json:"role"`
	// Companies it covers; empty is every company.
	Companies []string `json:"companies"`
	// Expires is the last day it works (YYYY-MM-DD), "" for never.
	Expires string `json:"expires,omitempty"`
	// From: legacy (the original single token) | config (the config file) | issued (the console)
	From      string     `json:"from"`
	Created   *time.Time `json:"created,omitempty"`
	CreatedBy string     `json:"createdBy,omitempty"`
	Used      *time.Time `json:"used,omitempty"`
	Revoked   *time.Time `json:"revoked,omitempty"`
	RevokedBy string     `json:"revokedBy,omitempty"`
	Expired   bool       `json:"expired,omitempty"`
}

// Attach brings in the tokens issued in the console and keeps the ones issued from now on.
func (k *Keyring) Attach(s *Store) error {
	rows, err := s.db.Query(`SELECT key, name, role, companies, sha256, expires FROM tokens WHERE revoked = 0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var issued []key
	for rows.Next() {
		var id, name, role, companies, sum, expires string
		if err := rows.Scan(&id, &name, &role, &companies, &sum, &expires); err != nil {
			return err
		}
		c, err := issuedKey(id, name, role, companies, sum, expires)
		if err != nil {
			return fmt.Errorf("issued token %q: %w", name, err)
		}
		issued = append(issued, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.store = s
	k.keys = append(k.keys, issued...)
	return nil
}

func issuedKey(id, name, role, companies, sum, expires string) (key, error) {
	r, err := ParseRole(role)
	if err != nil {
		return key{}, err
	}
	raw, err := hex.DecodeString(sum)
	if err != nil || len(raw) != 32 {
		return key{}, errors.New("bad hash")
	}
	var list []string
	if companies != "" {
		list = strings.Split(companies, ",")
	}
	exp, err := lastDay(expires)
	if err != nil {
		return key{}, err
	}
	c := key{who: Who{Name: name, Role: r, Companies: list, Key: id}, expires: exp, from: "issued"}
	copy(c.sum[:], raw)
	return c, nil
}

// lastDay is the moment a token that works through day (YYYY-MM-DD) stops working.
func lastDay(day string) (time.Time, error) {
	if day == "" {
		return time.Time{}, nil
	}
	d, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return time.Time{}, errors.New("expires must be YYYY-MM-DD")
	}
	return d.AddDate(0, 0, 1), nil
}

// touch notes that a token was used; issued tokens keep it, at most once a minute.
func (k *Keyring) touch(c key) {
	now := time.Now()
	k.mu.Lock()
	if k.used == nil {
		k.used = map[string]time.Time{}
	}
	last := k.used[c.who.Key]
	k.used[c.who.Key] = now
	s := k.store
	k.mu.Unlock()
	if s != nil && c.from == "issued" && now.Sub(last) > time.Minute {
		s.db.Exec(`UPDATE tokens SET used = ? WHERE key = ?`, now.UnixMilli(), c.who.Key)
	}
}

// Covers reports whether w's token reaches every company in companies (nil: every company).
func (w Who) Covers(companies []string) bool {
	if w.Everywhere() {
		return true
	}
	if companies == nil {
		return false
	}
	for _, c := range companies {
		if !w.In(c) {
			return false
		}
	}
	return true
}

// Manages reports whether w may see, issue and revoke a token covering companies: an
// administrator, for tokens within their own companies.
func (w Who) Manages(companies []string) bool { return w.Role == Admin && w.Covers(companies) }

// CanIssue reports whether tokens can be issued in the console (the server keeps data).
func (k *Keyring) CanIssue() bool {
	if k == nil {
		return false
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.store != nil
}

// Bootstrap gives a new installation its first administrator: when no token is accepted at all,
// it issues one for every company and returns it (shown once, by the caller); otherwise "".
func (k *Keyring) Bootstrap() (string, error) {
	if !k.Empty() {
		return "", nil
	}
	token, _, err := k.Issue(Who{Name: "安装", Role: Admin}, "管理员", Admin, nil, "")
	return token, err
}

var ErrNoStore = errors.New("this server keeps no data: tokens can only be added to the config file")

// Issue makes a token for name, kept (as a hash) in the store and accepted at once. companies
// nil is every company.
func (k *Keyring) Issue(by Who, name string, role Role, companies []string, expires string) (string, Account, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", Account{}, errors.New("给令牌起个名字：谁拿着它")
	}
	if role == Visitor {
		return "", Account{}, errors.New("角色要是成员、操作员或管理员")
	}
	if companies != nil && len(companies) == 0 {
		return "", Account{}, errors.New("至少选一家公司")
	}
	if !by.Manages(companies) {
		return "", Account{}, errors.New("只能给自己管理的公司发令牌")
	}
	exp, err := lastDay(expires)
	if err != nil {
		return "", Account{}, errors.New("到期日的格式是 YYYY-MM-DD")
	}
	if !exp.IsZero() && time.Now().After(exp) {
		return "", Account{}, errors.New("到期日已经过了")
	}
	k.mu.RLock()
	s := k.store
	k.mu.RUnlock()
	if s == nil {
		return "", Account{}, ErrNoStore
	}
	token, entry := NewToken(name, role, companies, expires)
	sum := sha256.Sum256([]byte(token))
	id := hex.EncodeToString(sum[:4])
	now := time.Now()
	if _, err := s.db.Exec(`INSERT INTO tokens (key, name, role, companies, sha256, expires, created, created_by) VALUES (?,?,?,?,?,?,?,?)`,
		id, name, role.String(), strings.Join(companies, ","), entry.SHA256, expires, now.UnixMilli(), by.Name); err != nil {
		return "", Account{}, err
	}
	k.mu.Lock()
	k.keys = append(k.keys, key{who: Who{Name: name, Role: role, Companies: companies, Key: id}, sum: sum, expires: exp, from: "issued"})
	k.mu.Unlock()
	return token, Account{Key: id, Name: name, Role: role, Companies: orEmpty(companies), Expires: expires, From: "issued", Created: &now, CreatedBy: by.Name}, nil
}

// Revoke stops an issued token working at once. It returns the account as it was.
func (k *Keyring) Revoke(by Who, id string) (Account, error) {
	list, err := k.Accounts(by)
	if err != nil {
		return Account{}, err
	}
	i := slices.IndexFunc(list, func(a Account) bool { return a.Key == id })
	if i < 0 {
		return Account{}, errors.New("没有这个令牌，或者它不归你管")
	}
	a := list[i]
	switch {
	case a.From != "issued":
		return a, errors.New("这个令牌写在服务器的配置文件里，要在那里删掉并重启")
	case a.Revoked != nil:
		return a, errors.New("这个令牌已经撤销了")
	case a.Key == by.Key:
		return a, errors.New("不能撤销你自己正在用的令牌")
	}
	if _, err := k.store.db.Exec(`UPDATE tokens SET revoked = ?, revoked_by = ? WHERE key = ?`, time.Now().UnixMilli(), by.Name, id); err != nil {
		return a, err
	}
	k.mu.Lock()
	k.keys = slices.DeleteFunc(k.keys, func(c key) bool { return c.from == "issued" && c.who.Key == id })
	k.mu.Unlock()
	return a, nil
}

// Accounts lists the tokens by can manage: the config file's, then the issued ones, newest first
// (revoked ones too, so the list says what happened to them).
func (k *Keyring) Accounts(by Who) ([]Account, error) {
	out := []Account{}
	k.mu.RLock()
	s := k.store
	for _, c := range k.keys {
		if c.from == "issued" || !by.Manages(c.who.Companies) {
			continue
		}
		a := Account{Key: c.who.Key, Name: c.who.Name, Role: c.who.Role, Companies: orEmpty(c.who.Companies), From: c.from}
		if !c.expires.IsZero() {
			a.Expires = c.expires.AddDate(0, 0, -1).Format("2006-01-02")
			a.Expired = time.Now().After(c.expires)
		}
		if t, ok := k.used[c.who.Key]; ok {
			a.Used = &t
		}
		out = append(out, a)
	}
	used := maps.Clone(k.used)
	k.mu.RUnlock()
	if s == nil {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT key, name, role, companies, expires, created, created_by, revoked, revoked_by, used FROM tokens ORDER BY created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Account
		var role, companies string
		var created, revoked, usedAt int64
		if err := rows.Scan(&a.Key, &a.Name, &role, &companies, &a.Expires, &created, &a.CreatedBy, &revoked, &a.RevokedBy, &usedAt); err != nil {
			return nil, err
		}
		a.Role, _ = ParseRole(role)
		a.Companies = []string{}
		var cover []string
		if companies != "" {
			a.Companies = strings.Split(companies, ",")
			cover = a.Companies
		}
		if !by.Manages(cover) {
			continue
		}
		a.From = "issued"
		t := time.UnixMilli(created)
		a.Created = &t
		if revoked > 0 {
			r := time.UnixMilli(revoked)
			a.Revoked = &r
		}
		if u, ok := used[a.Key]; ok {
			a.Used = &u
		} else if usedAt > 0 {
			u := time.UnixMilli(usedAt)
			a.Used = &u
		}
		if exp, _ := lastDay(a.Expires); !exp.IsZero() && time.Now().After(exp) {
			a.Expired = true
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// MarshalJSON lets Who travel in responses as the console shows the signed-in person.
func (w Who) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name       string   `json:"name"`
		Role       Role     `json:"role"`
		Companies  []string `json:"companies"`
		Everywhere bool     `json:"everywhere"`
		Key        string   `json:"key"`
	}{w.Name, w.Role, orEmpty(w.Companies), w.Everywhere(), w.Key})
}
