package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

	"smartfactory/internal/engine"
)

// A company's production orders in its knowledge base: each order is a node linked to its
// customer, its product and the line that runs it, with a passage saying where it stands, so
// "远岳动力的工单到哪了" walks from the customer to its orders. Orders still to make are all kept;
// finished ones for a while, withdrawn ones not at all.

// OrderInfo is an order as the ERP connection keeps it.
type OrderInfo struct {
	No, PartNo, Name, Customer string
	Plant, PlantName           string
	Line, LineName             string
	// Status: queued | running | done | cancelled
	Status            string
	Qty, Good, Scrap  int
	Due               *time.Time
	Started, Finished *time.Time
	Updated           time.Time
}

const (
	// keepDone is how many finished orders a company's base keeps, and keepDays for how long.
	keepDone = 300
	keepDays = 90
	// orderFormat changes whenever orderItem writes differently, so every order is written again.
	orderFormat = "1"
)

// orderState is what was last written of a company's orders.
type orderState struct {
	Items map[string]orderEntry `json:"items"`
}

type orderEntry struct {
	Hash     string `json:"hash"`
	Customer string `json:"customer,omitempty"`
}

func (b *Base) orderPath(id string) string { return filepath.Join(b.dir, id+".orders.json") }

func (b *Base) orderState(id string) orderState {
	st := orderState{Items: map[string]orderEntry{}}
	if raw, err := os.ReadFile(b.orderPath(id)); err == nil {
		json.Unmarshal(raw, &st)
	}
	if st.Items == nil {
		st.Items = map[string]orderEntry{}
	}
	return st
}

// keptOrders are the orders a base keeps: every one still to make, the latest finished ones
// within keepDays, none withdrawn.
func keptOrders(list []OrderInfo, now time.Time) []OrderInfo {
	var open, done []OrderInfo
	for _, o := range list {
		switch o.Status {
		case "queued", "running":
			open = append(open, o)
		case "done":
			at := o.Updated
			if o.Finished != nil {
				at = *o.Finished
			}
			if now.Sub(at) <= keepDays*24*time.Hour {
				done = append(done, o)
			}
		}
	}
	sort.SliceStable(done, func(i, j int) bool { return finishedAt(done[i]).After(finishedAt(done[j])) })
	if len(done) > keepDone {
		done = done[:keepDone]
	}
	return append(open, done...)
}

func finishedAt(o OrderInfo) time.Time {
	if o.Finished != nil {
		return *o.Finished
	}
	return o.Updated
}

var statusText = map[string]string{"queued": "排队等生产", "running": "生产中", "done": "已完成", "cancelled": "已撤回"}

func orderID(no string) string { return "order:" + no }

// orderItem is an order's passage and its links: the customer it is for, the product it makes
// and the line it runs on (the names those already have in the graph).
func orderItem(o OrderInfo, m *Model, n names, now time.Time) item {
	it := item{id: orderID(o.No), title: "工单 " + o.No, meta: map[string]string{"kind": "order", "order": o.No, "status": o.Status, "plant": o.Plant, "line": o.Line}}
	it.ent(o.No, TOrder, statusText[o.Status])
	var product string
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			if p.ID == o.Plant && l.ID == o.Line {
				product = l.Product.Name
			}
		}
	}
	line := n.line[o.Plant+"/"+o.Line]
	var b strings.Builder
	fmt.Fprintf(&b, "工单（订单）%s：", o.No)
	part := strings.TrimSpace(o.PartNo + " " + o.Name)
	switch {
	case product != "" && part != "":
		fmt.Fprintf(&b, "%s（料号 %s）", product, part)
	case product != "":
		b.WriteString(product)
	default:
		b.WriteString(part)
	}
	fmt.Fprintf(&b, " %d 件", o.Qty)
	if o.Customer != "" {
		fmt.Fprintf(&b, "，客户%s", o.Customer)
		it.ent(o.Customer, TCustomer, "")
		it.rel(o.No, RForCustomer, o.Customer)
	}
	if product != "" {
		it.ent(product, TProduct, o.PartNo)
		it.rel(o.No, ROfProduct, product)
	}
	if line != "" {
		it.ent(line, TLine, "")
		it.rel(o.No, RRunsOn, line)
		fmt.Fprintf(&b, "，在%s %s生产", o.PlantName, o.LineName)
	}
	fmt.Fprintf(&b, "。状态：%s", statusText[o.Status])
	switch o.Status {
	case "running":
		fmt.Fprintf(&b, "，已做 %d 件（%d%%）", o.Good, o.Good*100/max(1, o.Qty))
	case "done":
		fmt.Fprintf(&b, "，合格 %d 件", o.Good)
	}
	if o.Scrap > 0 {
		fmt.Fprintf(&b, "，报废 %d 件", o.Scrap)
	}
	if o.Due != nil {
		fmt.Fprintf(&b, "。交期 %s", o.Due.In(cst).Format("2006-01-02 15:04"))
		if (o.Status == "queued" || o.Status == "running") && now.After(*o.Due) {
			b.WriteString("，已过交期，延期了")
		}
	}
	if o.Started != nil {
		fmt.Fprintf(&b, "。开工 %s", o.Started.In(cst).Format("01-02 15:04"))
	}
	if o.Finished != nil {
		fmt.Fprintf(&b, "，完工 %s", o.Finished.In(cst).Format("01-02 15:04"))
	}
	b.WriteString("。")
	it.text = b.String()
	return it
}

var cst = time.FixedZone("CST", 8*3600)

// SyncOrders writes the company's orders into its knowledge base: the new and changed ones,
// and removes those no longer kept (withdrawn, finished long ago, past the most recent few
// hundred).
func (b *Base) SyncOrders(ctx context.Context, id string) error {
	if b.Orders == nil {
		return nil
	}
	list, err := b.Orders(id)
	if err != nil {
		return err
	}
	m, err := b.model(id)
	if err != nil {
		return err
	}
	n := nameModel(m)
	now := time.Now()
	st := b.orderState(id)
	next := orderState{Items: map[string]orderEntry{}}
	var write []item
	for _, o := range keptOrders(list, now) {
		it := orderItem(o, m, n, now)
		raw, _ := json.Marshal([]any{it.text, it.ents, it.rels})
		sum := sha256.Sum256(append(raw, orderFormat...))
		h := hex.EncodeToString(sum[:])
		next.Items[o.No] = orderEntry{Hash: h, Customer: o.Customer}
		if st.Items[o.No].Hash != h {
			write = append(write, it)
		}
	}
	var gone []string
	for no := range st.Items {
		if _, ok := next.Items[no]; !ok {
			gone = append(gone, no)
		}
	}
	if len(write) == 0 && len(gone) == 0 {
		return nil
	}
	c, err := b.open(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, no := range gone {
		c.db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: orderID(no)})
	}
	for _, it := range write {
		if err := save(ctx, c.db, it); err != nil {
			return fmt.Errorf("%s: %w", it.id, err)
		}
	}
	keep := map[string]bool{}
	for no := range next.Items {
		keep[no] = true
	}
	prune(ctx, c.db, keep)
	out, _ := json.Marshal(next)
	return os.WriteFile(b.orderPath(id), out, 0o644)
}

// ForgetOrders makes the next SyncOrders write every order again.
func (b *Base) ForgetOrders(id string) {
	if engine.ValidID.MatchString(id) {
		os.Remove(b.orderPath(id))
	}
}

// OrdersChanged asks for a company's orders to be written again soon; changes arriving together
// are written together.
func (b *Base) OrdersChanged(company string) {
	b.omu.Lock()
	b.dirty[company] = true
	b.omu.Unlock()
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

// RunOrders keeps every company's orders in its base: a few seconds after they change, and once
// a minute for the progress of the running ones.
func (b *Base) RunOrders(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.kick:
			// let a burst of changes (a batch from the ERP, a pull) settle into one write
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		case <-tick.C:
			for _, c := range b.eng.CompanyIDs() {
				b.omu.Lock()
				b.dirty[c] = true
				b.omu.Unlock()
			}
		}
		b.omu.Lock()
		dirty := b.dirty
		b.dirty = map[string]bool{}
		b.omu.Unlock()
		for c := range dirty {
			if err := b.SyncOrders(ctx, c); err != nil {
				b.log.Error("knowledge: orders", "company", c, "err", err)
			}
		}
	}
}

// orderVocabulary are the names orders add to the graph: their numbers, and customers the
// process does not know.
func (b *Base) orderVocabulary(id string) [][2]string {
	var out [][2]string
	for no, e := range b.orderState(id).Items {
		out = append(out, [2]string{no, TOrder})
		if e.Customer != "" {
			out = append(out, [2]string{e.Customer, TCustomer})
		}
	}
	return out
}
