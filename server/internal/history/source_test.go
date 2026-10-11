package history

import (
	"math"
	"os"
	"testing"
	"time"

	semantic "github.com/liliang-cn/semantic-go"
)

// A history database from before rows said where they came from takes the new columns, its old
// rows reading as simulated; new rows are measured, simulated or mixed by the seconds (or
// readings) the equipment reported, added up across the writes that make up one hour.
//
//	SF_TEST_PG=postgres://user:pass@localhost:port/scratch go test ./internal/history -run TestSourceColumns
func TestSourceColumns(t *testing.T) {
	dsn := os.Getenv("SF_TEST_PG")
	if dsn == "" {
		t.Skip("set SF_TEST_PG to a scratch Postgres database")
	}
	ctx := t.Context()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	// the database as it was: no source columns
	if _, err := st.db.Exec(ctx, `
		ALTER TABLE machine_hours DROP COLUMN IF EXISTS source, DROP COLUMN IF EXISTS measured_share, DROP COLUMN IF EXISTS measured_s;
		ALTER TABLE line_hours DROP COLUMN IF EXISTS source, DROP COLUMN IF EXISTS measured_share, DROP COLUMN IF EXISTS measured_s, DROP COLUMN IF EXISTS machine_s;
		ALTER TABLE quality_hours DROP COLUMN IF EXISTS source, DROP COLUMN IF EXISTS measured_share, DROP COLUMN IF EXISTS measured_n`); err != nil {
		t.Fatal(err)
	}
	dims := `{"company":{"id":"t","name":"测试"},"plants":[{"id":"p1","name":"一厂","hall":"车间","lines":[{"id":"p1-L1","name":"曲轴 1 线","process":"锻造","product":"crank","idealCycle":30,"orderQty":100,
		"machines":[{"id":"p1-HP-101","code":"HP-101","name":"压力机","kind":"press","seq":1,"cycle":30},{"id":"p1-HP-102","code":"HP-102","name":"压力机","kind":"press","seq":2,"cycle":30,"feature":"厚度","unit":"mm"}]}]}],
		"parts":[{"id":"crank","name":"曲轴","partNo":"QZ","material":"42CrMo","customer":"","family":"crank"}],"customers":[]}`
	if err := st.WriteDims(ctx, dims); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	if _, err := st.db.Exec(ctx, `INSERT INTO calendar VALUES ($1,'2026-10-11','早班')`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(ctx, `INSERT INTO machine_hours (machine_id, hour, run_s, idle_s, blocked_s, fault_s, maint_s, changeover_s, stopped_s, cycles, scrap, rework, energy_kwh, fuel)
		VALUES ('p1-HP-101', $1, 3600, 0, 0, 0, 0, 0, 0, 120, 0, 0, 10, 0)`, old); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// the next start migrates it
	if st, err = Open(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer st.Close()
	hour := `"2026-10-11 09:00:00"`
	write := func(js string) {
		t.Helper()
		if _, err := st.Write(ctx, js, time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	// HP-101 reported the whole hour, HP-102 half of it; the line has 7200 machine-seconds, 5400 measured
	write(`{"p1":{"machineHours":[
		{"machine":"p1-HP-101","hour":` + hour + `,"run":3000,"idle":600,"measured":3600,"cycles":100},
		{"machine":"p1-HP-102","hour":` + hour + `,"run":1800,"measured":1800,"cycles":60}],
		"lineHours":[{"line":"p1-L1","hour":` + hour + `,"planned":1800,"run":1500,"machineS":3600,"measuredS":2700,"idealCycle":30}],
		"quality":[{"machine":"p1-HP-102","hour":` + hour + `,"feature":"厚度","unit":"mm","n":100,"mean":22,"sd":0.1,"min":21.8,"max":22.2,"out":0,"measured":40}]}}`)
	// a restart in the hour: the rest of it, HP-102 simulated
	write(`{"p1":{"machineHours":[{"machine":"p1-HP-102","hour":` + hour + `,"run":1800,"measured":0,"cycles":60}],
		"lineHours":[{"line":"p1-L1","hour":` + hour + `,"planned":1800,"run":1500,"machineS":3600,"measuredS":2700,"idealCycle":30}]}}`)

	type row struct {
		source string
		share  float64
	}
	get := func(q string, args ...any) row {
		t.Helper()
		var r row
		if err := st.db.QueryRow(ctx, q, args...).Scan(&r.source, &r.share); err != nil {
			t.Fatal(q, err)
		}
		return r
	}
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	check := func(what string, got row, source string, share float64) {
		t.Helper()
		if got.source != source || math.Abs(got.share-share) > 1e-9 {
			t.Errorf("%s: %s %.4f, want %s %.4f", what, got.source, got.share, source, share)
		}
	}
	check("a row from before", get(`SELECT source, measured_share FROM machine_hours WHERE machine_id='p1-HP-101' AND hour=$1`, old), "simulated", 0)
	check("a machine reported all hour", get(`SELECT source, measured_share FROM machine_hours WHERE machine_id='p1-HP-101' AND hour=$1`, at), "measured", 1)
	check("a machine reported half the hour", get(`SELECT source, measured_share FROM machine_hours WHERE machine_id='p1-HP-102' AND hour=$1`, at), "mixed", 0.5)
	check("the line", get(`SELECT source, measured_share FROM line_hours WHERE line_id='p1-L1' AND hour=$1`, at), "mixed", 0.75)
	check("the inspection", get(`SELECT source, measured_share FROM quality_hours WHERE machine_id='p1-HP-102' AND hour=$1`, at), "mixed", 0.4)

	// what the advisor asks of these columns runs on the migrated database
	m, err := semantic.Load(Model)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []semantic.Query{
		{Metrics: []string{"oee", "line_reported_share"}, GroupBy: []string{"line_name", "line_data_source"}},
		{Metrics: []string{"machine_reported_share"}, GroupBy: []string{"machine", "machine_data_source"}},
		{Metrics: []string{"measure_mean", "inspection_reported_share"}, GroupBy: []string{"feature"},
			Where: []semantic.Filter{{Dimension: "quality_data_source", Op: "=", Values: []any{"mixed"}}}},
	} {
		c, err := semantic.Compile(m, q, semantic.Postgres{})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := st.db.Query(ctx, c.SQL, c.Args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", q.Metrics, err, c.SQL)
		}
		n := 0
		for rows.Next() {
			n++
		}
		if err := rows.Err(); err != nil || n == 0 {
			t.Errorf("%v: %d rows, %v", q.Metrics, n, err)
		}
		rows.Close()
	}
}
