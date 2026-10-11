// Package mcptools exposes the twin to AI agents over MCP: plant status, alarms, assets,
// live protocol values, adapter health, and operator commands.
package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// Snapshot mirrors what the front end reports (web/src/live/client.ts).
type snapshot struct {
	Company struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"company"`
	Plants []plant `json:"plants"`
}

type plant struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Hall      string           `json:"hall"`
	Unit      string           `json:"unit"`
	Kpis      map[string]any   `json:"kpis"`
	Lines     []map[string]any `json:"lines"`
	Alarms    []alarm          `json:"alarms"`
	Assets    []asset          `json:"assets"`
	Inventory []map[string]any `json:"inventory"`
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
	Tool   string  `json:"tool,omitempty"`
}

func load(eng *engine.Engine, company string) (*snapshot, error) {
	raw := eng.Snapshot(company)
	if raw == "null" {
		return nil, fmt.Errorf("no company %q; see list_sites", company)
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, err
	}
	return &snap, nil
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
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Plants   []sitePlant `json:"plants"`
	LiveTags int         `json:"liveTags"`
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
	Plant       string           `json:"plant"`
	Kpis        map[string]any   `json:"kpis"`
	Lines       []map[string]any `json:"lines"`
	ActiveAlarm int              `json:"activeAlarms"`
	Faulted     []asset          `json:"faulted"`
	LiveAssets  []asset          `json:"liveAssets"`
	Inventory   []map[string]any `json:"inventory"`
}

type alarmsIn struct {
	Company    string `json:"company" jsonschema:"company id"`
	Plant      string `json:"plant,omitempty" jsonschema:"plant id; omit for every plant"`
	ActiveOnly bool   `json:"activeOnly,omitempty" jsonschema:"only alarms that are still active"`
}
type alarmsOut struct {
	Alarms []alarmWithPlant `json:"alarms"`
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
	Asset    *asset     `json:"asset,omitempty"`
	Alarms   []alarm    `json:"alarms,omitempty"`
	LiveTags []tags.Tag `json:"liveTags"`
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
	Command string   `json:"command" jsonschema:"one of start, stop, reset, maint, speed, fault, restock, ship, retool (change a worn die / cutter now), toollimit (planned die / tool change point)"`
	Arg     *float64 `json:"arg,omitempty" jsonschema:"for speed: 0.5 to 1.5 (fraction of nominal); for toollimit: 0.3 to 1 (share of the tool life at which it is changed, e.g. 0.85)"`
}
type cmdOut struct {
	Sent string `json:"sent"`
}

var commands = []string{"start", "stop", "reset", "maint", "speed", "fault", "restock", "ship", "retool", "toollimit"}

// New builds the MCP server.
func New(hub *tags.Hub, eng *engine.Engine, version string, log *audit.Log) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "smartfactory", Title: "SmartFactory digital twin", Version: version}, &mcp.ServerOptions{
		Instructions: "SmartFactory is a 3D digital twin platform for factories and other sites (venues, campsites …). Companies contain plants, plants contain lines of machines (assets). New scenes can be created with scene_guide → create_industry → create_company. " +
			"Use list_sites first. The simulation runs on the server continuously; KPIs, alarms and machine state are current. " +
			"Values read over MQTT / Modbus TCP / OPC UA from real equipment are in read_tags and are marked 'live' on assets. " +
			"send_command acts on the twin (every viewer sees it) and is forwarded to devices through the protocol adapters.",
	})

	mcp.AddTool(s, &mcp.Tool{Name: "list_sites", Description: "List companies and their plants in the twin, with how many live protocol values each has."},
		guard(auth.Member, nil, func(_ context.Context, req *mcp.CallToolRequest, _ siteIn) (*mcp.CallToolResult, siteOut, error) {
			w := who(req)
			var list []struct {
				ID     string      `json:"id"`
				Name   string      `json:"name"`
				Plants []sitePlant `json:"plants"`
			}
			if err := json.Unmarshal([]byte(eng.List()), &list); err != nil {
				return nil, siteOut{}, err
			}
			out := siteOut{Companies: []siteCompany{}}
			for _, c := range list {
				if w.In(c.ID) {
					out.Companies = append(out.Companies, siteCompany{ID: c.ID, Name: c.Name, Plants: c.Plants, LiveTags: len(hub.Last(c.ID, "", ""))})
				}
			}
			return nil, out, nil
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "plant_status", Description: "KPIs (OEE, throughput, quality), per-line figures, inventory (in use / available / capacity), active alarm count, faulted machines and machines fed by live protocol data for one plant."},
		guard(auth.Member, func(in plantIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in plantIn) (*mcp.CallToolResult, plantOut, error) {
			snap, err := load(eng, in.Company)
			if err != nil {
				return nil, plantOut{}, err
			}
			p, err := snap.plant(in.Plant)
			if err != nil {
				return nil, plantOut{}, err
			}
			out := plantOut{Plant: p.Name + " " + p.Hall, Kpis: p.Kpis, Lines: p.Lines, Faulted: []asset{}, LiveAssets: []asset{}, Inventory: p.Inventory}
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
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "list_alarms", Description: "Alarms of a company, newest first, optionally for one plant and only active ones."},
		guard(auth.Member, func(in alarmsIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in alarmsIn) (*mcp.CallToolResult, alarmsOut, error) {
			snap, err := load(eng, in.Company)
			if err != nil {
				return nil, alarmsOut{}, err
			}
			out := alarmsOut{Alarms: []alarmWithPlant{}}
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
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "get_asset", Description: "One machine: state, temperature, power, vibration, health, utilisation, fault text, its alarms, and any live protocol values bound to it."},
		guard(auth.Member, func(in assetIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in assetIn) (*mcp.CallToolResult, assetOut, error) {
			out := assetOut{LiveTags: hub.Last(in.Company, in.Plant, in.Asset)}
			if snap, err := load(eng, in.Company); err == nil {
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
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "read_tags", Description: "Current values read from equipment over MQTT, Modbus TCP and OPC UA, with their source adapter and age."},
		guard(auth.Member, func(in tagsIn) string { return in.Company }, func(_ context.Context, _ *mcp.CallToolRequest, in tagsIn) (*mcp.CallToolResult, tagsOut, error) {
			out := tagsOut{Tags: []tagAge{}}
			for _, t := range hub.Last(in.Company, in.Plant, in.Asset) {
				out.Tags = append(out.Tags, tagAge{t, time.Since(t.At).Round(100 * time.Millisecond).String()})
			}
			return nil, out, nil
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "list_sources", Description: "Protocol adapters (embedded MQTT broker, external MQTT, Modbus TCP, OPC UA): connection state, errors, message counts."},
		guard(auth.Member, nil, func(_ context.Context, req *mcp.CallToolRequest, _ siteIn) (*mcp.CallToolResult, sourcesOut, error) {
			return nil, sourcesOut{Sources: visibleSources(who(req), hub.Sources())}, nil
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "send_command", Description: "Operate a machine: start, stop, reset a fault, toggle maintenance, set speed (arg 0.5–1.5), inject a fault drill, restock the raw store, ship finished goods, change a die now (retool) or set the planned die-change point (toollimit, arg 0.3–1 of the die life). Applies to the server-side twin that every viewer sees, and is forwarded to devices through the protocol adapters."},
		guard(auth.Operator, func(in cmdIn) string { return in.Company }, func(_ context.Context, req *mcp.CallToolRequest, in cmdIn) (*mcp.CallToolResult, cmdOut, error) {
			if !slices.Contains(commands, in.Command) {
				return nil, cmdOut{}, fmt.Errorf("unknown command %q; use one of %v", in.Command, commands)
			}
			if in.Company == "" || in.Plant == "" || in.Asset == "" {
				return nil, cmdOut{}, fmt.Errorf("company, plant and asset are required")
			}
			w := who(req)
			r := eng.Command(in.Company, in.Plant, in.Asset, in.Command, in.Arg)
			var err error
			if !r.OK {
				err = errors.New(r.Msg)
			}
			log.Record(w, "mcp", in.Company, "command", in.Plant+"/"+in.Asset, commandText(in.Command, in.Arg), err)
			if err != nil {
				return nil, cmdOut{}, err
			}
			hub.Command(tags.Command{Company: in.Company, Plant: in.Plant, Asset: in.Asset, Cmd: in.Command, Arg: in.Arg, By: "mcp", Name: w.Name})
			return nil, cmdOut{Sent: r.Msg}, nil
		}))

	addSceneTools(s, eng, log)
	addSimTool(s, eng)
	return s
}

type guideIn struct {
	Schema bool `json:"schema,omitempty" jsonschema:"also return the full JSON Schemas of both configs"`
}
type guideOut struct {
	Guide          string          `json:"guide"`
	IndustrySchema json.RawMessage `json:"industrySchema,omitempty"`
	CompanySchema  json.RawMessage `json:"companySchema,omitempty"`
}
type idIn struct {
	ID string `json:"id" jsonschema:"id"`
}
type configIn struct {
	Config map[string]any `json:"config" jsonschema:"the whole config as a JSON object"`
}

// addSceneTools lets an agent create new scenes: industry packs and companies, validated by the
// same schemas as the bundled ones and simulated immediately.
func addSceneTools(s *mcp.Server, eng *engine.Engine, log *audit.Log) {
	raw := func(js string) (json.RawMessage, error) {
		if js == "null" {
			return nil, fmt.Errorf("not found")
		}
		return json.RawMessage(js), nil
	}
	mcp.AddTool(s, &mcp.Tool{Name: "scene_guide", Description: "How to build a new digital-twin scene (any industry: factories, campsites, restaurants, hospitals …) from two JSON configs: read this before create_industry / create_company."},
		guard(auth.Member, nil, func(_ context.Context, _ *mcp.CallToolRequest, in guideIn) (*mcp.CallToolResult, guideOut, error) {
			out := guideOut{Guide: guide}
			if in.Schema {
				out.IndustrySchema, _ = raw(eng.Schema("industry"))
				out.CompanySchema, _ = raw(eng.Schema("company"))
			}
			return nil, out, nil
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "list_industries", Description: "Industry packs available (built-in and created), with their products and machines."},
		guard(auth.Member, nil, func(_ context.Context, _ *mcp.CallToolRequest, _ siteIn) (*mcp.CallToolResult, map[string]any, error) {
			js, err := raw(eng.IndustryList())
			return nil, map[string]any{"industries": js}, err
		}))
	mcp.AddTool(s, &mcp.Tool{Name: "get_industry", Description: "The full config of an industry pack (with defaults filled in): the best example to copy from."},
		guard(auth.Member, nil, func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, map[string]any, error) {
			js, err := raw(eng.Industry(in.ID))
			if err != nil {
				return nil, nil, fmt.Errorf("no industry pack %q; see list_industries", in.ID)
			}
			return nil, map[string]any{"industry": js}, nil
		}))
	// create applies a config and keeps who did it; what is the audit log's name for it
	create := func(what string, fn func([]byte) engine.ConfigResult) mcp.ToolHandlerFor[configIn, map[string]any] {
		return func(_ context.Context, req *mcp.CallToolRequest, in configIn) (*mcp.CallToolResult, map[string]any, error) {
			b, err := json.Marshal(in.Config)
			if err != nil {
				return nil, nil, err
			}
			r := fn(b)
			id, _ := in.Config["id"].(string)
			if !r.OK {
				err = fmt.Errorf("配置没有通过校验（%s）：\n- %s", r.Where, strings.Join(r.Errors, "\n- "))
			}
			company := id
			if what == "industry.create" {
				company = "*"
			}
			log.Record(who(req), "mcp", company, what, id, "", err)
			if err != nil {
				return nil, nil, err
			}
			var detail map[string]any
			json.Unmarshal(r.Detail, &detail)
			return nil, detail, nil
		}
	}
	configID := func(in configIn) string { id, _ := in.Config["id"].(string); return id }
	mcp.AddTool(s, &mcp.Tool{Name: "create_industry", Description: "Create or replace an industry pack (products, machines, routes). Validated; errors say exactly what to fix. Companies using it are rebuilt."},
		everywhere(auth.Admin, create("industry.create", eng.CreateIndustry)))
	mcp.AddTool(s, &mcp.Tool{Name: "create_company", Description: "Create or replace a company (its plants and lines). It is simulated immediately and viewable at /?company=<id> on this server."},
		guard(auth.Admin, configID, create("company.create", eng.CreateCompany)))
	mcp.AddTool(s, &mcp.Tool{Name: "delete_company", Description: "Remove a company that was created with create_company."},
		guard(auth.Admin, func(in idIn) string { return in.ID }, func(_ context.Context, req *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, map[string]any, error) {
			r := eng.DeleteCompany(in.ID)
			var err error
			if !r.OK {
				err = fmt.Errorf("%s", strings.Join(r.Errors, "; "))
			}
			log.Record(who(req), "mcp", in.ID, "company.delete", in.ID, "", err)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"deleted": in.ID}, nil
		}))
}

// commandText is a command as the audit log shows it: "speed 1.2", "stop".
func commandText(cmd string, arg *float64) string {
	if arg == nil {
		return cmd
	}
	return fmt.Sprintf("%s %g", cmd, *arg)
}
