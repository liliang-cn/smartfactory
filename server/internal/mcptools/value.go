package mcptools

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
)

type valueIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Days    int    `json:"days,omitempty" jsonschema:"days back from now (default 30; 7, 30 and 90 are the usual ones)"`
	From    string `json:"from,omitempty" jsonschema:"first day, YYYY-MM-DD (with to, instead of days)"`
	To      string `json:"to,omitempty" jsonschema:"last day, YYYY-MM-DD, included"`
}

// AddValueTool lets an assistant answer "what has this system done for us": report is the
// console's value report (package ops), which this package cannot import.
func AddValueTool(s *mcp.Server, report func(ctx context.Context, who auth.Who, company, days, from, to string) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: "value_report", Description: "What the twin and its AI delivered for a company over a period (这个系统给我们带来了什么): " +
		"the advisor's plans proposed, adopted, rejected, the adoption rate and time to decide, and for adopted plans the measured change against their baseline and their acceptance; " +
		"the console agent's proposals confirmed or dismissed; AI commands to machines sent, held for approval, approved, denied, refused by the rules, expired; " +
		"calibration (machines calibrated, replay error template → fitted); orders received, finished, on time; and the plant's OEE, output and downtime by week, " +
		"with before vs after the first adopted plan. Every figure has n (records it was counted from) and basis (what they are); value null means there was no data — say so, do not estimate. " +
		"Plant figures say whether they are measured from equipment or simulated by the twin: simulated ones are not the real plant. Before vs after is correlation, not proof that the plan caused it."},
		guard(auth.Member, func(in valueIn) string { return in.Company }, func(ctx context.Context, req *mcp.CallToolRequest, in valueIn) (*mcp.CallToolResult, map[string]any, error) {
			days := ""
			if in.Days > 0 {
				days = strconv.Itoa(in.Days)
			}
			v, err := report(ctx, who(req), in.Company, days, in.From, in.To)
			if err != nil {
				return nil, nil, err
			}
			var out map[string]any
			b, err := json.Marshal(v)
			if err == nil {
				err = json.Unmarshal(b, &out)
			}
			return nil, out, err
		}))
}
