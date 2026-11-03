package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/plan"
	"smartfactory/internal/tags"
)

// A planner plans the queued orders, moves one, locks it, plans again and publishes: the lines'
// queues then run in the plan's order, not plain earliest-due. Members read the plan; only
// operators and above change it; publishing is in the activity log.
func TestPlanPublish(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	if err := eng.Only([]string{"huaduan"}); err != nil {
		t.Fatal(err)
	}
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
	pst, err := plan.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pst.Close() })
	plans := plan.New(pst, eng, prod, log)

	_, adk := auth.NewToken("厂长", auth.Admin, nil, "")
	ptok, pk := auth.NewToken("计划员", auth.Operator, []string{"huaduan"}, "")
	mtok, mk := auth.NewToken("班长", auth.Member, []string{"huaduan"}, "")
	ts := httptest.NewServer((&Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", adk, pk, mk), Audit: trail, Log: log, Production: prod, Plans: plans}).Handler())
	t.Cleanup(ts.Close)
	do := func(token, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	// the 连杆 lines (p1 L5, L6) make three part numbers; ten orders, the latest due first
	var orders []map[string]any
	parts := []string{"LG-B15", "LG-B20", "LG-V30"}
	for i := range 10 {
		orders = append(orders, map[string]any{"no": fmt.Sprintf("PO-%02d", i), "partNo": parts[i%3], "qty": 300, "due": time.Now().Add(time.Duration(200-i*10) * time.Hour).Format(time.RFC3339)})
	}
	if code, b := do(ptok, "POST", "/api/erp/huaduan/orders", orders); code != 200 {
		t.Fatalf("orders: %d %s", code, b)
	}
	// the lines take the first two
	deadline := time.Now().Add(10 * time.Second)
	for {
		n, _ := store.Counts("huaduan")
		if n[erp.Running] == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lines did not start orders: %v", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
	before, _ := store.Queued("huaduan")

	type planned struct {
		No, Line string
		Seq      int
		Running  bool
		Locked   bool
		Start    time.Time
		End      time.Time
	}
	type planOut struct {
		ID     string
		Result struct {
			Orders []planned
			KPI    plan.KPI
		}
	}
	if code, b := do(mtok, "GET", "/api/plan/huaduan", nil); code != 200 || !strings.Contains(string(b), `"edit": false`) {
		t.Fatalf("member reads: %d %.300s", code, b)
	}
	if code, _ := do(mtok, "POST", "/api/plan/huaduan/replan", map[string]string{"goal": "setup"}); code != 403 {
		t.Fatalf("member re-plans: %d", code)
	}
	code, b := do(ptok, "POST", "/api/plan/huaduan/replan", map[string]string{"goal": "setup"})
	var p planOut
	if code != 200 || json.Unmarshal(b, &p) != nil {
		t.Fatalf("replan: %d %.300s", code, b)
	}
	if len(p.Result.Orders) != 10 {
		t.Fatalf("planned %d orders: %.500s", len(p.Result.Orders), b)
	}
	// move the last order on L6 to the front of L5, and lock it there
	var mv planned
	for _, o := range p.Result.Orders {
		if !o.Running {
			mv = o
		}
	}
	other := "p1/L5"
	if mv.Line == other {
		other = "p1/L6"
	}
	code, b = do(ptok, "POST", "/api/plan/huaduan/move", map[string]any{"no": mv.No, "line": other, "at": 0})
	if code != 200 || json.Unmarshal(b, &p) != nil {
		t.Fatalf("move: %d %s", code, b)
	}
	at := slices.IndexFunc(p.Result.Orders, func(o planned) bool { return o.No == mv.No })
	if at < 0 || p.Result.Orders[at].Line != other || p.Result.Orders[at].Seq != 1 {
		t.Fatalf("moved order: %+v", p.Result.Orders[at])
	}
	if code, b = do(ptok, "POST", "/api/plan/huaduan/lock", map[string]any{"no": mv.No, "lock": true}); code != 200 {
		t.Fatalf("lock: %d %s", code, b)
	}
	json.Unmarshal(b, &p)
	at = slices.IndexFunc(p.Result.Orders, func(o planned) bool { return o.No == mv.No })
	lockedAt := p.Result.Orders[at]
	// plan again: the locked order stays where it is
	code, b = do(ptok, "POST", "/api/plan/huaduan/replan", map[string]string{"goal": "due"})
	if code != 200 || json.Unmarshal(b, &p) != nil {
		t.Fatalf("replan: %d %s", code, b)
	}
	at = slices.IndexFunc(p.Result.Orders, func(o planned) bool { return o.No == mv.No })
	if o := p.Result.Orders[at]; o.Line != lockedAt.Line || !o.Locked || o.Start.Sub(lockedAt.Start).Abs() > time.Minute {
		t.Fatalf("locked order moved: %+v → %+v", lockedAt, o)
	}
	if code, _ := do(mtok, "POST", "/api/plan/huaduan/publish", nil); code != 403 {
		t.Fatalf("member publishes: %d", code)
	}
	code, b = do(ptok, "POST", "/api/plan/huaduan/publish", nil)
	if code != 200 {
		t.Fatalf("publish: %d %s", code, b)
	}
	// the lines' queues are the plan's order
	want := map[string][]string{}
	for _, o := range p.Result.Orders {
		if !o.Running {
			want[o.Line] = append(want[o.Line], o.No)
		}
	}
	got, _ := store.Queued("huaduan")
	for k, nos := range want {
		g := slices.DeleteFunc(slices.Clone(got[k]), func(no string) bool { return !slices.Contains(nos, no) })
		w := slices.DeleteFunc(slices.Clone(nos), func(no string) bool { return !slices.Contains(got[k], no) })
		if !slices.Equal(g, w) {
			t.Errorf("%s queue %v, plan %v", k, got[k], nos)
		}
	}
	if fmt.Sprint(got) == fmt.Sprint(before) {
		t.Errorf("queue unchanged by publishing: %v", got)
	}
	// the ERP sending an order again does not move it off the plan's line
	if code, b := do(ptok, "POST", "/api/erp/huaduan/orders", orders[0]); code != 200 {
		t.Fatalf("resend: %d %s", code, b)
	}
	if o, _ := store.Get("huaduan", "PO-00"); o.Status == erp.Queued && o.Seq == 0 {
		t.Errorf("re-sent order lost its place: %+v", o)
	}
	// what an urgent order would do
	code, b = do(mtok, "POST", "/api/plan/huaduan/rush", map[string]any{"part": "LG-B20", "qty": 500, "due": time.Now().Add(30 * time.Hour).Format("2006-01-02 15:04")})
	if code != 200 || !strings.Contains(string(b), `"pushed"`) {
		t.Fatalf("rush: %d %.400s", code, b)
	}
	// a running order withdrawn after publishing is a reason to plan again
	for _, o := range p.Result.Orders {
		if o.Running {
			if code, b := do(ptok, "DELETE", "/api/erp/huaduan/orders/"+o.No, nil); code != 200 {
				t.Fatalf("cancel: %d %s", code, b)
			}
			// the line lets the order go on its next step
			var b []byte
			for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
				if _, b = do(mtok, "GET", "/api/plan/huaduan", nil); strings.Contains(string(b), "撤回") {
					break
				}
			}
			if !strings.Contains(string(b), "撤回") || !strings.Contains(string(b), o.No) {
				t.Errorf("no re-plan suggestion after %s (to end %s) was withdrawn: %s", o.No, o.End, b[bytes.Index(b, []byte(`"suggest"`)):][:300])
			}
			break
		}
	}
	es, _ := trail.List(t.Context(), "huaduan", 0, 50)
	if !slices.ContainsFunc(es, func(e audit.Entry) bool { return e.Action == "plan.publish" && e.Who == "计划员" }) {
		t.Errorf("publishing not in the activity log")
	}

}
