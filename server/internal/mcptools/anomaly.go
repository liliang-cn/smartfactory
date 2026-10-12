package mcptools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/anomaly"
	"smartfactory/internal/auth"
)

// Hints answers for a company's readings unusual for their machine, as who may see them: the
// console's own operation (ops.Anomalies), so the checks are the same whichever way one asks.
type Hints func(who auth.Who, company string) (*anomaly.Report, error)

type hintsIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Asset   string `json:"asset,omitempty" jsonschema:"only this machine (asset id, e.g. HP-101); with it, also each field's normal per state"`
	Recent  bool   `json:"recent,omitempty" jsonschema:"also the hints of the last 30 days, ended ones included"`
}

type hintsOut struct {
	Current  []anomaly.Anomaly `json:"current"`
	Recent   []anomaly.Anomaly `json:"recent,omitempty"`
	Machines []anomaly.Machine `json:"machines,omitempty"`
	Note     string            `json:"note"`
}

// AddHints lets an agent ask which measured machines read unusually for themselves.
func AddHints(s *mcp.Server, hints Hints) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_anomalies", Description: "Readings from real equipment that are plausible but unusual for that machine: each machine's normal is learned per field (temp, power, vib …) and state (running, idle …), " +
		"and a hint is raised when readings stay K standard deviations off it (kind level) or their recent average drifts off it (kind drift). z is how far, in standard deviations; mean ± sigma is the normal in that state. " +
		"A hint is not an alarm: the machine runs on. Simulated machines have none."},
		guard(auth.Member, func(in hintsIn) string { return in.Company }, func(_ context.Context, req *mcp.CallToolRequest, in hintsIn) (*mcp.CallToolResult, hintsOut, error) {
			r, err := hints(who(req), in.Company)
			if err != nil {
				return nil, hintsOut{}, err
			}
			out := hintsOut{Current: []anomaly.Anomaly{}, Note: "No machine reads unusually now."}
			mine := func(a anomaly.Anomaly) bool { return in.Asset == "" || a.Asset == in.Asset }
			for _, a := range r.Current {
				if mine(a) {
					out.Current = append(out.Current, a)
				}
			}
			if len(out.Current) > 0 {
				out.Note = "Hints, not alarms: worth a look (a bearing, a sensor, a changed process), nothing was stopped."
			}
			if in.Recent {
				out.Recent = []anomaly.Anomaly{}
				for _, a := range r.Recent {
					if mine(a) {
						out.Recent = append(out.Recent, a)
					}
				}
			}
			if in.Asset != "" {
				for _, m := range r.Machines {
					if m.Asset == in.Asset {
						out.Machines = append(out.Machines, m)
					}
				}
			}
			return nil, out, nil
		}))
}
