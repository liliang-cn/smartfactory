package knowledge

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"smartfactory/internal/audit"
	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// chuan is a running engine with the 串串 fixture (testdata) as company "chuan".
func chuan(t *testing.T) (*engine.Engine, context.Context) {
	t.Helper()
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go eng.Run(ctx)
	for _, f := range []string{"chuan-pack.json", "chuan-company.json"} {
		raw, err := os.ReadFile("testdata/" + f)
		if err != nil {
			t.Fatal(err)
		}
		r := eng.CreateIndustry(raw)
		if f == "chuan-company.json" {
			r = eng.CreateCompany(raw)
		}
		if !r.OK {
			t.Fatal(f, r.Errors)
		}
	}
	return eng, ctx
}

func fact(fs []OntFact, s, rel, o string) *OntFact {
	for i, f := range fs {
		if f.Subject == s && f.Relation == rel && f.Object == o {
			return &fs[i]
		}
	}
	return nil
}

// The ontology is loaded with the process and inferred from once: a 毛肚 is a 食材 through its
// classes, 原料 is transitive, the rule carries an allergen from an ingredient to the dish (and
// says why, step by step), 存放于 read the other way is 存放, 同锅 goes both ways; the freezer
// station is a 冷藏设备 because its machine kind is bound to it, and the dish named by a product
// is that product. A dish without 原料 breaks the structural constraint. Loading again without a
// change does nothing; a rebuild loads the same.
func TestOntology(t *testing.T) {
	eng, ctx := chuan(t)
	kb, err := Open(t.TempDir(), eng, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer kb.Close()
	if err := kb.SyncProcess(ctx, "chuan"); err != nil {
		t.Fatal(err)
	}

	ov, err := kb.OntologyOverview(ctx, "chuan")
	if err != nil {
		t.Fatal(err)
	}
	count := map[string][2]int{}
	for _, c := range ov.Classes {
		count[c.Name] = [2]int{c.Asserted, c.Inferred}
	}
	// 菜品: 毛肚串 bound to it; 牛油锅底 and 清汤锅底 as 锅底, 花生碎 because it has 原料
	if !ov.Declared || ov.Inferred == 0 || count["冷藏设备"] != [2]int{1, 0} || count["工位"][0] != 3 || count["食材"] != [2]int{0, 6} || count["菜品"] != [2]int{1, 3} {
		t.Errorf("overview: declared %v, %d inferred, counts %v", ov.Declared, ov.Inferred, count)
	}
	var rule, chainRel bool
	for _, r := range ov.Rules {
		rule = rule || r.Text == "X 原料 Y，Y 含过敏原 Z → X 含过敏原 Z"
	}
	for _, r := range ov.Relations {
		chainRel = chainRel || r.Name == "原料" && r.Text == "菜品 原料是 食材；可以传递"
	}
	if !rule || !chainRel {
		t.Errorf("rules %+v, relations %+v", ov.Rules, ov.Relations)
	}
	var shacl *OntConstraintInfo
	for i, c := range ov.Constraints {
		if c.ID == "菜品写明原料" {
			shacl = &ov.Constraints[i]
		}
	}
	if shacl == nil || shacl.Live || shacl.Status != "violated" || len(shacl.Violations) != 1 || shacl.Violations[0].Text != "清汤锅底 没写原料" {
		t.Errorf("the structural constraint: %+v", shacl)
	}

	// subclasses: 鲜毛肚 is a 毛肚, so a 荤菜 (and a 食材, which 原料 also says)
	th, err := kb.OntologyThing(ctx, "chuan", "鲜毛肚")
	if err != nil {
		t.Fatal(err)
	}
	if f := fact(th.Classes, "鲜毛肚", "是", "荤菜"); f == nil || !f.Inferred || th.Note != "每天早上到货" || !slices.Equal(f.Why, []string{"鲜毛肚 是 毛肚；毛肚 是一种 荤菜 → 鲜毛肚 是 荤菜"}) {
		t.Errorf("鲜毛肚 is a 荤菜: %+v", th)
	}
	if f := fact(th.Classes, "鲜毛肚", "是", "食材"); f == nil || !f.Inferred || len(f.Why) == 0 || !strings.Contains(f.Why[len(f.Why)-1], "→ 鲜毛肚 是 食材") {
		t.Errorf("鲜毛肚 is a 食材: %+v", f)
	}
	if f := fact(th.Classes, "鲜毛肚", "是", "毛肚"); f == nil || f.Inferred {
		t.Errorf("鲜毛肚 is a 毛肚 as given: %+v", th.Classes)
	}

	// transitive, the rule, and why
	th, err = kb.OntologyThing(ctx, "chuan", "牛油锅底")
	if err != nil {
		t.Fatal(err)
	}
	if f := fact(th.Facts, "牛油锅底", "原料", "花生"); f == nil || !f.Inferred || !slices.Contains(f.Why, "牛油锅底 原料 花生碎；花生碎 原料 花生 → 牛油锅底 原料 花生（「原料」可以传递）") {
		t.Errorf("原料 is transitive: %+v", f)
	}
	al := fact(th.Facts, "牛油锅底", "含过敏原", "花生过敏原")
	if al == nil || !al.Inferred || al.Why[len(al.Why)-1] != "牛油锅底 原料 花生；花生 含过敏原 花生过敏原 → 牛油锅底 含过敏原 花生过敏原（规则「过敏原随原料传递」）" {
		t.Errorf("the allergen comes with the ingredient: %+v", al)
	}
	if th.Kind != TProduct || fact(th.Classes, "牛油锅底", "是", "锅底") == nil || fact(th.Classes, "牛油锅底", "是", "产品") == nil {
		t.Errorf("the product is the named dish: %+v", th)
	}
	if f := fact(th.Facts, "牛油锅底", "同锅", "毛肚串"); f == nil || f.Inferred {
		t.Errorf("同锅 as given: %+v", th.Facts)
	}

	// inverse, symmetric, a station of a bound kind
	th, err = kb.OntologyThing(ctx, "chuan", "FZ-101")
	if err != nil {
		t.Fatal(err)
	}
	if f := fact(th.Facts, "FZ-101", "存放", "鲜毛肚"); f == nil || !f.Inferred || len(f.Why) != 1 || !strings.Contains(f.Why[0], "「存放于」反过来是「存放」") {
		t.Errorf("存放 is 存放于 the other way: %+v", th.Facts)
	}
	if f := fact(th.Classes, "FZ-101", "是", "冷藏设备"); f == nil || f.Inferred || fact(th.Classes, "FZ-101", "是", "设备") == nil || th.Kind != TStation {
		t.Errorf("the freezer station: %+v", th)
	}
	th, _ = kb.OntologyThing(ctx, "chuan", "毛肚串")
	if f := fact(th.Facts, "毛肚串", "同锅", "牛油锅底"); f == nil || !f.Inferred {
		t.Errorf("同锅 both ways: %+v", th.Facts)
	}

	// the things of a class, with inference, and filtered by a relation's value
	in, err := kb.OntologyInstances(ctx, "chuan", "菜品", "含过敏原", "花生过敏原", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Instances) != 2 || in.Instances[0].Name != "牛油锅底" || in.Instances[1].Name != "花生碎" || in.Instances[0].Fact == nil || !in.Instances[0].Fact.Inferred {
		t.Errorf("dishes with peanut: %+v", in)
	}
	in, _ = kb.OntologyInstances(ctx, "chuan", "锅底", "", "", 0)
	if len(in.Instances) != 2 || in.Instances[0].Inferred {
		t.Errorf("锅底: %+v", in.Instances)
	}
	in, _ = kb.OntologyInstances(ctx, "chuan", "食材", "", "", 0)
	i := slices.IndexFunc(in.Instances, func(x OntInstance) bool { return x.Name == "鲜毛肚" })
	if i < 0 || !in.Instances[i].Inferred || in.Total != 6 {
		t.Errorf("食材: %+v", in.Instances)
	}
	if _, err := kb.OntologyInstances(ctx, "chuan", "海鲜", "", "", 0); err == nil {
		t.Error("a class nobody declared")
	}
	cl, err := kb.OntologyThing(ctx, "chuan", "毛肚")
	if err != nil || cl.Kind != "class" || fact(cl.Super, "毛肚", "是一种", "食材") == nil || cl.Instances.Total != 1 {
		t.Errorf("the class 毛肚: %+v %v", cl, err)
	}

	// read-only SPARQL, with the prefixes and the graph given
	a, err := kb.OntologySPARQL(ctx, "chuan", `SELECT ?x WHERE { ?x r:含过敏原 t:花生过敏原 }`)
	if err != nil || len(a.Rows) != 3 {
		t.Errorf("SPARQL: %+v %v", a, err)
	}
	if _, err := kb.OntologySPARQL(ctx, "chuan", `INSERT DATA { t:a r:b t:c }`); err == nil {
		t.Error("SPARQL changed the ontology")
	}

	// loading again: nothing changed, nothing done; a rebuild loads the same
	st := kb.ontoState("chuan")
	fi, _ := os.Stat(kb.ontoPath("chuan"))
	if err := kb.SyncProcess(ctx, "chuan"); err != nil {
		t.Fatal(err)
	}
	if fj, _ := os.Stat(kb.ontoPath("chuan")); !fj.ModTime().Equal(fi.ModTime()) {
		t.Error("an unchanged ontology was loaded again")
	}
	kb.Forget("chuan")
	if err := kb.SyncProcess(ctx, "chuan"); err != nil {
		t.Fatal(err)
	}
	again := kb.ontoState("chuan")
	if again.Hash != st.Hash || again.Explicit != st.Explicit || again.Inferred != st.Inferred || len(again.Violations) != len(st.Violations) {
		t.Errorf("a rebuild: %+v, before %+v", again, st)
	}
	ov2, _ := kb.OntologyOverview(ctx, "chuan")
	for _, c := range ov2.Classes {
		if count[c.Name] != [2]int{c.Asserted, c.Inferred} {
			t.Errorf("after a rebuild %s: %d/%d, before %v", c.Name, c.Asserted, c.Inferred, count[c.Name])
		}
	}

	// a company without an ontology: the built-in classes, counted from the knowledge graph
	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	ov, err = kb.OntologyOverview(ctx, "huaduan")
	if err != nil || ov.Declared || ov.Classes[3].Name != "工位" || ov.Classes[3].Asserted < 50 {
		t.Errorf("huaduan: %+v %v", ov, err)
	}
	if _, err := os.Stat(kb.ontoPath("huaduan")); err == nil {
		t.Error("a file for a company without an ontology")
	}
}

// The 签语串串香 template on its two packs: the dishes the peanut crumb carries its allergen into,
// the dishes that need the cold chain through what they are made of, the new skewer with nowhere
// to be kept, and each fridge watched once though both packs declare the 0–4 ℃ limit.
func TestOntologyQianyu(t *testing.T) {
	eng, ctx := chuan(t)
	kb, err := Open(t.TempDir(), eng, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer kb.Close()
	kb.Live = NewLive(t.TempDir(), nil)
	if err := kb.SyncProcess(ctx, "qianyu"); err != nil {
		t.Fatal(err)
	}
	names := func(in *OntInstances) []string {
		var out []string
		for _, x := range in.Instances {
			out = append(out, x.Name)
		}
		return out
	}
	in, err := kb.OntologyInstances(ctx, "qianyu", "菜品", "含过敏原", "花生", 0)
	if err != nil || !slices.Equal(names(in), []string{"干碟", "油碟"}) || !in.Instances[0].Fact.Inferred {
		t.Errorf("dishes with peanut: %+v %v", in, err)
	}
	in, _ = kb.OntologyInstances(ctx, "qianyu", "菜品", "需冷链", "冷藏", 0)
	if !slices.Contains(names(in), "毛肚串") || in.Total < 10 {
		t.Errorf("dishes that need the cold chain: %v", names(in))
	}
	ov, err := kb.OntologyOverview(ctx, "qianyu")
	if err != nil {
		t.Fatal(err)
	}
	chilled := 0
	for _, c := range ov.Constraints {
		switch c.ID {
		case "chilled-temp":
			chilled++
			if c.Watching != 5 {
				t.Errorf("fridges watched: %d", c.Watching)
			}
		case "skewer-has-storage":
			if len(c.Violations) != 1 || c.Violations[0].Name != "掌中宝串（新品）" {
				t.Errorf("the skewer kept nowhere: %+v", c.Violations)
			}
		}
	}
	if chilled != 1 {
		t.Errorf("the 0–4 ℃ limit is there %d times", chilled)
	}
}

type alerts struct {
	mu   sync.Mutex
	list []string
}

func (a *alerts) alert(company, plant, asset, code, text string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = append(a.list, strings.Join([]string{company, plant, asset, code, text}, "|"))
	return true
}

func (a *alerts) last() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.list) == 0 {
		return ""
	}
	return a.list[len(a.list)-1]
}

// A live constraint watches the stations of its class: a reading out of range for less than its
// seconds does nothing; longer, it raises an alarm on the machine, goes into the activity log and
// becomes a record in the graph beside the station; back in range, the alarm clears and the
// record ends. Readings of fields and machines nobody constrains cost no allocation.
func TestLiveConstraint(t *testing.T) {
	eng, ctx := chuan(t)
	dir := t.TempDir()
	trail, err := audit.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer trail.Close()
	kb, err := Open(dir, eng, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer kb.Close()
	var al alerts
	live := NewLive(dir, nil)
	live.Alert, live.Audit, live.Changed = al.alert, trail, kb.EventsChanged
	kb.Live = live
	if err := kb.SyncProcess(ctx, "chuan"); err != nil {
		t.Fatal(err)
	}
	if w := live.Watching("chuan"); w["冷链"] != 1 {
		t.Fatalf("watching: %v", w)
	}

	t0 := time.Now()
	now := t0
	live.Now = func() time.Time { return now }
	read := func(asset, field string, v float64, after time.Duration) {
		now = t0.Add(after)
		live.Add(tags.Tag{Company: "chuan", Plant: "s1", Asset: asset, Field: field, Value: v, Source: "plc", At: now})
		live.reconcile()
	}
	read("FZ-101", "temp", 3.5, 0)
	read("FZ-101", "temp", 6, time.Second)
	read("FZ-101", "temp", 6.2, 2*time.Second)
	read("FZ-101", "temp", 3, 3*time.Second) // back before 3 s were up
	read("FZ-101", "temp", 6.5, 4*time.Second)
	read("FZ-101", "temp", 7.1, 6*time.Second)
	read("ST-101", "temp", 30, 6*time.Second) // nobody constrains the table
	if len(al.list) != 0 || len(live.Current("chuan")) != 0 {
		t.Fatalf("fired too soon: %v", al.list)
	}
	read("FZ-101", "temp", 6.8, 7500*time.Millisecond)
	if got := al.last(); got != "chuan|s1|FZ-101|rule:冷链|FZ-101 温度 6.8 ℃，冷藏要求不高于 4 ℃" {
		t.Fatalf("the alarm: %q", got)
	}
	cur := live.Current("chuan")
	if len(cur) != 1 || cur[0].Name != "FZ-101" || *cur[0].Value != 7.1 {
		t.Errorf("now out of range: %+v", cur)
	}
	ov, _ := kb.OntologyOverview(ctx, "chuan")
	for _, c := range ov.Constraints {
		if c.ID == "冷链" && (c.Status != "violated" || c.Watching != 1 || len(c.Violations) != 1) {
			t.Errorf("the live constraint in the overview: %+v", c)
		}
	}
	if err := kb.SyncEvents(ctx, "chuan"); err != nil {
		t.Fatal(err)
	}
	e, _ := kb.Lookup(ctx, "chuan", "FZ-101")
	var rec string
	for _, l := range e.Links {
		if l.Kind == TViolation && l.Type == RBreachedOn && !l.Out {
			rec = l.Name
		}
	}
	if !strings.HasPrefix(rec, "FZ-101 · 约束 冷链 ") {
		t.Fatalf("the record beside the station: %+v", e.Links)
	}
	if r, _ := kb.Lookup(ctx, "chuan", rec); r == nil || !slices.ContainsFunc(r.Detail, func(f Fact) bool { return f.Label == "结束" && f.Value == "还没恢复" }) {
		t.Errorf("the record: %+v", r)
	}
	a, _ := kb.Search(ctx, "chuan", "FZ-101 冷链约束告警", nil, nil, 3)
	if len(a.Passages) == 0 || a.Passages[0].From != TViolation {
		t.Errorf("searching for it: %+v", a.Passages)
	}

	read("FZ-101", "temp", 3.2, 9*time.Second)
	if got := al.last(); got != "chuan|s1|FZ-101|rule:冷链|" {
		t.Errorf("the alarm cleared: %q", got)
	}
	recs := live.Records("chuan", t0.Add(-time.Hour))
	if len(recs) != 1 || recs[0].Ended == nil || recs[0].Worst != 7.1 || recs[0].Value != 6.8 {
		t.Errorf("the record ended: %+v", recs)
	}
	es, _ := trail.Find(ctx, "chuan", t0.Add(-time.Hour), "ontology", []string{"ontology.violation", "ontology.violation.end"}, 10)
	if len(es) != 2 || !strings.Contains(es[1].Detail+es[0].Detail, "FZ-101 温度 6.8") {
		t.Errorf("the activity log: %+v", es)
	}
	kb.SyncEvents(ctx, "chuan")
	if r, _ := kb.Lookup(ctx, "chuan", rec); r == nil || !slices.ContainsFunc(r.Detail, func(f Fact) bool { return f.Label == "最远" && f.Value == "7.1" }) {
		t.Errorf("the record once ended: %+v", r)
	}

	// a restart: a record left open is ended and its alarm cleared
	read("FZ-101", "temp", 9, 10*time.Second)
	read("FZ-101", "temp", 9, 14*time.Second)
	if len(live.Current("chuan")) != 1 {
		t.Fatal("did not fire again")
	}
	var al2 alerts
	restarted := NewLive(dir, nil)
	restarted.Alert = al2.alert
	restarted.Set("chuan", nil)
	if got := al2.last(); got != "chuan|s1|FZ-101|rule:冷链|" {
		t.Errorf("an alarm left by the last run: %q", got)
	}
	if r := restarted.Records("chuan", t0.Add(-time.Hour)); len(r) != 2 || r[0].Ended == nil {
		t.Errorf("records after a restart: %+v", r)
	}

	// a freezer no equipment reports for: the twin's own value is judged
	var al3 alerts
	twin := NewLive(t.TempDir(), nil)
	twin.Alert, twin.Now = al3.alert, func() time.Time { return now }
	temp := 8.0
	twin.Sim = func(company string, keys [][3]string) []*float64 {
		out := make([]*float64, len(keys))
		for i, k := range keys {
			if k == [3]string{"s1", "FZ-101", "temp"} {
				out[i] = &temp
			}
		}
		return out
	}
	m, _ := kb.model("chuan")
	twin.Set("chuan", liveWatches(m))
	for i := range 5 {
		now = t0.Add(time.Minute + time.Duration(i)*time.Second)
		if twin.sample() {
			twin.reconcile()
		}
	}
	if got := al3.last(); !strings.HasPrefix(got, "chuan|s1|FZ-101|rule:冷链|FZ-101 温度 8 ℃") {
		t.Errorf("the twin's value out of range: %q", got)
	}

	idle := tags.Tag{Company: "chuan", Plant: "s1", Asset: "ST-101", Field: "temp", Value: 30.0, Source: "plc", At: now}
	cold := tags.Tag{Company: "chuan", Plant: "s1", Asset: "FZ-101", Field: "temp", Value: 2.0, Source: "plc", At: now}
	live.Add(cold)
	if n := testing.AllocsPerRun(1000, func() { live.Add(idle); live.Add(cold) }); n != 0 {
		t.Errorf("%v allocations a reading", n)
	}
}
