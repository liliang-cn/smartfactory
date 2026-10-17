package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"

	"smartfactory/internal/engine"
)

// A company's ontology: the classes its industry packs (and the company) declare, how things of
// them relate, rules, named things, and which of the company's products and stations are of which
// class. It is loaded as RDF into a CortexDB file of its own beside the knowledge base, in one named
// graph: the classes as rdfs:Class with rdfs:subClassOf, the relations with their domain, range and
// OWL characteristics (transitive, symmetric, inverse), each rule as an owl:propertyChainAxiom, the
// named things with their facts, and the process (company, plants, lines, stations, machines,
// products, suppliers, customers) as things of the built-in classes, related as the process graph
// relates them. Inference runs once each time the ontology or the process changes, never in the
// background, and every inferred fact keeps what it was inferred from. Structural constraints
// (requires) are checked with SHACL then; live ones (a reading's limits) by Live.
//
// The ontology is kept apart from the knowledge base's file because CortexDB keeps a node and an
// edge in its property graph for every RDF term and triple: there they would sit among the
// passages and entities the search walks.

// Ontology is a company's, as the engine merges it from its industry packs and its own.
type Ontology struct {
	Classes     []OntClass      `json:"classes"`
	Relations   []OntRelation   `json:"relations"`
	Rules       []OntRule       `json:"rules"`
	Individuals []OntIndividual `json:"individuals"`
	Constraints []OntConstraint `json:"constraints"`
}

type OntClass struct {
	Name string   `json:"name"`
	Sub  []string `json:"sub"`
	Note string   `json:"note,omitempty"`
}

type OntRelation struct {
	Name       string `json:"name"`
	Label      string `json:"label,omitempty"`
	Domain     string `json:"domain,omitempty"`
	Range      string `json:"range,omitempty"`
	Transitive bool   `json:"transitive,omitempty"`
	Symmetric  bool   `json:"symmetric,omitempty"`
	Inverse    string `json:"inverse,omitempty"`
	Note       string `json:"note,omitempty"`
}

// OntRule: X If[0] Y, Y If[1] Z … give X Then Z.
type OntRule struct {
	ID   string   `json:"id"`
	If   []string `json:"if"`
	Then string   `json:"then"`
	Note string   `json:"note,omitempty"`
}

type OntIndividual struct {
	Name  string              `json:"name"`
	A     []string            `json:"a"`
	Note  string              `json:"note,omitempty"`
	Facts map[string][]string `json:"facts"`
}

// OntConstraint is a live limit on a reading (Field with Min / Max, out of range For seconds) or
// a relation everything of Class must have (Requires, its values of ValueClass).
type OntConstraint struct {
	ID         string   `json:"id"`
	Class      string   `json:"class"`
	Field      string   `json:"field,omitempty"`
	Min        *float64 `json:"min,omitempty"`
	Max        *float64 `json:"max,omitempty"`
	For        *float64 `json:"for,omitempty"`
	Requires   string   `json:"requires,omitempty"`
	ValueClass string   `json:"valueClass,omitempty"`
	Say        string   `json:"say,omitempty"`
	Note       string   `json:"note,omitempty"`
}

// The built-in classes, each the kind of thing in the knowledge graph it is.
var builtinClasses = []struct{ Name, Kind string }{
	{"公司", TCompany}, {"工厂", TPlant}, {"产线", TLine}, {"工位", TStation}, {"设备", TMachine}, {"产品", TProduct},
	{"供应商", TSupplier}, {"客户", TCustomer}, {"工单", TOrder},
	{"点检", TCheck}, {"读数异常", TAnomaly}, {"校准", TCalibration}, {"AI指令", TCommand}, {"约束告警", TViolation},
}

// The built-in relations: the process graph's, with their domain and range.
var builtinRelations = []struct{ Name, Domain, Range, Type string }{
	{"有工厂", "公司", "工厂", RHasPlant}, {"有产线", "工厂", "产线", RHasLine}, {"有工位", "产线", "工位", RHasStation},
	{"生产", "产线", "产品", RMakes}, {"设备是", "工位", "设备", RIsA}, {"下游", "工位", "工位", RFeeds},
	{"供应", "供应商", "产品", RSupplies}, {"卖给", "产品", "客户", RSoldTo},
}

// declares is whether the ontology declares a relation of that name (nil declares none).
func (o *Ontology) declares(relation string) bool {
	if o == nil {
		return false
	}
	for _, r := range o.Relations {
		if r.Name == relation {
			return true
		}
	}
	return false
}

func builtinClass(name string) (kind string, ok bool) {
	for _, c := range builtinClasses {
		if c.Name == name {
			return c.Kind, true
		}
	}
	return "", false
}

func classOfKind(kind string) string {
	for _, c := range builtinClasses {
		if c.Kind == kind {
			return c.Name
		}
	}
	return ""
}

const (
	nsRDF  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	nsRDFS = "http://www.w3.org/2000/01/rdf-schema#"
	nsOWL  = "http://www.w3.org/2002/07/owl#"
	nsSH   = "http://www.w3.org/ns/shacl#"
	nsXSD  = "http://www.w3.org/2001/XMLSchema#"

	// a company's own terms: its classes, relations and things, each by its name
	nsClass = "urn:smartfactory:class:"
	nsRel   = "urn:smartfactory:rel:"
	nsThing = "urn:smartfactory:thing:"
	nsShape = "urn:smartfactory:shape:"
	// OntologyGraph is the named graph a company's ontology is in.
	OntologyGraph = "urn:smartfactory:ontology"

	// ontologyFormat changes whenever compileOntology writes differently, so every ontology is
	// loaded again.
	ontologyFormat = "1"
)

var (
	iriType       = graph.NewIRI(nsRDF + "type")
	iriSubClassOf = graph.NewIRI(nsRDFS + "subClassOf")
	iriLabel      = graph.NewIRI(nsRDFS + "label")
	iriComment    = graph.NewIRI(nsRDFS + "comment")
	ontoGraph     = graph.NewIRI(OntologyGraph)
)

// esc keeps a name readable in an IRI: letters (Chinese too), digits and -._~ as they are, the
// rest percent-encoded.
func esc(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.~", r) {
			b.WriteRune(r)
			continue
		}
		for _, c := range []byte(string(r)) {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func classIRI(name string) graph.RDFTerm { return graph.NewIRI(nsClass + esc(name)) }
func relIRI(name string) graph.RDFTerm   { return graph.NewIRI(nsRel + esc(name)) }
func thingIRI(name string) graph.RDFTerm { return graph.NewIRI(nsThing + esc(name)) }

// nameOf is the name an IRI of one of the namespaces stands for, and which namespace it is in.
func nameOf(t graph.RDFTerm) (name, ns string) {
	if t.Kind != graph.RDFTermIRI {
		return "", ""
	}
	for _, n := range []string{nsClass, nsRel, nsThing} {
		if rest, ok := strings.CutPrefix(t.Value, n); ok {
			if v, err := url.PathUnescape(rest); err == nil {
				return v, n
			}
			return rest, n
		}
	}
	return "", ""
}

// ontoPlan is what a company's ontology loads as.
type ontoPlan struct {
	triples []graph.RDFTriple
	shapes  []graph.RDFTriple
	// Things are the process's things by name, with their kind; Alias the product names that are
	// another name of an individual.
	things map[string]string
	alias  map[string]string
	// chains are the rules by the blank node that heads their chain.
	chains map[string]string
}

func chainHead(i int) string { return fmt.Sprintf("rule%d-0", i) }

// compileOntology turns the company's ontology and process into triples and SHACL shapes.
func compileOntology(m *Model) *ontoPlan {
	o := m.Ontology
	if o == nil {
		o = &Ontology{}
	}
	p := &ontoPlan{things: map[string]string{}, alias: map[string]string{}, chains: map[string]string{}}
	add := func(s, pr, ob graph.RDFTerm) {
		p.triples = append(p.triples, graph.RDFTriple{Subject: s, Predicate: pr, Object: ob, Graph: &ontoGraph})
	}
	note := func(s graph.RDFTerm, text string) {
		if text != "" {
			add(s, iriComment, graph.NewLiteral(text))
		}
	}

	classes := map[string]bool{}
	class := func(name string) graph.RDFTerm {
		c := classIRI(name)
		if !classes[name] {
			classes[name] = true
			add(c, iriType, graph.NewIRI(nsRDFS+"Class"))
		}
		return c
	}
	for _, c := range builtinClasses {
		class(c.Name)
	}
	for _, c := range o.Classes {
		ci := class(c.Name)
		for _, s := range c.Sub {
			add(ci, iriSubClassOf, class(s))
		}
		note(ci, c.Note)
	}

	rels := map[string]bool{}
	rel := func(name string) graph.RDFTerm {
		r := relIRI(name)
		if !rels[name] {
			rels[name] = true
			add(r, iriType, graph.NewIRI(nsOWL+"ObjectProperty"))
		}
		return r
	}
	for _, r := range builtinRelations {
		ri := rel(r.Name)
		// a pack that declares a relation of the same name says what it relates
		if !o.declares(r.Name) {
			add(ri, graph.NewIRI(nsRDFS+"domain"), class(r.Domain))
			add(ri, graph.NewIRI(nsRDFS+"range"), class(r.Range))
		}
	}
	for _, r := range o.Relations {
		ri := rel(r.Name)
		if r.Label != "" {
			add(ri, iriLabel, graph.NewLiteral(r.Label))
		}
		if r.Domain != "" {
			add(ri, graph.NewIRI(nsRDFS+"domain"), class(r.Domain))
		}
		if r.Range != "" {
			add(ri, graph.NewIRI(nsRDFS+"range"), class(r.Range))
		}
		if r.Transitive {
			add(ri, iriType, graph.NewIRI(nsOWL+"TransitiveProperty"))
		}
		if r.Symmetric {
			add(ri, iriType, graph.NewIRI(nsOWL+"SymmetricProperty"))
		}
		if r.Inverse != "" && r.Inverse != r.Name {
			add(ri, graph.NewIRI(nsOWL+"inverseOf"), rel(r.Inverse))
		}
		note(ri, r.Note)
	}
	// a rule is a property chain: its relations as an RDF list
	for i, r := range o.Rules {
		cells := make([]graph.RDFTerm, len(r.If))
		for j := range r.If {
			cells[j] = graph.NewBlankNode(fmt.Sprintf("rule%d-%d", i, j))
		}
		p.chains[chainHead(i)] = r.ID
		add(rel(r.Then), graph.NewIRI(nsOWL+"propertyChainAxiom"), cells[0])
		for j, link := range r.If {
			add(cells[j], graph.NewIRI(nsRDF+"first"), rel(link))
			next := graph.NewIRI(nsRDF + "nil")
			if j+1 < len(cells) {
				next = cells[j+1]
			}
			add(cells[j], graph.NewIRI(nsRDF+"rest"), next)
		}
		add(cells[0], iriLabel, graph.NewLiteral(r.ID))
	}

	// the process: each thing of its built-in class (and the classes it is bound to), related as
	// in the process graph. A product bound to an individual is that individual.
	n := nameModel(m)
	typed := map[string]bool{}
	thing := func(name, kind string, extra ...string) graph.RDFTerm {
		t := thingIRI(name)
		if _, ok := p.things[name]; !ok && kind != "" {
			p.things[name] = kind
		}
		for _, c := range append([]string{classOfKind(kind)}, extra...) {
			if c != "" && !typed[name+"\x00"+c] {
				typed[name+"\x00"+c] = true
				add(t, iriType, class(c))
			}
		}
		return t
	}
	link := func(from graph.RDFTerm, relation string, to graph.RDFTerm) { add(from, rel(relation), to) }
	product := func(pr Product) graph.RDFTerm {
		name := pr.Name
		if pr.Individual != "" && pr.Individual != pr.Name {
			name = pr.Individual
			p.alias[pr.Name] = name
		}
		t := thing(name, TProduct, pr.Classes...)
		for _, s := range uniq(pr.Suppliers) {
			link(thing(s, TSupplier), "供应", t)
		}
		for _, c := range uniq(pr.Customers) {
			link(t, "卖给", thing(c, TCustomer))
		}
		return t
	}
	co := thing(m.Name, TCompany)
	for _, pl := range m.Plants {
		pt := thing(pl.Name, TPlant)
		link(co, "有工厂", pt)
		for _, l := range pl.Lines {
			lt := thing(n.line[pl.ID+"/"+l.ID], TLine)
			link(pt, "有产线", lt)
			link(lt, "生产", product(l.Product))
			var prev graph.RDFTerm
			for i, s := range l.Stations {
				st := thing(n.station[pl.ID+"/"+s.ID], TStation, s.Classes...)
				link(lt, "有工位", st)
				link(st, "设备是", thing(s.Label, TMachine))
				if i > 0 {
					link(prev, "下游", st)
				}
				prev = st
			}
		}
	}
	name := func(v string) string {
		if a, ok := p.alias[v]; ok {
			return a
		}
		return v
	}
	for _, ind := range o.Individuals {
		t := thing(name(ind.Name), "", ind.A...)
		note(t, ind.Note)
		for _, r := range sortedKeys(ind.Facts) {
			for _, v := range ind.Facts[r] {
				link(t, r, thingIRI(name(v)))
			}
		}
	}

	for i, c := range o.Constraints {
		if c.Requires == "" {
			continue
		}
		shape, prop := graph.NewIRI(nsShape+esc(c.ID)), graph.NewBlankNode(fmt.Sprintf("shape%d", i))
		sh := func(s, pr, ob graph.RDFTerm) {
			p.shapes = append(p.shapes, graph.RDFTriple{Subject: s, Predicate: pr, Object: ob})
		}
		sh(shape, iriType, graph.NewIRI(nsSH+"NodeShape"))
		sh(shape, graph.NewIRI(nsSH+"targetClass"), classIRI(c.Class))
		sh(shape, graph.NewIRI(nsSH+"property"), prop)
		sh(prop, graph.NewIRI(nsSH+"path"), relIRI(c.Requires))
		sh(prop, graph.NewIRI(nsSH+"minCount"), graph.NewTypedLiteral("1", nsXSD+"integer"))
		if c.ValueClass != "" {
			sh(prop, graph.NewIRI(nsSH+"class"), classIRI(c.ValueClass))
		}
	}
	return p
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ---- the file ----

// ontoState is what was last loaded for a company's ontology.
type ontoState struct {
	Hash     string `json:"hash"`
	Explicit int    `json:"explicit"`
	Inferred int    `json:"inferred"`
	// Violations are the structural constraints' (SHACL), as of the load.
	Violations []OntViolation `json:"violations"`
	// Inconsistencies are what the OWL rules found contradicting.
	Inconsistencies []string          `json:"inconsistencies,omitempty"`
	Things          map[string]string `json:"things"`
	Alias           map[string]string `json:"alias"`
	Chains          map[string]string `json:"chains"`
}

// OntViolation is something that breaks a constraint: a thing without a relation it requires
// (structural), or a machine's reading out of its limits (live).
type OntViolation struct {
	Constraint string `json:"constraint"`
	// Name is the thing (for a live one, the station).
	Name string `json:"name"`
	Text string `json:"text"`
	// a live one: the reading, its value (the furthest out of range) and since when (epoch ms)
	Plant string   `json:"plant,omitempty"`
	Field string   `json:"field,omitempty"`
	Value *float64 `json:"value,omitempty"`
	Since int64    `json:"since,omitempty"`
}

type ontoDB struct {
	mu sync.RWMutex
	db *cortexdb.DB // nil: the company has no ontology
}

func (b *Base) ontoPath(id string) string      { return filepath.Join(b.dir, id+".ontology.db") }
func (b *Base) ontoStatePath(id string) string { return filepath.Join(b.dir, id+".ontology.json") }

func (b *Base) ontoState(id string) ontoState {
	var st ontoState
	if raw, err := os.ReadFile(b.ontoStatePath(id)); err == nil {
		json.Unmarshal(raw, &st)
	}
	return st
}

// onto is the company's ontology file, opened when first used (nil db when it has none).
func (b *Base) onto(id string) *ontoDB {
	b.mu.Lock()
	defer b.mu.Unlock()
	if o, ok := b.ontos[id]; ok {
		return o
	}
	o := &ontoDB{}
	_, noState := os.Stat(b.ontoStatePath(id))
	if _, noFile := os.Stat(b.ontoPath(id)); noState == nil && noFile == nil {
		if db, err := openOnto(b.ontoPath(id)); err == nil {
			o.db = db
		} else {
			b.log.Warn("knowledge: ontology", "company", id, "err", err)
		}
	}
	b.ontos[id] = o
	return o
}

func openOnto(path string) (*cortexdb.DB, error) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(path), cortexdb.WithAutoInference(false))
	if err != nil {
		return nil, err
	}
	// what is asked of it is only what it was given: not the property graph CortexDB keeps
	// beside every RDF term
	db.Graph().SetPropertyGraphProjection(false)
	return db, nil
}

func removeDB(path string) {
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		os.Remove(f)
	}
}

// syncOntology loads the company's ontology again if it or the process changed since the last
// time: a new file, inference run once, the structural constraints checked.
func (b *Base) syncOntology(ctx context.Context, id string, m *Model) error {
	o := b.onto(id)
	if m.Ontology == nil {
		if o.db == nil {
			return nil
		}
		o.mu.Lock()
		defer o.mu.Unlock()
		o.db.Close()
		o.db = nil
		removeDB(b.ontoPath(id))
		os.Remove(b.ontoStatePath(id))
		return nil
	}
	plan := compileOntology(m)
	raw, _ := json.Marshal([]any{plan.triples, plan.shapes})
	sum := sha256.Sum256(append(raw, ontologyFormat...))
	hash := hex.EncodeToString(sum[:])
	if o.db != nil && b.ontoState(id).Hash == hash {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.db != nil {
		o.db.Close()
		o.db = nil
	}
	path := b.ontoPath(id)
	removeDB(path)
	db, err := openOnto(path)
	if err != nil {
		return err
	}
	st := ontoState{Hash: hash, Things: plan.things, Alias: plan.alias, Chains: plan.chains, Violations: []OntViolation{}}
	if _, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: plan.triples}); err != nil {
		db.Close()
		return err
	}
	inf, err := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{Mode: cortexdb.KnowledgeGraphInferenceRefreshModeFull})
	if err != nil {
		db.Close()
		return err
	}
	st.Explicit, st.Inferred = inf.Result.ExplicitCount, inf.Result.InferredCount
	for _, x := range inf.Result.Inconsistencies {
		st.Inconsistencies = append(st.Inconsistencies, fmt.Sprint(x))
	}
	if len(plan.shapes) > 0 {
		rep, err := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{Shapes: plan.shapes})
		if err != nil {
			db.Close()
			return err
		}
		st.Violations = structural(m.Ontology, rep.Report)
	}
	o.db = db
	out, _ := json.Marshal(st)
	return os.WriteFile(b.ontoStatePath(id), out, 0o644)
}

// structural is what SHACL found, as people read it.
func structural(o *Ontology, rep graph.SHACLReport) []OntViolation {
	out := []OntViolation{}
	seen := map[string]bool{}
	for _, r := range rep.Results {
		focus, _ := nameOf(r.FocusNode)
		path, _ := nameOf(r.Path)
		var c *OntConstraint
		for i := range o.Constraints {
			if k := &o.Constraints[i]; k.Requires == path && r.Source.Kind == graph.RDFTermBlankNode && r.Source.Value == fmt.Sprintf("shape%d", i) {
				c = k
			}
		}
		if c == nil || focus == "" {
			continue
		}
		var text string
		switch {
		case strings.HasSuffix(r.Component, "ClassConstraintComponent"):
			value, _ := nameOf(r.Value)
			text = fmt.Sprintf("%s 的「%s」%s 不是%s", focus, path, value, c.ValueClass)
		case c.Say != "":
			text = say(c.Say, map[string]string{"name": focus, "class": c.Class})
		default:
			text = fmt.Sprintf("%s 没有「%s」", focus, path)
		}
		if k := c.ID + "\x00" + text; !seen[k] {
			seen[k] = true
			out = append(out, OntViolation{Constraint: c.ID, Name: focus, Text: text})
		}
	}
	return out
}

// say fills a constraint's message: {name}, {value} … from vars.
func say(tmpl string, vars map[string]string) string {
	for k, v := range vars {
		tmpl = strings.ReplaceAll(tmpl, "{"+k+"}", v)
	}
	return tmpl
}

// ForgetOntology makes the next SyncProcess load the company's ontology again.
func (b *Base) ForgetOntology(id string) {
	if engine.ValidID.MatchString(id) {
		os.Remove(b.ontoStatePath(id))
	}
}

func (b *Base) dropOntology(id string) {
	if o, ok := b.ontos[id]; ok {
		o.mu.Lock()
		if o.db != nil {
			o.db.Close()
		}
		o.mu.Unlock()
		delete(b.ontos, id)
	}
	if engine.ValidID.MatchString(id) {
		removeDB(b.ontoPath(id))
		os.Remove(b.ontoStatePath(id))
	}
}
