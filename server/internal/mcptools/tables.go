package mcptools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
	"smartfactory/internal/tables"
)

type tablesFreeIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Plant   string `json:"plant,omitempty" jsonschema:"only this shop (plant id)"`
	Seats   int    `json:"seats,omitempty" jsonschema:"only tables seating at least this many"`
}
type tableBrief struct {
	Plant   string  `json:"plant"`
	Shop    string  `json:"shop"`
	Table   string  `json:"table"`
	Area    string  `json:"area"`
	Seats   int     `json:"seats"`
	State   string  `json:"state"`
	People  int     `json:"people,omitempty"`
	Minutes float64 `json:"minutes"`
	// Source: sim for the twin's guess, else what reported it
	Source   string `json:"source"`
	Reported bool   `json:"reported"`
}
type tablesFreeOut struct {
	Free    []tableBrief `json:"free"`
	Taken   int          `json:"taken"`
	Note    string       `json:"note"`
	Measure string       `json:"source"`
}

type tablesKpiIn struct {
	Company string `json:"company" jsonschema:"company id"`
}
type tablesKpiOut struct {
	Total  tables.KPIs   `json:"total"`
	Plants []tables.KPIs `json:"plants"`
	Note   string        `json:"note"`
}

type tableNowIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Table   string `json:"table" jsonschema:"table id, e.g. A03"`
	Plant   string `json:"plant,omitempty" jsonschema:"the shop (plant id), when several have this table"`
}

type tableHistoryIn struct {
	Company string `json:"company" jsonschema:"company id"`
	From    string `json:"from,omitempty" jsonschema:"first day YYYY-MM-DD (default 7 days ago)"`
	To      string `json:"to,omitempty" jsonschema:"last day YYYY-MM-DD, included (default today)"`
	By      string `json:"by,omitempty" jsonschema:"hour or day (default day)"`
}
type tableHistoryOut struct {
	Buckets []tables.Bucket `json:"buckets"`
}

const sourceNote = "source: measured = every table is reported by a camera box, POS, sensor or a waiter; simulated = the twin's own guess (not the real shop); mixed = some of each (measuredShare is the share of tables reported)."

// AddTables gives MCP clients a restaurant's tables: which are free now, today's dining figures
// (上座率, 座位利用率, 翻台率, 平均用餐时长), how long a table has been taken, and the history.
func AddTables(s *mcp.Server, svc *tables.Service) {
	mcp.AddTool(s, &mcp.Tool{Name: "tables_free", Description: "Which tables of a restaurant are free now (现在哪些桌空着), by shop and area with their seats; how many are taken. Each says whether a report (camera, POS, waiter) or the twin's simulation has it. " + sourceNote},
		guard(auth.Visitor, func(in tablesFreeIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in tablesFreeIn) (*mcp.CallToolResult, tablesFreeOut, error) {
			ov, err := svc.Overview(in.Company)
			if err != nil {
				return nil, tablesFreeOut{}, err
			}
			out := tablesFreeOut{Free: []tableBrief{}, Note: sourceNote, Measure: ov.Total.Source}
			for _, t := range ov.Tables {
				if in.Plant != "" && t.Plant != in.Plant {
					continue
				}
				if t.State != tables.Free {
					out.Taken++
					continue
				}
				if in.Seats > 0 && t.Seats < in.Seats {
					continue
				}
				out.Free = append(out.Free, brief(t))
			}
			return nil, out, nil
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "tables_kpis", Description: "A restaurant's dining figures today (business day from 05:00) per shop and in all: 上座率 occupancy (occupied tables / tables, now), 座位利用率 seat use (people / seats, now), 翻台率 turnover (sessions per table today), 平均用餐时长 average minutes of the sessions that ended today, guests, sessions, tables free / reserved / cleaning. " + sourceNote},
		guard(auth.Visitor, func(in tablesKpiIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in tablesKpiIn) (*mcp.CallToolResult, tablesKpiOut, error) {
			ov, err := svc.Overview(in.Company)
			if err != nil {
				return nil, tablesKpiOut{}, err
			}
			return nil, tablesKpiOut{Total: ov.Total, Plants: ov.Plants, Note: sourceNote}, nil
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "table_now", Description: "One table (A03 坐了多久): its state now, how many people, minutes in that state, the session in progress (since when, minutes so far) and today's sessions at it, with who reported each (sim = the twin's guess)."},
		guard(auth.Visitor, func(in tableNowIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in tableNowIn) (*mcp.CallToolResult, *tables.Current, error) {
			c, err := svc.Table(in.Company, in.Plant, in.Table)
			return nil, c, err
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "tables_history", Description: "A restaurant's dining sessions by day or hour over a range, per shop: sessions started, guests, average minutes, table-hours occupied, and how many were reported (the rest simulated)."},
		guard(auth.Visitor, func(in tableHistoryIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in tableHistoryIn) (*mcp.CallToolResult, tableHistoryOut, error) {
			from, to, err := tables.Range(svc.Now(), in.From, in.To)
			if err != nil {
				return nil, tableHistoryOut{}, err
			}
			b, err := svc.History(in.Company, from, to, in.By != "hour")
			return nil, tableHistoryOut{Buckets: b}, err
		}))
}

func brief(t tables.TableNow) tableBrief {
	return tableBrief{Plant: t.Plant, Shop: t.PlantName, Table: t.ID, Area: t.Area, Seats: t.Seats, State: t.State, People: t.People, Minutes: t.Minutes, Source: t.Source, Reported: t.Reported}
}
