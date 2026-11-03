package mcptools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
)

type planIn struct {
	Company string `json:"company" jsonschema:"company id"`
}

type rushIn struct {
	Company  string `json:"company" jsonschema:"company id"`
	Part     string `json:"part" jsonschema:"part number or product name, e.g. QZ-B20-4"`
	Qty      int    `json:"qty" jsonschema:"pieces"`
	Due      string `json:"due" jsonschema:"due date, 2026-11-11 or 2026-11-11 17:00 (China time; a date alone is 17:00)"`
	Customer string `json:"customer,omitempty" jsonschema:"customer (optional)"`
}

// PlanTools are the production plan's answers: the console's operations (package ops), which
// this package cannot import.
type PlanTools struct {
	Status func(who auth.Who, company string) (any, error)
	Rush   func(who auth.Who, company, part string, qty int, due, customer string) (any, error)
}

func asMap(v any, err error) (*mcp.CallToolResult, map[string]any, error) {
	if err != nil {
		return nil, nil, err
	}
	var out map[string]any
	b, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(b, &out)
	}
	return nil, out, err
}

// AddPlan adds plan_status and plan_whatif.
func AddPlan(s *mcp.Server, t PlanTools) {
	mcp.AddTool(s, &mcp.Tool{Name: "plan_status", Description: "The production plan (生产计划) in force, or the queue as it stands when none was published: on-time rate, hours late, changeovers, " +
		"each line's load and rate source, each order's line, start and end, lateness and why (capacity, material, die, due date too close), orders held back for missing material, " +
		"and what changed since the plan was made (orders added or withdrawn, a line stopped)."},
		guard(auth.Member, func(in planIn) string { return in.Company }, func(_ context.Context, req *mcp.CallToolRequest, in planIn) (*mcp.CallToolResult, map[string]any, error) {
			return asMap(t.Status(who(req), in.Company))
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "plan_whatif", Description: "What an urgent order would do to the production plan, worked out by the scheduler and not stored: " +
		"it goes where it costs least on the lines that make the part, the others keep their order; returns where it goes, when it would be done and whether on time, " +
		"the orders it pushes back (by how much, whether they become late, whose they are) and the plan's figures before and after."},
		guard(auth.Member, func(in rushIn) string { return in.Company }, func(_ context.Context, req *mcp.CallToolRequest, in rushIn) (*mcp.CallToolResult, map[string]any, error) {
			return asMap(t.Rush(who(req), in.Company, in.Part, in.Qty, in.Due, in.Customer))
		}))
}
