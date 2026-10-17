package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tables"
	"smartfactory/internal/tables/replay"
)

// floorTwin stands in for the engine's tables of a shop: what it has and what it was told.
type floorTwin struct {
	ids    []string
	tables []engine.Table
}

func (f *floorTwin) CompanyIDs() []string { return f.ids }
func (f *floorTwin) TableList(c string) []engine.Table {
	if c != "qianyu" {
		return nil
	}
	return append([]engine.Table{}, f.tables...)
}
func (f *floorTwin) ReportTable(c, plant, table, state string, people int, since time.Time, src string) bool {
	for i, t := range f.tables {
		if t.Plant == plant && t.ID == table {
			f.tables[i].State, f.tables[i].People, f.tables[i].Since, f.tables[i].Source, f.tables[i].Reported = state, people, since.UnixMilli(), src, true
			return true
		}
	}
	return false
}
func (f *floorTwin) TableSessions(string) []engine.SimSession { return nil }

// A shop's tables reported over the API: a reporter token of the company reports a batch, the
// same batch again is kept once, what a camera box missed arrives late and is filed where it
// belongs; the token cannot do anything else (the console, the ERP order API, MCP, another
// company); members cannot report; anyone reads the tables, a table's day and the history; a
// replayed day of a camera box gives the shop measured figures; every answer holds to the protocol.
func TestTableReports(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.NewOffline(log)
	if err != nil {
		t.Fatal(err)
	}
	twin := &floorTwin{ids: eng.CompanyIDs()}
	for i, id := range []string{"A01", "A02", "A03", "A04", "B01", "B02"} {
		twin.tables = append(twin.tables, engine.Table{Plant: "chunxi", PlantName: "春熙路店", Line: "L1", LineName: "大厅 A 区", ID: id, Area: "大厅", Seats: 2 + 2*(i%3), State: tables.Free, Source: tables.Sim})
	}
	svc, err := tables.Open(t.TempDir(), twin, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })

	cam, camK := auth.NewToken("前厅摄像头", auth.Reporter, []string{"qianyu"}, "")
	other, otherK := auth.NewToken("别家摄像头", auth.Reporter, []string{"huaduan"}, "")
	member, memberK := auth.NewToken("店员", auth.Member, []string{"qianyu"}, "")
	op, opK := auth.NewToken("店长", auth.Operator, []string{"qianyu"}, "")
	srv := &Server{Engine: eng, Keys: keyring(t, "", camK, otherK, memberK, opK), Version: "test", Log: log, Tables: svc,
		MCP: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{}")) })}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	call := func(method, path, token string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
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
	now := time.Now()
	ms := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	batch := []map[string]any{
		{"table": "A01", "state": "occupied", "people": 3, "since": ms(-40 * time.Minute), "source": "cam-1"},
		{"table": "A02", "plant": "chunxi", "state": "reserved", "since": now.Add(-5 * time.Minute).Format(time.RFC3339)},
		{"table": "Z99", "state": "free"},
		{"table": "B01", "state": "eating"},
	}
	code, b := call("POST", "/api/report/qianyu/tables", cam, batch)
	if code != 200 {
		t.Fatalf("report: %d %s", code, b)
	}
	std.Check("TableReportResult", b)
	var res tables.Result
	json.Unmarshal(b, &res)
	if res.Accepted != 2 || res.Unknown != 1 || res.Invalid != 1 {
		t.Fatalf("batch: %s", b)
	}
	std.Check("TableReports", mustJSON(batch[:2]))
	// the camera box sends it again after a timeout: nothing changes
	code, b = call("POST", "/api/report/qianyu/tables", cam, map[string]any{"reports": batch[:2]})
	json.Unmarshal(b, &res)
	if code != 200 || res.Duplicate != 2 || res.Accepted != 0 {
		t.Fatalf("again: %d %s", code, b)
	}
	if a := twin.tables[0]; a.State != tables.Occupied || a.People != 3 || a.Source != "cam-1" {
		t.Fatalf("the twin: %+v", a)
	}
	// it had been offline: what it saw an hour ago arrives now
	code, b = call("POST", "/api/report/qianyu/tables", cam, []map[string]any{
		{"table": "A01", "state": "occupied", "people": 2, "since": ms(-120 * time.Minute), "source": "cam-1"},
		{"table": "A01", "state": "cleaning", "since": ms(-60 * time.Minute), "source": "cam-1"},
	})
	json.Unmarshal(b, &res)
	if code != 200 || res.Late != 2 || twin.tables[0].State != tables.Occupied {
		t.Fatalf("late: %d %s %+v", code, b, twin.tables[0])
	}
	code, b = call("GET", "/api/tables/qianyu/chunxi/A01/sessions", "", nil)
	if code != 200 {
		t.Fatalf("a table's day: %d %s", code, b)
	}
	std.Check("TableDay", b)
	var day struct{ Sessions []tables.Session }
	json.Unmarshal(b, &day)
	if len(day.Sessions) != 2 || day.Sessions[0].People != 2 || day.Sessions[1].People != 3 || day.Sessions[1].End != nil {
		t.Fatalf("A01's sessions: %s", b)
	}
	// a POS's events
	code, b = call("POST", "/api/report/qianyu/sessions", op, []map[string]any{
		{"table": "B02", "event": "seated", "people": 4, "at": ms(-30 * time.Minute), "source": "pos"},
		{"table": "B02", "event": "paid", "at": ms(-1 * time.Minute), "source": "pos"},
	})
	if json.Unmarshal(b, &res); code != 200 || res.Accepted != 2 {
		t.Fatalf("POS: %d %s", code, b)
	}
	std.Check("TableEvents", mustJSON([]map[string]any{{"table": "B02", "event": "seated", "people": 4, "at": ms(0)}}))

	// what the reporter token, members and strangers may not do
	for _, c := range []struct {
		method, path, token string
		want                int
	}{
		{"POST", "/api/report/qianyu/tables", "", 401},
		{"POST", "/api/report/qianyu/tables", member, 403},
		{"POST", "/api/report/qianyu/tables", other, 403},
		{"GET", "/api/admin", cam, 403},
		{"GET", "/api/audit/qianyu", cam, 403},
		{"POST", "/mcp", cam, 401},
		{"GET", "/api/tables/qianyu", other, 403},
	} {
		if code, b := call(c.method, c.path, c.token, batch[:1]); code != c.want {
			t.Errorf("%s %s (token %d): %d %s, want %d", c.method, c.path, len(c.token), code, b, c.want)
		}
	}

	code, b = call("GET", "/api/tables/qianyu", "", nil)
	if code != 200 {
		t.Fatalf("tables: %d %s", code, b)
	}
	std.Check("TablesOverview", b)
	var ov tables.Overview
	json.Unmarshal(b, &ov)
	if ov.Total.Tables != 6 || ov.Total.Occupied != 1 || ov.Total.Reserved != 1 || ov.Total.Cleaning != 1 || ov.Total.Source != "mixed" {
		t.Fatalf("overview: %+v", ov.Total)
	}
	code, b = call("GET", "/api/tables/qianyu/chunxi/A01", "", nil)
	if code != 200 {
		t.Fatalf("A01 now: %d %s", code, b)
	}
	std.Check("TableCurrent", b)

	// a camera box's whole day so far on every table: the shop's figures are measured
	reports := replay.Day(replayTables(twin), now, "cam-replay", 7)
	sofar := replay.Until(reports, now)
	r, err := (&replay.Client{Base: ts.URL, Company: "qianyu", Token: cam}).Send(t.Context(), sofar)
	if err != nil {
		t.Fatal(err)
	}
	if r.Invalid+r.Unknown > 0 {
		t.Fatalf("replay: %+v", r)
	}
	code, b = call("GET", "/api/tables/qianyu", "", nil)
	json.Unmarshal(b, &ov)
	if len(sofar) > 0 && (ov.Total.Source != "measured" || ov.Total.ReportedSessions == 0) {
		t.Errorf("after a replayed day: %+v", ov.Total)
	}
	from := tables.DayStart(now).Format("2006-01-02")
	code, b = call("GET", "/api/tables/qianyu/history?by=hour&from="+from, "", nil)
	if code != 200 {
		t.Fatalf("history: %d %s", code, b)
	}
	std.Check("TableHistory", b)
}

func replayTables(f *floorTwin) []replay.Table {
	out := []replay.Table{}
	for _, t := range f.tables {
		out = append(out, replay.Table{Plant: t.Plant, ID: t.ID, Seats: t.Seats})
	}
	return out
}
