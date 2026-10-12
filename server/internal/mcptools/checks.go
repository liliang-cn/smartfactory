package mcptools

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
	"smartfactory/internal/checks"
)

type checkFormsIn struct {
	Company string `json:"company" jsonschema:"company id"`
}
type checkFormsOut struct {
	Forms []checks.Form `json:"forms"`
}

type checkRoundsIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Status  string `json:"status,omitempty" jsonschema:"overdue (default), due, done, open (no schedule) or all"`
	Asset   string `json:"asset,omitempty" jsonschema:"only this machine's rounds (asset id)"`
}
type checkRoundsOut struct {
	Rounds []checks.Round `json:"rounds"`
}

type checkResultsIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Form    string `json:"form,omitempty" jsonschema:"only this form (its id, F-…)"`
	Asset   string `json:"asset,omitempty" jsonschema:"only this machine (asset id)"`
	Hours   int    `json:"hours,omitempty" jsonschema:"how far back, in hours (default 24, up to 720)"`
	Bad     bool   `json:"bad,omitempty" jsonschema:"only submissions with a value out of spec or an abnormal answer"`
	Limit   int    `json:"limit,omitempty" jsonschema:"at most this many (default 30, up to 200)"`
}
type checkResultsOut struct {
	Submissions []checks.Submission `json:"submissions"`
}

// AddChecks gives MCP clients the forms people fill in by hand at the machines: the forms, the
// rounds that are due or overdue, and what was written.
func AddChecks(s *mcp.Server, svc *checks.Service) {
	company := func(in checkFormsIn) string { return in.Company }
	mcp.AddTool(s, &mcp.Tool{Name: "check_forms", Description: "The forms people fill in by hand at the machines (patrol rounds 点检, first-article checks 首件检验, lab results 化验): what each asks, with units and spec limits, which plant, lines or machines it is for, and how often a round is due."},
		guard(auth.Member, company, func(_ context.Context, _ *mcp.CallToolRequest, in checkFormsIn) (*mcp.CallToolResult, checkFormsOut, error) {
			fs, err := svc.Forms(in.Company)
			return nil, checkFormsOut{Forms: fs}, err
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "check_rounds", Description: "Rounds of those forms on each machine: overdue (past its time, or the last round was missed, with since when), due (with the time it must be done by), done, or open (no schedule); with when and by whom each was last filled in. Overdue first, longest overdue first."},
		guard(auth.Member, func(in checkRoundsIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in checkRoundsIn) (*mcp.CallToolResult, checkRoundsOut, error) {
			var status []string
			switch in.Status {
			case "", "overdue":
				status = []string{"overdue"}
			case "all":
			case "due", "done", "open":
				status = []string{in.Status}
			default:
				return nil, checkRoundsOut{}, fmt.Errorf("status is overdue, due, done, open or all")
			}
			rs, err := svc.Rounds(in.Company, status...)
			if in.Asset != "" {
				rs = slices.DeleteFunc(rs, func(r checks.Round) bool { return r.Asset != in.Asset })
			}
			return nil, checkRoundsOut{Rounds: rs}, err
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "check_results", Description: "What people wrote on those forms, the latest first: each value with its unit and spec, whether it was out of spec or abnormal (bad, why), who wrote it, when it was taken (taken) and received; late ones were sent from a phone that was offline. Numbers are also stored as readings with source manual."},
		guard(auth.Member, func(in checkResultsIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in checkResultsIn) (*mcp.CallToolResult, checkResultsOut, error) {
			hours := in.Hours
			if hours <= 0 {
				hours = 24
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 30
			}
			list, err := svc.Submissions(in.Company, checks.Filter{Form: in.Form, Asset: in.Asset, Bad: in.Bad, Limit: min(limit, 200),
				From: svc.Now().Add(-time.Duration(min(hours, 720)) * time.Hour)})
			return nil, checkResultsOut{Submissions: list}, err
		}))
}
