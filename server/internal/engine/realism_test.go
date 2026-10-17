package engine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The places as they run: the shops' day of guests, the kitchen delivering to them, the campsite,
// and the factories' shifts, breaks and material deliveries.

// shanghai is the plants' clock.
var shanghai = time.FixedZone("CST", 8*3600)

// A Saturday and a Tuesday in October 2026.
var (
	saturday = time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai)
	tuesday  = time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai)
)

// at starts a company's plants at a local wall-clock moment (Asia/Shanghai).
func at(t *testing.T, e *Engine, c string, when time.Time) {
	t.Helper()
	if _, err := e.call("setClock", c, float64(when.UnixMilli())); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, e *Engine, c string, d time.Duration, dt float64) {
	t.Helper()
	if !e.Advance(c, d.Seconds(), dt) {
		t.Fatal("advance")
	}
}

type figure struct {
	ID, Label, Text, Sub string
	Value                float64
}

type place struct {
	ID, Area, State, Source string
	Seats, People, Minutes  int
}

type plantSnap struct {
	ID   string
	Kpis struct {
		Clock string
		Oee   float64
	}
	Figures []figure
	Places  []place
	Queue   []struct{ People, Minutes int }
	Venue   *struct {
		Places, Occupied, Queue int
		Occupancy, Turnover     float64
		Ticket, Rating          float64
		Day                     map[string]float64
	}
	Depot *struct {
		Otif, Deliveries, Samples, Wasted, Stock float64
		Batches                                  []struct {
			No, Product string
			Qty         int
			HoursLeft   float64
		}
	}
	Incoming []struct {
		No    string
		Items map[string]int
	}
	Inventory []struct {
		Key       string
		Available int
		Capacity  int
	}
	Inbound *struct {
		Supplier string
		Late     bool
	}
	Shift, Pause string
	Lines        []struct {
		Name        string
		Oee         float64
		Good, Scrap int
	}
	Assets []struct {
		ID, State string
	}
}

func snapOf(t *testing.T, e *Engine, c string) map[string]plantSnap {
	t.Helper()
	var s struct{ Plants []plantSnap }
	if v, err := e.call("snapshot", c); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal([]byte(v.String()), &s); err != nil {
		t.Fatal(err)
	}
	out := map[string]plantSnap{}
	for _, p := range s.Plants {
		out[p.ID] = p
	}
	return out
}

func (p plantSnap) stock(key string) int {
	for _, i := range p.Inventory {
		if i.Key == key {
			return i.Available
		}
	}
	return -1
}

func (p plantSnap) figure(id string) figure {
	for _, f := range p.Figures {
		if f.ID == id {
			return f
		}
	}
	return figure{}
}

// logOf is a plant's recent log (newest first).
func logOf(t *testing.T, e *Engine, c, plant string) []string {
	t.Helper()
	v, err := e.call("plantFrame", c, plant)
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Log []struct{ Text string } }
	json.Unmarshal([]byte(v.String()), &f)
	out := make([]string, len(f.Log))
	for i, x := range f.Log {
		out[i] = x.Text
	}
	return out
}

func offline(t *testing.T, companies ...string) *Engine {
	t.Helper()
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(discard())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Only(companies); err != nil {
		t.Fatal(err)
	}
	return e
}

// Guests come by the day's curve: lunch and dinner fill the shop, the afternoon is quiet, a
// Saturday brings more than a Tuesday; at the peak a queue forms at the door and some give up.
func TestShopDay(t *testing.T) {
	if testing.Short() {
		t.Skip("a day of plant time")
	}
	seatedBy := func(day time.Time) (hours map[int]float64, peakQueue int, gaveUp float64, s plantSnap) {
		e := offline(t, "qianyu")
		at(t, e, "qianyu", day.Add(10*time.Hour))
		hours = map[int]float64{}
		last := 0.0
		for h := 10; h < 23; h++ {
			run(t, e, "qianyu", time.Hour, 2)
			s = snapOf(t, e, "qianyu")["chunxi"]
			hours[h] = s.Venue.Day["sessions"] - last
			last = s.Venue.Day["sessions"]
			peakQueue = max(peakQueue, s.Venue.Queue)
		}
		return hours, peakQueue, s.Venue.Day["balked"] + s.Venue.Day["reneged"], s
	}
	sat, satQueue, satGaveUp, s := seatedBy(saturday)
	tue, _, _, _ := seatedBy(tuesday)
	t.Logf("seated by hour, Saturday %v, Tuesday %v; queue at most %d, %v gave up", sat, tue, satQueue, satGaveUp)
	// before opening nobody; lunch and dinner beat mid-afternoon
	if sat[10] != 0 {
		t.Errorf("%v groups seated 10–11, before the shop opens", sat[10])
	}
	if sat[12] <= sat[15] || sat[18]+sat[19] <= sat[15]+sat[16] {
		t.Errorf("lunch %v / dinner %v should beat the afternoon %v", sat[12], sat[18]+sat[19], sat[15]+sat[16])
	}
	sum := func(m map[int]float64) (n float64) {
		for _, v := range m {
			n += v
		}
		return
	}
	if sum(sat) <= sum(tue)*1.15 {
		t.Errorf("Saturday seated %v, Tuesday %v: the weekend should be busier", sum(sat), sum(tue))
	}
	if satQueue == 0 || satGaveUp == 0 {
		t.Errorf("a Saturday dinner with no queue (%d) or nobody giving up (%v)", satQueue, satGaveUp)
	}
	if satQueue > 2*6+1 {
		t.Errorf("queue of %d groups: newcomers should walk away well before that", satQueue)
	}

	// the figures come from the guests: 翻台率 is sessions per table, 上座率 occupied tables, 客单价 revenue per person
	occupied := 0
	for _, p := range s.Places {
		if p.State == "occupied" {
			occupied++
		}
	}
	v := s.Venue
	if v.Places != len(s.Places) || v.Places != 16 {
		t.Fatalf("places %d / %d, want the 16 tables", v.Places, len(s.Places))
	}
	if got := s.figure("occupancy").Value; abs(got-float64(occupied)/16) > 0.07 {
		t.Errorf("上座率 %v, %d of 16 tables occupied", got, occupied)
	}
	if got := s.figure("turnover").Value; abs(got-v.Day["sessions"]/16) > 0.01 {
		t.Errorf("翻台率 %v, %v sessions at 16 tables", got, v.Day["sessions"])
	}
	if got := s.figure("ticket").Value; v.Day["persons"] == 0 || abs(got-v.Day["revenue"]/v.Day["persons"]) > 1 {
		t.Errorf("客单价 %v, revenue %v from %v people", got, v.Day["revenue"], v.Day["persons"])
	}
	if t2 := s.figure("ticket").Value; t2 < 40 || t2 > 120 {
		t.Errorf("客单价 %v: a 串串 meal is 40–120 元 a head", t2)
	}
	if v.Day["takeaway"] == 0 {
		t.Error("no delivery orders picked up all day")
	}
}

// The kitchen's van leaves at the delivery hour with what each shop is short of, oldest batches
// first; the shop's stock goes up by what it brought when it arrives.
func TestKitchenDeliversToShops(t *testing.T) {
	if testing.Short() {
		t.Skip("hours of plant time")
	}
	e := offline(t, "qianyu")
	at(t, e, "qianyu", saturday.Add(13*time.Hour+50*time.Minute))
	run(t, e, "qianyu", 30*time.Minute, 1)
	before := snapOf(t, e, "qianyu")
	kitchen := before["kitchen"].Depot
	if kitchen == nil || len(kitchen.Batches) == 0 || kitchen.Samples == 0 {
		t.Fatalf("the kitchen has no batches or no samples in its store: %+v", kitchen)
	}
	stockOf := func(d *struct {
		Otif, Deliveries, Samples, Wasted, Stock float64
		Batches                                  []struct {
			No, Product string
			Qty         int
			HoursLeft   float64
		}
	}, product string) (n int) {
		for _, b := range d.Batches {
			if b.Product == product {
				n += b.Qty
			}
		}
		return
	}
	// the store is kept oldest first: the van takes from the front
	oldest, oldestQty := "", 0
	for _, b := range kitchen.Batches {
		if b.Product == "beef-skewer" || b.Product == "lotus-skewer" {
			oldest, oldestQty = b.No, b.Qty
			break
		}
	}
	skewers := stockOf(kitchen, "beef-skewer") + stockOf(kitchen, "lotus-skewer")
	// past the 14:30 departure: both shops have a delivery on the road
	run(t, e, "qianyu", 15*time.Minute, 1)
	mid := snapOf(t, e, "qianyu")
	sent := 0
	for _, shop := range []string{"chunxi", "yulin"} {
		if len(mid[shop].Incoming) != 1 {
			t.Fatalf("%s: %d deliveries on the road after the 14:30 departure", shop, len(mid[shop].Incoming))
		}
		sent += mid[shop].Incoming[0].Items["skewers"]
	}
	left := stockOf(mid["kitchen"].Depot, "beef-skewer") + stockOf(mid["kitchen"].Depot, "lotus-skewer")
	// what left the store is what went on the vans (the kitchen goes on making meanwhile)
	if sent == 0 || skewers-left > sent {
		t.Errorf("skewers in the kitchen %d → %d, %d sent", skewers, left, sent)
	}
	for _, b := range mid["kitchen"].Depot.Batches {
		if b.No == oldest && sent >= oldestQty {
			t.Errorf("batch %s, the oldest (%d), is still in the store after %d went out", oldest, oldestQty, sent)
		}
	}
	want := mid["chunxi"].Incoming[0].Items["skewers"]
	had := mid["chunxi"].stock("skewers")
	run(t, e, "qianyu", 2*time.Hour, 1)
	after := snapOf(t, e, "qianyu")
	if len(after["chunxi"].Incoming) != 0 {
		t.Fatalf("the delivery has not arrived an hour later")
	}
	if got := after["chunxi"].stock("skewers"); got < had+want/2 {
		t.Errorf("chunxi skewers %d → %d with %d delivered", had, got, want)
	}
	if f := after["kitchen"].figure("onTime"); f.ID == "" || after["kitchen"].Depot.Deliveries < 2 {
		t.Errorf("准时配送率 not worked out from the deliveries: %+v %+v", f, after["kitchen"].Depot)
	}
	if f := after["kitchen"].figure("coldChain"); f.Value < 0.5 || f.Value > 1 {
		t.Errorf("冷链达标率 %v", f.Value)
	}
}

// A table a camera or the POS reports on follows the reports; the simulation seats nobody there.
func TestReportedTable(t *testing.T) {
	e := offline(t, "qianyu")
	at(t, e, "qianyu", saturday.Add(15*time.Hour))
	since := saturday.Add(15 * time.Hour).UnixMilli()
	if v, _ := e.call("reportTable", "qianyu", "chunxi", "A03", "occupied", 5.0, float64(since), "cam-1"); !v.ToBoolean() {
		t.Fatal("report refused")
	}
	if v, _ := e.call("reportTable", "qianyu", "chunxi", "Z99", "occupied", 2.0, float64(since), "cam-1"); v.ToBoolean() {
		t.Error("a report for a table the shop does not have was taken")
	}
	run(t, e, "qianyu", 20*time.Minute, 1)
	s := snapOf(t, e, "qianyu")["chunxi"]
	for _, p := range s.Places {
		if p.ID == "A03" && (p.State != "occupied" || p.People != 5 || p.Source != "cam-1") {
			t.Errorf("A03 after the report: %+v", p)
		}
	}
	// an older report does not move it back; a newer one does
	e.call("reportTable", "qianyu", "chunxi", "A03", "free", 0.0, float64(since-60_000), "cam-1")
	e.call("reportTable", "qianyu", "chunxi", "B01", "free", 0.0, float64(since+60_000), "pos")
	run(t, e, "qianyu", time.Minute, 1)
	var list []Table
	v, _ := e.call("tables", "qianyu")
	json.Unmarshal([]byte(v.String()), &list)
	reported := 0
	for _, x := range list {
		if x.Reported {
			reported++
		}
		if x.ID == "A03" && x.Plant == "chunxi" && x.State != "occupied" {
			t.Errorf("a late report moved A03 to %s", x.State)
		}
	}
	if reported != 2 {
		t.Errorf("%d tables reported, want A03 and B01", reported)
	}
	if f := snapOf(t, e, "qianyu")["chunxi"].Figures; len(f) == 0 {
		t.Fatal("no figures")
	}
	// the simulated sessions stay with the tables nobody reports
	var sessions []SimSession
	v, _ = e.call("tableSessions", "qianyu")
	json.Unmarshal([]byte(v.String()), &sessions)
	for _, x := range sessions {
		if x.Plant == "chunxi" && x.Table == "B01" && x.Start > since+60_000 {
			t.Errorf("the simulation seated guests at B01 after it was reported free: %+v", x)
		}
	}
}

// What a restart must not lose: the queue, who sits where, the kitchen's store and the vans on the road.
func TestVenueSaveRestore(t *testing.T) {
	e := offline(t, "qianyu")
	at(t, e, "qianyu", saturday.Add(14*time.Hour+20*time.Minute))
	run(t, e, "qianyu", 20*time.Minute, 1)
	saved := e.SaveJSON("qianyu")
	a := snapOf(t, e, "qianyu")

	f := offline(t, "qianyu")
	if r := f.RestoreJSON("qianyu", saved); !strings.Contains(r, `"restored":["kitchen","chunxi","yulin"]`) {
		t.Fatalf("restore: %s", r)
	}
	b := snapOf(t, f, "qianyu")
	for _, id := range []string{"chunxi", "yulin"} {
		if len(a[id].Queue) != len(b[id].Queue) || len(a[id].Incoming) != len(b[id].Incoming) {
			t.Errorf("%s: queue %d → %d, deliveries %d → %d", id, len(a[id].Queue), len(b[id].Queue), len(a[id].Incoming), len(b[id].Incoming))
		}
		for i := range a[id].Places {
			if x, y := a[id].Places[i], b[id].Places[i]; x.State != y.State || x.People != y.People {
				t.Errorf("%s %s: %+v → %+v", id, x.ID, x, y)
			}
		}
		if a[id].Venue.Day["sessions"] != b[id].Venue.Day["sessions"] {
			t.Errorf("%s: today's sessions %v → %v", id, a[id].Venue.Day["sessions"], b[id].Venue.Day["sessions"])
		}
	}
	if len(a["kitchen"].Depot.Batches) != len(b["kitchen"].Depot.Batches) || a["kitchen"].Depot.Samples != b["kitchen"].Depot.Samples {
		t.Errorf("kitchen store %d batches / %v samples → %d / %v", len(a["kitchen"].Depot.Batches), a["kitchen"].Depot.Samples, len(b["kitchen"].Depot.Batches), b["kitchen"].Depot.Samples)
	}
	// and the groups carry on: those at tables leave in their own time, not all at once
	run(t, f, "qianyu", time.Minute, 1)
	if got := snapOf(t, f, "qianyu")["chunxi"].Venue.Occupied; got < a["chunxi"].Venue.Occupied-3 {
		t.Errorf("occupied tables %d before the restart, %d a minute after", a["chunxi"].Venue.Occupied, got)
	}
}

// The campsite lets pitches and tents, fills in the afternoon and empties at check-out.
func TestCampsite(t *testing.T) {
	if testing.Short() {
		t.Skip("a day of plant time")
	}
	e := offline(t, "tiankong")
	at(t, e, "tiankong", saturday.Add(9*time.Hour))
	run(t, e, "tiankong", 9*time.Hour, 2)
	evening := snapOf(t, e, "tiankong")["camp"]
	run(t, e, "tiankong", 18*time.Hour, 2) // to 12:00 on Sunday
	noon := snapOf(t, e, "tiankong")["camp"]
	tents := func(p plantSnap) int {
		for _, i := range p.Inventory {
			if i.Key == "tent" {
				return i.Capacity - i.Available
			}
		}
		return -1
	}
	t.Logf("18:00 Saturday %d/%d pitches, %d tents out; 12:00 Sunday %d pitches, %d tents, checked out %v", evening.Venue.Occupied, evening.Venue.Places, tents(evening), noon.Venue.Occupied, tents(noon), noon.Venue.Day["checkouts"])
	if evening.Venue.Places != 20 || evening.Venue.Occupied < 15 {
		t.Errorf("a Saturday evening with %d of %d pitches let", evening.Venue.Occupied, evening.Venue.Places)
	}
	if tents(evening) < evening.Venue.Occupied-1 || tents(evening) > 20 {
		t.Errorf("%d tents out for %d pitches let", tents(evening), evening.Venue.Occupied)
	}
	if noon.Venue.Day["checkouts"] < float64(evening.Venue.Occupied)/2 || noon.Venue.Occupied > evening.Venue.Occupied {
		t.Errorf("Sunday noon: %v checked out, %d still on their pitches", noon.Venue.Day["checkouts"], noon.Venue.Occupied)
	}
	if f := evening.figure("inUse"); f.Text == "" || !strings.Contains(f.Label, "帐篷") {
		t.Errorf("帐篷在用 figure: %+v", f)
	}
}

// The factory's day: at the shift change the lines slow while the crews hand over and the note
// goes into the log; a meal break is a planned stop OEE does not count against the line.
func TestShiftHandover(t *testing.T) {
	if testing.Short() {
		t.Skip("hours of plant time")
	}
	e := offline(t, "huaduan")
	good := func() (n int) {
		for _, l := range snapOf(t, e, "huaduan")["p1"].Lines {
			n += l.Good
		}
		return
	}
	at(t, e, "huaduan", tuesday.Add(18*time.Hour))
	run(t, e, "huaduan", 50*time.Minute, 1)
	g0 := good()
	run(t, e, "huaduan", 20*time.Minute, 1) // 18:50–19:10, a normal stretch
	g1 := good()
	run(t, e, "huaduan", 50*time.Minute, 1) // to 20:00
	g2 := good()
	run(t, e, "huaduan", 20*time.Minute, 1) // 20:00–20:20, the handover
	g3 := good()
	t.Logf("p1 made %d in a normal 20 minutes, %d over the handover", g1-g0, g3-g2)
	if g3-g2 >= g1-g0 {
		t.Errorf("the handover 20 minutes made %d, a normal 20 minutes %d", g3-g2, g1-g0)
	}
	note := false
	for _, x := range logOf(t, e, "huaduan", "p1") {
		if strings.HasPrefix(x, "交接班 白班→夜班") {
			note = true
		}
	}
	if !note {
		t.Error("no handover note in the log")
	}
	if s := snapOf(t, e, "huaduan")["p1"]; s.Shift != "夜班" {
		t.Errorf("shift after 20:00: %q", s.Shift)
	}
	// lunch: the lines stop taking in steel, OEE holds
	at(t, e, "huaduan", tuesday.Add(11*time.Hour+25*time.Minute))
	run(t, e, "huaduan", 4*time.Minute, 1)
	before := snapOf(t, e, "huaduan")["p1"].Kpis.Oee
	run(t, e, "huaduan", 30*time.Minute, 1)
	during := snapOf(t, e, "huaduan")["p1"]
	if during.Pause != "午饭" && !strings.Contains(strings.Join(logOf(t, e, "huaduan", "p1"), "\n"), "午饭") {
		t.Errorf("no lunch break: pause %q", during.Pause)
	}
	if abs(during.Kpis.Oee-before) > 0.12 {
		t.Errorf("OEE %v before lunch, %v at the end of it: a planned stop should not count", before, during.Kpis.Oee)
	}
}

// Steel that comes hours late runs the store dry and the lines starve, visibly; when it arrives they run again.
func TestLateSteelStarvesLines(t *testing.T) {
	if testing.Short() {
		t.Skip("hours of plant time")
	}
	e := offline(t)
	var pack map[string]any
	json.Unmarshal([]byte(e.Industry("forging")), &pack)
	pack["id"] = "forging-late"
	// a supplier who is always late, deliveries every two hours, the store holding little over that
	pr := pack["products"].(map[string]any)["conrod"].(map[string]any)
	pr["suppliers"] = []any{map[string]any{"name": "迟到钢厂", "share": 1, "late": 1, "lead": 10, "price": 4500}}
	pack["logistics"].(map[string]any)["inbound"] = map[string]any{"vehicle": "truck", "every": 2, "delay": []any{7, 8}}
	raw, _ := json.Marshal(pack)
	if v, err := e.call("addIndustry", string(raw)); err != nil || !strings.Contains(v.String(), `"ok":true`) {
		t.Fatalf("add pack: %v %v", v, err)
	}
	company := `{"id":"late-co","name":"迟到锻造","short":"迟到","industry":"forging-late","plants":[{"id":"p","name":"一厂","lines":[{"name":"连杆线","product":"conrod","route":"hot-die-conrod"}]}]}`
	if v, err := e.call("addCompany", company); err != nil || !strings.Contains(v.String(), `"ok":true`) {
		t.Fatalf("add company: %v %v", v, err)
	}
	at(t, e, "late-co", tuesday.Add(9*time.Hour))
	starved, warned := false, false
	for i := 0; i < 48 && !(starved && warned); i++ {
		run(t, e, "late-co", 15*time.Minute, 1)
		s := snapOf(t, e, "late-co")["p"]
		for _, a := range s.Assets {
			if a.ID == "BF-1" && a.State == "empty" {
				starved = true
			}
		}
		if s.Inbound != nil && s.Inbound.Late {
			warned = true
		}
	}
	if !warned || !starved {
		t.Fatalf("a supplier always hours late: warned %v, line starved %v", warned, starved)
	}
	v, _ := e.call("plantFrame", "late-co", "p")
	var f struct{ Alarms []struct{ Text string } }
	json.Unmarshal([]byte(v.String()), &f)
	var alarms []string
	for _, a := range f.Alarms {
		alarms = append(alarms, a.Text)
	}
	if all := strings.Join(alarms, "\n"); !strings.Contains(all, "迟到钢厂") || !strings.Contains(all, "缺料") {
		t.Errorf("the alarms do not say who is late and that the line is short:\n%s", all)
	}
}

// Scrap follows the die: a worn forging die leaves more flaws and drifts the forged size off.
func TestScrapFollowsToolWear(t *testing.T) {
	if testing.Short() {
		t.Skip("hours of plant time")
	}
	scrap := func(share float64) (int, int) {
		e := offline(t, "huaduan")
		at(t, e, "huaduan", tuesday.Add(9*time.Hour))
		s0 := snapOf(t, e, "huaduan")["p1"]
		for i := 0; i < 12; i++ {
			// the dies' counters as the presses would report them (held there for the run)
			for _, id := range []string{"HP-101", "HP-201", "HP-301", "HP-401"} {
				e.call("ingest", "huaduan", "p1", id, "toolUsed", share*6000, "test")
			}
			run(t, e, "huaduan", 15*time.Minute, 1)
		}
		s1 := snapOf(t, e, "huaduan")["p1"]
		var good, bad int
		for i, l := range s1.Lines[:4] {
			good += l.Good - s0.Lines[i].Good
			bad += l.Scrap - s0.Lines[i].Scrap
		}
		return good, bad
	}
	ng, ns := scrap(0.1)
	wg, ws := scrap(0.99)
	t.Logf("new dies: %d good, %d scrap; worn dies: %d good, %d scrap", ng, ns, wg, ws)
	rate := func(g, s int) float64 { return float64(s) / float64(max(1, g+s)) }
	if rate(wg, ws) <= rate(ng, ns)*1.5 {
		t.Errorf("scrap rate with worn dies %.4f, new %.4f", rate(wg, ws), rate(ng, ns))
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
