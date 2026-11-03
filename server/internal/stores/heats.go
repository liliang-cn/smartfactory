package stores

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// HeatStock is one heat of bar in the store: what came in, what went where, what is left. Weights
// are in the bar item's unit.
type HeatStock struct {
	No       string `json:"no"`
	Item     string `json:"item"`
	ItemName string `json:"itemName"`
	// Grade and Size are the bar's 牌号 and 规格.
	Grade string `json:"grade,omitempty"`
	Size  string `json:"size,omitempty"`
	Mill  string `json:"mill"`
	Cert  string `json:"cert,omitempty"`
	Unit  string `json:"unit"`
	// Received came in; Issued went out of the store by hand (back to the mill, to a subcontractor);
	// Counted is what counts changed; Used is what the lines forged of it.
	Received float64   `json:"received"`
	Issued   float64   `json:"issued"`
	Counted  float64   `json:"counted"`
	Used     float64   `json:"used"`
	Left     float64   `json:"left"`
	At       time.Time `json:"at"`
}

// Use is what one line forged of one heat for one order.
type Use struct {
	Heat     string    `json:"heat"`
	Mill     string    `json:"mill,omitempty"`
	Plant    string    `json:"plant"`
	Line     string    `json:"line"`
	LineName string    `json:"lineName"`
	Order    string    `json:"order,omitempty"`
	Pieces   int       `json:"pieces"`
	Kg       float64   `json:"kg"`
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
	// Stored: the heat was received into the store (false: the twin made it up, or nobody booked it).
	Stored bool `json:"stored"`
}

// Heats are the company's heats of bar received into the store, the oldest first (no "": all).
func (s *Service) Heats(company, no string) ([]HeatStock, error) {
	q := `SELECT heat, item, kind, delta, mill, cert, at FROM moves WHERE company = ? AND heat != ''`
	args := []any{company}
	if no != "" {
		q += ` AND heat = ?`
		args = append(args, no)
	}
	rows, err := s.db.Query(q+` ORDER BY at, id`, args...)
	if err != nil {
		return nil, err
	}
	byNo := map[string]*HeatStock{}
	var order []string
	for rows.Next() {
		var h, item, kind, mill, cert string
		var delta float64
		var at int64
		if err := rows.Scan(&h, &item, &kind, &delta, &mill, &cert, &at); err != nil {
			rows.Close()
			return nil, err
		}
		x := byNo[h]
		if x == nil {
			x = &HeatStock{No: h, Item: item, Mill: mill, Cert: cert, At: time.UnixMilli(at)}
			byNo[h] = x
			order = append(order, h)
		}
		if x.Cert == "" {
			x.Cert = cert
		}
		switch kind {
		case In:
			x.Received += delta
		case Out:
			x.Issued -= delta
		case Count:
			x.Counted += delta
		}
	}
	rows.Close()
	if len(order) == 0 {
		return []HeatStock{}, nil
	}
	items, _ := getAll[Item](s.db, `SELECT json FROM items WHERE company = ?`, company)
	used, err := s.usedKg(company)
	if err != nil {
		return nil, err
	}
	out := make([]HeatStock, 0, len(order))
	for _, no := range order {
		h := byNo[no]
		if i := slices.IndexFunc(items, func(it Item) bool { return it.ID == h.Item }); i >= 0 {
			it := items[i]
			h.ItemName, h.Unit = it.Label(), it.Unit
			h.Grade, h.Size = it.Attrs["牌号"], it.Attrs["规格"]
			if f := kgPer(it.Unit); f > 0 {
				h.Used = round(used[no] / f)
			}
		}
		h.Received, h.Issued, h.Counted = round(h.Received), round(h.Issued), round(h.Counted)
		h.Left = round(h.Received - h.Issued + h.Counted - h.Used)
		out = append(out, *h)
	}
	return out, nil
}

func (s *Service) usedKg(company string) (map[string]float64, error) {
	rows, err := s.db.Query(`SELECT heat, SUM(kg) FROM usage WHERE company = ? GROUP BY heat`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var h string
		var kg float64
		rows.Scan(&h, &kg)
		out[h] = kg
	}
	return out, rows.Err()
}

// used books pieces of a heat a line forged for an order.
func (s *Service) used(company, heat, mill, plant, line, lineName, order string, pieces int, kg float64, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO usage (company, heat, plant, line, ord, mill, line_name, pieces, kg, first, last) VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (company, heat, plant, line, ord) DO UPDATE SET pieces = pieces + excluded.pieces, kg = kg + excluded.kg, last = excluded.last`,
		company, heat, plant, line, order, mill, lineName, pieces, kg, ms(at), ms(at))
	return err
}

// Uses are what the lines forged of a heat (heat set), or for an order (order set), the latest first.
func (s *Service) Uses(company, heat, order string) ([]Use, error) {
	q := `SELECT heat, mill, plant, line, line_name, ord, pieces, kg, first, last FROM usage WHERE company = ?`
	args := []any{company}
	if heat != "" {
		q += ` AND heat = ?`
		args = append(args, heat)
	}
	if order != "" {
		q += ` AND ord = ?`
		args = append(args, order)
	}
	rows, err := s.db.Query(q+` ORDER BY last DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stored := map[string]bool{}
	hs, _ := s.Heats(company, "")
	for _, h := range hs {
		stored[h.No] = true
	}
	out := []Use{}
	for rows.Next() {
		var u Use
		var first, last int64
		if err := rows.Scan(&u.Heat, &u.Mill, &u.Plant, &u.Line, &u.LineName, &u.Order, &u.Pieces, &u.Kg, &first, &last); err != nil {
			return nil, err
		}
		u.First, u.Last, u.Kg, u.Stored = time.UnixMilli(first), time.UnixMilli(last), round(u.Kg), stored[u.Heat]
		out = append(out, u)
	}
	return out, rows.Err()
}

// Trace is where a heat came from and went, or which heats an order was forged from.
type Trace struct {
	// Query is what was asked: a heat number or an order number.
	Query string `json:"query"`
	// Heat is the heat as the store has it (nil when it was never received into the store).
	Heat  *HeatStock `json:"heat,omitempty"`
	Moves []Move     `json:"moves"`
	// Uses: the lines and orders that forged it; for an order, the heats it was forged from.
	Uses []Use  `json:"uses"`
	Note string `json:"note,omitempty"`
}

// TraceOf traces q: a heat number, or an order number (MO-…).
func (s *Service) TraceOf(company, q string) (*Trace, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, &Problems{[]string{"填炉号或工单号"}}
	}
	t := &Trace{Query: q, Moves: []Move{}}
	hs, err := s.Heats(company, q)
	if err != nil {
		return nil, err
	}
	if len(hs) > 0 {
		t.Heat = &hs[0]
		if t.Moves, err = s.Moves(company, "", q, 100); err != nil {
			return nil, err
		}
	}
	if t.Uses, err = s.Uses(company, q, ""); err != nil {
		return nil, err
	}
	if t.Heat == nil && len(t.Uses) == 0 {
		// an order: the heats it was forged from
		if t.Uses, err = s.Uses(company, "", q); err != nil {
			return nil, err
		}
		if len(t.Uses) == 0 {
			return nil, ErrNotFound
		}
	}
	for _, u := range t.Uses {
		if !u.Stored {
			t.Note = fmt.Sprintf("炉号 %s 没有在库房入库登记过：钢厂、质保书查不到，按入库补登后就能追溯", u.Heat)
			break
		}
	}
	return t, nil
}
