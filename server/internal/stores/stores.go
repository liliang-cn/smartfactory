// Package stores keeps what a plant keeps in its stores and its die shop: items by category (steel
// bar received by heat, standard and spare parts, finished goods), every receipt, issue and count
// against them, the dies one by one with the strokes each has struck, and new parts being
// developed. The categories come from the company's industry pack; what the lines use comes from
// the twin: the steel a line forges is taken off its heat, a mounted die counts the press's strokes.
package stores

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"smartfactory/internal/engine"
)

// Category kinds.
const (
	Bar     = "bar"
	Part    = "part"
	Product = "product"
	Other   = "other"
)

// Category is a kind of thing the stores keep, as the industry pack declares it (or a person adds).
type Category struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Kind: bar (steel received by heat) | part | product | other
	Kind   string   `json:"kind"`
	Unit   string   `json:"unit"`
	Fields []string `json:"fields,omitempty"`
}

// Item is one thing kept: a grade and size of bar, a bolt, a finished part.
type Item struct {
	ID   string `json:"id"`
	Cat  string `json:"cat"`
	Code string `json:"code"`
	Name string `json:"name"`
	// Attrs are the category's fields (牌号, 规格 …).
	Attrs map[string]string `json:"attrs,omitempty"`
	Unit  string            `json:"unit"`
	// Safety is the stock below which it is reordered (0: none kept).
	Safety float64 `json:"safety"`
	// Place is where in the store it is kept (库位).
	Place string `json:"place,omitempty"`
	// Stock is what is in store now; Low: at or below the safety stock.
	Stock float64 `json:"stock"`
	Low   bool    `json:"low"`
	// Heats are a bar's heats with something left (oldest first).
	Heats   []HeatStock `json:"heats,omitempty"`
	Updated time.Time   `json:"updated"`
}

// Label names an item in lists: its code and name, or the name alone when the code is the name.
func (it Item) Label() string {
	if it.Code == "" || it.Code == it.Name {
		return it.Name
	}
	return it.Code + " " + it.Name
}

// Move kinds.
const (
	In    = "in"
	Out   = "out"
	Count = "count"
)

// Move is one receipt (入库), issue (出库) or count (盘点).
type Move struct {
	ID   int64     `json:"id"`
	At   time.Time `json:"at"`
	Item string    `json:"item"`
	// ItemName is the item's code and name, for lists.
	ItemName string `json:"itemName,omitempty"`
	// Kind: in | out | count
	Kind string `json:"kind"`
	// Qty is what came in or went out; for a count, what was counted.
	Qty float64 `json:"qty"`
	// Delta is what the move changed the stock by (a count: counted − book).
	Delta float64 `json:"delta"`
	// Heat, Mill and Cert are a bar's heat number (炉号), steel mill and mill certificate (质保书).
	Heat string `json:"heat,omitempty"`
	Mill string `json:"mill,omitempty"`
	Cert string `json:"cert,omitempty"`
	// Ref is what it was for: an order number, a machine, a supplier's delivery note.
	Ref  string `json:"ref,omitempty"`
	Who  string `json:"who"`
	Note string `json:"note,omitempty"`
}

// Problems are what is wrong with what was sent.
type Problems struct{ List []string }

func (p *Problems) Error() string { return strings.Join(p.List, "；") }

// ErrNotFound: no such item, die or project.
var ErrNotFound = errors.New("not found")

// Twin is what the stores need of the simulation.
type Twin interface {
	Materials(company string) []engine.PlantMaterial
	SetHeats(company string, heats []engine.Heat) bool
	MountTool(company, plant, asset string, used, life int64) bool
	Alert(company, plant, asset, code, text string) bool
	CompanyIDs() []string
}

// Service keeps every company's stores, dies, development projects and BOMs in {data}/stores.db.
type Service struct {
	Twin Twin
	Log  *slog.Logger
	Now  func() time.Time
	// Categories are the categories a company's industry packs declare (nil: none).
	Categories func(company string) []Category
	// Products are the part numbers a company's lines make, from its config (nil: none known).
	Products func(company string) []ProductRef
	// BOMChanged hears that a company's BOMs changed (nil: no one).
	BOMChanged func(company string)
	// Event hears what deserves a note in the activity log: a die near the end of its life, an item
	// below its safety stock (nil: no one).
	Event func(company, kind, target, text string)

	db *sql.DB
	mu sync.Mutex
	// what the last look at the twin saw: a line's heat and pieces left, a press's cycles and tool wear
	seen map[string]seenLine
	hit  map[string]seenPress
}

type seenLine struct {
	heat string
	left int
}

type seenPress struct {
	cycles, used int64
}

func Open(dir string, twin Twin, log *slog.Logger) (*Service, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "stores.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS cats (company TEXT NOT NULL, key TEXT NOT NULL, json TEXT NOT NULL, PRIMARY KEY (company, key));
		CREATE TABLE IF NOT EXISTS items (company TEXT NOT NULL, id TEXT NOT NULL, json TEXT NOT NULL, PRIMARY KEY (company, id));
		CREATE TABLE IF NOT EXISTS moves (id INTEGER PRIMARY KEY AUTOINCREMENT, company TEXT NOT NULL, at INTEGER NOT NULL, item TEXT NOT NULL,
			kind TEXT NOT NULL, qty REAL NOT NULL, delta REAL NOT NULL, heat TEXT NOT NULL, mill TEXT NOT NULL, cert TEXT NOT NULL,
			ref TEXT NOT NULL, who TEXT NOT NULL, note TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS moves_item ON moves (company, item, at);
		CREATE INDEX IF NOT EXISTS moves_heat ON moves (company, heat);
		CREATE TABLE IF NOT EXISTS usage (company TEXT NOT NULL, heat TEXT NOT NULL, plant TEXT NOT NULL, line TEXT NOT NULL,
			ord TEXT NOT NULL, mill TEXT NOT NULL, line_name TEXT NOT NULL, pieces INTEGER NOT NULL, kg REAL NOT NULL,
			first INTEGER NOT NULL, last INTEGER NOT NULL, PRIMARY KEY (company, heat, plant, line, ord));
		CREATE INDEX IF NOT EXISTS usage_ord ON usage (company, ord);
		CREATE TABLE IF NOT EXISTS dies (company TEXT NOT NULL, no TEXT NOT NULL, json TEXT NOT NULL, PRIMARY KEY (company, no));
		CREATE TABLE IF NOT EXISTS die_events (id INTEGER PRIMARY KEY AUTOINCREMENT, company TEXT NOT NULL, die TEXT NOT NULL,
			at INTEGER NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL, who TEXT NOT NULL, strokes INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS die_events_die ON die_events (company, die, at);
		CREATE TABLE IF NOT EXISTS projects (company TEXT NOT NULL, id TEXT NOT NULL, json TEXT NOT NULL, PRIMARY KEY (company, id));
		CREATE TABLE IF NOT EXISTS trials (company TEXT NOT NULL, project TEXT NOT NULL, id TEXT NOT NULL, at INTEGER NOT NULL, json TEXT NOT NULL,
			PRIMARY KEY (company, id));
		CREATE INDEX IF NOT EXISTS trials_project ON trials (company, project, at);
		CREATE TABLE IF NOT EXISTS boms (company TEXT NOT NULL, id TEXT NOT NULL, json TEXT NOT NULL, PRIMARY KEY (company, id));
		CREATE TABLE IF NOT EXISTS ecos (company TEXT NOT NULL, id TEXT NOT NULL, part TEXT NOT NULL, at INTEGER NOT NULL, json TEXT NOT NULL,
			PRIMARY KEY (company, id));
		CREATE INDEX IF NOT EXISTS ecos_part ON ecos (company, part, at);
		CREATE TABLE IF NOT EXISTS purchases (company TEXT NOT NULL, id TEXT NOT NULL, json TEXT NOT NULL, PRIMARY KEY (company, id));
		CREATE TABLE IF NOT EXISTS seeded (company TEXT PRIMARY KEY)`); err != nil {
		db.Close()
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{Twin: twin, Log: log, Now: time.Now, db: db, seen: map[string]seenLine{}, hit: map[string]seenPress{}}, nil
}

func (s *Service) Close() error { return s.db.Close() }

func newID(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + strings.ToUpper(hex.EncodeToString(b))
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func round(v float64) float64 { return math.Round(v*1000) / 1000 }

// kgPer is how many kilograms one unit of a bar item is (0: a unit the twin cannot weigh).
func kgPer(unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "t", "吨":
		return 1000
	case "kg", "公斤", "千克":
		return 1
	}
	return 0
}

func (s *Service) putJSON(table, company, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	col := map[string]string{"cats": "key", "items": "id", "dies": "no", "projects": "id"}[table]
	_, err = s.db.Exec(fmt.Sprintf(`INSERT INTO %s (company, %s, json) VALUES (?, ?, ?)
		ON CONFLICT (company, %s) DO UPDATE SET json = excluded.json`, table, col, col), company, key, string(raw))
	return err
}

func getAll[T any](db *sql.DB, query string, args ...any) ([]T, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v T
		if json.Unmarshal([]byte(raw), &v) == nil {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// ---------- categories ----------

// Cats are the company's categories: the ones it added, then its industry packs' it has not.
func (s *Service) Cats(company string) ([]Category, error) {
	own, err := getAll[Category](s.db, `SELECT json FROM cats WHERE company = ? ORDER BY rowid`, company)
	if err != nil {
		return nil, err
	}
	out := []Category{}
	if s.Categories != nil {
		for _, c := range s.Categories(company) {
			if i := slices.IndexFunc(own, func(o Category) bool { return o.Key == c.Key }); i >= 0 {
				c = own[i]
				own = slices.Delete(own, i, i+1)
			}
			out = append(out, c)
		}
	}
	return append(out, own...), nil
}

func (s *Service) cat(company, key string) (Category, bool) {
	cs, _ := s.Cats(company)
	for _, c := range cs {
		if c.Key == key {
			return c, true
		}
	}
	return Category{}, false
}

// SaveCat adds a category of the company's own, or changes one.
func (s *Service) SaveCat(company string, c Category) (*Category, error) {
	var p []string
	c.Key, c.Label, c.Unit = strings.TrimSpace(c.Key), strings.TrimSpace(c.Label), strings.TrimSpace(c.Unit)
	if c.Label == "" {
		p = append(p, "分类要有名字")
	}
	if c.Key == "" {
		c.Key = strings.ToLower(newID("c"))
	}
	if !slices.Contains([]string{Bar, Part, Product, Other}, c.Kind) {
		c.Kind = Other
	}
	if c.Unit == "" {
		c.Unit = "件"
	}
	if c.Kind == Bar && kgPer(c.Unit) == 0 {
		p = append(p, "棒料的单位要用 t 或 kg，这样生产用掉的钢才能按重量扣")
	}
	if len(p) > 0 {
		return nil, &Problems{p}
	}
	return &c, s.putJSON("cats", company, c.Key, c)
}

// ---------- items ----------

// Items are the company's items with their stock, the category's first (cat "" every one).
func (s *Service) Items(company, cat string) ([]Item, error) {
	items, err := getAll[Item](s.db, `SELECT json FROM items WHERE company = ?`, company)
	if err != nil {
		return nil, err
	}
	stock, err := s.stocks(company)
	if err != nil {
		return nil, err
	}
	heats, err := s.Heats(company, "")
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, it := range items {
		if cat != "" && it.Cat != cat {
			continue
		}
		it.Stock = round(stock[it.ID])
		it.Low = it.Safety > 0 && it.Stock <= it.Safety
		for _, h := range heats {
			if h.Item == it.ID && h.Left > 1e-9 {
				it.Heats = append(it.Heats, h)
			}
		}
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, b Item) int { return strings.Compare(a.Cat+a.Code, b.Cat+b.Code) })
	return out, nil
}

// Item is one item with its stock.
func (s *Service) Item(company, id string) (*Item, error) {
	items, err := s.Items(company, "")
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ID == id || it.Code == id {
			return &it, nil
		}
	}
	return nil, ErrNotFound
}

// SaveItem adds an item or changes the one it names (by id).
func (s *Service) SaveItem(company string, it Item) (*Item, error) {
	var p []string
	it.Code, it.Name = strings.TrimSpace(it.Code), strings.TrimSpace(it.Name)
	c, ok := s.cat(company, it.Cat)
	if !ok {
		p = append(p, "选一个分类")
	}
	if it.Name == "" {
		p = append(p, "物料要有名称")
	}
	if utf8.RuneCountInString(it.Name) > 60 {
		p = append(p, "名称太长（最多 60 字）")
	}
	if it.Safety < 0 {
		p = append(p, "安全库存不能是负数")
	}
	if len(p) > 0 {
		return nil, &Problems{p}
	}
	items, err := getAll[Item](s.db, `SELECT json FROM items WHERE company = ?`, company)
	if err != nil {
		return nil, err
	}
	if it.Code == "" {
		it.Code = it.Name
	}
	for _, o := range items {
		if o.ID != it.ID && o.Code == it.Code {
			return nil, &Problems{[]string{fmt.Sprintf("编码 %s 已经有了（%s）", it.Code, o.Name)}}
		}
	}
	if it.ID == "" {
		it.ID = newID("I")
	} else if !slices.ContainsFunc(items, func(o Item) bool { return o.ID == it.ID }) {
		return nil, ErrNotFound
	}
	if it.Unit = strings.TrimSpace(it.Unit); it.Unit == "" {
		it.Unit = c.Unit
	}
	it.Stock, it.Low, it.Heats, it.Updated = 0, false, nil, s.Now()
	if err := s.putJSON("items", company, it.ID, it); err != nil {
		return nil, err
	}
	return s.Item(company, it.ID)
}

// stocks is every item's stock: receipts − issues + counts' differences − what the lines used of a bar.
func (s *Service) stocks(company string) (map[string]float64, error) {
	out := map[string]float64{}
	rows, err := s.db.Query(`SELECT item, SUM(delta) FROM moves WHERE company = ? GROUP BY item`, company)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var v float64
		rows.Scan(&id, &v)
		out[id] += v
	}
	rows.Close()
	heats, err := s.Heats(company, "")
	if err != nil {
		return nil, err
	}
	for _, h := range heats {
		out[h.Item] -= h.Used
	}
	return out, nil
}

// MoveInput is what a person records: a receipt, an issue or a count.
type MoveInput struct {
	Item string  `json:"item"`
	Kind string  `json:"kind"`
	Qty  float64 `json:"qty"`
	Heat string  `json:"heat,omitempty"`
	Mill string  `json:"mill,omitempty"`
	Cert string  `json:"cert,omitempty"`
	Ref  string  `json:"ref,omitempty"`
	Note string  `json:"note,omitempty"`
}

// Record books a receipt, an issue or a count by who, and gives the item as it is after it.
func (s *Service) Record(company, who string, in MoveInput) (*Move, *Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, err := s.Item(company, in.Item)
	if err != nil {
		return nil, nil, err
	}
	c, _ := s.cat(company, it.Cat)
	in.Heat, in.Mill, in.Cert, in.Ref, in.Note = strings.TrimSpace(in.Heat), strings.TrimSpace(in.Mill), strings.TrimSpace(in.Cert), strings.TrimSpace(in.Ref), strings.TrimSpace(in.Note)
	var p []string
	if !slices.Contains([]string{In, Out, Count}, in.Kind) {
		p = append(p, "是入库、出库还是盘点？")
	}
	if in.Qty < 0 || (in.Kind != Count && in.Qty == 0) || math.IsNaN(in.Qty) || math.IsInf(in.Qty, 0) {
		p = append(p, "数量要大于 0（盘点可以是 0）")
	}
	bar := c.Kind == Bar
	var heat *HeatStock
	if bar {
		if in.Heat == "" {
			p = append(p, "棒料要填炉号")
		} else {
			hs, _ := s.Heats(company, in.Heat)
			if len(hs) > 0 {
				heat = &hs[0]
				if heat.Item != it.ID {
					p = append(p, fmt.Sprintf("炉号 %s 已经记在 %s 下面", in.Heat, heat.ItemName))
				}
			}
		}
		if in.Kind == In && heat == nil && in.Mill == "" {
			p = append(p, "新炉号入库要填钢厂")
		}
		if in.Kind != In && heat == nil && in.Heat != "" {
			p = append(p, fmt.Sprintf("库里没有炉号 %s", in.Heat))
		}
	}
	if len(p) > 0 {
		return nil, nil, &Problems{p}
	}
	m := Move{At: s.Now(), Item: it.ID, Kind: in.Kind, Qty: round(in.Qty), Heat: in.Heat, Mill: in.Mill, Cert: in.Cert, Ref: in.Ref, Who: who, Note: in.Note}
	if heat != nil {
		if m.Mill == "" {
			m.Mill = heat.Mill
		}
		if m.Cert == "" {
			m.Cert = heat.Cert
		}
	}
	// what it is taken from: the heat's for a bar, the item's otherwise
	book := it.Stock
	if heat != nil {
		book = heat.Left
	}
	switch in.Kind {
	case In:
		m.Delta = m.Qty
	case Out:
		if m.Qty > book+1e-9 {
			what := it.Name
			if heat != nil {
				what = "炉号 " + heat.No
			}
			return nil, nil, &Problems{[]string{fmt.Sprintf("%s 只剩 %s %s，出不了 %s", what, num(book), it.Unit, num(m.Qty))}}
		}
		m.Delta = -m.Qty
	case Count:
		m.Delta = round(m.Qty - book)
	}
	res, err := s.db.Exec(`INSERT INTO moves (company, at, item, kind, qty, delta, heat, mill, cert, ref, who, note) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		company, ms(m.At), m.Item, m.Kind, m.Qty, m.Delta, m.Heat, m.Mill, m.Cert, m.Ref, m.Who, m.Note)
	if err != nil {
		return nil, nil, err
	}
	m.ID, _ = res.LastInsertId()
	m.ItemName = it.Label()
	if in.Kind == In {
		s.settle(company, it.ID, in.Ref, m.Qty)
	}
	after, err := s.Item(company, it.ID)
	if err == nil && after.Low && !it.Low && s.Event != nil {
		s.Event(company, "stores.low", after.Code, fmt.Sprintf("%s 库存 %s %s，到了安全库存 %s 以下", after.Name, num(after.Stock), after.Unit, num(after.Safety)))
	}
	return &m, after, err
}

func num(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
}

// Moves are the company's receipts, issues and counts, the newest first: of one item, one heat,
// or (both "") all of them.
func (s *Service) Moves(company, item, heat string, limit int) ([]Move, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT m.id, m.at, m.item, m.kind, m.qty, m.delta, m.heat, m.mill, m.cert, m.ref, m.who, m.note FROM moves m WHERE m.company = ?`
	args := []any{company}
	if item != "" {
		q += ` AND m.item = ?`
		args = append(args, item)
	}
	if heat != "" {
		q += ` AND m.heat = ?`
		args = append(args, heat)
	}
	q += ` ORDER BY m.at DESC, m.id DESC LIMIT ?`
	rows, err := s.db.Query(q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]string{}
	items, _ := getAll[Item](s.db, `SELECT json FROM items WHERE company = ?`, company)
	for _, it := range items {
		names[it.ID] = it.Label()
	}
	out := []Move{}
	for rows.Next() {
		var m Move
		var at int64
		if err := rows.Scan(&m.ID, &at, &m.Item, &m.Kind, &m.Qty, &m.Delta, &m.Heat, &m.Mill, &m.Cert, &m.Ref, &m.Who, &m.Note); err != nil {
			return nil, err
		}
		m.At, m.ItemName = time.UnixMilli(at), names[m.Item]
		out = append(out, m)
	}
	return out, rows.Err()
}
