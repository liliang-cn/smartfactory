package mcptools

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
	"smartfactory/internal/mapping"
	"smartfactory/internal/series"
)

type trendIn struct {
	Company string   `json:"company" jsonschema:"company id"`
	Plant   string   `json:"plant,omitempty" jsonschema:"plant id (needed only when the asset id is in more than one plant; without an asset: rank this plant's machines only)"`
	Asset   string   `json:"asset,omitempty" jsonschema:"asset id, e.g. HP-101; empty: rank the machines by how long they stood still"`
	Fields  []string `json:"fields,omitempty" jsonschema:"readings to sum up (temp, power, vib, speed, health, toolLife …); empty: the usual ones"`
	Hours   float64  `json:"hours,omitempty" jsonschema:"the last this many hours (default 8, at most 2160)"`
	From    string   `json:"from,omitempty" jsonschema:"start, RFC 3339 or epoch milliseconds (instead of hours)"`
	To      string   `json:"to,omitempty" jsonschema:"end, RFC 3339 or epoch milliseconds (default: now)"`
}

type trendOut struct {
	Trend    *series.Trend         `json:"trend,omitempty"`
	Machines []series.MachineStops `json:"machines,omitempty"`
	Note     string                `json:"note"`
}

// AddTrend lets an agent ask what the machines did over a span, from the readings the equipment
// sent: the same summing up as the in-app assistant's machine_trend.
func AddTrend(s *mcp.Server, rd *series.Store) {
	mcp.AddTool(s, &mcp.Tool{Name: "machine_trend", Description: "What machines did over a span, from the readings real (or emulated) equipment sent — simulated-only machines keep no history. " +
		"With an asset: each reading's low and high (with the minute), mean, first and last, and a coarse curve of [time, mean, low, high]; the minutes in each state; " +
		"the longest stops (fault, stopped, maint, changeover) with from, to and the fault text that stopped it; stops summed by reason. " +
		"Without an asset: the company's machines ranked by stop minutes over the span (idle and blocked wait for the line and are not stops). Use plant_status / get_asset for now, this for the past."},
		guard(auth.Member, func(in trendIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in trendIn) (*mcp.CallToolResult, trendOut, error) {
			from, to, err := trendSpan(in)
			if err != nil {
				return nil, trendOut{}, err
			}
			kept, err := rd.Machines(in.Company)
			if err != nil {
				return nil, trendOut{}, err
			}
			if len(kept) == 0 {
				return nil, trendOut{Note: "This company has no equipment data: its twin is simulated only, and the simulation keeps no history. Use plant_status for now."}, nil
			}
			if in.Asset == "" {
				rank, err := rd.Downtime(in.Company, in.Plant, from, to, time.UTC)
				if err != nil {
					return nil, trendOut{}, err
				}
				return nil, trendOut{Machines: rank[:min(len(rank), 25)], Note: fmt.Sprintf("%d machines with data, ranked by stop minutes (fault, stopped, maint, changeover).", len(rank))}, nil
			}
			var hits [][2]string
			for _, m := range kept {
				if strings.EqualFold(m[1], in.Asset) && (in.Plant == "" || m[0] == in.Plant) {
					hits = append(hits, m)
				}
			}
			switch {
			case len(hits) == 0:
				return nil, trendOut{}, fmt.Errorf("no readings are kept for %s in %s (a simulated-only machine keeps none)", in.Asset, in.Company)
			case len(hits) > 1:
				var ps []string
				for _, h := range hits {
					ps = append(ps, h[0])
				}
				return nil, trendOut{}, fmt.Errorf("%s is in plants %s: give plant", in.Asset, strings.Join(ps, ", "))
			}
			fields := in.Fields
			if len(fields) == 0 {
				have, err := rd.Fields(in.Company, hits[0][0], hits[0][1])
				if err != nil {
					return nil, trendOut{}, err
				}
				for _, f := range []string{"temp", "power", "vib", "speed", "health", "toolLife", "measure"} {
					if slices.Contains(have, f) {
						fields = append(fields, f)
					}
				}
			}
			tr, err := rd.Trend(series.TrendQuery{Company: in.Company, Plant: hits[0][0], Asset: hits[0][1], Fields: fields, From: from, To: to, Loc: time.UTC})
			if err != nil {
				return nil, trendOut{}, err
			}
			note := "temp is the machine body's own temperature, not the work's. Flagged readings were not believed and are left out."
			if tr.Note != "" {
				note = tr.Note + "; " + note
			}
			return nil, trendOut{Trend: tr, Note: note}, nil
		}))
}

func trendSpan(in trendIn) (time.Time, time.Time, error) {
	to := time.Now()
	if in.To != "" {
		t, ok := mapping.ParseTime(in.To)
		if !ok {
			return time.Time{}, time.Time{}, fmt.Errorf("to must be RFC 3339 or epoch milliseconds")
		}
		to = t
	}
	if in.From != "" {
		t, ok := mapping.ParseTime(in.From)
		if !ok {
			return time.Time{}, time.Time{}, fmt.Errorf("from must be RFC 3339 or epoch milliseconds")
		}
		return t, to, nil
	}
	h := in.Hours
	if h <= 0 {
		h = 8
	}
	h = min(h, series.MaxTrendSpan.Hours())
	return to.Add(-time.Duration(h * float64(time.Hour))), to, nil
}
