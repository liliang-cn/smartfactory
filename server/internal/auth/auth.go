// Package auth says who is calling and what they may do. Everyone without a token is a visitor;
// a token names a person or client, their role, and the companies it covers.
//
//	visitor   watch; demo commands that undo themselves; assistants answer and draft
//	member    + record through the assistants: notes, tickets, ticket status
//	operator  + commands that reach equipment, the plant clock, the advisor's decisions
//	admin     + scenes, documents, assistants themselves
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"smartfactory/internal/config"
)

type Role int

const (
	Visitor Role = iota
	Member
	Operator
	Admin
)

var roleNames = [...]string{"visitor", "member", "operator", "admin"}
var roleLabels = [...]string{"访客", "成员", "操作员", "管理员"}

func (r Role) String() string { return roleNames[r] }

// Label is the role's name as people read it.
func (r Role) Label() string { return roleLabels[r] }

func (r Role) MarshalJSON() ([]byte, error) { return json.Marshal(r.String()) }

// ParseRole reads a role a token can carry: member, operator or admin.
func ParseRole(s string) (Role, error) {
	for i, n := range roleNames {
		if n == s && i > 0 {
			return Role(i), nil
		}
	}
	return Visitor, fmt.Errorf("role %q: use member, operator or admin", s)
}

// Who is the caller.
type Who struct {
	Name string
	Role Role
	// Companies the token covers; nil is every company.
	Companies []string
	// Key names the token (the start of its hash), "" for a visitor.
	Key string
}

// Anonymous is a caller with no token.
var Anonymous = Who{Name: "访客"}

// In reports whether the caller's token covers company.
func (w Who) In(company string) bool {
	return w.Companies == nil || slices.Contains(w.Companies, company)
}

// Everywhere reports whether the token covers every company.
func (w Who) Everywhere() bool { return w.Companies == nil }

// Can reports whether the caller may act at role need on company. Anyone may act as a visitor.
func (w Who) Can(company string, need Role) bool {
	return need == Visitor || (w.Role >= need && w.In(company))
}

// RoleIn is the caller's role on company: their own where the token covers it, a visitor's
// elsewhere.
func (w Who) RoleIn(company string) Role {
	if w.In(company) {
		return w.Role
	}
	return Visitor
}

type key struct {
	who     Who
	sum     [32]byte
	expires time.Time
	// from: legacy | config | issued (in the console, kept in the store)
	from string
}

// Keyring holds the tokens the server accepts: the ones in the config file, which only an edit
// of the file changes, and the ones issued in the console, which take effect at once.
type Keyring struct {
	mu    sync.RWMutex
	keys  []key
	store *Store
	used  map[string]time.Time
}

// NewKeyring reads the configured tokens. legacy is the original single token, kept working
// as an administrator of every company.
func NewKeyring(legacy string, keys []config.Key) (*Keyring, error) {
	k := &Keyring{}
	if legacy != "" {
		k.keys = append(k.keys, key{who: Who{Name: "管理员", Role: Admin, Key: "token"}, sum: sha256.Sum256([]byte(legacy)), from: "legacy"})
	}
	seen := map[string]bool{}
	for _, c := range keys {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return nil, fmt.Errorf("tokens: a token without a name")
		}
		role, err := ParseRole(c.Role)
		if err != nil {
			return nil, fmt.Errorf("token %q: %w", name, err)
		}
		raw, err := hex.DecodeString(strings.TrimSpace(c.SHA256))
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("token %q: sha256 must be 64 hex digits", name)
		}
		var companies []string
		for _, co := range c.Companies {
			if co == "*" {
				companies = nil
				break
			}
			companies = append(companies, co)
		}
		if len(c.Companies) == 0 {
			companies = nil
		}
		var exp time.Time
		if c.Expires != "" {
			day, err := time.ParseInLocation("2006-01-02", c.Expires, time.Local)
			if err != nil {
				return nil, fmt.Errorf("token %q: expires must be YYYY-MM-DD", name)
			}
			exp = day.AddDate(0, 0, 1)
		}
		id := hex.EncodeToString(raw[:4])
		if seen[id] {
			return nil, fmt.Errorf("token %q: the same token is listed twice", name)
		}
		seen[id] = true
		var sum [32]byte
		copy(sum[:], raw)
		k.keys = append(k.keys, key{who: Who{Name: name, Role: role, Companies: companies, Key: id}, sum: sum, expires: exp, from: "config"})
	}
	return k, nil
}

// Empty reports whether no token is accepted at all.
func (k *Keyring) Empty() bool {
	if k == nil {
		return true
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.keys) == 0
}

// Lookup finds the holder of token.
func (k *Keyring) Lookup(token string) (Who, bool) {
	if k == nil || token == "" {
		return Anonymous, false
	}
	sum := sha256.Sum256([]byte(token))
	found := -1
	k.mu.RLock()
	// compare against every key, so the time taken says nothing about which one matched
	for i, c := range k.keys {
		if subtle.ConstantTimeCompare(sum[:], c.sum[:]) == 1 {
			found = i
		}
	}
	var c key
	if found >= 0 {
		c = k.keys[found]
	}
	k.mu.RUnlock()
	if found < 0 {
		return Anonymous, false
	}
	if !c.expires.IsZero() && time.Now().After(c.expires) {
		return Anonymous, false
	}
	k.touch(c)
	return c.who, true
}

// Token is the token a request carries: a bearer token, or ?token= on the URL (the live link
// from a browser cannot set headers).
func Token(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// Of is the caller of an HTTP request.
func (k *Keyring) Of(r *http.Request) Who {
	w, _ := k.Lookup(Token(r))
	return w
}

type ctxKey struct{}

// With carries who through ctx.
func With(ctx context.Context, w Who) context.Context { return context.WithValue(ctx, ctxKey{}, w) }

// From is the caller ctx carries, a visitor if none.
func From(ctx context.Context) Who {
	if w, ok := ctx.Value(ctxKey{}).(Who); ok {
		return w
	}
	return Anonymous
}

// NewToken makes a token and the config entry that accepts it.
func NewToken(name string, role Role, companies []string, expires string) (token string, entry config.Key) {
	b := make([]byte, 24)
	rand.Read(b)
	token = "sf_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	return token, config.Key{Name: name, Role: role.String(), Companies: companies, SHA256: hex.EncodeToString(sum[:]), Expires: expires}
}
