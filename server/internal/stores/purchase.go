package stores

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Purchase orders: what has been ordered from a supplier and is due in. One line is one item,
// a quantity and the day it is expected. They exist for one reason, to tell the production plan
// when the material it lacks will be there; receiving the goods closes them (a receipt booked
// with the order's number as its reference counts the same).

// Purchase statuses.
const (
	POOpen      = "open"
	PODone      = "done"
	POCancelled = "cancelled"
)

// Purchase is one line of a purchase order.
type Purchase struct {
	ID string `json:"id"`
	// Ref is the purchase order's number as the buyer knows it.
	Ref  string `json:"ref,omitempty"`
	Item string `json:"item"`
	// ItemName and Unit are the item's, for lists.
	ItemName string `json:"itemName,omitempty"`
	Unit     string `json:"unit,omitempty"`
	// Qty is what was ordered, Got what has been received.
	Qty      float64   `json:"qty"`
	Got      float64   `json:"got"`
	Expected time.Time `json:"expected"`
	Supplier string    `json:"supplier,omitempty"`
	Status   string    `json:"status"`
	Created  time.Time `json:"created"`
	Who      string    `json:"who,omitempty"`
	Note     string    `json:"note,omitempty"`
}

// Left is what has still to come.
func (p Purchase) Left() float64 { return round(math.Max(0, p.Qty-p.Got)) }

// Name is how the line reads: its number, or its id.
func (p Purchase) Name() string { return cmp.Or(p.Ref, p.ID) }

func (s *Service) purchases(company string) ([]Purchase, error) {
	return getAll[Purchase](s.db, `SELECT json FROM purchases WHERE company = ?`, company)
}

// Purchases are the company's purchase lines, the open ones first and by expected date; status
// "open" gives only those.
func (s *Service) Purchases(company, status string) ([]Purchase, error) {
	all, err := s.purchases(company)
	if err != nil {
		return nil, err
	}
	items, err := s.Items(company, "")
	if err != nil {
		return nil, err
	}
	name := map[string]Item{}
	for _, it := range items {
		name[it.ID] = it
	}
	out := []Purchase{}
	for _, p := range all {
		if status != "" && p.Status != status {
			continue
		}
		it := name[p.Item]
		p.ItemName, p.Unit = it.Label(), it.Unit
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b Purchase) int {
		return cmp.Or(cmp.Compare(rankOf(a.Status), rankOf(b.Status)), a.Expected.Compare(b.Expected), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func rankOf(status string) int {
	if status == POOpen {
		return 0
	}
	return 1
}

// SavePurchase adds a purchase line, or changes one that is still open (its id given).
func (s *Service) SavePurchase(company, who string, in Purchase) (*Purchase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.savePurchase(company, who, in)
}

func (s *Service) savePurchase(company, who string, in Purchase) (*Purchase, error) {
	it, err := s.Item(company, in.Item)
	var p []string
	if err != nil {
		p = append(p, "要选一个库里有的物料")
	}
	if in.Qty <= 0 || math.IsNaN(in.Qty) || math.IsInf(in.Qty, 0) {
		p = append(p, "订的数量要大于 0")
	}
	if in.Expected.IsZero() {
		p = append(p, "要填预计到货时间")
	}
	var cur *Purchase
	if in.ID != "" {
		all, err := s.purchases(company)
		if err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(all, func(x Purchase) bool { return x.ID == in.ID }); i >= 0 {
			cur = &all[i]
		} else {
			return nil, ErrNotFound
		}
		if cur.Status != POOpen {
			p = append(p, "这条采购已经收完或取消了，不能改")
		}
	}
	if len(p) > 0 {
		return nil, &Problems{p}
	}
	x := Purchase{ID: in.ID, Ref: strings.TrimSpace(in.Ref), Item: it.ID, Qty: round(in.Qty), Expected: in.Expected, Supplier: strings.TrimSpace(in.Supplier),
		Status: POOpen, Created: s.Now(), Who: who, Note: strings.TrimSpace(in.Note)}
	if cur != nil {
		x.Got, x.Created, x.Who = cur.Got, cur.Created, cur.Who
		if x.Qty <= x.Got+1e-9 {
			x.Status = PODone
		}
	} else {
		x.ID = newID("PO")
	}
	if err := s.putPurchase(company, x); err != nil {
		return nil, err
	}
	x.ItemName, x.Unit = it.Label(), it.Unit
	return &x, nil
}

func (s *Service) putPurchase(company string, p Purchase) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO purchases (company, id, json) VALUES (?, ?, ?) ON CONFLICT (company, id) DO UPDATE SET json = excluded.json`, company, p.ID, string(raw))
	return err
}

// CancelPurchase calls an open line off.
func (s *Service) CancelPurchase(company, id string) (*Purchase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.purchases(company)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(all, func(x Purchase) bool { return x.ID == id })
	if i < 0 {
		return nil, ErrNotFound
	}
	p := all[i]
	if p.Status != POOpen {
		return nil, &Problems{[]string{"这条采购已经收完或取消了"}}
	}
	p.Status = POCancelled
	return &p, s.putPurchase(company, p)
}

// ReceivePurchase books what came against a purchase line: a receipt into the stores under the
// line's number, and the line is closed once all of it is in. in.Item is the line's.
func (s *Service) ReceivePurchase(company, who, id string, in MoveInput) (*Move, *Purchase, error) {
	all, err := s.purchases(company)
	if err != nil {
		return nil, nil, err
	}
	i := slices.IndexFunc(all, func(x Purchase) bool { return x.ID == id })
	if i < 0 {
		return nil, nil, ErrNotFound
	}
	p := all[i]
	if p.Status != POOpen {
		return nil, nil, &Problems{[]string{"这条采购已经收完或取消了"}}
	}
	if in.Qty <= 0 {
		in.Qty = p.Left()
	}
	in.Item, in.Kind, in.Ref = p.Item, In, p.Name()
	m, _, err := s.Record(company, who, in) // settles the line
	if err != nil {
		return nil, nil, err
	}
	all, _ = s.purchases(company)
	for _, x := range all {
		if x.ID == id {
			p = x
		}
	}
	return m, &p, nil
}

// settle counts a receipt of an item against its open purchase lines: the one the reference
// names, else the earliest due.
func (s *Service) settle(company, item, ref string, qty float64) {
	all, err := s.purchases(company)
	if err != nil {
		return
	}
	var open []Purchase
	for _, p := range all {
		if p.Status == POOpen && p.Item == item {
			open = append(open, p)
		}
	}
	named := slices.ContainsFunc(open, func(p Purchase) bool {
		return ref != "" && (strings.EqualFold(p.ID, ref) || strings.EqualFold(p.Ref, ref))
	})
	slices.SortStableFunc(open, func(a, b Purchase) int { return a.Expected.Compare(b.Expected) })
	for _, p := range open {
		if qty <= 1e-9 {
			return
		}
		if named && !strings.EqualFold(p.ID, ref) && !strings.EqualFold(p.Ref, ref) {
			continue
		}
		take := math.Min(qty, p.Left())
		p.Got = round(p.Got + take)
		if p.Left() <= 1e-9 {
			p.Status = PODone
		}
		qty -= take
		s.putPurchase(company, p)
	}
}

// Arrival is when the material an order lacks is expected, from the open purchase lines.
type Arrival struct {
	// At is when the last of it comes (zero when that cannot be told).
	At time.Time `json:"at"`
	// Source names the lines it comes on.
	Source string `json:"source,omitempty"`
	// Note says what keeps At from being told.
	Note string `json:"note,omitempty"`
}

// Arrivals says, for each order short of material in the kitting, when the purchase lines bring
// what it lacks. The orders are served in the kitting's order, each taking the earliest lines
// first, so two orders short of the same item do not count the same delivery twice. A line
// that was due before now and is not in is not counted on.
func (s *Service) Arrivals(company string, k *Kitting, now time.Time) (map[string]Arrival, error) {
	out := map[string]Arrival{}
	all, err := s.purchases(company)
	if err != nil {
		return out, err
	}
	items, err := s.Items(company, "")
	if err != nil {
		return out, err
	}
	unit := map[string]string{}
	for _, it := range items {
		unit[it.ID] = it.Unit
	}
	type lane struct {
		p    Purchase
		left float64
	}
	lanes := map[string][]*lane{}
	for _, p := range all {
		if p.Status == POOpen && p.Left() > 1e-9 {
			lanes[p.Item] = append(lanes[p.Item], &lane{p, p.Left()})
		}
	}
	for _, ls := range lanes {
		slices.SortStableFunc(ls, func(a, b *lane) int { return a.p.Expected.Compare(b.p.Expected) })
	}
	clock := func(t time.Time) string { return t.In(cst).Format("01-02 15:04") }
	for _, o := range k.Orders {
		if o.Short == 0 {
			continue
		}
		var a Arrival
		var src []string
		var why []string
		for _, n := range o.Needs {
			if n.Short <= 1e-9 {
				continue
			}
			if n.Item == "" || n.Problem != "" {
				why = append(why, fmt.Sprintf("%s 库房没建档，不知道谁在送", strings.TrimSpace(n.Name+" "+n.Spec)))
				continue
			}
			f, ok := convert(unit[n.Item], n.Unit)
			if !ok || f <= 0 {
				why = append(why, fmt.Sprintf("%s 单位对不上", n.Name))
				continue
			}
			want := n.Short / f // in the store's unit
			var at time.Time
			for _, l := range lanes[n.Item] {
				if want <= 1e-9 {
					break
				}
				if l.p.Expected.Before(now) {
					continue
				}
				take := math.Min(want, l.left)
				if take <= 1e-9 {
					continue
				}
				l.left -= take
				want -= take
				if l.p.Expected.After(at) {
					at = l.p.Expected
				}
				src = append(src, fmt.Sprintf("采购单 %s（%s）", l.p.Name(), clock(l.p.Expected)))
			}
			switch {
			case want > 1e-9:
				late := ""
				for _, l := range lanes[n.Item] {
					if l.p.Expected.Before(now) {
						late = fmt.Sprintf("，采购单 %s 说好 %s 到，到现在没收到", l.p.Name(), clock(l.p.Expected))
						break
					}
				}
				why = append(why, fmt.Sprintf("%s 在途的采购还差 %s %s%s", strings.TrimSpace(n.Name+" "+n.Spec), num(want), unit[n.Item], late))
			case at.After(a.At):
				a.At = at
			}
		}
		if len(why) > 0 {
			a.At = time.Time{}
			a.Note = strings.Join(why, "；")
		}
		a.Source = strings.Join(dedupe(src), "、")
		if a.At.IsZero() && a.Note == "" {
			a.Note = "没有采购单"
		}
		out[o.No] = a
	}
	return out, nil
}

func dedupe(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
