package ops

import (
	"context"
	"slices"
	"strings"

	"smartfactory/internal/auth"
	"smartfactory/internal/knowledge"
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

func (o *Ops) KnowledgeSearch(ctx context.Context, who auth.Who, company, query string, limit int) (*knowledge.Answer, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fail(400, "问点什么")
	}
	a, err := o.KB.Search(ctx, company, query, nil, nil, max(1, min(limit, 20)))
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

func (o *Ops) KnowledgeEntity(ctx context.Context, who auth.Who, company, name string) (*EntityAnswer, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	e, err := o.KB.Lookup(ctx, company, strings.TrimSpace(name))
	if err != nil {
		return nil, err
	}
	out := &EntityAnswer{Entity: e, Candidates: []knowledge.Link{}}
	if e == nil {
		if out.Candidates, err = o.KB.Find(ctx, company, strings.TrimSpace(name), "", 20); err != nil {
			return nil, err
		}
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
