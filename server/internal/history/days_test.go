package history

import (
	"math"
	"os"
	"strconv"
	"testing"
	"time"
)

// Days adds a company's hours up by local day and source: the lines' OEE parts and output, the
// machines' hours lost; hours outside the span are left out.
//
//	SF_TEST_PG=postgres://user:pass@localhost:port/scratch go test ./internal/history -run TestDays
func TestDays(t *testing.T) {
	dsn := os.Getenv("SF_TEST_PG")
	if dsn == "" {
		t.Skip("set SF_TEST_PG to a scratch Postgres database")
	}
	ctx := t.Context()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	dims := `{"company":{"id":"t","name":"测试"},"plants":[{"id":"p1","name":"一厂","hall":"车间","lines":[{"id":"p1-L1","name":"曲轴 1 线","process":"锻造","product":"crank","idealCycle":30,"orderQty":100,
		"machines":[{"id":"p1-HP-101","code":"HP-101","name":"压力机","kind":"press","seq":1,"cycle":30}]}]}],
		"parts":[{"id":"crank","name":"曲轴","partNo":"QZ","material":"42CrMo","customer":"","family":"crank"}],"customers":[]}`
	if err := st.WriteDims(ctx, dims); err != nil {
		t.Fatal(err)
	}
	write := func(hour string, measured float64, fault float64) {
		t.Helper()
		js := `{"p1":{"machineHours":[{"machine":"p1-HP-101","hour":"` + hour + `","run":3000,"fault":` + ftoa(fault) + `,"idle":` + ftoa(600-fault) + `,"measured":` + ftoa(measured) + `,"cycles":100}],
			"lineHours":[{"line":"p1-L1","hour":"` + hour + `","planned":3600,"run":3000,"produced":100,"good":90,"idealCycle":30,"machineS":3600,"measuredS":` + ftoa(measured) + `}]}}`
		if _, err := st.Write(ctx, js, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	// two simulated hours on the 9th, a measured one on the 10th, one on the 11th outside the span
	write("2026-10-09 08:00:00", 0, 600)
	write("2026-10-09 09:00:00", 0, 0)
	write("2026-10-10 08:00:00", 3600, 300)
	write("2026-10-11 08:00:00", 3600, 0)
	cst := time.FixedZone("CST", 8*3600)
	got, err := st.Days(ctx, time.Date(2026, 10, 9, 0, 0, 0, 0, cst), time.Date(2026, 10, 11, 0, 0, 0, 0, cst))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("days: %+v", got)
	}
	sim, real := got[0], got[1]
	if sim.Day.Format("2006-01-02") != "2026-10-09" || sim.Source != "simulated" || sim.LineHours != 2 || sim.Produced != 200 || sim.Good != 180 ||
		sim.MachineHours != 2 || sim.DownS != 600 || sim.PlannedS != 7200 || math.Abs(sim.GoodIdealS-180*30) > 1e-9 {
		t.Errorf("the 9th: %+v", sim)
	}
	if real.Day.Format("2006-01-02") != "2026-10-10" || real.Source != "measured" || real.LineHours != 1 || real.DownS != 300 {
		t.Errorf("the 10th: %+v", real)
	}
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
