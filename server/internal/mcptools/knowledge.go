package mcptools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
	"smartfactory/internal/knowledge"
)

// Knowledge is a company's knowledge base as the console reads it (package ops, which this
// package cannot import), with the same checks whichever way one asks.
type Knowledge struct {
	Stats    func(ctx context.Context, who auth.Who, company string) (any, error)
	Search   func(ctx context.Context, who auth.Who, company, query string, names, keywords []string, limit int) (*knowledge.Answer, error)
	Entity   func(ctx context.Context, who auth.Who, company, name string) (*knowledge.Entity, []knowledge.Link, error)
	Mentions func(ctx context.Context, who auth.Who, company, name string, limit int) ([]knowledge.Passage, int, error)
	Graph    func(ctx context.Context, who auth.Who, company string) (*knowledge.Graph, error)
	// ReadDoc reads n passages of a library document from passage from on; nil without a library.
	ReadDoc func(ctx context.Context, who auth.Who, company, doc string, from, n int) (title string, total int, parts []DocPart, err error)
}

// DocPart is one passage of a library document.
type DocPart struct {
	Part    int    `json:"part"`
	Heading string `json:"heading,omitempty"`
	Text    string `json:"text"`
}

var graphKinds = []string{knowledge.TCompany, knowledge.TPlant, knowledge.TLine, knowledge.TStation, knowledge.TMachine, knowledge.TProduct, knowledge.TSupplier, knowledge.TCustomer, knowledge.TOrder}

type kbSearchIn struct {
	Company  string   `json:"company" jsonschema:"company id"`
	Query    string   `json:"query" jsonschema:"the question, in the user's words (Chinese works best for Chinese documents)"`
	Names    []string `json:"names,omitempty" jsonschema:"things the question is about: station numbers (HP-101), machine names, lines, products, customers, suppliers, order numbers"`
	Keywords []string `json:"keywords,omitempty" jsonschema:"other words for what is asked (synonyms, other phrasings)"`
	Limit    int      `json:"limit,omitempty" jsonschema:"at most this many passages (default 8, up to 20)"`
}

type kbPassage struct {
	// From: doc (a library document) | process (the company's process as configured) | order (a production order)
	From  string `json:"from"`
	Title string `json:"title"`
	// Doc and Part locate a document passage: read_doc reads it and its neighbours.
	Doc  string `json:"doc,omitempty"`
	Part *int   `json:"part,omitempty"`
	// Cite is how to name the source to the user.
	Cite string `json:"cite"`
	Text string `json:"text"`
	// Cut: the text was shortened.
	Cut      bool     `json:"cut,omitempty"`
	Entities []string `json:"entities,omitempty"`
}

type kbSearchOut struct {
	// Mode is how it searched: ppr (a walk over the graph from the things the question names) or lexical.
	Mode string `json:"mode"`
	// Recognized are the things of the graph the question was found to name.
	Recognized []string    `json:"recognized,omitempty"`
	Passages   []kbPassage `json:"passages"`
	Note       string      `json:"note"`
}

type kbEntityIn struct {
	Company  string `json:"company" jsonschema:"company id"`
	Name     string `json:"name" jsonschema:"a station number (HP-101, hp101), machine name, line, product, customer, supplier, plant or order number"`
	Passages int    `json:"passages,omitempty" jsonschema:"at most this many passages that name it (default 10, up to 50)"`
}

type kbName struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type kbLink struct {
	Type string `json:"type"`
	// Dir: out when the thing is the subject (IH-101 feeds RF-101 is out from IH-101), in when it is the object.
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type kbEntity struct {
	Name  string   `json:"name"`
	Kind  string   `json:"kind"`
	Links []kbLink `json:"links"`
	// LinksLeftOut are relations not listed (orders first): knowledge_graph lists them.
	LinksLeftOut int `json:"linksLeftOut,omitempty"`
}

type kbEntityOut struct {
	Entity        *kbEntity   `json:"entity,omitempty"`
	Passages      []kbPassage `json:"passages,omitempty"`
	PassagesTotal int         `json:"passagesTotal,omitempty"`
	Candidates    []kbName    `json:"candidates,omitempty"`
	Note          string      `json:"note"`
}

type kbGraphIn struct {
	Company string   `json:"company" jsonschema:"company id"`
	Around  string   `json:"around,omitempty" jsonschema:"a thing's name: the part of the graph around it"`
	Hops    int      `json:"hops,omitempty" jsonschema:"with around: how many relations away (default 1, up to 3)"`
	Kinds   []string `json:"kinds,omitempty" jsonschema:"only things of these kinds: company, plant, line, station, machine, product, supplier, customer, order"`
	Limit   int      `json:"limit,omitempty" jsonschema:"at most this many things (default 100, up to 200)"`
}

type kbGraphOut struct {
	// Summary: with only company, the counts per kind and relation and the passages indexed.
	Summary      any                   `json:"summary,omitempty"`
	Center       string                `json:"center,omitempty"`
	Nodes        []knowledge.GraphNode `json:"nodes,omitempty"`
	Edges        []knowledge.GraphEdge `json:"edges,omitempty"`
	NodesLeftOut int                   `json:"nodesLeftOut,omitempty"`
	EdgesLeftOut int                   `json:"edgesLeftOut,omitempty"`
	Candidates   []kbName              `json:"candidates,omitempty"`
	Note         string                `json:"note"`
}

type readDocIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Doc     string `json:"doc" jsonschema:"document id (doc of a knowledge_search passage)"`
	From    int    `json:"from,omitempty" jsonschema:"first passage, from 0"`
	Count   int    `json:"count,omitempty" jsonschema:"how many passages (default 20, up to 40)"`
}

type readDocOut struct {
	Doc   string    `json:"doc"`
	Title string    `json:"title"`
	Total int       `json:"total"`
	Parts []DocPart `json:"parts"`
	// Next is the passage to read from for the rest.
	Next *int `json:"next,omitempty"`
}

const (
	searchText  = 1500 // runes of a passage knowledge_search returns
	mentionText = 400  // runes of a passage naming a thing
	entityLinks = 100
	graphEdges  = 400
)

// AddKnowledge lets an agent search the company's documents, process and orders, and walk the
// graph that links them.
func AddKnowledge(s *mcp.Server, kb Knowledge) {
	mcp.AddTool(s, &mcp.Tool{Name: "knowledge_search", Description: "Search a company's knowledge base: passages of the documents in its library, descriptions of its process (plants, lines, each station with what comes before and after it, machines, products, suppliers, customers) " +
		"and its production orders (customer, product, line, progress, due date). Station numbers, machines, lines, products, customers and order numbers named in the query (or in names) are recognised " +
		"and the search walks the graph from them (mode ppr), so a document about a machine is found from a question naming only a station built from it; otherwise it searches words (mode lexical). " +
		"Returns the best passages, each with from (doc / process / order), title, doc id and part for a document passage, cite (how to name the source), its text and the things it names. " +
		"Answer from the passages and cite them; long passages are cut, read_doc reads a document whole."},
		guard(auth.Member, func(in kbSearchIn) string { return in.Company }, func(ctx context.Context, req *mcp.CallToolRequest, in kbSearchIn) (*mcp.CallToolResult, kbSearchOut, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 8
			}
			a, err := kb.Search(ctx, who(req), in.Company, in.Query, in.Names, in.Keywords, min(limit, 20))
			if err != nil {
				return nil, kbSearchOut{}, err
			}
			out := kbSearchOut{Mode: a.Mode, Recognized: a.Entities, Passages: []kbPassage{}}
			cut := false
			for _, p := range a.Passages {
				kp := passage(p, searchText)
				cut = cut || kp.Cut
				out.Passages = append(out.Passages, kp)
			}
			switch {
			case len(out.Passages) == 0:
				out.Note = "Nothing found: try other words or keywords, or knowledge_entity for a name."
			case cut:
				out.Note = "Some passages are cut: read_doc reads a document passage whole."
			default:
				out.Note = "Cite the passages you answer from."
			}
			return nil, out, nil
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "knowledge_entity", Description: "One thing in a company's knowledge graph (a station such as HP-101, a machine, line, product, customer, supplier, plant or production order) and all its relations both ways: " +
		"which station feeds it and which it feeds, which line it is on, which machine it is (is_a), what a line makes, who supplies a product and who it is sold to, a customer's orders, an order's product and line; " +
		"plus the passages (documents first, then process, then orders) that name it, shortened, with passagesTotal. Numbers are matched however they are written (hp101, HP 101 → HP-101). " +
		"When the name is not one thing (a partial name, a station number found in several plants), it returns candidates instead: call again with one of their names."},
		guard(auth.Member, func(in kbEntityIn) string { return in.Company }, func(ctx context.Context, req *mcp.CallToolRequest, in kbEntityIn) (*mcp.CallToolResult, kbEntityOut, error) {
			w := who(req)
			e, cands, err := kb.Entity(ctx, w, in.Company, in.Name)
			if err != nil {
				return nil, kbEntityOut{}, err
			}
			if e == nil {
				if len(cands) == 0 {
					return nil, kbEntityOut{}, fmt.Errorf("nothing in %s's knowledge graph is called %q; try knowledge_search", in.Company, in.Name)
				}
				return nil, kbEntityOut{Candidates: names(cands), Note: fmt.Sprintf("Nothing is called %q exactly; it may be one of these: call again with its name.", in.Name)}, nil
			}
			out := kbEntityOut{Entity: &kbEntity{Name: e.Name, Kind: e.Kind, Links: []kbLink{}}, Passages: []kbPassage{}}
			links := slices.Clone(e.Links)
			// a customer or line can have hundreds of orders: the rest of its relations first
			slices.SortStableFunc(links, func(a, b knowledge.Link) int { return order(a) - order(b) })
			for _, l := range links {
				if len(out.Entity.Links) == entityLinks {
					out.Entity.LinksLeftOut++
					continue
				}
				dir := "in"
				if l.Out {
					dir = "out"
				}
				out.Entity.Links = append(out.Entity.Links, kbLink{Type: l.Type, Dir: dir, Name: l.Name, Kind: l.Kind})
			}
			n := in.Passages
			if n <= 0 {
				n = 10
			}
			ps, total, err := kb.Mentions(ctx, w, in.Company, e.Name, min(n, 50))
			if err != nil {
				return nil, kbEntityOut{}, err
			}
			for _, p := range ps {
				out.Passages = append(out.Passages, passage(p, mentionText))
			}
			out.PassagesTotal = total
			out.Note = fmt.Sprintf("%d relations, %d passages name it.", len(e.Links), total)
			if out.Entity.LinksLeftOut > 0 {
				out.Note += fmt.Sprintf(" %d relations are not listed: knowledge_graph with around lists them.", out.Entity.LinksLeftOut)
			}
			if total > len(ps) {
				out.Note += " Ask for more passages with passages (up to 50), or knowledge_search."
			}
			return nil, out, nil
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "knowledge_graph", Description: "Explore a company's knowledge graph. With only company: what it holds (things per kind, relations per type, document / process / order passages, library documents and how many are indexed). " +
		"With around (a name): the things within hops relations of it either way (default 1, up to 3), nearest first, and the relations among them. With kinds: only things of those kinds (alone: every thing of those kinds). " +
		"Kinds: company, plant, line, station, machine, product, supplier, customer, order. Relations: has_plant, has_line, has_station, feeds (station → next station), is_a (station → machine), makes (line → product), " +
		"supplies (supplier → product), sold_to (product → customer), for_customer / of_product / runs_on (order → customer / product / line). Each node has mentions, how many passages name it. " +
		"At most limit things (default 100, up to 200) and 400 relations; nodesLeftOut / edgesLeftOut say what was cut: narrow it with kinds or fewer hops."},
		guard(auth.Member, func(in kbGraphIn) string { return in.Company }, func(ctx context.Context, req *mcp.CallToolRequest, in kbGraphIn) (*mcp.CallToolResult, kbGraphOut, error) {
			w := who(req)
			for _, k := range in.Kinds {
				if !slices.Contains(graphKinds, k) {
					return nil, kbGraphOut{}, fmt.Errorf("no kind %q; kinds are %s", k, strings.Join(graphKinds, ", "))
				}
			}
			if strings.TrimSpace(in.Around) == "" && len(in.Kinds) == 0 {
				st, err := kb.Stats(ctx, w, in.Company)
				if err != nil {
					return nil, kbGraphOut{}, err
				}
				return nil, kbGraphOut{Summary: st, Note: "Counts only: call with around (a name) for the part of the graph around it, or kinds to list things of those kinds."}, nil
			}
			out := kbGraphOut{}
			if strings.TrimSpace(in.Around) != "" {
				e, cands, err := kb.Entity(ctx, w, in.Company, in.Around)
				if err != nil {
					return nil, out, err
				}
				if e == nil {
					if len(cands) == 0 {
						return nil, out, fmt.Errorf("nothing in %s's knowledge graph is called %q; try knowledge_search", in.Company, in.Around)
					}
					return nil, kbGraphOut{Candidates: names(cands), Note: fmt.Sprintf("Nothing is called %q exactly; it may be one of these: call again with its name.", in.Around)}, nil
				}
				out.Center = e.Name
			}
			g, err := kb.Graph(ctx, w, in.Company)
			if err != nil {
				return nil, out, err
			}
			hops := in.Hops
			if hops <= 0 {
				hops = 1
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 100
			}
			sub, moreNodes, moreEdges := g.Around(out.Center, min(hops, 3), in.Kinds, min(limit, 200), graphEdges)
			out.Nodes, out.Edges, out.NodesLeftOut, out.EdgesLeftOut = sub.Nodes, sub.Edges, moreNodes, moreEdges
			out.Note = fmt.Sprintf("%d things, %d relations.", len(sub.Nodes), len(sub.Edges))
			if moreNodes > 0 || moreEdges > 0 {
				out.Note += fmt.Sprintf(" Cut: %d more things and %d more relations; narrow it with kinds or fewer hops.", moreNodes, moreEdges)
			}
			return nil, out, nil
		}))

	if kb.ReadDoc == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{Name: "read_doc", Description: "Read a document of a company's library passage by passage (the doc id and part of a knowledge_search passage): the text around a passage that was found, or the whole document. " +
		"Returns the title, the total number of passages, the passages read, and next when there is more."},
		guard(auth.Member, func(in readDocIn) string { return in.Company }, func(ctx context.Context, req *mcp.CallToolRequest, in readDocIn) (*mcp.CallToolResult, readDocOut, error) {
			n := in.Count
			if n <= 0 {
				n = 20
			}
			title, total, parts, err := kb.ReadDoc(ctx, who(req), in.Company, in.Doc, max(in.From, 0), min(n, 40))
			if err != nil {
				return nil, readDocOut{}, err
			}
			out := readDocOut{Doc: in.Doc, Title: title, Total: total, Parts: parts}
			if len(parts) > 0 {
				if next := parts[len(parts)-1].Part + 1; next < total {
					out.Next = &next
				}
			}
			return nil, out, nil
		}))
}

// passage is a passage as an agent gets it: its text at most n runes, and how to cite it.
func passage(p knowledge.Passage, n int) kbPassage {
	out := kbPassage{From: p.From, Title: p.Title, Text: p.Text, Entities: p.Entities}
	switch p.From {
	case "doc":
		part := p.Part
		out.Doc, out.Part = p.Doc, &part
		out.Cite = fmt.Sprintf("%s (doc %s, part %d)", p.Title, p.Doc, p.Part)
	case "order":
		out.Cite = p.Title + " (production order)"
	default:
		out.Cite = p.Title + " (process)"
	}
	if r := []rune(p.Text); len(r) > n {
		out.Text, out.Cut = string(r[:n])+"…", true
	}
	return out
}

func names(ls []knowledge.Link) []kbName {
	out := make([]kbName, len(ls))
	for i, l := range ls {
		out[i] = kbName{Name: l.Name, Kind: l.Kind}
	}
	return out
}

func order(l knowledge.Link) int {
	if l.Kind == knowledge.TOrder {
		return 1
	}
	return 0
}
