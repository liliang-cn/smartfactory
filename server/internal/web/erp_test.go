package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// A company's ERP sends production orders with its own token; each goes to a line that makes
// its part, the lines run them, and the ERP hears what started and finished on a signed
// callback. Orders it is not allowed to send, or that no line can make, are refused one by one;
// a pulled ERP's answer is mapped field by field, its credentials never shown back.
func TestERP(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	eng.Single()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	store, err := erp.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	prod := erp.New(store, eng, log)
	prod.Every = 50 * time.Millisecond
	go prod.Run(ctx)

	admin, adk := auth.NewToken("厂长", auth.Admin, nil, "")
	robot, rk := auth.NewToken("ERP 接口", auth.Operator, []string{"jianghe"}, "")
	member, mk := auth.NewToken("计划员", auth.Member, []string{"jianghe"}, "")
	ts := httptest.NewServer((&Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", adk, rk, mk), Audit: trail, Log: log, Production: prod}).Handler())
	t.Cleanup(ts.Close)
	do := func(token, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	order := func(no string) erp.Order {
		t.Helper()
		_, b := do(member, "GET", "/api/erp/jianghe/orders/"+no, nil)
		var o erp.Order
		json.Unmarshal(b, &o)
		return o
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting: %s", what)
			}
		}
	}

	// the factory, from the built-in forging template
	tpl := json.RawMessage(eng.BuiltinTemplate("huaduan"))
	var cfg map[string]any
	json.Unmarshal(tpl, &cfg)
	cfg["id"], cfg["name"], cfg["short"] = "jianghe", "江河锻造", "江河"
	if got, b := do(admin, "POST", "/api/admin/companies", cfg); got != 200 {
		t.Fatalf("build: %d %s", got, b)
	}

	// the ERP's callback, which checks the signature
	var mu sync.Mutex
	var heard []string
	var secret string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(b)
		if r.Header.Get("X-SF-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			http.Error(w, "bad signature", 401)
			heard = append(heard, "bad signature")
			return
		}
		var m struct {
			Event string
			Order erp.Order
		}
		json.Unmarshal(b, &m)
		heard = append(heard, m.Event+" "+m.Order.No)
	}))
	t.Cleanup(hook.Close)

	if got, _ := do(robot, "PUT", "/api/admin/erp/jianghe", map[string]any{"enabled": true}); got != 403 {
		t.Errorf("the ERP's own token set the connection up: %d", got)
	}
	got, b := do(admin, "PUT", "/api/admin/erp/jianghe", map[string]any{"enabled": true, "whenEmpty": "idle", "notify": map[string]any{"url": hook.URL}})
	if got != 200 {
		t.Fatalf("connect: %d %s", got, b)
	}
	var set struct{ Secret string }
	json.Unmarshal(b, &set)
	if !strings.HasPrefix(set.Secret, "whsec_") {
		t.Fatalf("no signing secret given: %s", b)
	}
	mu.Lock()
	secret = set.Secret
	mu.Unlock()
	_, b = do(admin, "GET", "/api/admin/erp/jianghe", nil)
	std.Check("ERPOverview", b)
	if strings.Contains(string(b), set.Secret) {
		t.Errorf("the signing secret is shown back: %s", b)
	}

	// orders: one the connecting rod lines make, one for a named line, and two refused
	got, b = do(robot, "POST", "/api/erp/jianghe/orders", map[string]any{"orders": []any{
		map[string]any{"no": "MO-1", "partNo": "LG-B15", "qty": "2", "due": "2026-10-20", "customer": "远岳动力"},
		map[string]any{"no": "MO-2", "partNo": "NOPE-1", "qty": 1},
		map[string]any{"no": "", "partNo": "LG-B15", "qty": 1},
		map[string]any{"no": "MO-3", "line": "曲轴 1 线", "qty": 5000},
	}})
	std.Check("OrderResults", b)
	var res []erp.Result
	json.Unmarshal(b, &res)
	if got != 200 || len(res) != 4 || !res[0].OK || !strings.HasPrefix(res[0].LineName, "连杆") || res[1].OK || res[2].OK || !res[3].OK || res[3].LineName != "曲轴 1 线" {
		t.Fatalf("orders sent: %d %s", got, b)
	}
	// a batch of the same part spreads over the lines that make it, away from the busy one
	_, b = do(robot, "POST", "/api/erp/jianghe/orders", []any{
		map[string]any{"no": "MO-5", "partNo": "QZ-B15-4", "qty": 100}, map[string]any{"no": "MO-6", "partNo": "QZ-B15-4", "qty": 100},
		map[string]any{"no": "MO-7", "partNo": "QZ-B15-4", "qty": 100},
	})
	json.Unmarshal(b, &res)
	lines := map[string]bool{}
	for _, r := range res {
		lines[r.LineName] = true
	}
	if len(lines) != 3 || lines["曲轴 1 线"] {
		t.Errorf("a batch of one part went to %v", lines)
	}
	if got, _ := do(member, "POST", "/api/erp/jianghe/orders", map[string]any{"no": "MO-4", "partNo": "LG-B15", "qty": 1}); got != 403 {
		t.Errorf("a member sent an order: %d", got)
	}
	if got, _ := do("", "GET", "/api/erp/jianghe/orders", nil); got != 401 {
		t.Errorf("orders read without a token: %d", got)
	}

	// the lines take them, run them and say so
	waitFor("MO-1 running", func() bool { return order("MO-1").Status == erp.Running })
	waitFor("MO-3 running", func() bool { return order("MO-3").Status == erp.Running })
	if !strings.Contains(eng.Frame("jianghe"), `"job":["MO-3"`) {
		t.Fatal("the twin does not show the line running MO-3")
	}
	waitFor("MO-1 done", func() bool { eng.FastForward("jianghe", 120); return order("MO-1").Status == erp.Done })
	if o := order("MO-1"); o.Good < 2 || o.Finished == nil || o.Customer != "远岳动力" {
		t.Errorf("finished order: %+v", o)
	}
	_, b = do(member, "GET", "/api/erp/jianghe/orders", nil)
	std.Check("Orders", b)

	// withdrawn while running: the line drops it
	if got, b := do(robot, "DELETE", "/api/erp/jianghe/orders/MO-3", nil); got != 200 {
		t.Fatalf("withdraw: %d %s", got, b)
	}
	if got, _ := do(robot, "DELETE", "/api/erp/jianghe/orders/MO-1", nil); got != 409 {
		t.Errorf("withdrew a finished order: %d", got)
	}
	waitFor("signed callbacks", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(strings.Join(heard, ","), "order.done MO-1")
	})
	mu.Lock()
	if all := strings.Join(heard, ","); !strings.Contains(all, "order.started MO-1") || !strings.Contains(all, "order.started MO-3") || strings.Contains(all, "bad signature") {
		t.Errorf("callbacks: %s", all)
	}
	mu.Unlock()
	eng.FastForward("jianghe", 60)
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(eng.Frame("jianghe"), `"job":["MO-3"`) {
		t.Error("the line still runs the withdrawn order")
	}
	if o := order("MO-3"); o.Status != erp.Cancelled {
		t.Errorf("withdrawn order: %+v", o)
	}

	// an ERP that is pulled: rows as arrays, behind a key
	erpAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer erp-key" {
			http.Error(w, "who are you", 401)
			return
		}
		io.WriteString(w, `{"data":{"rows":[["MO-9","TQ-300-N","5","2026-10-21 08:00"],["MO-10","ZZ-9","1",""]]}}`)
	}))
	t.Cleanup(erpAPI.Close)
	pull := map[string]any{"url": erpAPI.URL, "headers": map[string]string{"Authorization": "Bearer erp-key"}, "items": "data.rows",
		"fields": map[string]string{"no": "0", "partNo": "1", "qty": "2", "due": "3"}}
	if got, b := do(admin, "PUT", "/api/admin/erp/jianghe", map[string]any{"enabled": true, "pull": pull, "notify": map[string]any{"url": hook.URL}}); got != 200 || strings.Contains(string(b), "whsec_") {
		t.Fatalf("pull set up: %d %s", got, b)
	}
	got, b = do(admin, "POST", "/api/admin/erp/jianghe/pull?dry=1", nil)
	std.Check("PullResult", b)
	var dry erp.PullResult
	json.Unmarshal(b, &dry)
	if got != 200 || dry.Fetched != 2 || !strings.Contains(dry.Items[0].LineName, "套圈") || dry.Items[1].Error == "" || order("MO-9").No != "" {
		t.Fatalf("dry pull: %d %s", got, b)
	}
	_, b = do(admin, "GET", "/api/admin/erp/jianghe", nil)
	if strings.Contains(string(b), "erp-key") {
		t.Errorf("the ERP's key is shown back: %s", b)
	}
	// saved again without the key's value: it is kept
	pull["headers"] = map[string]string{"Authorization": ""}
	do(admin, "PUT", "/api/admin/erp/jianghe", map[string]any{"enabled": true, "pull": pull})
	got, b = do(admin, "POST", "/api/admin/erp/jianghe/pull", nil)
	var pulled erp.PullResult
	json.Unmarshal(b, &pulled)
	if got != 200 || pulled.Created != 1 || pulled.Failed != 1 {
		t.Fatalf("pull: %d %s", got, b)
	}
	if o := order("MO-9"); o.Source != "pull" || o.Qty != 5 || o.Due == nil || o.Due.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04") != "2026-10-21 08:00" {
		t.Errorf("pulled order: %+v", o)
	}

	entries, _ := trail.List(t.Context(), "jianghe", 0, 50)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action+"/"+e.Via)
	}
	for _, want := range []string{"erp.config/page", "erp.orders/api", "erp.cancel/api", "erp.pull/page"} {
		if !strings.Contains(strings.Join(actions, ","), want) {
			t.Errorf("not in the activity log: %s (have %v)", want, actions)
		}
	}
}
