package mcptools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
)

type bomQueryIn struct {
	Company string  `json:"company" jsonschema:"company id"`
	Part    string  `json:"part,omitempty" jsonschema:"part number or part of its name, e.g. QZ-B20-4 or 曲轴; omit to list the company's part numbers with how their BOMs stand"`
	Rev     string  `json:"rev,omitempty" jsonschema:"revision to explode, e.g. A (default: the one in effect)"`
	Qty     float64 `json:"qty,omitempty" jsonschema:"how many to make; the BOM is exploded for this many and set against the stores (default 1)"`
}

type whereUsedIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Query   string `json:"query" jsonschema:"a material's code, name, grade or spec, e.g. 40Cr, M10, 油道螺塞"`
}

type kittingIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Order   string `json:"order,omitempty" jsonschema:"one open order's number; omit for every running or queued order"`
}

type ecoIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Part    string `json:"part,omitempty" jsonschema:"only this part number's changes"`
	Limit   int    `json:"limit,omitempty" jsonschema:"how many, newest first (default 100)"`
}

// BOMTools are the BOM's answers: the console's operations (package ops), which this package
// cannot import. All are read-only.
type BOMTools struct {
	Query     func(who auth.Who, company, part, rev string, qty float64) (any, error)
	WhereUsed func(who auth.Who, company, query string) (any, error)
	Kitting   func(who auth.Who, company, order string) (any, error)
	ECOs      func(who auth.Who, company, part string, limit int) (any, error)
}

// AddBOM adds bom_query, bom_where_used, bom_kitting and bom_ecos.
func AddBOM(s *mcp.Server, t BOMTools) {
	mcp.AddTool(s, &mcp.Tool{Name: "bom_query", Description: "A part's bill of materials (BOM, 物料清单): its revisions (status, drawing, effective date, who approved) and the revision in effect " +
		"(or the one asked for) exploded level by level down to what is bought or cut from bar, with how much of each goes into one part (scrap included) and, for qty parts, how much in all, " +
		"what the stores hold and what is short; a sub-assembly (a line whose code has a BOM of its own) opens into that BOM. Without part, lists the company's part numbers and how their BOMs stand; " +
		"a name that matches several parts lists them. Read-only."},
		guard(auth.Member, func(in bomQueryIn) string { return in.Company }, func(_ context.Context, req *mcp.CallToolRequest, in bomQueryIn) (*mcp.CallToolResult, map[string]any, error) {
			return asMap(t.Query(who(req), in.Company, in.Part, in.Rev, in.Qty))
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "bom_where_used", Description: "Where a material goes (反查): every part whose BOM in effect uses a component matching the query " +
		"(code, name, grade or spec, e.g. 40Cr, M10), at any depth, with how much goes into one part, the sub-assembly it goes in through and the operation. Read-only."},
		guard(auth.Member, func(in whereUsedIn) string { return in.Company }, func(_ context.Context, req *mcp.CallToolRequest, in whereUsedIn) (*mcp.CallToolResult, map[string]any, error) {
			return asMap(t.WhereUsed(who(req), in.Company, in.Query))
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "bom_kitting", Description: "Whether the open orders (running and queued) have their materials (齐套, 缺料): each order is exploded by its part's BOM in effect and, " +
		"in the order the lines run them, takes what the stores hold before the next one does; returns per order whether it is complete and what it lacks, " +
		"and the items short in all with the orders that go without. With order, only that order. Orders whose part has no BOM are listed without needs. Read-only."},
		guard(auth.Member, func(in kittingIn) string { return in.Company }, func(_ context.Context, req *mcp.CallToolRequest, in kittingIn) (*mcp.CallToolResult, map[string]any, error) {
			return asMap(t.Kitting(who(req), in.Company, in.Order))
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "bom_ecos", Description: "Engineering change records (ECO) of a company's BOMs, newest first: part, revision from and to, drawing revisions, " +
		"what changed line by line, why, and who approved it. Read-only."},
		guard(auth.Member, func(in ecoIn) string { return in.Company }, func(_ context.Context, req *mcp.CallToolRequest, in ecoIn) (*mcp.CallToolResult, map[string]any, error) {
			v, err := t.ECOs(who(req), in.Company, in.Part, in.Limit)
			if err != nil {
				return nil, nil, err
			}
			return asMap(map[string]any{"ecos": v}, nil)
		}))
}
