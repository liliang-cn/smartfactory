package knowledge

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Asking the ontology: its classes with how many things are of each (as given, and inferred), its
// relations, rules and constraints with where they stand; the things of a class; one thing with
// its classes and facts, each inferred one with why.

// OntOverview is a company's ontology at a glance.
type OntOverview struct {
	// Declared: the company's industry packs (or the company) declare an ontology; without one
	// there are only the built-in classes and relations.
	Declared    bool                `json:"declared"`
	Classes     []OntClassInfo      `json:"classes"`
	Relations   []OntRelationInfo   `json:"relations"`
	Rules       []OntRuleInfo       `json:"rules"`
	Constraints []OntConstraintInfo `json:"constraints"`
	// Explicit facts were given; Inferred follow from them.
	Explicit        int      `json:"explicit"`
	Inferred        int      `json:"inferred"`
	Inconsistencies []string `json:"inconsistencies,omitempty"`
}

type OntClassInfo struct {
	Name string `json:"name"`
	// Sub are the classes it is a kind of.
	Sub     []string `json:"sub"`
	Note    string   `json:"note,omitempty"`
	Builtin bool     `json:"builtin,omitempty"`
	// Kind is the kind of thing in the knowledge graph a built-in class is.
	Kind string `json:"kind,omitempty"`
	// Asserted things were given as of the class (or bound to it); Inferred are of it by inference only.
	Asserted int `json:"asserted"`
	Inferred int `json:"inferred"`
}

type OntRelationInfo struct {
	OntRelation
	Builtin bool `json:"builtin,omitempty"`
	// Text is the relation in a line: 菜品 原料是 食材；可以传递.
	Text string `json:"text"`
}

type OntRuleInfo struct {
	OntRule
	// Text is the rule in a line: X 原料 Y，Y 含过敏原 Z → X 含过敏原 Z.
	Text string `json:"text"`
}

type OntConstraintInfo struct {
	OntConstraint
	// Live: a limit on a reading, watched as equipment sends it; otherwise checked on the model.
	Live bool   `json:"live"`
	Text string `json:"text"`
	// Status: ok | violated (a structural one not met, a live one out of range now)
	Status string `json:"status"`
	// Watching is how many stations a live one watches.
	Watching   int            `json:"watching"`
	Violations []OntViolation `json:"violations"`
}

// OntFact is one statement: Subject Relation Object ("鲜毛肚 存放于 FZ-101", or "鲜毛肚 是 食材"
// for a class). Why says, for an inferred one, what it follows from, a step a line.
type OntFact struct {
	Subject  string   `json:"subject"`
	Relation string   `json:"relation"`
	Object   string   `json:"object"`
	Inferred bool     `json:"inferred"`
	Rule     string   `json:"rule,omitempty"`
	Why      []string `json:"why,omitempty"`
	id       string
}

type OntInstance struct {
	Name string `json:"name"`
	// Kind is the kind of thing it is in the knowledge graph (station, product …); "" for a named thing.
	Kind string `json:"kind,omitempty"`
	// Inferred: of the class by inference only.
	Inferred bool `json:"inferred"`
	// Fact is the matching fact, when the things were asked for by a relation's value.
	Fact *OntFact `json:"fact,omitempty"`
}

type OntInstances struct {
	Class     string        `json:"class"`
	Relation  string        `json:"relation,omitempty"`
	Value     string        `json:"value,omitempty"`
	Instances []OntInstance `json:"instances"`
	Total     int           `json:"total"`
}

// OntThing is a class or a thing with everything the ontology says about it.
type OntThing struct {
	Name string `json:"name"`
	// Kind: class, or the kind of thing in the knowledge graph (station, product …), or thing (a named thing)
	Kind string `json:"kind"`
	// Also are the other names it goes by (a product's name, when it is a named thing).
	Also []string `json:"also,omitempty"`
	Note string   `json:"note,omitempty"`
	// a thing: its classes, what it relates to, what relates to it
	Classes []OntFact `json:"classes,omitempty"`
	Facts   []OntFact `json:"facts,omitempty"`
	Back    []OntFact `json:"back,omitempty"`
	// a class: what it is a kind of, its kinds, the relations and constraints about it, its things
	Super       []OntFact     `json:"super,omitempty"`
	Subs        []string      `json:"subs,omitempty"`
	Relations   []string      `json:"relations,omitempty"`
	Constraints []string      `json:"constraints,omitempty"`
	Instances   *OntInstances `json:"instances,omitempty"`
}

// OntologyError is a question the ontology cannot answer as asked: a class or name it does not
// have (Missing), a query that is not a read.
type OntologyError struct {
	Msg     string
	Missing bool
}

func (e *OntologyError) Error() string { return e.Msg }

// read runs fn with the company's ontology file (nil when it has none) held for reading.
func (b *Base) read(id string, fn func(db *cortexdb.DB, st ontoState) error) error {
	if _, err := b.open(id); err != nil {
		return err
	}
	o := b.onto(id)
	o.mu.RLock()
	defer o.mu.RUnlock()
	var st ontoState
	if o.db != nil {
		st = b.ontoState(id)
	}
	return fn(o.db, st)
}

func find(ctx context.Context, db *cortexdb.DB, s, p, o *graph.RDFTerm) ([]graph.RDFTriple, error) {
	r, err := db.FindKnowledgeGraph(ctx, cortexdb.KnowledgeGraphFindRequest{Pattern: graph.TriplePattern{Subject: s, Predicate: p, Object: o, Graph: &ontoGraph}})
	if err != nil {
		return nil, err
	}
	return r.Triples, nil
}

func ref(t graph.RDFTerm) *graph.RDFTerm { return &t }

// classesOf is every class of the company: the built-in ones, then those declared.
func classesOf(m *Model) []OntClassInfo {
	var out []OntClassInfo
	at := map[string]int{}
	for _, c := range builtinClasses {
		at[c.Name] = len(out)
		out = append(out, OntClassInfo{Name: c.Name, Sub: []string{}, Builtin: true, Kind: c.Kind})
	}
	if m.Ontology != nil {
		for _, c := range m.Ontology.Classes {
			if i, ok := at[c.Name]; ok {
				out[i].Sub, out[i].Note = append(out[i].Sub, c.Sub...), c.Note
				continue
			}
			at[c.Name] = len(out)
			out = append(out, OntClassInfo{Name: c.Name, Sub: append([]string{}, c.Sub...), Note: c.Note})
		}
	}
	return out
}

// recordKinds are the built-in classes whose things are not in the ontology (orders and what
// happened to the machines change by the minute): the knowledge graph counts them.
var recordKinds = map[string]bool{TOrder: true, TCheck: true, TAnomaly: true, TCalibration: true, TCommand: true, TViolation: true}

func relationText(r OntRelation) string {
	or := func(s string) string { return cmp.Or(s, "任何东西") }
	t := fmt.Sprintf("%s %s %s", or(r.Domain), cmp.Or(r.Label, r.Name), or(r.Range))
	if r.Transitive {
		t += "；可以传递"
	}
	if r.Symmetric {
		t += "；是相互的"
	}
	if r.Inverse != "" && r.Inverse != r.Name {
		t += "；反过来叫「" + r.Inverse + "」"
	}
	return t
}

func ruleText(r OntRule) string {
	vars := []string{"X", "Y", "Z", "W", "V", "U", "T"}
	v := func(i int) string {
		if i < len(vars) {
			return vars[i]
		}
		return fmt.Sprint("X", i)
	}
	var parts []string
	for i, p := range r.If {
		parts = append(parts, fmt.Sprintf("%s %s %s", v(i), p, v(i+1)))
	}
	return fmt.Sprintf("%s → X %s %s", strings.Join(parts, "，"), r.Then, v(len(r.If)))
}

func constraintText(c OntConstraint) string {
	if c.Field != "" {
		hold := liveDefaultFor
		if c.For != nil {
			hold = time.Duration(*c.For * float64(time.Second))
		}
		return fmt.Sprintf("每台%s的%s要 %s，超出 %s 就报警", c.Class, anomalyField(c.Field), limits(c), seconds(hold))
	}
	t := fmt.Sprintf("每个%s都要有「%s」", c.Class, c.Requires)
	if c.ValueClass != "" {
		t += "，而且是" + c.ValueClass
	}
	return t
}

// OntologyOverview is the company's ontology: its classes with how many things are of each, its
// relations, rules and constraints with where each stands now.
func (b *Base) OntologyOverview(ctx context.Context, company string) (*OntOverview, error) {
	m, err := b.model(company)
	if err != nil {
		return nil, err
	}
	out := &OntOverview{Declared: m.Ontology != nil, Classes: classesOf(m), Relations: []OntRelationInfo{}, Rules: []OntRuleInfo{}, Constraints: []OntConstraintInfo{}}
	for _, r := range builtinRelations {
		rel := OntRelation{Name: r.Name, Domain: r.Domain, Range: r.Range}
		out.Relations = append(out.Relations, OntRelationInfo{OntRelation: rel, Builtin: true, Text: relationText(rel)})
	}
	o := m.Ontology
	if o == nil {
		o = &Ontology{}
	}
	for _, r := range o.Relations {
		out.Relations = append(out.Relations, OntRelationInfo{OntRelation: r, Text: relationText(r)})
	}
	for _, r := range o.Rules {
		out.Rules = append(out.Rules, OntRuleInfo{OntRule: r, Text: ruleText(r)})
	}
	stats, err := b.Stats(ctx, company)
	if err != nil {
		return nil, err
	}
	var structural []OntViolation
	err = b.read(company, func(db *cortexdb.DB, st ontoState) error {
		out.Explicit, out.Inferred, out.Inconsistencies, structural = st.Explicit, st.Inferred, st.Inconsistencies, st.Violations
		if db == nil {
			for i, c := range out.Classes {
				if c.Kind != "" {
					out.Classes[i].Asserted = stats.Kinds[c.Kind]
				}
			}
			return nil
		}
		types, err := find(ctx, db, nil, &iriType, nil)
		if err != nil {
			return err
		}
		given := map[string]map[string]bool{}
		of := map[string]map[string]bool{}
		for _, t := range types {
			c, ns := nameOf(t.Object)
			if ns != nsClass {
				continue
			}
			s := t.Subject.Value
			if of[c] == nil {
				of[c], given[c] = map[string]bool{}, map[string]bool{}
			}
			of[c][s] = true
			if !t.Inferred {
				given[c][s] = true
			}
		}
		for i, c := range out.Classes {
			if recordKinds[c.Kind] {
				out.Classes[i].Asserted = stats.Kinds[c.Kind]
				continue
			}
			out.Classes[i].Asserted = len(given[c.Name])
			out.Classes[i].Inferred = len(of[c.Name]) - len(given[c.Name])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var live []OntViolation
	watching := map[string]int{}
	if b.Live != nil {
		live, watching = b.Live.Current(company), b.Live.Watching(company)
	}
	for _, c := range o.Constraints {
		ci := OntConstraintInfo{OntConstraint: c, Live: c.Field != "", Text: constraintText(c), Status: "ok", Violations: []OntViolation{}}
		from := structural
		if ci.Live {
			from, ci.Watching = live, watching[c.ID]
		}
		for _, v := range from {
			if v.Constraint == c.ID {
				ci.Violations = append(ci.Violations, v)
			}
		}
		if len(ci.Violations) > 0 {
			ci.Status = "violated"
		}
		out.Constraints = append(out.Constraints, ci)
	}
	return out, nil
}

// classKind is the kind a class name is: a built-in one's, "" for one declared, or false when the
// company has no such class.
func classKind(m *Model, name string) (string, bool) {
	if k, ok := builtinClass(name); ok {
		return k, true
	}
	if m.Ontology != nil {
		for _, c := range m.Ontology.Classes {
			if c.Name == name {
				return "", true
			}
		}
	}
	return "", false
}

// OntologyInstances are the things of class (those of its subclasses included), at most limit;
// with relation and value, only those that relate so to value ("菜品" with 含过敏原 = 花生过敏原).
// Those given as of the class come first.
func (b *Base) OntologyInstances(ctx context.Context, company, class, relation, value string, limit int) (*OntInstances, error) {
	m, err := b.model(company)
	if err != nil {
		return nil, err
	}
	kind, ok := classKind(m, class)
	if !ok {
		return nil, &OntologyError{Missing: true, Msg: fmt.Sprintf("没有「%s」这个类", class)}
	}
	if limit <= 0 {
		limit = 100
	}
	out := &OntInstances{Class: class, Relation: relation, Value: value, Instances: []OntInstance{}}
	err = b.read(company, func(db *cortexdb.DB, st ontoState) error {
		if db == nil || recordKinds[kind] {
			if kind == "" || relation != "" {
				return nil
			}
			ls, err := b.Find(ctx, company, "", kind, 1000)
			for _, l := range ls {
				out.Instances = append(out.Instances, OntInstance{Name: l.Name, Kind: l.Kind})
			}
			return err
		}
		types, err := find(ctx, db, nil, &iriType, ref(classIRI(class)))
		if err != nil {
			return err
		}
		var match map[string]*OntFact
		if relation != "" {
			if a, ok := st.Alias[value]; ok {
				value = a
			}
			facts, err := find(ctx, db, nil, ref(relIRI(relation)), ref(thingIRI(value)))
			if err != nil {
				return err
			}
			match = map[string]*OntFact{}
			for _, f := range facts {
				s, _ := nameOf(f.Subject)
				match[s] = &OntFact{Subject: s, Relation: relation, Object: value, Inferred: f.Inferred, Rule: f.Rule}
			}
		}
		at := map[string]int{}
		for _, t := range types {
			s, ns := nameOf(t.Subject)
			if ns != nsThing {
				continue
			}
			if match != nil && match[s] == nil {
				continue
			}
			if i, ok := at[s]; ok {
				out.Instances[i].Inferred = out.Instances[i].Inferred && t.Inferred
				continue
			}
			at[s] = len(out.Instances)
			out.Instances = append(out.Instances, OntInstance{Name: s, Kind: st.Things[s], Inferred: t.Inferred, Fact: match[s]})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(out.Instances, func(a, b OntInstance) int {
		if a.Inferred != b.Inferred {
			if a.Inferred {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Name, b.Name)
	})
	out.Total = len(out.Instances)
	if len(out.Instances) > limit {
		out.Instances = out.Instances[:limit]
	}
	return out, nil
}

// whyLimit is how many inferred facts of one thing are explained.
const whyLimit = 40

// OntologyThing is a class or a thing with what the ontology says about it: a class's
// superclasses, subclasses, relations, constraints and things; a thing's classes, facts both ways,
// each inferred one with why.
func (b *Base) OntologyThing(ctx context.Context, company, name string) (*OntThing, error) {
	m, err := b.model(company)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if _, ok := classKind(m, name); ok {
		return b.ontologyClass(ctx, company, m, name)
	}
	var out *OntThing
	err = b.read(company, func(db *cortexdb.DB, st ontoState) error {
		if db == nil {
			return nil
		}
		if a, ok := st.Alias[name]; ok {
			name = a
		}
		me := thingIRI(name)
		mine, err := find(ctx, db, &me, nil, nil)
		if err != nil {
			return err
		}
		theirs, err := find(ctx, db, nil, nil, &me)
		if err != nil {
			return err
		}
		if len(mine)+len(theirs) == 0 {
			return nil
		}
		out = &OntThing{Name: name, Kind: cmp.Or(st.Things[name], "thing"), Classes: []OntFact{}, Facts: []OntFact{}, Back: []OntFact{}}
		for k, v := range st.Alias {
			if v == name {
				out.Also = append(out.Also, k)
			}
		}
		explained := 0
		fact := func(t graph.RDFTriple, rel string) OntFact {
			s, _ := nameOf(t.Subject)
			o, _ := nameOf(t.Object)
			f := OntFact{Subject: s, Relation: rel, Object: o, Inferred: t.Inferred, Rule: t.Rule, id: t.ID}
			if t.Inferred && explained < whyLimit {
				explained++
				f.Why = why(ctx, db, t.ID, st.Chains)
			}
			return f
		}
		for _, t := range mine {
			switch _, ns := nameOf(t.Object); {
			case t.Predicate.Value == nsRDFS+"comment":
				out.Note = t.Object.Value
			case t.Predicate.Value == iriType.Value && ns == nsClass:
				out.Classes = append(out.Classes, fact(t, "是"))
			default:
				if rel, pns := nameOf(t.Predicate); pns == nsRel && ns == nsThing {
					out.Facts = append(out.Facts, fact(t, rel))
				}
			}
		}
		for _, t := range theirs {
			if rel, pns := nameOf(t.Predicate); pns == nsRel {
				if _, ns := nameOf(t.Subject); ns == nsThing {
					out.Back = append(out.Back, fact(t, rel))
				}
			}
		}
		for _, l := range [][]OntFact{out.Classes, out.Facts, out.Back} {
			slices.SortStableFunc(l, func(a, b OntFact) int {
				if a.Inferred != b.Inferred {
					if a.Inferred {
						return 1
					}
					return -1
				}
				return cmp.Or(strings.Compare(a.Relation, b.Relation), strings.Compare(a.Object, b.Object), strings.Compare(a.Subject, b.Subject))
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		// a thing of the knowledge graph in a company without an ontology: its built-in class
		e, err := b.Lookup(ctx, company, name)
		if err != nil {
			return nil, err
		}
		if e == nil {
			return nil, &OntologyError{Missing: true, Msg: fmt.Sprintf("本体里没有叫「%s」的类或东西", name)}
		}
		out = &OntThing{Name: e.Name, Kind: e.Kind, Classes: []OntFact{{Subject: e.Name, Relation: "是", Object: classOfKind(e.Kind)}}, Facts: []OntFact{}, Back: []OntFact{}}
	}
	return out, nil
}

func (b *Base) ontologyClass(ctx context.Context, company string, m *Model, name string) (*OntThing, error) {
	out := &OntThing{Name: name, Kind: "class", Super: []OntFact{}, Subs: []string{}, Relations: []string{}, Constraints: []string{}}
	for _, c := range classesOf(m) {
		if c.Name == name {
			out.Note = c.Note
		}
		if slices.Contains(c.Sub, name) {
			out.Subs = append(out.Subs, c.Name)
		}
	}
	for _, r := range builtinRelations {
		if r.Domain == name || r.Range == name {
			out.Relations = append(out.Relations, relationText(OntRelation{Name: r.Name, Domain: r.Domain, Range: r.Range}))
		}
	}
	if o := m.Ontology; o != nil {
		for _, r := range o.Relations {
			if r.Domain == name || r.Range == name {
				out.Relations = append(out.Relations, relationText(r))
			}
		}
		for _, c := range o.Constraints {
			if c.Class == name {
				out.Constraints = append(out.Constraints, c.ID+"："+constraintText(c))
			}
		}
	}
	err := b.read(company, func(db *cortexdb.DB, st ontoState) error {
		if db == nil {
			return nil
		}
		me := classIRI(name)
		ups, err := find(ctx, db, &me, &iriSubClassOf, nil)
		if err != nil {
			return err
		}
		for _, t := range ups {
			o, ns := nameOf(t.Object)
			if ns != nsClass || o == name {
				continue
			}
			f := OntFact{Subject: name, Relation: "是一种", Object: o, Inferred: t.Inferred, Rule: t.Rule}
			if t.Inferred {
				f.Why = why(ctx, db, t.ID, st.Chains)
			}
			out.Super = append(out.Super, f)
		}
		slices.SortStableFunc(out.Super, func(a, b OntFact) int {
			if a.Inferred != b.Inferred {
				if a.Inferred {
					return 1
				}
				return -1
			}
			return strings.Compare(a.Object, b.Object)
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out.Instances, err = b.OntologyInstances(ctx, company, name, "", "", 60); err != nil {
		return nil, err
	}
	return out, nil
}

// why is what an inferred fact follows from, a step a line from what was given to the fact:
// "牛油锅底 原料 花生碎；花生碎 含过敏原 花生过敏原 → 牛油锅底 含过敏原 花生过敏原".
func why(ctx context.Context, db *cortexdb.DB, id string, chains map[string]string) []string {
	r, err := db.ExplainKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceExplainRequest{TripleID: id, Depth: 12})
	if err != nil || len(r.Trace) == 0 {
		return nil
	}
	entry := map[string]graph.RDFSInferenceTraceEntry{}
	kids := map[string][]string{}
	for _, e := range r.Trace {
		if _, ok := entry[e.TripleID]; !ok {
			entry[e.TripleID] = e
		}
		if e.ParentTripleID != "" && !slices.Contains(kids[e.ParentTripleID], e.TripleID) {
			kids[e.ParentTripleID] = append(kids[e.ParentTripleID], e.TripleID)
		}
	}
	var steps []string
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		e := entry[id]
		if e.Explanation.Explicit || seen[id] {
			return
		}
		seen[id] = true
		// the facts it follows from, in the order they lead from its subject to its object; what
		// the ontology says about its relations and classes (可以传递, 规则 …) after it
		var facts []graph.RDFTriple
		var notes []string
		for _, k := range kids[id] {
			walk(k)
			t := entry[k].Explanation.Triple
			s := statement(t, chains)
			switch {
			case s == "":
			case chainable(t):
				facts = append(facts, t)
			case !slices.Contains(notes, s):
				notes = append(notes, s)
			}
		}
		concl := statement(e.Explanation.Triple, chains)
		if concl == "" || len(facts)+len(notes) == 0 {
			return
		}
		var lead []string
		for at := e.Explanation.Triple.Subject.Value; len(facts) > 0; {
			i := slices.IndexFunc(facts, func(t graph.RDFTriple) bool { return t.Subject.Value == at })
			if i < 0 {
				i = 0
			}
			lead = append(lead, statement(facts[i], chains))
			at = facts[i].Object.Value
			facts = slices.Delete(facts, i, i+1)
		}
		step := strings.Join(lead, "；") + " → " + concl
		if len(lead) == 0 {
			step = strings.Join(notes, "；") + " → " + concl
		} else if len(notes) > 0 {
			step += "（" + strings.Join(notes, "；") + "）"
		}
		if !slices.Contains(steps, step) {
			steps = append(steps, step)
		}
	}
	walk(r.Trace[0].TripleID)
	return steps
}

// chainable: a fact about things or classes (a relation, 是, 是一种), not about the ontology's
// relations.
func chainable(t graph.RDFTriple) bool {
	if _, ns := nameOf(t.Predicate); ns == nsRel {
		return true
	}
	_, ons := nameOf(t.Object)
	return (t.Predicate.Value == iriType.Value || t.Predicate.Value == iriSubClassOf.Value) && ons == nsClass
}

// statement is a triple as people read it ("" for what is only how RDF writes things down).
func statement(t graph.RDFTriple, chains map[string]string) string {
	s, _ := nameOf(t.Subject)
	o, ons := nameOf(t.Object)
	p := t.Predicate.Value
	switch {
	case p == iriType.Value && ons == nsClass:
		return s + " 是 " + o
	case p == iriType.Value && t.Object.Value == nsOWL+"TransitiveProperty":
		return "「" + s + "」可以传递"
	case p == iriType.Value && t.Object.Value == nsOWL+"SymmetricProperty":
		return "「" + s + "」是相互的"
	case p == iriSubClassOf.Value && ons == nsClass && s != o:
		return s + " 是一种 " + o
	case p == nsRDFS+"domain" && ons == nsClass:
		return "有「" + s + "」的是 " + o
	case p == nsRDFS+"range" && ons == nsClass:
		return "「" + s + "」指向的是 " + o
	case p == nsOWL+"inverseOf":
		return "「" + s + "」反过来是「" + o + "」"
	case p == nsOWL+"propertyChainAxiom":
		if id := chains[t.Object.Value]; id != "" {
			return "规则「" + id + "」"
		}
		return "「" + s + "」的规则"
	}
	if rel, ns := nameOf(t.Predicate); ns == nsRel && s != "" && o != "" {
		return s + " " + rel + " " + o
	}
	return ""
}

// OntGraph is the ontology's classes for drawing beside the knowledge graph: the classes declared
// (and the built-in ones they are kinds of), and which things of the graph are given as of them.
type OntGraph struct {
	Classes []OntGraphClass `json:"classes"`
	Types   []OntGraphType  `json:"types"`
}

type OntGraphClass struct {
	Name    string   `json:"name"`
	Sub     []string `json:"sub"`
	Builtin bool     `json:"builtin,omitempty"`
}

type OntGraphType struct {
	Thing string `json:"thing"`
	Class string `json:"class"`
}

func (b *Base) OntologyGraph(ctx context.Context, company string) (*OntGraph, error) {
	m, err := b.model(company)
	if err != nil {
		return nil, err
	}
	out := &OntGraph{Classes: []OntGraphClass{}, Types: []OntGraphType{}}
	if m.Ontology == nil {
		return out, nil
	}
	declared := map[string]bool{}
	used := map[string]bool{}
	for _, c := range m.Ontology.Classes {
		if _, builtin := builtinClass(c.Name); builtin {
			continue
		}
		declared[c.Name] = true
		out.Classes = append(out.Classes, OntGraphClass{Name: c.Name, Sub: append([]string{}, c.Sub...)})
		for _, s := range c.Sub {
			used[s] = true
		}
	}
	for _, c := range builtinClasses {
		if used[c.Name] {
			out.Classes = append(out.Classes, OntGraphClass{Name: c.Name, Sub: []string{}, Builtin: true})
		}
	}
	err = b.read(company, func(db *cortexdb.DB, st ontoState) error {
		if db == nil {
			return nil
		}
		types, err := find(ctx, db, nil, &iriType, nil)
		if err != nil {
			return err
		}
		for _, t := range types {
			c, ns := nameOf(t.Object)
			s, _ := nameOf(t.Subject)
			if t.Inferred || ns != nsClass || !declared[c] || st.Things[s] == "" {
				continue
			}
			// a product that is a named thing is drawn under its product name
			for k, v := range st.Alias {
				if v == s {
					s = k
				}
			}
			out.Types = append(out.Types, OntGraphType{Thing: s, Class: c})
		}
		return nil
	})
	return out, err
}

// ---- SPARQL ----

// SPARQLAnswer is what a read-only SPARQL query found, its terms by name.
type SPARQLAnswer struct {
	Vars []string            `json:"vars"`
	Rows []map[string]string `json:"rows"`
	// Boolean answers an ASK.
	Boolean *bool `json:"boolean,omitempty"`
	Count   int   `json:"count"`
	// Cut: more rows than are returned.
	Cut bool `json:"cut,omitempty"`
}

var (
	sparqlPrologue = regexp.MustCompile(`(?is)^\s*(?:#[^\n]*\n\s*|(?:PREFIX\s+[\w-]*:\s*<[^>]*>|BASE\s*<[^>]*>)\s*)*`)
	sparqlForm     = regexp.MustCompile(`(?i)^(SELECT|ASK|CONSTRUCT|DESCRIBE)\b`)
	sparqlScoped   = regexp.MustCompile(`(?i)\b(FROM|GRAPH)\b`)
	sparqlWhere    = regexp.MustCompile(`(?i)\bWHERE\b|\{`)
	sparqlUpdate   = regexp.MustCompile(`(?i)\b(INSERT|DELETE|LOAD|CLEAR|DROP|CREATE|ADD|MOVE|COPY)\b\s*(DATA|WHERE|GRAPH|SILENT|ALL|DEFAULT|NAMED|\{|<)`)
)

// expandNames writes c:食材, r:原料 and t:FZ-101 out as whole IRIs: CortexDB's SPARQL reads a
// Chinese name in an IRI, not after a prefix.
func expandNames(q string) string {
	ns := map[byte]string{'c': nsClass, 'r': nsRel, 't': nsThing}
	var b strings.Builder
	for i := 0; i < len(q); {
		switch ch := q[i]; {
		case ch == '<' || ch == '"' || ch == '\'':
			end := map[byte]byte{'<': '>', '"': '"', '\'': '\''}[ch]
			j := strings.IndexByte(q[i+1:], end)
			if j < 0 {
				b.WriteString(q[i:])
				return b.String()
			}
			b.WriteString(q[i : i+j+2])
			i += j + 2
		case ns[ch] != "" && i+1 < len(q) && q[i+1] == ':' && (i == 0 || strings.IndexByte(" \t\n({;,/|^", q[i-1]) >= 0):
			j := i + 2
			for j < len(q) && strings.IndexByte(" \t\n(){};,<>\"'|/^*+?", q[j]) < 0 {
				j++
			}
			name := strings.TrimRight(q[i+2:j], ".")
			if name == "" {
				b.WriteByte(ch)
				i++
				continue
			}
			b.WriteString("<" + ns[ch] + esc(name) + ">")
			i += 2 + len(name)
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}

// SPARQLPrefixes are the prefixes every query may use without declaring them.
const SPARQLPrefixes = "PREFIX rdf: <" + nsRDF + ">\nPREFIX rdfs: <" + nsRDFS + ">\nPREFIX owl: <" + nsOWL + ">\n" +
	"PREFIX c: <" + nsClass + ">\nPREFIX r: <" + nsRel + ">\nPREFIX t: <" + nsThing + ">\n"

// OntologySPARQL runs a read-only query (SELECT, ASK, CONSTRUCT, DESCRIBE) over the company's
// ontology, inferred facts included. The prefixes c: (classes), r: (relations), t: (things), rdf:,
// rdfs: and owl: need no declaring; a query that names no graph reads the ontology's.
func (b *Base) OntologySPARQL(ctx context.Context, company, query string) (*SPARQLAnswer, error) {
	prologue := sparqlPrologue.FindString(query)
	body := query[len(prologue):]
	if !sparqlForm.MatchString(body) || sparqlUpdate.MatchString(body) {
		return nil, &OntologyError{Msg: "只能查（SELECT、ASK、CONSTRUCT、DESCRIBE），不能改"}
	}
	if !sparqlScoped.MatchString(body) {
		if loc := sparqlWhere.FindStringIndex(body); loc != nil {
			body = body[:loc[0]] + "FROM <" + OntologyGraph + "> " + body[loc[0]:]
		}
	}
	query = SPARQLPrefixes + prologue + expandNames(body)
	out := &SPARQLAnswer{Vars: []string{}, Rows: []map[string]string{}}
	err := b.read(company, func(db *cortexdb.DB, st ontoState) error {
		if db == nil {
			return &OntologyError{Missing: true, Msg: "这家公司的行业包没有声明本体"}
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		r, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: query})
		if err != nil {
			return &OntologyError{Msg: "SPARQL：" + err.Error()}
		}
		res := r.Result
		out.Vars, out.Count = append(out.Vars, res.Vars...), res.Count
		if strings.EqualFold(res.QueryType, "ASK") {
			v := res.Boolean
			out.Boolean = &v
		}
		term := func(t graph.RDFTerm) string {
			if n, _ := nameOf(t); n != "" {
				return n
			}
			return t.Value
		}
		for _, row := range res.Bindings {
			if len(out.Rows) == 200 {
				out.Cut = true
				break
			}
			m := map[string]string{}
			for k, v := range row {
				m[k] = term(v)
			}
			out.Rows = append(out.Rows, m)
		}
		if len(res.Triples) > 0 && len(out.Rows) == 0 {
			out.Vars = []string{"subject", "predicate", "object"}
			for _, t := range res.Triples {
				if len(out.Rows) == 200 {
					out.Cut = true
					break
				}
				out.Rows = append(out.Rows, map[string]string{"subject": term(t.Subject), "predicate": term(t.Predicate), "object": term(t.Object)})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
