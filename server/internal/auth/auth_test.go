package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"smartfactory/internal/config"
)

func TestKeyring(t *testing.T) {
	tok, entry := NewToken("张工", Operator, []string{"huaduan"}, "")
	old, gone := NewToken("老王", Admin, nil, time.Now().AddDate(0, 0, -2).Format("2006-01-02"))
	k, err := NewKeyring("legacy-token", []config.Key{entry, gone})
	if err != nil {
		t.Fatal(err)
	}
	w, ok := k.Lookup(tok)
	if !ok || w.Name != "张工" || w.Role != Operator || w.Everywhere() || w.Key != entry.SHA256[:8] {
		t.Fatalf("lookup = %+v, %v", w, ok)
	}
	if !w.Can("huaduan", Operator) || w.Can("huaduan", Admin) || w.Can("tiankong", Member) || !w.Can("tiankong", Visitor) {
		t.Errorf("an operator of huaduan: wrong permissions")
	}
	if w.RoleIn("tiankong") != Visitor {
		t.Errorf("outside its companies a token is a visitor's")
	}
	if a, ok := k.Lookup("legacy-token"); !ok || a.Role != Admin || !a.Everywhere() {
		t.Errorf("the original token must stay an administrator of every company: %+v", a)
	}
	if _, ok := k.Lookup(old); ok {
		t.Error("an expired token still works")
	}
	if _, ok := k.Lookup("sf_guess"); ok {
		t.Error("an unknown token works")
	}

	r := httptest.NewRequest("GET", "/api/live?token="+tok, nil)
	if k.Of(r).Name != "张工" {
		t.Error("?token= is not read")
	}
	r = httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if k.Of(r).Name != "张工" {
		t.Error("the bearer token is not read")
	}
	if k.Of(httptest.NewRequest("GET", "/x", nil)).Role != Visitor {
		t.Error("no token must be a visitor")
	}
}

func TestKeyringRejectsBadEntries(t *testing.T) {
	_, good := NewToken("a", Member, nil, "")
	for name, c := range map[string]config.Key{
		"no name":      {Role: "member", SHA256: good.SHA256},
		"visitor role": {Name: "a", Role: "visitor", SHA256: good.SHA256},
		"short hash":   {Name: "a", Role: "member", SHA256: "abcd"},
		"bad date":     {Name: "a", Role: "member", SHA256: good.SHA256, Expires: "next week"},
	} {
		if _, err := NewKeyring("", []config.Key{c}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewKeyring("", []config.Key{good, good}); err == nil {
		t.Error("the same token twice: accepted")
	}
	k, _ := NewKeyring("", []config.Key{{Name: "all", Role: "admin", SHA256: good.SHA256, Companies: []string{"huaduan", "*"}}})
	if w, _ := k.Lookup("x"); w.Key != "" {
		t.Error("lookup of a wrong token returned a key")
	}
	if !k.keys[0].who.Everywhere() {
		t.Error(`"*" among the companies means every company`)
	}
}
