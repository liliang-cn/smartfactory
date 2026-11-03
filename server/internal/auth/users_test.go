package auth

import (
	"errors"
	"testing"
	"time"
)

func newKeyring(t *testing.T) (*Keyring, *Store) {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	k, _ := NewKeyring("", nil)
	if err := k.Attach(st); err != nil {
		t.Fatal(err)
	}
	return k, st
}

var admin = Who{Name: "王厂长", Role: Admin, Key: "u:boss", User: "boss"}

// A password is kept hashed, signs in, and a wrong one is refused without saying which part
// was wrong.
func TestPasswordSignIn(t *testing.T) {
	k, st := newKeyring(t)
	if _, err := st.CreateUser("", NewUser{Name: "张班长", Login: "13800000001", Role: "operator", Password: "short"}, false); err == nil {
		t.Fatal("a 5-character password was accepted")
	}
	u, err := st.CreateUser("", NewUser{Name: "张班长", Login: "13800000001", Role: "operator", Password: "correct horse"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	st.db.QueryRow(`SELECT hash FROM users WHERE id = ?`, u.ID).Scan(&hash)
	if hash == "" || hash == "correct horse" {
		t.Fatalf("password kept as %q", hash)
	}
	if _, _, err := k.SignIn("13800000001", "wrong horse", "10.0.0.1", ""); !errors.Is(err, ErrSignIn) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, err := k.SignIn("nobody", "correct horse", "10.0.0.1", ""); !errors.Is(err, ErrSignIn) {
		t.Fatalf("unknown login: %v", err)
	}
	id, _, err := k.SignIn("13800000001", "correct horse", "10.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	who, ok := k.Session(id)
	if !ok || who.Name != "张班长" || who.Role != Operator || who.User != u.ID {
		t.Fatalf("session: %+v %v", who, ok)
	}
	if _, err := st.CreateUser("", NewUser{Name: "重名", Login: "13800000001", Role: "member", Password: "another one"}, false); err == nil {
		t.Fatal("the same login twice")
	}
}

// Five wrong passwords hold a login back, even with the right one; another login is not.
func TestLockout(t *testing.T) {
	k, st := newKeyring(t)
	st.CreateUser("", NewUser{Name: "甲", Login: "a001", Role: "member", Password: "password-a"}, false)
	st.CreateUser("", NewUser{Name: "乙", Login: "b001", Role: "member", Password: "password-b"}, false)
	for range lockPerLogin {
		k.SignIn("a001", "nope-nope", "10.0.0.2", "")
	}
	if _, _, err := k.SignIn("a001", "password-a", "10.0.0.2", ""); !errors.Is(err, ErrLocked) {
		t.Fatalf("after %d wrong tries: %v", lockPerLogin, err)
	}
	if _, _, err := k.SignIn("b001", "password-b", "10.0.0.3", ""); err != nil {
		t.Fatalf("another login held back: %v", err)
	}
	// an address trying many logins is held back too
	for i := range lockPerIP {
		k.SignIn("x"+string(rune('a'+i)), "nope-nope", "10.0.0.9", "")
	}
	if _, _, err := k.SignIn("b001", "password-b", "10.0.0.9", ""); !errors.Is(err, ErrLocked) {
		t.Fatalf("an address after %d failures: %v", lockPerIP, err)
	}
}

// A session ends when it expires, when the person signs out, when the account is disabled and
// when an administrator resets the password.
func TestSessionEnds(t *testing.T) {
	k, st := newKeyring(t)
	u, _ := st.CreateUser("", NewUser{Name: "李质检", Login: "qc01", Role: "inspector", Password: "inspect-me"}, false)
	sign := func() string {
		id, _, err := k.SignIn("qc01", "inspect-me", "10.0.0.4", "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	id := sign()
	st.db.Exec(`UPDATE sessions SET expires = ?`, time.Now().Add(-time.Minute).UnixMilli())
	if _, ok := k.Session(id); ok {
		t.Fatal("an expired session still works")
	}

	id = sign()
	k.SignOut(id)
	if _, ok := k.Session(id); ok {
		t.Fatal("signed out, still in")
	}

	id = sign()
	yes := true
	if _, err := st.UpdateUser(admin, u.ID, UserChange{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, ok := k.Session(id); ok {
		t.Fatal("disabled, still in")
	}
	if _, _, err := k.SignIn("qc01", "inspect-me", "10.0.0.4", ""); err == nil {
		t.Fatal("a disabled account signed in")
	}
	no := false
	st.UpdateUser(admin, u.ID, UserChange{Disabled: &no})

	id = sign()
	pw := "reset-by-boss"
	if _, err := st.UpdateUser(admin, u.ID, UserChange{Password: &pw}); err != nil {
		t.Fatal(err)
	}
	if _, ok := k.Session(id); ok {
		t.Fatal("password reset, old session still in")
	}
	got, _ := st.User(u.ID)
	if !got.MustChange {
		t.Fatal("a reset password should be changed at the next sign-in")
	}
}

// The ladder: an inspector fills in checks but is not an operator; ERP tokens stand with visitors.
func TestInspectorRank(t *testing.T) {
	in := Who{Role: Inspector}
	if !in.Can("a", Inspector) || !in.Can("a", Member) || in.Can("a", Operator) {
		t.Fatal("inspector: fills checks, does not command machines")
	}
	if (Who{Role: Member}).Can("a", Inspector) {
		t.Fatal("a member is not an inspector")
	}
	if (Who{Role: ERP}).Can("a", Member) {
		t.Fatal("an ERP token stands with visitors")
	}
	if r, err := ParseRole("inspector"); err != nil || r != Inspector || r.Label() != "质检员" {
		t.Fatalf("parse inspector: %v %v", r, err)
	}
}
