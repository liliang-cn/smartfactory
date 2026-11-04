package stores

import (
	"strings"
	"testing"
	"time"
)

// What is on order tells the plan when an order's missing material comes: the earliest lines go
// to the orders first in line, a line that is late is not counted on, and receiving closes lines.
func TestPurchaseArrivals(t *testing.T) {
	s, _ := open(t)
	crank(s, t)
	plug, _ := s.SaveItem("acme", Item{Cat: "part", Code: "PLUG", Name: "油道螺塞", Unit: "件"})
	s.Record("acme", "王", MoveInput{Item: plug.ID, Kind: In, Qty: 100})
	bar, _ := s.SaveItem("acme", Item{Cat: "bar", Code: "GB-42CrMo-80", Name: "42CrMo 圆钢 φ80", Attrs: map[string]string{"牌号": "42CrMo", "规格": "Φ80"}, Unit: "t"})
	s.Record("acme", "王", MoveInput{Item: bar.ID, Kind: In, Qty: 40, Heat: "H1", Mill: "华东特钢"})
	bar2, _ := s.SaveItem("acme", Item{Cat: "bar", Code: "GB-40Cr-90", Name: "40Cr 圆钢 φ90", Attrs: map[string]string{"牌号": "40Cr", "规格": "φ90"}, Unit: "t"})
	s.Record("acme", "王", MoveInput{Item: bar2.ID, Kind: In, Qty: 40, Heat: "H2", Mill: "华东特钢"})
	now := time.Now()
	h := func(n float64) time.Time { return now.Add(time.Duration(n * float64(time.Hour))) }

	// 3 orders of 100 cranks: two plugs each; the stock covers the first order's half
	k, err := s.Kit("acme", []Demand{{No: "MO-1", Part: "QZ-1", Qty: 100}, {No: "MO-2", Part: "QZ-1", Qty: 100}, {No: "MO-3", Part: "QZ-1", Qty: 100}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Arrivals("acme", k, now)
	if err != nil || a["MO-1"].Note != "没有采购单" && !strings.Contains(a["MO-1"].Note, "还差") || !a["MO-1"].At.IsZero() {
		t.Fatalf("nothing on order: %+v %v", a, err)
	}

	if _, err := s.SavePurchase("acme", "周", Purchase{Item: plug.ID, Qty: 0, Expected: h(5)}); err == nil || !strings.Contains(err.Error(), "大于 0") {
		t.Fatalf("a line of nothing: %v", err)
	}
	if _, err := s.SavePurchase("acme", "周", Purchase{Item: plug.ID, Qty: 5}); err == nil || !strings.Contains(err.Error(), "到货时间") {
		t.Fatalf("a line with no date: %v", err)
	}
	first, err := s.SavePurchase("acme", "周", Purchase{Item: plug.ID, Qty: 150, Expected: h(10), Ref: "CG-1", Supplier: "甲"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePurchase("acme", "周", Purchase{Item: plug.ID, Qty: 200, Expected: h(30), Ref: "CG-2"}); err != nil {
		t.Fatal(err)
	}
	// MO-1 is short 100 plugs (200 needed, 100 in store), MO-2 200, MO-3 200
	if k, err = s.Kit("acme", []Demand{{No: "MO-1", Part: "QZ-1", Qty: 100}, {No: "MO-2", Part: "QZ-1", Qty: 100}, {No: "MO-3", Part: "QZ-1", Qty: 100}}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Arrivals("acme", k, now)
	if x := a["MO-1"]; x.At.IsZero() || x.At.Sub(h(10)).Abs() > time.Second || !strings.Contains(x.Source, "CG-1") {
		t.Errorf("MO-1 takes the first delivery: %+v", x)
	}
	if x := a["MO-2"]; x.At.IsZero() || x.At.Sub(h(30)).Abs() > time.Second || !strings.Contains(x.Source, "CG-2") {
		t.Errorf("MO-2 gets the rest of the first and the second: %+v", x)
	}
	if x := a["MO-3"]; !x.At.IsZero() || !strings.Contains(x.Note, "还差 150") {
		t.Errorf("MO-3 is not covered by what is on order: %+v", x)
	}

	// a line that should have come and has not is not counted on
	a, _ = s.Arrivals("acme", k, h(35))
	if x := a["MO-1"]; !x.At.IsZero() || !strings.Contains(x.Note, "到现在没收到") {
		t.Errorf("late line counted on: %+v", x)
	}

	// goods in against a line close it, by its number or just as the item
	if _, _, err := s.ReceivePurchase("acme", "王", first.ID, MoveInput{Qty: 100}); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.Purchases("acme", POOpen)
	if len(ps) != 2 || !near(ps[0].Got, 100) || !near(ps[0].Left(), 50) {
		t.Fatalf("part received: %+v", ps)
	}
	if _, _, err := s.Record("acme", "王", MoveInput{Item: plug.ID, Kind: In, Qty: 50, Ref: "送货单 8841"}); err != nil {
		t.Fatal(err)
	}
	ps, _ = s.Purchases("acme", POOpen)
	if len(ps) != 1 || ps[0].Ref != "CG-2" {
		t.Fatalf("the first line should be done: %+v", ps)
	}
	all, _ := s.Purchases("acme", "")
	if len(all) != 2 || all[1].Status != PODone {
		t.Fatalf("history: %+v", all)
	}
	if _, err := s.CancelPurchase("acme", ps[0].ID); err != nil {
		t.Fatal(err)
	}
	if ps, _ = s.Purchases("acme", POOpen); len(ps) != 0 {
		t.Errorf("cancelled still open: %+v", ps)
	}
}
