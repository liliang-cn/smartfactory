package knowledge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

	"smartfactory/internal/engine"
)

func base(t *testing.T) (*Base, context.Context) {
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
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	kb, err := Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kb.Close)
	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	return kb, ctx
}

func at(d time.Duration) *time.Time { t := time.Now().Add(d); return &t }

// An ERP's orders join the graph: each linked to its customer, product and line, found from a
// question that names the customer, the line or the number; a withdrawn order leaves, a finished
// one stays a while, and progress is written as it goes.
func TestOrders(t *testing.T) {
	kb, ctx := base(t)
	orders := []OrderInfo{
		{No: "MO-1", PartNo: "QZ-B15-4", Customer: "远岳动力", Plant: "p1", PlantName: "一厂", Line: "L1", LineName: "曲轴 1 线", Status: "running", Qty: 120, Good: 30, Due: at(-time.Hour), Started: at(-2 * time.Hour), Updated: time.Now()},
		{No: "MO-2", PartNo: "LG-B15", Customer: "衡川汽车", Plant: "p1", PlantName: "一厂", Line: "L5", LineName: "连杆 1 线", Status: "queued", Qty: 300, Updated: time.Now()},
		{No: "MO-3", PartNo: "FL-1200-Q355", Customer: "新客户风机", Plant: "p2", PlantName: "二厂", Line: "L1", LineName: "法兰 1 线", Status: "done", Qty: 12, Good: 12, Finished: at(-24 * time.Hour), Updated: time.Now()},
		{No: "MO-OLD", PartNo: "QZ-B15-4", Customer: "远岳动力", Plant: "p1", PlantName: "一厂", Line: "L1", LineName: "曲轴 1 线", Status: "done", Qty: 10, Good: 10, Finished: at(-100 * 24 * time.Hour), Updated: time.Now()},
	}
	kb.Orders = func(string) ([]OrderInfo, error) { return slices.Clone(orders), nil }
	if err := kb.SyncOrders(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	st, _ := kb.Stats(ctx, "huaduan")
	if st.Orders != 3 || st.Kinds[TOrder] != 3 || st.Relations[RRunsOn] != 3 || st.Relations[RForCustomer] != 3 {
		t.Fatalf("orders in the graph: %+v", st)
	}
	e, _ := kb.Lookup(ctx, "huaduan", "MO-1")
	if e == nil || e.Kind != TOrder {
		t.Fatalf("MO-1: %+v", e)
	}
	links := map[string]string{}
	for _, l := range e.Links {
		links[l.Type] = l.Name
	}
	if links[RForCustomer] != "远岳动力" || links[ROfProduct] != "发动机曲轴" || links[RRunsOn] != "曲轴 1 线" {
		t.Errorf("MO-1 links: %+v", e.Links)
	}
	if c, _ := kb.Lookup(ctx, "huaduan", "新客户风机"); c == nil || c.Kind != TCustomer {
		t.Errorf("a customer only an order names: %+v", c)
	}
	if old, _ := kb.Lookup(ctx, "huaduan", "MO-OLD"); old != nil {
		t.Error("an order finished 100 days ago is kept")
	}

	first := func(q string) Passage {
		t.Helper()
		a, err := kb.Search(ctx, "huaduan", q, nil, nil, 6)
		if err != nil || len(a.Passages) == 0 {
			t.Fatalf("%s: %v %+v", q, err, a)
		}
		return a.Passages[0]
	}
	if p := first("远岳动力的工单到哪了"); p.From != "order" || !strings.Contains(p.Text, "MO-1") || !strings.Contains(p.Text, "已过交期") {
		t.Errorf("a customer's order: %+v", p)
	}
	if p := first("MO-1"); p.Title != "工单 MO-1" || !strings.Contains(p.Text, "已做 30 件") {
		t.Errorf("an order by number: %+v", p)
	}
	a, _ := kb.Search(ctx, "huaduan", "曲轴 1 线在做什么", nil, nil, 6)
	if len(a.Passages) < 2 || !strings.HasSuffix(a.Passages[0].Title, "曲轴 1 线") || a.Passages[1].Title != "工单 MO-1" {
		t.Errorf("what a line is making: %+v", a.Passages)
	}

	// progress, a withdrawal
	orders[0].Good = 80
	orders[1].Status = "cancelled"
	kb.OrdersChanged("huaduan")
	if err := kb.SyncOrders(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	if p := first("MO-1 做到多少了"); !strings.Contains(p.Text, "已做 80 件") {
		t.Errorf("progress not written: %+v", p)
	}
	if gone, _ := kb.Lookup(ctx, "huaduan", "MO-2"); gone != nil {
		t.Errorf("a withdrawn order is still in the graph: %+v", gone)
	}
	if st, _ := kb.Stats(ctx, "huaduan"); st.Orders != 2 || st.Kinds[TOrder] != 2 {
		t.Errorf("after the withdrawal: %+v", st)
	}
	// a rebuild writes them all again
	kb.ForgetOrders("huaduan")
	if err := kb.SyncOrders(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	if st, _ := kb.Stats(ctx, "huaduan"); st.Orders != 2 || st.Kinds[TOrder] != 2 {
		t.Errorf("after a rebuild: %+v", st)
	}
}

// The most recent finished orders are kept, up to keepDone; every open one is.
func TestKeptOrders(t *testing.T) {
	var list []OrderInfo
	for i := range keepDone + 20 {
		list = append(list, OrderInfo{No: fmt.Sprint("D", i), Status: "done", Finished: at(-time.Duration(i) * time.Hour)})
	}
	list = append(list, OrderInfo{No: "Q", Status: "queued"}, OrderInfo{No: "C", Status: "cancelled"})
	kept := keptOrders(list, time.Now())
	names := map[string]bool{}
	for _, o := range kept {
		names[o.No] = true
	}
	if len(kept) != keepDone+1 || !names["Q"] || names["C"] || !names["D0"] || names[fmt.Sprint("D", keepDone)] {
		t.Errorf("kept %d", len(kept))
	}
}

// Questions about a station, a line and a product by a short name find the passage about it, and
// the walk over the graph does not wander through the prefixes CortexDB extracts on its own.
func TestRetrieval(t *testing.T) {
	kb, ctx := base(t)
	doc := []Part{{Seq: 0, Heading: "加热", Text: "中频感应加热炉 IH-101 把 42CrMo 坯料加热到 1150～1200°C，出炉后 30 秒内上热模锻压力机。"}}
	if err := kb.AddDoc(ctx, "huaduan", "d1", "曲轴热锻作业指导书", doc); err != nil {
		t.Fatal(err)
	}
	c, _ := kb.open("huaduan")
	r, err := c.db.QueryCypher(ctx, cortexdb.CypherQueryRequest{Query: `MATCH (n:entity) RETURN count(n)`})
	if err != nil || len(r.Rows) != 1 || fmt.Sprint(r.Rows[0][0]) != "0" {
		t.Errorf("extracted entities left in the graph: %v %v", r, err)
	}
	if got := kb.Recognize("huaduan", "法兰谁在做，给谁"); !slices.Contains(got, "风电法兰") {
		t.Errorf("a product by the end of its name: %v", got)
	}
	a, _ := kb.Search(ctx, "huaduan", "HP-101 的上游", nil, nil, 4)
	if len(a.Passages) == 0 || !strings.HasPrefix(a.Passages[0].Title, "HP-101") || !strings.Contains(a.Passages[0].Text, "上游（上一道）是 RF-101") {
		t.Errorf("a station's upstream: %+v", a.Passages)
	}
	a, _ = kb.Search(ctx, "huaduan", "IH-101 加热到多少度", nil, nil, 6)
	if !slices.ContainsFunc(a.Passages, func(p Passage) bool { return p.From == "doc" && strings.Contains(p.Text, "1150") }) {
		t.Errorf("the heating card from the station: %+v", a.Passages)
	}
	a, _ = kb.Search(ctx, "huaduan", "法兰卖给谁", nil, nil, 4)
	if len(a.Passages) == 0 || a.Passages[0].Title != "风电法兰" || !strings.Contains(a.Passages[0].Text, "北辰风电") {
		t.Errorf("a product's customers: %+v", a.Passages)
	}
}
