package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// A company's own factory from the start: the first administrator is set up with the
// installation code, people sign in with a password, only operators work the machines, requests
// from other sites riding on the cookie are refused, and a private factory shows visitors nothing.
func TestAccounts(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	if err := eng.Single(); err != nil {
		t.Fatal(err)
	}
	eng.LoadConfigs()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	cfg, _ := json.Marshal(map[string]any{"id": "xinghe", "name": "星河锻造", "short": "星河", "industry": "forging",
		"plants": []any{map[string]any{"id": "p1", "name": "一厂", "lines": []any{map[string]any{"name": "曲轴 {n} 线", "product": "crankshaft", "route": "hot-die-crank"}}}}})
	if r := eng.CreateCompany(cfg); !r.OK {
		t.Fatal(r.Errors)
	}

	data := t.TempDir()
	st, err := auth.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	keys := keyring(t, "")
	if err := keys.Attach(st); err != nil {
		t.Fatal(err)
	}
	code, err := keys.Bootstrap()
	if err != nil || code == "" {
		t.Fatalf("bootstrap: %v", err)
	}
	first := filepath.Join(data, "initial-admin-token")
	os.WriteFile(first, []byte(code+"\n"), 0o600)
	trail, err := audit.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer trail.Close()
	srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keys, Audit: trail, Log: log, FirstToken: first,
		Static: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	browser := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	call := func(c *http.Client, method, path, body string, hdr map[string]string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	proxied := map[string]string{"X-Forwarded-For": "203.0.113.7"}

	// setup from elsewhere needs the installation code
	boss := browser()
	if c, _ := call(boss, "POST", "/api/auth/setup", `{"code":"wrong","name":"王厂长","login":"boss","password":"factory-boss-1"}`, proxied); c != http.StatusForbidden {
		t.Fatalf("setup with a wrong code: %d", c)
	}
	body, _ := json.Marshal(map[string]string{"code": code, "name": "王厂长", "login": "boss", "password": "factory-boss-1"})
	if c, b := call(boss, "POST", "/api/auth/setup", string(body), proxied); c != http.StatusOK {
		t.Fatalf("setup: %d %s", c, b)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("the installation code is still on disk after setup")
	}
	if _, ok := keys.Lookup(code); ok {
		t.Fatal("the installation code still signs in as a token")
	}
	if c, _ := call(browser(), "POST", "/api/auth/setup", string(body), proxied); c != http.StatusConflict {
		t.Fatalf("a second setup: %d", c)
	}

	// the administrator makes accounts
	for _, u := range []string{
		`{"name":"小陈","login":"m001","role":"member","password":"member-pass-1"}`,
		`{"name":"张班长","login":"op01","role":"operator","password":"operator-pass-1"}`,
	} {
		if c, b := call(boss, "POST", "/api/admin/users", u, nil); c != http.StatusOK {
			t.Fatalf("create %s: %d %s", u, c, b)
		}
	}
	signIn := func(login, pw string) *http.Client {
		c := browser()
		if code, b := call(c, "POST", "/api/auth/signin", `{"login":"`+login+`","password":"`+pw+`"}`, nil); code != http.StatusOK {
			t.Fatalf("sign in %s: %d %s", login, code, b)
		}
		return c
	}
	member, operator := signIn("m001", "member-pass-1"), signIn("op01", "operator-pass-1")
	if c, _ := call(member, "GET", "/api/admin/users", "", nil); c != http.StatusForbidden {
		t.Fatalf("a member listed the accounts: %d", c)
	}

	// another site's page riding on the cookie is refused; the console's own requests are not
	if c, _ := call(member, "POST", "/api/auth/password", `{"old":"member-pass-1","next":"member-pass-2"}`, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); c != http.StatusForbidden {
		t.Fatalf("a cross-site request with the cookie: %d", c)
	}
	if c, b := call(member, "POST", "/api/auth/password", `{"old":"member-pass-1","next":"member-pass-2"}`, map[string]string{"Origin": ts.URL, "Sec-Fetch-Site": "same-origin"}); c != http.StatusOK {
		t.Fatalf("changing one's own password: %d %s", c, b)
	}

	// the live link: a member is refused at the machine, an operator is not
	command := func(c *http.Client) map[string]any {
		t.Helper()
		u, _ := http.NewRequest("GET", ts.URL, nil)
		hdr := http.Header{}
		for _, ck := range c.Jar.Cookies(u.URL) {
			hdr.Add("Cookie", ck.String())
		}
		cctx, done := context.WithTimeout(t.Context(), 10*time.Second)
		defer done()
		ws, _, err := websocket.Dial(cctx, strings.Replace(ts.URL, "http", "ws", 1)+"/api/live?company=xinghe", &websocket.DialOptions{HTTPHeader: hdr})
		if err != nil {
			t.Fatal(err)
		}
		defer ws.CloseNow()
		wsjson.Write(cctx, ws, map[string]any{"type": "watch", "company": "xinghe", "plant": "p1"})
		wsjson.Write(cctx, ws, map[string]any{"type": "cmd", "company": "xinghe", "plant": "p1", "asset": "HP-101", "cmd": "stop"})
		for {
			typ, b, err := ws.Read(cctx)
			if err != nil {
				t.Fatal(err)
			}
			if typ != websocket.MessageText {
				continue
			}
			var m map[string]any
			if json.Unmarshal(b, &m) == nil && m["type"] == "result" {
				return m
			}
		}
	}
	if m := command(member); m["ok"] == true || !strings.Contains(m["msg"].(string), "只有操作员") {
		t.Fatalf("a member's command: %v", m)
	}
	if m := command(operator); m["ok"] != true {
		t.Fatalf("an operator's command: %v", m)
	}

	// a private factory: a visitor sees the sign-in and nothing else
	if c, _ := call(browser(), "GET", "/api/tags", "", nil); c != http.StatusOK {
		t.Fatalf("visitors kept out before the switch: %d", c)
	}
	if c, _ := call(boss, "PUT", "/api/admin/settings", `{"mustLogin":true}`, nil); c != http.StatusOK {
		t.Fatalf("must-login: %d", c)
	}
	if c, _ := call(browser(), "GET", "/api/tags", "", nil); c != http.StatusUnauthorized {
		t.Fatalf("a visitor of a private factory: %d", c)
	}
	if c, _ := call(browser(), "GET", "/api/auth/state", "", nil); c != http.StatusOK {
		t.Fatalf("the sign-in itself is kept out: %d", c)
	}
	if c, _ := call(operator, "GET", "/api/tags", "", nil); c != http.StatusOK {
		t.Fatalf("a signed-in operator of a private factory: %d", c)
	}
}
