package knowledge

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/liveview"
)

// A company's knowledge base drawn by CortexDB's live view: a 3D graph that keeps up with the base
// as it is written, with find, ask and Cypher over it (read only), the record behind a node, and
// what the assistant reads lit as it reads it. One view a company, made when it is first opened
// and kept until the company is dropped or the server closes; the web server mounts its routes.

type view struct {
	sv     *liveview.Server
	cancel context.CancelFunc
}

// lit is the most nodes one search lights: the things it walked from and the ones its passages are
// about, not everything a long passage mentions.
const lit = 32

// View is the company's live view.
func (b *Base) View(id string) (*liveview.Server, error) {
	c, err := b.open(id)
	if err != nil {
		return nil, err
	}
	b.vmu.Lock()
	defer b.vmu.Unlock()
	if v, ok := b.views[id]; ok {
		return v.sv, nil
	}
	name := id
	if m, err := b.model(id); err == nil && m.Name != "" {
		name = m.Name
	}
	src := liveview.SourceFor(c.db, name)
	// the ontology is the company's own, kept in a file beside the base (ontology.go), not a
	// schema saved in it; nothing is drafted from the base
	src.Ontology = func(ctx context.Context, _ liveview.OntologyQuery) (liveview.OntologyReport, error) {
		return b.ontologyReport(ctx, id)
	}
	src.Draft = nil
	ctx, cancel := context.WithCancel(context.Background())
	// the recall log, the library shelves and the ontology's saved versions are CortexDB's own
	// bookkeeping, which this base does not keep
	sv, err := liveview.New(ctx, src, liveview.DefaultInterval, true,
		liveview.WithoutPages(liveview.PageRecalls, liveview.PageLibrary, liveview.PageVersions))
	if err != nil {
		cancel()
		return nil, err
	}
	if b.views == nil {
		b.views = map[string]*view{}
	}
	b.views[id] = &view{sv: sv, cancel: cancel}
	return sv, nil
}

// closeView stops a company's view, before its base is closed under it.
func (b *Base) closeView(id string) {
	b.vmu.Lock()
	defer b.vmu.Unlock()
	if v, ok := b.views[id]; ok {
		v.cancel()
		v.sv.Close()
		delete(b.views, id)
	}
}

func (b *Base) closeViews() {
	b.vmu.Lock()
	ids := make([]string, 0, len(b.views))
	for id := range b.views {
		ids = append(ids, id)
	}
	b.vmu.Unlock()
	for _, id := range ids {
		b.closeView(id)
	}
}

// observe shows what was read on the company's view, if somebody opened one: names are the things
// read, lit by their node ids.
func (b *Base) observe(company, tool, text string, names []string) {
	b.vmu.Lock()
	v, ok := b.views[company]
	b.vmu.Unlock()
	if !ok {
		return
	}
	ids := []string{}
	for _, n := range names {
		if id := cortexdb.EntityNodeID(n); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
		if len(ids) == lit {
			break
		}
	}
	v.sv.Observe(liveview.Event{At: time.Now().UnixMilli(), Kind: liveview.KindQuery, Tool: tool, Text: text, Nodes: ids})
}

// ontologyReport is the company's ontology as the view's ontology page draws it: each class an
// object type, a class others are kinds of also an interface they implement, and each relation
// with a domain and a range a link between them.
func (b *Base) ontologyReport(ctx context.Context, id string) (liveview.OntologyReport, error) {
	ov, err := b.OntologyOverview(ctx, id)
	if err != nil {
		return liveview.OntologyReport{Available: false, Reason: err.Error(), State: liveview.OntologyUnreadable}, nil
	}
	out := liveview.OntologyReport{
		// it names what may be said and infers from it; nothing written is refused by it
		Available: true, State: liveview.OntologyLive, Saved: true, Active: true, Enforcement: string(cortexdb.OntologyEnforcementVocabulary),
		Schemas: []liveview.OntologySchemaRef{}, Name: "SmartFactory 本体",
		ObjectTypes: []liveview.OntologyObjectNode{}, LinkTypes: []liveview.OntologyLinkEdge{}, Interfaces: []liveview.OntologyInterfaceNode{},
		Usage: liveview.OntologyUsage{Reason: "用的是 SmartFactory 自己的本体，实例数见各个类", UndeclaredNodes: []liveview.OntologyStrayType{}, UndeclaredEdges: []liveview.OntologyStrayType{}},
		At:    time.Now().UnixMilli(),
	}
	if !ov.Declared {
		out.Description = "行业包没有声明本体，这里只有内置的类和关系"
	}
	kinds := map[string][]string{} // a class: the classes that are kinds of it
	for _, c := range ov.Classes {
		for _, s := range c.Sub {
			kinds[s] = append(kinds[s], c.Name)
		}
	}
	for _, c := range ov.Classes {
		out.ObjectTypes = append(out.ObjectTypes, liveview.OntologyObjectNode{
			APIName: c.Name, DisplayName: c.Name, Description: c.Note,
			Implements: append([]string{}, c.Sub...), Properties: []liveview.OntologyProp{},
			Instances: c.Asserted + c.Inferred,
		})
		if subs := kinds[c.Name]; len(subs) > 0 {
			out.Interfaces = append(out.Interfaces, liveview.OntologyInterfaceNode{
				APIName: c.Name, DisplayName: c.Name, Description: c.Note,
				Extends: slices.Clone(c.Sub), Properties: []liveview.OntologyProp{}, Implementors: subs,
			})
		}
	}
	for _, r := range ov.Relations {
		if r.Domain == "" || r.Range == "" {
			continue
		}
		label := cmp.Or(r.Label, r.Name)
		out.LinkTypes = append(out.LinkTypes, liveview.OntologyLinkEdge{
			APIName: r.Name, Description: cmp.Or(r.Text, r.Note),
			A:            liveview.OntologyLinkEnd{APIName: r.Name, DisplayName: label, ObjectType: r.Domain, Cardinality: string(cortexdb.OntologyCardinalityMany)},
			B:            liveview.OntologyLinkEnd{APIName: cmp.Or(r.Inverse, r.Name), DisplayName: cmp.Or(r.Inverse, label), ObjectType: r.Range, Cardinality: string(cortexdb.OntologyCardinalityMany)},
			Multiplicity: "many-to-many",
		})
	}
	return out, nil
}
