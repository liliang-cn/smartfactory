// Package knowledge is each company's knowledge base: its process as a graph (plants, lines,
// stations in order, products, machines, suppliers, customers) laid out by the engine, its
// production orders linked to the customer, product and line of each, and its documents cut into
// passages and linked to the things in that graph they name. Questions are answered from all of
// them at once: a passage that names a machine is found from a question that only names the
// station built from it, a customer's orders from a question that names the customer. It is a
// CortexDB file per company, without embeddings (BM25 plus a personalized PageRank walk over the
// graph, started from the things the question names).
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"

	"smartfactory/internal/engine"
)

// Base holds every company's knowledge, opened when first used.
type Base struct {
	dir string
	eng *engine.Engine
	log *slog.Logger

	mu  sync.Mutex
	dbs map[string]*company

	// Orders reads a company's production orders (from the ERP connection); nil keeps none.
	Orders func(company string) ([]OrderInfo, error)
	omu    sync.Mutex
	dirty  map[string]bool
	kick   chan struct{}
}

type company struct {
	mu sync.Mutex // one writer at a time
	db *cortexdb.DB
}

func Open(dir string, eng *engine.Engine, log *slog.Logger) (*Base, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Base{dir: dir, eng: eng, log: log, dbs: map[string]*company{}, dirty: map[string]bool{}, kick: make(chan struct{}, 1)}, nil
}

func (b *Base) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.dbs {
		c.db.Close()
	}
	b.dbs = map[string]*company{}
}

func (b *Base) open(id string) (*company, error) {
	if !engine.ValidID.MatchString(id) {
		return nil, fmt.Errorf("company %q", id)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.dbs[id]; ok {
		return c, nil
	}
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(b.dir, id+".db")))
	if err != nil {
		return nil, err
	}
	c := &company{db: db}
	b.dbs[id] = c
	return c, nil
}

// Drop forgets a deleted company's knowledge.
func (b *Base) Drop(id string) {
	b.mu.Lock()
	if c, ok := b.dbs[id]; ok {
		c.db.Close()
		delete(b.dbs, id)
	}
	b.mu.Unlock()
	if engine.ValidID.MatchString(id) {
		for _, f := range []string{id + ".db", id + ".db-wal", id + ".db-shm", id + ".process.json", id + ".orders.json"} {
			os.Remove(filepath.Join(b.dir, f))
		}
	}
}

// ---- the process ----

// Model is a company's process as the engine lays it out.
type Model struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Plants []Plant `json:"plants"`
}

type Plant struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Hall          string   `json:"hall"`
	Site          string   `json:"site"`
	Industry      string   `json:"industry"`
	Unit          string   `json:"unit"`
	Warehouse     string   `json:"warehouse"`
	FinishedGoods string   `json:"finishedGoods"`
	Carriers      []string `json:"carriers"`
	Lines         []Line   `json:"lines"`
}

type Line struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Note     string    `json:"note"`
	Product  Product   `json:"product"`
	Stations []Station `json:"stations"`
}

type Product struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Stages    []string `json:"stages"`
	PartNo    string   `json:"partNo"`
	Material  string   `json:"material"`
	Weight    float64  `json:"weight"`
	Customers []string `json:"customers"`
	Suppliers []string `json:"suppliers"`
	// Variants are the part numbers of the family a line can run.
	Variants []string `json:"variants"`
}

type Station struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Name  string  `json:"name"`
	Code  string  `json:"code"`
	Cycle float64 `json:"cycle"`
	Makes string  `json:"makes"`
	Kind  string  `json:"kind"`
}

// The kinds of things in the graph, and how they relate.
const (
	TCompany  = "company"
	TPlant    = "plant"
	TLine     = "line"
	TStation  = "station"
	TMachine  = "machine"
	TProduct  = "product"
	TSupplier = "supplier"
	TCustomer = "customer"
	TOrder    = "order"

	RHasPlant   = "has_plant"
	RHasLine    = "has_line"
	RMakes      = "makes"
	RHasStation = "has_station"
	RIsA        = "is_a"
	RFeeds      = "feeds"
	RSupplies   = "supplies"
	RSoldTo     = "sold_to"
	// an order is for a customer, of a product, and runs on a line
	RForCustomer = "for_customer"
	ROfProduct   = "of_product"
	RRunsOn      = "runs_on"
)

// relations are the relation types the graph keeps (the rest are CortexDB's own: mentions, …).
var relations = []string{RHasPlant, RHasLine, RMakes, RHasStation, RIsA, RFeeds, RSupplies, RSoldTo, RForCustomer, ROfProduct, RRunsOn}

func (b *Base) model(id string) (*Model, error) {
	js := b.eng.ProcessModel(id)
	if js == "null" {
		return nil, fmt.Errorf("no company %q", id)
	}
	var m Model
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// names gives each thing in the model the name it is known by in the graph. A line or station
// name that recurs in another plant gets the plant's name in front ("二厂 HP-101").
type names struct {
	line    map[string]string // plant/line → name
	station map[string]string // plant/station → name
}

func nameModel(m *Model) names {
	n := names{line: map[string]string{}, station: map[string]string{}}
	lineCount, stCount := map[string]int{}, map[string]int{}
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			lineCount[l.Name]++
			for _, s := range l.Stations {
				stCount[s.ID]++
			}
		}
	}
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			n.line[p.ID+"/"+l.ID] = l.Name
			if lineCount[l.Name] > 1 {
				n.line[p.ID+"/"+l.ID] = p.Name + " " + l.Name
			}
			for _, s := range l.Stations {
				n.station[p.ID+"/"+s.ID] = s.ID
				if stCount[s.ID] > 1 {
					n.station[p.ID+"/"+s.ID] = p.Name + " " + s.ID
				}
			}
		}
	}
	return n
}

// item is one passage the base keeps, with the things it is about.
type item struct {
	id, title, text string
	meta            map[string]string
	ents            []cortexdb.ToolEntityInput
	rels            []cortexdb.ToolRelationInput
}

func (it *item) ent(name, typ, desc string) {
	for _, e := range it.ents {
		if e.Name == name && e.Type == typ {
			return
		}
	}
	it.ents = append(it.ents, cortexdb.ToolEntityInput{Name: name, Type: typ, Description: desc, ChunkIDs: []string{chunkID(it.id)}})
}

func (it *item) rel(from, typ, to string) {
	it.rels = append(it.rels, cortexdb.ToolRelationInput{From: from, To: to, Type: typ, ChunkIDs: []string{chunkID(it.id)}})
}

func chunkID(kid string) string { return "chunk:" + kid + ":000" }

// processItems writes the model out as passages: the company, each plant, each line with its
// stations in order, each product with its suppliers and customers.
func processItems(m *Model) []item {
	n := nameModel(m)
	var out []item
	co := item{id: "proc:company", title: m.Name, meta: map[string]string{"kind": "process"}}
	co.ent(m.Name, TCompany, "")
	var plants []string
	for _, p := range m.Plants {
		lines := 0
		for range p.Lines {
			lines++
		}
		plants = append(plants, fmt.Sprintf("%s（%s，%d 条线）", p.Name, p.Hall, lines))
		co.ent(p.Name, TPlant, p.Hall)
		co.rel(m.Name, RHasPlant, p.Name)
	}
	co.text = fmt.Sprintf("%s有 %d 个工厂：%s。", m.Name, len(m.Plants), strings.Join(plants, "、"))
	out = append(out, co)

	products := map[string]Product{}
	var order []string
	for _, p := range m.Plants {
		pl := item{id: "proc:plant:" + p.ID, title: m.Name + " " + p.Name, meta: map[string]string{"kind": "process", "plant": p.ID}}
		pl.ent(p.Name, TPlant, p.Hall)
		var lines []string
		for _, l := range p.Lines {
			ln := n.line[p.ID+"/"+l.ID]
			pl.ent(ln, TLine, l.Note)
			pl.rel(p.Name, RHasLine, ln)
			lines = append(lines, fmt.Sprintf("%s（%s，%d 台设备）", ln, l.Product.Name, len(l.Stations)))
		}
		site := "车间"
		if p.Site == "outdoor" {
			site = "户外场地"
		}
		pl.text = fmt.Sprintf("%s的%s，%s「%s」，行业：%s。有 %d 条线：%s。原料存放在%s，成品放在%s%s。",
			m.Name, p.Name, site, p.Hall, p.Industry, len(p.Lines), strings.Join(lines, "、"), p.Warehouse, p.FinishedGoods,
			func() string {
				if len(p.Carriers) == 0 {
					return ""
				}
				return "，由" + strings.Join(uniq(p.Carriers), "、") + "运给客户"
			}())
		out = append(out, pl)

		for _, l := range p.Lines {
			ln := n.line[p.ID+"/"+l.ID]
			it := item{id: "proc:line:" + p.ID + ":" + l.ID, title: p.Name + " " + l.Name, meta: map[string]string{"kind": "process", "plant": p.ID, "line": l.ID}}
			it.ent(ln, TLine, l.Note)
			it.ent(l.Product.Name, TProduct, l.Product.PartNo)
			it.rel(ln, RMakes, l.Product.Name)
			var steps []string
			var prev string
			slowest := Station{}
			for i, s := range l.Stations {
				sn := n.station[p.ID+"/"+s.ID]
				it.ent(sn, TStation, s.Name)
				it.ent(s.Label, TMachine, "")
				it.rel(ln, RHasStation, sn)
				it.rel(sn, RIsA, s.Label)
				if prev != "" {
					it.rel(prev, RFeeds, sn)
				}
				prev = sn
				step := fmt.Sprintf("%d. %s %s，节拍 %g 秒", i+1, s.Name, s.ID, s.Cycle)
				if s.Name != s.Label {
					step += "（" + s.Label + "）"
				}
				if s.Makes != "" && readable(s.Makes) {
					step += "，做成" + s.Makes
				}
				steps = append(steps, step)
				if s.Cycle > slowest.Cycle {
					slowest = s
				}
			}
			it.text = fmt.Sprintf("%s %s%s生产%s。工序依次是：\n%s\n最慢的工序是 %s %s（%g 秒），决定这条线的节拍。",
				p.Name, l.Name, paren(l.Note), l.Product.Name, strings.Join(steps, "\n"), slowest.ID, slowest.Name, slowest.Cycle)
			out = append(out, it)
			out = append(out, stationItems(n, p, l, ln)...)
			if _, ok := products[l.Product.Key]; !ok {
				products[l.Product.Key] = l.Product
				order = append(order, l.Product.Key)
			}
		}
	}
	for _, k := range order {
		pr := products[k]
		it := item{id: "proc:product:" + k, title: pr.Name, meta: map[string]string{"kind": "process", "product": k}}
		it.ent(pr.Name, TProduct, pr.PartNo)
		var b strings.Builder
		fmt.Fprintf(&b, "%s", pr.Name)
		if pr.PartNo != "" {
			fmt.Fprintf(&b, "（零件号 %s）", pr.PartNo)
		}
		if vs := uniq(pr.Variants); len(vs) > 1 || len(vs) == 1 && vs[0] != pr.PartNo {
			fmt.Fprintf(&b, "，可做料号 %s", strings.Join(vs, "、"))
		}
		if pr.Material != "" {
			fmt.Fprintf(&b, "，材料 %s", pr.Material)
		}
		if pr.Weight > 0 {
			fmt.Fprintf(&b, "，单件 %g kg", pr.Weight)
		}
		if len(pr.Stages) > 0 && readable(pr.Stages[0]) {
			fmt.Fprintf(&b, "。工序状态：%s", strings.Join(pr.Stages, " → "))
		}
		for _, s := range uniq(pr.Suppliers) {
			it.ent(s, TSupplier, "")
			it.rel(s, RSupplies, pr.Name)
		}
		for _, c := range uniq(pr.Customers) {
			it.ent(c, TCustomer, "")
			it.rel(pr.Name, RSoldTo, c)
		}
		if len(pr.Suppliers) > 0 {
			fmt.Fprintf(&b, "。原材料供应商：%s", strings.Join(uniq(pr.Suppliers), "、"))
		}
		if len(pr.Customers) > 0 {
			fmt.Fprintf(&b, "。客户：%s", strings.Join(uniq(pr.Customers), "、"))
		}
		b.WriteString("。")
		it.text = b.String()
		out = append(out, it)
	}
	return out
}

// stationItems are one passage per station of a line: where it is, what it is, what comes before
// and after it, so "HP-101 的上游" is answered by the passage about HP-101.
func stationItems(n names, p Plant, l Line, ln string) []item {
	out := make([]item, 0, len(l.Stations))
	for i, s := range l.Stations {
		sn := n.station[p.ID+"/"+s.ID]
		it := item{id: "proc:station:" + p.ID + ":" + s.ID, title: sn + " " + s.Name, meta: map[string]string{"kind": "process", "plant": p.ID, "line": l.ID, "station": s.ID}}
		it.ent(sn, TStation, s.Name)
		it.ent(s.Label, TMachine, "")
		it.ent(ln, TLine, l.Note)
		var b strings.Builder
		fmt.Fprintf(&b, "%s 是%s %s的第 %d 道工序：%s", sn, p.Name, l.Name, i+1, s.Name)
		if s.Name != s.Label {
			fmt.Fprintf(&b, "（%s）", s.Label)
		}
		fmt.Fprintf(&b, "，节拍 %g 秒", s.Cycle)
		if s.Makes != "" && readable(s.Makes) {
			fmt.Fprintf(&b, "，做成%s", s.Makes)
		}
		// the neighbours are named in the text only: declared, every station's passage would sit
		// next to two others and a walk from one station would spread along the whole line
		if i > 0 {
			up := l.Stations[i-1]
			fmt.Fprintf(&b, "。上游（上一道）是 %s %s", n.station[p.ID+"/"+up.ID], up.Name)
		} else {
			b.WriteString("。它是这条线的第一道，上游是原料")
		}
		if i+1 < len(l.Stations) {
			dn := l.Stations[i+1]
			fmt.Fprintf(&b, "；下游（下一道）是 %s %s。", n.station[p.ID+"/"+dn.ID], dn.Name)
		} else {
			b.WriteString("；它是最后一道，下游是成品。")
		}
		it.text = b.String()
		out = append(out, it)
	}
	return out
}

func paren(s string) string {
	if s == "" {
		return ""
	}
	return "（" + s + "）"
}

// readable reports whether a stage name is written for people (a config's own keys, like
// "billet", are not).
func readable(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}

func uniq(s []string) []string {
	out := []string{}
	for _, x := range s {
		if x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// processFormat changes whenever processItems writes differently, so every base is written again.
const processFormat = "5"

// processState is what was last written for a company's process, so an unchanged process is not
// written again.
type processState struct {
	Hash string   `json:"hash"`
	IDs  []string `json:"ids"`
}

func (b *Base) statePath(id string) string { return filepath.Join(b.dir, id+".process.json") }

// SyncProcess writes the company's process into its knowledge base, if it changed since the
// last time.
func (b *Base) SyncProcess(ctx context.Context, id string) error {
	m, err := b.model(id)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(append(raw, processFormat...))
	hash := hex.EncodeToString(sum[:])
	var st processState
	if prev, err := os.ReadFile(b.statePath(id)); err == nil {
		json.Unmarshal(prev, &st)
	}
	if st.Hash == hash {
		return nil
	}
	c, err := b.open(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	items := processItems(m)
	keep := map[string]bool{}
	for _, it := range items {
		keep[it.id] = true
	}
	for _, old := range st.IDs {
		if !keep[old] {
			c.db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: old})
		}
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		if err := save(ctx, c.db, it); err != nil {
			return fmt.Errorf("%s: %w", it.id, err)
		}
		ids = append(ids, it.id)
	}
	prune(ctx, c.db, nil)
	out, _ := json.Marshal(processState{Hash: hash, IDs: ids})
	return os.WriteFile(b.statePath(id), out, 0o644)
}

// prune removes the nodes CortexDB's own extraction adds beside the ones declared here: the
// prefixes of codes ("HP" of HP-101, which would tie every press of every plant together) and
// passage titles read as names ("一厂 曲轴 1 线" beside the line 曲轴 1 线). A walk over the graph
// leaks through them into everything that shares a prefix. Given the orders to keep, it also
// removes the order nodes of the others.
func prune(ctx context.Context, db *cortexdb.DB, keepOrders map[string]bool) {
	types := []string{"entity"}
	if keepOrders != nil {
		types = append(types, TOrder)
	}
	nodes, err := db.Graph().ListNodes(ctx, &graph.GraphFilter{NodeTypes: types})
	if err != nil {
		return
	}
	var ids []string
	for _, n := range nodes {
		if n.NodeType == TOrder {
			name, _ := n.Properties["name"].(string)
			if name == "" {
				name = n.Content
			}
			if keepOrders[name] {
				continue
			}
		}
		ids = append(ids, n.ID)
	}
	if len(ids) > 0 {
		db.Graph().DeleteNodesBatch(ctx, ids)
	}
}

func save(ctx context.Context, db *cortexdb.DB, it item) error {
	_, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
		KnowledgeID: it.id, Title: it.title, Content: it.text, ChunkSize: 8000, Metadata: it.meta,
		Entities: it.ents, Relations: it.rels,
	})
	return err
}

// ---- documents ----

// Part is one passage of a document as the library cut it.
type Part struct {
	Seq     int
	Heading string
	Text    string
}

// vocabulary is the names in the company's graph, with their kinds, longest first: what a
// question is matched against.
func (b *Base) vocabulary(id string) [][2]string {
	m, err := b.model(id)
	if err != nil {
		return nil
	}
	n := nameModel(m)
	seen := map[string]string{}
	add := func(name, typ string) {
		if len([]rune(name)) >= 2 {
			if _, ok := seen[name]; !ok {
				seen[name] = typ
			}
		}
	}
	for _, p := range m.Plants {
		add(p.Name, TPlant)
		for _, l := range p.Lines {
			add(n.line[p.ID+"/"+l.ID], TLine)
			add(l.Product.Name, TProduct)
			for _, s := range uniq(l.Product.Suppliers) {
				add(s, TSupplier)
			}
			for _, c := range uniq(l.Product.Customers) {
				add(c, TCustomer)
			}
			for _, s := range l.Stations {
				add(n.station[p.ID+"/"+s.ID], TStation)
				add(s.Label, TMachine)
			}
		}
	}
	add(m.Name, TCompany)
	for _, v := range b.orderVocabulary(id) {
		add(v[0], v[1])
	}
	out := make([][2]string, 0, len(seen))
	for k, v := range seen {
		out = append(out, [2]string{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := len([]rune(out[i][0])), len([]rune(out[j][0])); a != b {
			return a > b
		}
		return out[i][0] < out[j][0]
	})
	return out
}

// linkTerms is every way a document can name something in the company's process graph: names,
// station numbers and machine codes, line names, part numbers — the terms a document is linked
// to the process by.
func (b *Base) linkTerms(id string) []term {
	m, err := b.model(id)
	if err != nil {
		return nil
	}
	return terms(m)
}

func docID(doc string, seq int) string { return fmt.Sprintf("doc:%s:%03d", doc, seq) }

// AddDoc keeps a document's passages, each linked to the machines, stations, lines, products,
// suppliers and customers of the process it names.
func (b *Base) AddDoc(ctx context.Context, company, doc, title string, parts []Part) error {
	c, err := b.open(company)
	if err != nil {
		return err
	}
	vocab := b.linkTerms(company)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range parts {
		// a document's first heading is usually its title again
		heading := strings.TrimPrefix(strings.TrimPrefix(p.Heading, title), " › ")
		t := title
		if heading != "" {
			t += " › " + heading
		}
		it := item{id: docID(doc, p.Seq), title: t, text: p.Text, meta: map[string]string{"kind": "doc", "doc": doc, "doc_title": title, "heading": heading, "part": fmt.Sprint(p.Seq)}}
		for _, e := range link(vocab, heading+"\n"+p.Text) {
			it.ent(e.name, e.typ, "")
		}
		if err := save(ctx, c.db, it); err != nil {
			return err
		}
	}
	prune(ctx, c.db, nil)
	return nil
}

// RemoveDoc forgets a document's passages.
func (b *Base) RemoveDoc(ctx context.Context, company, doc string, parts int) {
	c, err := b.open(company)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range parts {
		c.db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: docID(doc, i)})
	}
}

// HasDoc reports whether a document's passages are in the base.
func (b *Base) HasDoc(ctx context.Context, company, doc string) bool {
	c, err := b.open(company)
	if err != nil {
		return false
	}
	_, err = c.db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: docID(doc, 0)})
	return err == nil
}

// ---- asking ----

// Passage is one piece of what the base knows, found for a question.
type Passage struct {
	// From: doc (a document in the library) | process (the company's process) | order (a
	// production order)
	From  string `json:"from"`
	Title string `json:"title"`
	// Doc and Part locate a document passage (read_doc reads around it).
	Doc      string   `json:"doc,omitempty"`
	Part     int      `json:"part"`
	Text     string   `json:"text"`
	Entities []string `json:"entities,omitempty"`
}

// Answer is what a search found and how it searched.
type Answer struct {
	Passages []Passage `json:"passages"`
	// Mode is how it searched: lexical, or ppr (a walk from the things the question names)
	Mode     string   `json:"mode"`
	Entities []string `json:"entities,omitempty"`
}

// Search finds the passages that answer query. names are things the question is about, if the
// caller knows them; keywords other words to look for.
func (b *Base) Search(ctx context.Context, company, query string, names, keywords []string, k int) (Answer, error) {
	c, err := b.open(company)
	if err != nil {
		return Answer{}, err
	}
	if k <= 0 {
		k = 6
	}
	// the things the question names, in the company's own words: the walk starts from them
	found := b.Recognize(company, query)
	names = uniq(append(slices.Clone(names), found...))
	// more than asked for, to put the passages about the things named first
	pool := max(k*3, 12)
	req := cortexdb.KnowledgeSearchRequest{
		Query: query, EntityNames: names, Keywords: uniq(append(slices.Clone(keywords), found...)), TopK: pool, MaxContextChunks: pool, RetrievalMode: "auto",
	}
	if len(found) > 0 {
		// co-occurrence is a statistical guess; the relations written here are facts
		req.RetrievalMode = "ppr"
		req.PPR = &cortexdb.PPRRetrievalOptions{EdgeTypeWeights: map[string]float64{"co_occurs": 0}}
	}
	r, err := c.db.SearchKnowledge(ctx, req)
	if err != nil {
		return Answer{}, err
	}
	meta := map[string]map[string]string{}
	titles := map[string]string{}
	for _, h := range r.Results {
		meta[h.KnowledgeID] = h.Metadata
		titles[h.KnowledgeID] = h.Title
	}
	out := Answer{Passages: []Passage{}, Mode: r.Decision.EffectiveMode, Entities: r.Entities}
	seen := map[string]bool{}
	for _, ch := range r.Chunks {
		if seen[ch.DocumentID] {
			continue
		}
		seen[ch.DocumentID] = true
		p := Passage{From: "process", Title: titles[ch.DocumentID], Text: ch.Content, Entities: ch.Entities}
		if m := meta[ch.DocumentID]; m["kind"] == "order" || strings.HasPrefix(ch.DocumentID, "order:") {
			p.From = "order"
		} else if m["kind"] == "doc" || strings.HasPrefix(ch.DocumentID, "doc:") {
			p.From = "doc"
			p.Doc = m["doc"]
			fmt.Sscan(m["part"], &p.Part)
			if p.Doc == "" {
				parts := strings.Split(ch.DocumentID, ":")
				if len(parts) == 3 {
					p.Doc = parts[1]
					fmt.Sscan(parts[2], &p.Part)
				}
			}
		}
		out.Passages = append(out.Passages, p)
	}
	out.Passages = aboutFirst(out.Passages, found)
	if len(out.Passages) > k {
		out.Passages = out.Passages[:k]
	}
	return out, nil
}

// aboutFirst puts first the passages about a thing the question names (its name in the title:
// the station's own passage, the line's, the order's), then the orders that name one (a line's or
// a customer's orders), keeping the search's order otherwise.
func aboutFirst(ps []Passage, named []string) []Passage {
	if len(named) == 0 {
		return ps
	}
	rank := func(p Passage) int {
		for _, n := range named {
			if strings.HasPrefix(p.Title, n) || strings.HasSuffix(p.Title, " "+n) || p.Title == n {
				return 0
			}
		}
		if p.From == "order" {
			for _, n := range named {
				if strings.Contains(p.Text, n) {
					return 1
				}
			}
		}
		return 2
	}
	out := slices.Clone(ps)
	slices.SortStableFunc(out, func(a, b Passage) int { return rank(a) - rank(b) })
	return out
}

// orderNo is how an order number looks in a question: letters, a dash, letters and digits.
var orderNo = regexp.MustCompile(`[A-Za-z]{1,6}-[A-Za-z0-9][A-Za-z0-9-]*`)

// Recognize is the things in the company's graph a question names: whole names (the longest
// first, each part of the question used once), a product, customer or supplier by the end of its
// name ("法兰" for 风电法兰), and order numbers.
func (b *Base) Recognize(company, query string) []string {
	var out []string
	rest := query
	vocab := b.vocabulary(company)
	for _, v := range vocab {
		if strings.Contains(rest, v[0]) {
			out = append(out, v[0])
			rest = strings.ReplaceAll(rest, v[0], " ")
		}
	}
	for _, v := range vocab {
		if v[1] != TProduct && v[1] != TCustomer && v[1] != TSupplier || slices.Contains(out, v[0]) {
			continue
		}
		r := []rune(v[0])
		for n := min(4, len(r)-1); n >= 2; n-- {
			if tail := string(r[len(r)-n:]); readable(tail) && strings.Contains(rest, tail) {
				out = append(out, v[0])
				break
			}
		}
	}
	orders := b.orderState(company).Items
	for _, no := range orderNo.FindAllString(query, -1) {
		if _, ok := orders[no]; ok && !slices.Contains(out, no) {
			out = append(out, no)
		}
	}
	return out
}

// ---- the graph ----

// Link is one relation of a thing in the graph.
type Link struct {
	Type string `json:"type"`
	// Out: the thing is the subject ("IH-101 feeds HP-101" is out from IH-101)
	Out   bool   `json:"out"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
}

// Entity is one thing in the graph with everything it is related to.
type Entity struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Links []Link `json:"links"`
	// Mentions are the passages that name it.
	Mentions []string `json:"mentions"`
}

var kinds = []string{TCompany, TPlant, TLine, TStation, TMachine, TProduct, TSupplier, TCustomer, TOrder}

// Lookup is the thing called name (of kind, if given) and its relations.
func (b *Base) Lookup(ctx context.Context, company, name string) (*Entity, error) {
	c, err := b.open(company)
	if err != nil {
		return nil, err
	}
	q := func(cy string) [][]any {
		r, err := c.db.QueryCypher(ctx, cortexdb.CypherQueryRequest{Query: cy, Params: map[string]any{"name": name}, MaxRows: 400})
		if err != nil {
			b.log.Warn("knowledge cypher", "err", err)
			return nil
		}
		return r.Rows
	}
	e := &Entity{Name: name, Links: []Link{}, Mentions: []string{}}
	for _, row := range q(`MATCH (n {name: $name}) RETURN labels(n)`) {
		for _, l := range row[0].([]any) {
			if s, _ := l.(string); slices.Contains(kinds, s) {
				e.Kind = s
			}
		}
	}
	if e.Kind == "" {
		return nil, nil
	}
	add := func(rows [][]any, out bool) {
		for _, row := range rows {
			typ, _ := row[0].(string)
			other, _ := row[2].(string)
			var kind string
			for _, l := range asList(row[1]) {
				if slices.Contains(kinds, l) {
					kind = l
				}
			}
			if typ == "mentions" || kind == "" {
				if typ == "mentions" && !out && other != "" && !slices.Contains(e.Mentions, other) {
					e.Mentions = append(e.Mentions, other)
				}
				continue
			}
			e.Links = append(e.Links, Link{Type: typ, Out: out, Name: other, Kind: kind})
		}
	}
	add(q(`MATCH (n {name: $name})-[r]->(m) RETURN type(r), labels(m), m.name`), true)
	add(q(`MATCH (m)-[r]->(n {name: $name}) RETURN type(r), labels(m), m.name`), false)
	return e, nil
}

func anyList(s []string) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x
	}
	return out
}

func asList(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// Find lists the things whose name contains q (all of kind, if given), up to limit.
func (b *Base) Find(ctx context.Context, company, q, kind string, limit int) ([]Link, error) {
	c, err := b.open(company)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	label := ""
	if slices.Contains(kinds, kind) {
		label = ":" + kind
	}
	r, err := c.db.QueryCypher(ctx, cortexdb.CypherQueryRequest{
		Query:  fmt.Sprintf(`MATCH (n%s) WHERE n.name CONTAINS $q RETURN n.name, labels(n) LIMIT %d`, label, limit*3),
		Params: map[string]any{"q": q}, MaxRows: limit * 3,
	})
	if err != nil {
		return nil, err
	}
	out := []Link{}
	for _, row := range r.Rows {
		name, _ := row[0].(string)
		for _, l := range asList(row[1]) {
			if slices.Contains(kinds, l) && len(out) < limit {
				out = append(out, Link{Name: name, Kind: l})
			}
		}
	}
	return out, nil
}

// Stats counts the things and relations in a company's graph, and its passages.
type Stats struct {
	Kinds     map[string]int `json:"kinds"`
	Relations map[string]int `json:"relations"`
	Docs      int            `json:"docs"`
	Process   int            `json:"process"`
	// Orders are the production orders kept, one passage each.
	Orders int `json:"orders"`
}

func (b *Base) Stats(ctx context.Context, company string) (Stats, error) {
	c, err := b.open(company)
	if err != nil {
		return Stats{}, err
	}
	st := Stats{Kinds: map[string]int{}, Relations: map[string]int{}}
	s, err := c.db.GraphSchema(ctx, cortexdb.GraphSchemaRequest{})
	if err != nil {
		return st, err
	}
	for _, t := range s.NodeTypes {
		if slices.Contains(kinds, t.NodeType) {
			st.Kinds[t.NodeType] = t.Count
		}
		if t.NodeType == "chunk" {
			st.Docs = t.Count
		}
	}
	for _, t := range s.EdgeTypes {
		if slices.Contains(relations, t.EdgeType) || t.EdgeType == "mentions" {
			st.Relations[t.EdgeType] = t.Count
		}
	}
	var ps processState
	if raw, err := os.ReadFile(b.statePath(company)); err == nil {
		json.Unmarshal(raw, &ps)
	}
	st.Process = len(ps.IDs)
	st.Orders = len(b.orderState(company).Items)
	st.Docs -= st.Process + st.Orders
	return st, nil
}

// Cypher runs a read-only query over the company's graph.
func (b *Base) Cypher(ctx context.Context, company, query string) (*cortexdb.CypherQueryResponse, error) {
	c, err := b.open(company)
	if err != nil {
		return nil, err
	}
	return c.db.QueryCypher(ctx, cortexdb.CypherQueryRequest{Query: query, MaxRows: 200, TimeoutMS: 5000})
}

// Graph is a company's process graph whole, for drawing: every thing with how many passages
// name it, and every relation between them.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type GraphNode struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Mentions is how many passages (documents and process) name it.
	Mentions int `json:"mentions"`
}

type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

func (b *Base) Graph(ctx context.Context, company string) (Graph, error) {
	c, err := b.open(company)
	if err != nil {
		return Graph{}, err
	}
	g := Graph{Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	index := map[string]int{}
	for _, k := range kinds {
		r, err := c.db.QueryCypher(ctx, cortexdb.CypherQueryRequest{Query: fmt.Sprintf(`MATCH (n:%s) OPTIONAL MATCH (m)-[:mentions]-(n) RETURN n.name, count(m)`, k), MaxRows: 5000})
		if err != nil {
			return g, err
		}
		for _, row := range r.Rows {
			name, _ := row[0].(string)
			if _, dup := index[name]; dup || name == "" {
				continue
			}
			n := 0
			switch v := row[1].(type) {
			case float64:
				n = int(v)
			case int64:
				n = int(v)
			case int:
				n = v
			}
			index[name] = len(g.Nodes)
			g.Nodes = append(g.Nodes, GraphNode{Name: name, Kind: k, Mentions: n})
		}
	}
	r, err := c.db.QueryCypher(ctx, cortexdb.CypherQueryRequest{
		Query:   `MATCH (a)-[r]->(b) WHERE type(r) IN $types RETURN a.name, type(r), b.name`,
		Params:  map[string]any{"types": anyList(relations)},
		MaxRows: 10000,
	})
	if err != nil {
		return g, err
	}
	for _, row := range r.Rows {
		from, _ := row[0].(string)
		typ, _ := row[1].(string)
		to, _ := row[2].(string)
		if _, ok := index[from]; !ok {
			continue
		}
		if _, ok := index[to]; !ok {
			continue
		}
		g.Edges = append(g.Edges, GraphEdge{From: from, To: to, Type: typ})
	}
	return g, nil
}

// Forget makes the next SyncProcess write the company's process even if it has not changed.
func (b *Base) Forget(id string) {
	if engine.ValidID.MatchString(id) {
		os.Remove(b.statePath(id))
	}
}
