// Package mcptools exposes the twin to AI agents over MCP: plant status, alarms, assets,
// live protocol values, adapter health, and operator commands.
package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/tags"
)

// Snapshot mirrors what the front end reports (src/live/client.ts).
type snapshot struct {
	Company struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"company"`
	Plants []plant `json:"plants"`
}

type plant struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Hall   string           `json:"hall"`
	Unit   string           `json:"unit"`
	Kpis   map[string]any   `json:"kpis"`
	Lines  []map[string]any `json:"lines"`
	Alarms []alarm          `json:"alarms"`
	Assets []asset          `json:"assets"`
}

type alarm struct {
	Asset  string `json:"asset"`
	Text   string `json:"text"`
	Prio   int    `json:"prio"`
	At     string `json:"at"`
	Active bool   `json:"active"`
	Acked  bool   `json:"acked"`
}

type asset struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Line   string  `json:"line,omitempty"`
	State  string  `json:"state"`
	Fault  string  `json:"fault,omitempty"`
	Temp   float64 `json:"temp,omitempty"`
	Power  float64 `json:"power,omitempty"`
	Vib    float64 `json:"vib,omitempty"`
	Health float64 `json:"health,omitempty"`
	Util   float64 `json:"util,omitempty"`
	Speed  float64 `json:"speed,omitempty"`
	Live   string  `json:"live,omitempty"`
}

func load(hub *tags.Hub, company string) (*snapshot, time.Duration, error) {
	s, ok := hub.Snapshot(company)
	if !ok {
		return nil, 0, fmt.Errorf("no snapshot for %q yet: the simulation runs in the web app, open https://factory.superleo.cn/?company=%s once (live protocol values are available without it via read_tags)", company, company)
	}
	var snap snapshot
	if err := json.Unmarshal(s.Data, &snap); err != nil {
		return nil, 0, err
	}
	return &snap, time.Since(s.At).Round(time.Second), nil
}

func (s *snapshot) plant(id string) (*plant, error) {
	for i := range s.Plants {
		if s.Plants[i].ID == id {
			return &s.Plants[i], nil
		}
	}
	ids := make([]string, len(s.Plants))
	for i, p := range s.Plants {
		ids[i] = p.ID
	}
	return nil, fmt.Errorf("no plant %q in %s; plants: %v", id, s.Company.ID, ids)
}

// ---- inputs / outputs ----

type siteIn struct{}
type siteOut struct {
	Companies []siteCompany `json:"companies"`
}
type siteCompany struct {
	ID          string      `json:"id"`
	Name        string      `json:"name,omitempty"`
	SnapshotAge string      `json:"snapshotAge,omitempty"`
	Plants      []sitePlant `json:"plants,omitempty"`
	LiveTags    int         `json:"liveTags"`
}
type sitePlant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Hall string `json:"hall"`
}

type plantIn struct {
	Company string `json:"company" jsonschema:"company id, e.g. huaduan"`
	Plant   string `json:"plant" jsonschema:"plant id, e.g. p1"`
}
type plantOut struct {
	SnapshotAge string           `json:"snapshotAge"`
	Plant       string           `json:"plant"`
	Kpis        map[string]any   `json:"kpis"`
	Lines       []map[string]any `json:"lines"`
	ActiveAlarm int              `json:"activeAlarms"`
	Faulted     []asset          `json:"faulted"`
	LiveAssets  []asset          `json:"liveAssets"`
}

type alarmsIn struct {
	Company    string `json:"company" jsonschema:"company id"`
	Plant      string `json:"plant,omitempty" jsonschema:"plant id; omit for every plant"`
	ActiveOnly bool   `json:"activeOnly,omitempty" jsonschema:"only alarms that are still active"`
}
type alarmsOut struct {
	SnapshotAge string           `json:"snapshotAge"`
	Alarms      []alarmWithPlant `json:"alarms"`
}
type alarmWithPlant struct {
	Plant string `json:"plant"`
	alarm
}

type assetIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Plant   string `json:"plant" jsonschema:"plant id"`
	Asset   string `json:"asset" jsonschema:"asset id, e.g. HP-101"`
}
type assetOut struct {
	SnapshotAge string     `json:"snapshotAge,omitempty"`
	Asset       *asset     `json:"asset,omitempty"`
	Alarms      []alarm    `json:"alarms,omitempty"`
	LiveTags    []tags.Tag `json:"liveTags"`
}

type tagsIn struct {
	Company string `json:"company,omitempty" jsonschema:"filter by company id"`
	Plant   string `json:"plant,omitempty" jsonschema:"filter by plant id"`
	Asset   string `json:"asset,omitempty" jsonschema:"filter by asset id"`
}
type tagsOut struct {
	Tags []tagAge `json:"tags"`
}
type tagAge struct {
	tags.Tag
	Age string `json:"age"`
}

type sourcesOut struct {
	Sources []tags.SourceStatus `json:"sources"`
}

type cmdIn struct {
	Company string   `json:"company" jsonschema:"company id"`
	Plant   string   `json:"plant" jsonschema:"plant id"`
	Asset   string   `json:"asset" jsonschema:"asset id"`
	Command string   `json:"command" jsonschema:"one of start, stop, reset, maint, speed, fault, restock, ship"`
	Arg     *float64 `json:"arg,omitempty" jsonschema:"for speed: 0.5 to 1.5 (fraction of nominal)"`
}
type cmdOut struct {
	Sent string `json:"sent"`
}

var commands = []string{"start", "stop", "reset", "maint", "speed", "fault", "restock", "ship"}

// New builds the MCP server.
func New(hub *tags.Hub, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "smartfactory", Title: "SmartFactory digital twin", Version: version}, &mcp.ServerOptions{
		Instructions: "SmartFactory is a 3D digital twin of factories. Companies contain plants, plants contain production lines of machines (assets). " +
			"Use list_sites first. KPIs, alarms and simulated machine state come from the running twin (snapshotAge says how fresh). " +
			"Values read over MQTT / Modbus TCP / OPC UA from real equipment are in read_tags and are marked 'live' on assets. " +
			"send_command acts on the twin and is forwarded to devices through the protocol adapters.",
	})

	mcp.AddTool(s, &mcp.Tool{Name: "list_sites", Description: "List companies and their plants known to the twin, with how fresh each company's snapshot is."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ siteIn) (*mcp.CallToolResult, siteOut, error) {
			var out siteOut
			for _, c := range hub.Companies() {
				sc := siteCompany{ID: c, LiveTags: len(hub.Last(c, "", ""))}
				if snap, age, err := load(hub, c); err == nil {
					sc.Name, sc.SnapshotAge = snap.Company.Name, age.String()
					for _, p := range snap.Plants {
						sc.Plants = append(sc.Plants, sitePlant{p.ID, p.Name, p.Hall})
					}
				}
				out.Companies = append(out.Companies, sc)
			}
			return nil, out, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "plant_status", Description: "KPIs (OEE, throughput, quality), per-line figures, active alarm count, faulted machines and machines fed by live protocol data for one plant."},
		func(_ context.Context, _ *mcp.CallToolRequest, in plantIn) (*mcp.CallToolResult, plantOut, error) {
			snap, age, err := load(hub, in.Company)
			if err != nil {
				return nil, plantOut{}, err
			}
			p, err := snap.plant(in.Plant)
			if err != nil {
				return nil, plantOut{}, err
			}
			out := plantOut{SnapshotAge: age.String(), Plant: p.Name + " " + p.Hall, Kpis: p.Kpis, Lines: p.Lines, Faulted: []asset{}, LiveAssets: []asset{}}
			for _, a := range p.Alarms {
				if a.Active {
					out.ActiveAlarm++
				}
			}
			for _, a := range p.Assets {
				if a.State == "fault" {
					out.Faulted = append(out.Faulted, a)
				}
				if a.Live != "" {
					out.LiveAssets = append(out.LiveAssets, a)
				}
			}
			return nil, out, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_alarms", Description: "Alarms of a company, newest first, optionally for one plant and only active ones."},
		func(_ context.Context, _ *mcp.CallToolRequest, in alarmsIn) (*mcp.CallToolResult, alarmsOut, error) {
			snap, age, err := load(hub, in.Company)
			if err != nil {
				return nil, alarmsOut{}, err
			}
			out := alarmsOut{SnapshotAge: age.String(), Alarms: []alarmWithPlant{}}
			for _, p := range snap.Plants {
				if in.Plant != "" && p.ID != in.Plant {
					continue
				}
				for _, a := range p.Alarms {
					if in.ActiveOnly && !a.Active {
						continue
					}
					out.Alarms = append(out.Alarms, alarmWithPlant{p.ID, a})
				}
			}
			return nil, out, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_asset", Description: "One machine: state, temperature, power, vibration, health, utilisation, fault text, its alarms, and any live protocol values bound to it."},
		func(_ context.Context, _ *mcp.CallToolRequest, in assetIn) (*mcp.CallToolResult, assetOut, error) {
			out := assetOut{LiveTags: hub.Last(in.Company, in.Plant, in.Asset)}
			if snap, age, err := load(hub, in.Company); err == nil {
				out.SnapshotAge = age.String()
				if p, err := snap.plant(in.Plant); err == nil {
					for i := range p.Assets {
						if p.Assets[i].ID == in.Asset {
							out.Asset = &p.Assets[i]
						}
					}
					for _, a := range p.Alarms {
						if a.Asset == in.Asset {
							out.Alarms = append(out.Alarms, a)
						}
					}
				}
			}
			if out.Asset == nil && len(out.LiveTags) == 0 {
				return nil, out, fmt.Errorf("no asset %s/%s/%s", in.Company, in.Plant, in.Asset)
			}
			return nil, out, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "read_tags", Description: "Current values read from equipment over MQTT, Modbus TCP and OPC UA, with their source adapter and age."},
		func(_ context.Context, _ *mcp.CallToolRequest, in tagsIn) (*mcp.CallToolResult, tagsOut, error) {
			out := tagsOut{Tags: []tagAge{}}
			for _, t := range hub.Last(in.Company, in.Plant, in.Asset) {
				out.Tags = append(out.Tags, tagAge{t, time.Since(t.At).Round(100 * time.Millisecond).String()})
			}
			return nil, out, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_sources", Description: "Protocol adapters (embedded MQTT broker, external MQTT, Modbus TCP, OPC UA): connection state, errors, message counts."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ siteIn) (*mcp.CallToolResult, sourcesOut, error) {
			return nil, sourcesOut{Sources: hub.Sources()}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "send_command", Description: "Operate a machine: start, stop, reset a fault, toggle maintenance, set speed (arg 0.5–1.5), inject a fault drill, restock the raw store, ship finished goods. Applies to the twin in every open browser and is forwarded to devices through the protocol adapters."},
		func(_ context.Context, _ *mcp.CallToolRequest, in cmdIn) (*mcp.CallToolResult, cmdOut, error) {
			if !slices.Contains(commands, in.Command) {
				return nil, cmdOut{}, fmt.Errorf("unknown command %q; use one of %v", in.Command, commands)
			}
			if in.Company == "" || in.Plant == "" || in.Asset == "" {
				return nil, cmdOut{}, fmt.Errorf("company, plant and asset are required")
			}
			hub.Command(tags.Command{Company: in.Company, Plant: in.Plant, Asset: in.Asset, Cmd: in.Command, Arg: in.Arg, By: "mcp"})
			return nil, cmdOut{Sent: fmt.Sprintf("%s → %s/%s/%s", in.Command, in.Company, in.Plant, in.Asset)}, nil
		})

	return s
}
