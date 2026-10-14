package ops

import (
	"context"
	"errors"
	"slices"
	"strings"

	"smartfactory/internal/agents"
	"smartfactory/internal/auth"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/mcptools"
)

// A company's knowledge base: members of the company read it, its administrators rebuild it.

func (o *Ops) kb(who auth.Who, company string, need auth.Role) error {
	if o.KB == nil {
		return fail(503, "这台服务器没有知识库（没有数据目录）")
	}
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return fail(404, "没有公司 %s", company)
	}
	if !who.Can(company, need) {
		if who.Key == "" {
			return fail(401, "用令牌登录")
		}
		return fail(403, "要公司 %s %s以上的令牌", company, need.Label())
	}
	return nil
}

// KnowledgeOverview is what a company's knowledge base holds.
type KnowledgeOverview struct {
	knowledge.Stats
	// Docs in the library, and how many of them are in the knowledge base.
	Library int `json:"library"`
	Indexed int `json:"indexed"`
}

func (o *Ops) KnowledgeStats(ctx context.Context, who auth.Who, company string) (*KnowledgeOverview, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	st, err := o.KB.Stats(ctx, company)
	if err != nil {
		return nil, err
	}
	out := &KnowledgeOverview{Stats: st}
	if o.Agents != nil {
		if docs, err := o.Agents.Store.Docs(company); err == nil {
			out.Library = len(docs)
			for _, d := range docs {
				if o.KB.HasDoc(ctx, company, d.ID) {
					out.Indexed++
				}
			}
		}
	}
	return out, nil
}

// KnowledgeSearch finds the passages that answer query. names are things the question is about
// and keywords other words for it, if the caller knows them (both can be nil).
func (o *Ops) KnowledgeSearch(ctx context.Context, who auth.Who, company, query string, names, keywords []string, limit int) (*knowledge.Answer, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fail(400, "问点什么")
	}
	var named []string
	for _, n := range names {
		// a name as the graph has it, when it means one thing: "rf101" is RF-101
		if ls := o.KB.Resolve(company, n); len(ls) == 1 {
			n = ls[0].Name
		}
		named = append(named, strings.TrimSpace(n))
	}
	a, err := o.KB.Search(ctx, company, query, named, keywords, max(1, min(limit, 20)))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// EntityAnswer is the thing asked for, or the things whose names contain the words.
type EntityAnswer struct {
	Entity     *knowledge.Entity `json:"entity,omitempty"`
	Candidates []knowledge.Link  `json:"candidates"`
}

// KnowledgeEntity is the thing called name, also as people write it ("rf101" is RF-101); when
// that is not one thing, the things it may be.
func (o *Ops) KnowledgeEntity(ctx context.Context, who auth.Who, company, name string) (*EntityAnswer, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fail(400, "查什么")
	}
	e, err := o.KB.Lookup(ctx, company, name)
	if err != nil {
		return nil, err
	}
	out := &EntityAnswer{Entity: e, Candidates: []knowledge.Link{}}
	if e != nil {
		return out, nil
	}
	resolved := o.KB.Resolve(company, name)
	if len(resolved) == 1 {
		if out.Entity, err = o.KB.Lookup(ctx, company, resolved[0].Name); err != nil || out.Entity != nil {
			return out, err
		}
	}
	found, err := o.KB.Find(ctx, company, name, "", 20)
	if err != nil {
		return nil, err
	}
	for _, l := range append(resolved, found...) {
		if len(out.Candidates) < 20 && !slices.Contains(out.Candidates, l) {
			out.Candidates = append(out.Candidates, l)
		}
	}
	return out, nil
}

// KnowledgeMentions is the passages that name the thing called name (as the graph has it), up
// to limit, and how many there are.
func (o *Ops) KnowledgeMentions(ctx context.Context, who auth.Who, company, name string, limit int) ([]knowledge.Passage, int, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, 0, err
	}
	return o.KB.Mentioning(ctx, company, name, limit)
}

// DocParts is a run of a library document's passages.
type DocParts struct {
	Doc   string        `json:"doc"`
	Title string        `json:"title"`
	Total int           `json:"total"`
	Parts []agents.Part `json:"parts"`
}

// KnowledgeDoc reads n passages of a library document from passage from on: around what a
// search found in it, or all of it.
func (o *Ops) KnowledgeDoc(ctx context.Context, who auth.Who, company, doc string, from, n int) (*DocParts, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	if o.Agents == nil {
		return nil, fail(503, "这台服务器没有资料库")
	}
	parts, total, err := o.Agents.Store.ReadDoc(company, doc, from, n)
	if errors.Is(err, agents.ErrNotFound) {
		return nil, fail(404, "没有文档 %s", doc)
	}
	if err != nil {
		return nil, err
	}
	out := &DocParts{Doc: doc, Total: total, Parts: parts}
	if len(parts) > 0 {
		out.Title = parts[0].Title
	}
	return out, nil
}

func (o *Ops) KnowledgeGraph(ctx context.Context, who auth.Who, company string) (*knowledge.Graph, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	g, err := o.KB.Graph(ctx, company)
	return &g, err
}

// Rebuilt is what a rebuild did.
type Rebuilt struct {
	Docs int `json:"docs"`
}

// KnowledgeRebuild writes the company's process again and indexes every document again.
func (o *Ops) KnowledgeRebuild(ctx context.Context, who auth.Who, via, company string) (*Rebuilt, error) {
	if err := o.kb(who, company, auth.Admin); err != nil {
		return nil, err
	}
	o.KB.Forget(company)
	o.KB.ForgetOrders(company)
	o.KB.ForgetEvents(company)
	err := o.KB.SyncProcess(ctx, company)
	if err == nil {
		err = o.KB.SyncOrders(ctx, company)
	}
	if err == nil {
		err = o.KB.SyncEvents(ctx, company)
	}
	n := 0
	if err == nil && o.Agents != nil {
		n, err = o.Agents.IndexDocs(ctx, company, true)
	}
	o.Audit.Record(who, via, company, "knowledge.rebuild", company, "", err)
	if err != nil {
		return nil, err
	}
	return &Rebuilt{Docs: n}, nil
}

// KnowledgeTools are these operations as MCP's knowledge tools ask them.
func (o *Ops) KnowledgeTools() mcptools.Knowledge {
	k := mcptools.Knowledge{
		Stats: func(ctx context.Context, who auth.Who, company string) (any, error) {
			return o.KnowledgeStats(ctx, who, company)
		},
		Search: o.KnowledgeSearch,
		Entity: func(ctx context.Context, who auth.Who, company, name string) (*knowledge.Entity, []knowledge.Link, error) {
			a, err := o.KnowledgeEntity(ctx, who, company, name)
			if err != nil {
				return nil, nil, err
			}
			return a.Entity, a.Candidates, nil
		},
		Mentions: o.KnowledgeMentions,
		Graph:    o.KnowledgeGraph,

		Ontology:          o.Ontology,
		OntologyInstances: o.OntologyInstances,
		OntologyThing:     o.OntologyThing,
	}
	if o.Agents != nil {
		k.ReadDoc = func(ctx context.Context, who auth.Who, company, doc string, from, n int) (string, int, []mcptools.DocPart, error) {
			d, err := o.KnowledgeDoc(ctx, who, company, doc, from, n)
			if err != nil {
				return "", 0, nil, err
			}
			parts := make([]mcptools.DocPart, len(d.Parts))
			for i, p := range d.Parts {
				parts[i] = mcptools.DocPart{Part: p.Seq, Heading: p.Heading, Text: p.Text}
			}
			return d.Title, d.Total, parts, nil
		}
	}
	return k
}
