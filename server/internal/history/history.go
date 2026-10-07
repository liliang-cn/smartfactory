// Package history keeps what each plant did, hour by hour, in a Postgres database per company:
// machine states and output, quality, downtime, die changes, defects, spares and work orders.
// The advisor (DataIntelligence) reads it through the semantic model in model.yaml; nothing here
// interprets the numbers.
package history

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// Model is the DataIntelligence semantic model over this schema (the same for every company).
//
//go:embed model.yaml
var Model []byte

const layout = "2006-01-02 15:04:05"

// Store is one company's history database.
type Store struct {
	db *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(ctx, schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("history schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() { s.db.Close() }

// Reset empties every table (a backfill starting over).
func (s *Store) Reset(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `TRUNCATE complaints, shipments, purchases, customers, lots, work_orders, defects, tool_changes, downtime, stock_hours, quality_hours,
		line_hours, machine_hours, calendar, machines, lines, parts, plants RESTART IDENTITY`)
	return err
}

// Dims is what the engine's historyDims returns.
type Dims struct {
	Company struct{ ID, Name string } `json:"company"`
	Plants  []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Hall  string `json:"hall"`
		Lines []struct {
			ID         string  `json:"id"`
			Name       string  `json:"name"`
			Process    string  `json:"process"`
			Product    string  `json:"product"`
			IdealCycle float64 `json:"idealCycle"`
			OrderQty   int     `json:"orderQty"`
			Machines   []struct {
				ID       string  `json:"id"`
				Code     string  `json:"code"`
				Name     string  `json:"name"`
				Kind     string  `json:"kind"`
				Seq      int     `json:"seq"`
				Cycle    float64 `json:"cycle"`
				Tool     *string `json:"tool"`
				ToolLife *int    `json:"toolLife"`
				Feature  *string `json:"feature"`
				Unit     *string `json:"unit"`
			} `json:"machines"`
		} `json:"lines"`
	} `json:"plants"`
	Parts []struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		PartNo   string   `json:"partNo"`
		Material string   `json:"material"`
		Weight   *float64 `json:"weight"`
		Input    *float64 `json:"input"`
		Customer string   `json:"customer"`
		Family   string   `json:"family"`
		Price    *float64 `json:"price"`
	} `json:"parts"`
	Customers []struct {
		Name    string  `json:"name"`
		Transit float64 `json:"transit"`
	} `json:"customers"`
}

// WriteDims upserts plants, parts, lines and machines.
func (s *Store) WriteDims(ctx context.Context, raw string) error {
	var d Dims
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return fmt.Errorf("dims: %w", err)
	}
	b := &pgx.Batch{}
	for _, p := range d.Parts {
		b.Queue(`INSERT INTO parts (part_id, part_no, name, material, weight_kg, input_kg, customer, family, price) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (part_id) DO UPDATE SET part_no=$2, name=$3, material=$4, weight_kg=$5, input_kg=$6, customer=$7, family=$8, price=$9`,
			p.ID, p.PartNo, p.Name, p.Material, p.Weight, p.Input, p.Customer, p.Family, p.Price)
	}
	for _, c := range d.Customers {
		b.Queue(`INSERT INTO customers VALUES ($1,$2) ON CONFLICT (name) DO UPDATE SET transit_h=$2`, c.Name, c.Transit)
	}
	for _, p := range d.Plants {
		b.Queue(`INSERT INTO plants VALUES ($1,$2,$3) ON CONFLICT (plant_id) DO UPDATE SET name=$2, hall=$3`, p.ID, p.Name, p.Hall)
		for _, l := range p.Lines {
			b.Queue(`INSERT INTO lines VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (line_id) DO UPDATE SET plant_id=$2, name=$3, process=$4, part_id=$5, ideal_cycle_s=$6, order_qty=$7`,
				l.ID, p.ID, l.Name, l.Process, l.Product, l.IdealCycle, l.OrderQty)
			for _, m := range l.Machines {
				b.Queue(`INSERT INTO machines VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (machine_id) DO UPDATE SET
					line_id=$2, plant_id=$3, code=$4, label=$5, name=$6, kind=$7, seq=$8, cycle_s=$9, tool=$10, tool_life=$11, feature=$12, unit=$13`,
					m.ID, l.ID, p.ID, m.Code, p.Name+" "+m.Code, m.Name, m.Kind, m.Seq, m.Cycle, m.Tool, m.ToolLife, m.Feature, m.Unit)
			}
		}
	}
	return s.db.SendBatch(ctx, b).Close()
}

type batch struct {
	MachineHours []struct {
		Machine, Hour                                         string
		Run, Idle, Blocked, Fault, Maint, Changeover, Stopped float64
		Cycles, Scrap, Rework                                 int
		Kwh, Fuel, PowerCost, FuelCost                        float64
	} `json:"machineHours"`
	LineHours []struct {
		Line, Hour                             string
		Planned, Run                           float64
		NeckCycles                             int
		Started, Produced, Good, Rework, Scrap int
		GoodKg, InputKg, IdealCycle            float64
	} `json:"lineHours"`
	Quality []struct {
		Machine, Hour, Feature, Unit string
		N                            int
		Mean, Sd, Min, Max           float64
		Lsl, Usl                     *float64
		Out                          int
	} `json:"quality"`
	Stock []struct {
		Item, Label, Hour string
		Min, End, Cap     int
	} `json:"stock"`
	Downtime []struct {
		Machine, Line, Start, End, Category, Reason string
	} `json:"downtime"`
	ToolChanges []struct {
		Machine, Line, Tool, Start, End string
		Hits, Life                      int
		Limit                           float64
		Trigger                         string
		Cost                            float64
	} `json:"toolChanges"`
	Defects []struct {
		Machine, Line, At, Type, Feature string
		Value                            *float64
		Disposition, Order               string
		Heat, Supplier                   string
	} `json:"defects"`
	Orders []struct {
		No, Line, Part, PartName, Customer, Heat, Supplier string
		Qty                                                int
		Price                                              float64
		Issued, Due, Promise                               string
		Completed                                          *string
		Good, Scrap                                        int
	} `json:"orders"`
	Purchases  []map[string]any `json:"purchases"`
	Shipments  []map[string]any `json:"shipments"`
	Complaints []map[string]any `json:"complaints"`
	Lots       []struct {
		Heat, Supplier, Line, Part, PartName, First, Last string
		Produced, Good, Rework, Scrap                     int
	} `json:"lots"`
}

// cst is the zone the history's wall-clock times are in.
var cst = time.FixedZone("CST", 8*3600)

// wall is t as the database stores times: China's wall clock, read as a zoneless time.
func wall(t time.Time) time.Time {
	l := t.In(cst)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, time.UTC)
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func nullTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t := parse(s)
	return &t
}

func parse(t string) time.Time {
	v, _ := time.Parse(layout, t)
	return v
}

func hourOf(t string) time.Time { return parse(t).Truncate(time.Hour) }

func shiftOf(h time.Time) string {
	switch {
	case h.Hour() < 8:
		return "夜班"
	case h.Hour() < 16:
		return "早班"
	}
	return "中班"
}

// Write stores what the engine's history call returned: {plantId: batch}. now decides whether
// an open work order is late.
func (s *Store) Write(ctx context.Context, raw string, now time.Time) (rows int, err error) {
	var plants map[string]batch
	if err := json.Unmarshal([]byte(raw), &plants); err != nil {
		return 0, fmt.Errorf("history: %w", err)
	}
	now = wall(now)
	b := &pgx.Batch{}
	hours := map[time.Time]bool{}
	cal := func(t string) time.Time {
		h := hourOf(t)
		if !hours[h] {
			hours[h] = true
			b.Queue(`INSERT INTO calendar VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, h, h.Format("2006-01-02"), shiftOf(h))
		}
		return h
	}
	for plant, x := range plants {
		for _, r := range x.MachineHours {
			b.Queue(`INSERT INTO machine_hours (machine_id, hour, run_s, idle_s, blocked_s, fault_s, maint_s, changeover_s, stopped_s, cycles, scrap, rework, energy_kwh, fuel, power_cost, fuel_cost)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
				ON CONFLICT (machine_id, hour) DO UPDATE SET run_s=machine_hours.run_s+$3, idle_s=machine_hours.idle_s+$4,
				blocked_s=machine_hours.blocked_s+$5, fault_s=machine_hours.fault_s+$6, maint_s=machine_hours.maint_s+$7,
				changeover_s=machine_hours.changeover_s+$8, stopped_s=machine_hours.stopped_s+$9, cycles=machine_hours.cycles+$10,
				scrap=machine_hours.scrap+$11, rework=machine_hours.rework+$12, energy_kwh=machine_hours.energy_kwh+$13, fuel=machine_hours.fuel+$14,
				power_cost=machine_hours.power_cost+$15, fuel_cost=machine_hours.fuel_cost+$16`,
				r.Machine, cal(r.Hour), r.Run, r.Idle, r.Blocked, r.Fault, r.Maint, r.Changeover, r.Stopped, r.Cycles, r.Scrap, r.Rework, r.Kwh, r.Fuel, r.PowerCost, r.FuelCost)
		}
		for _, r := range x.LineHours {
			b.Queue(`INSERT INTO line_hours (line_id, hour, planned_s, run_s, started, produced, good, rework, scrap, good_kg, input_kg, ideal_cycle_s, neck_cycles)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
				ON CONFLICT (line_id, hour) DO UPDATE SET planned_s=line_hours.planned_s+$3, run_s=line_hours.run_s+$4,
				started=line_hours.started+$5, produced=line_hours.produced+$6, good=line_hours.good+$7, rework=line_hours.rework+$8,
				scrap=line_hours.scrap+$9, good_kg=line_hours.good_kg+$10, input_kg=line_hours.input_kg+$11, neck_cycles=line_hours.neck_cycles+$13`,
				r.Line, cal(r.Hour), r.Planned, r.Run, r.Started, r.Produced, r.Good, r.Rework, r.Scrap, r.GoodKg, r.InputKg, r.IdealCycle, r.NeckCycles)
		}
		for _, r := range x.Quality {
			b.Queue(`INSERT INTO quality_hours VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT DO NOTHING`,
				r.Machine, cal(r.Hour), r.Feature, r.Unit, r.N, r.Mean, r.Sd, r.Min, r.Max, r.Lsl, r.Usl, r.Out)
		}
		for _, r := range x.Stock {
			b.Queue(`INSERT INTO stock_hours VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`,
				plant, r.Item, r.Label, cal(r.Hour), r.Min, r.End, r.Cap)
		}
		for _, r := range x.Downtime {
			st, en := parse(r.Start), parse(r.End)
			b.Queue(`INSERT INTO downtime (machine_id, line_id, hour, started_at, ended_at, minutes, category, reason)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`,
				r.Machine, r.Line, cal(r.Start), st, en, en.Sub(st).Minutes(), r.Category, r.Reason)
		}
		for _, r := range x.ToolChanges {
			st, en := parse(r.Start), parse(r.End)
			b.Queue(`INSERT INTO tool_changes (machine_id, line_id, hour, started_at, ended_at, minutes, tool, hits, life, limit_pct, trigger, cost)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT DO NOTHING`,
				r.Machine, r.Line, cal(r.Start), st, en, en.Sub(st).Minutes(), r.Tool, r.Hits, r.Life, r.Limit*100, r.Trigger, r.Cost)
		}
		for _, r := range x.Defects {
			b.Queue(`INSERT INTO defects (machine_id, line_id, hour, at, defect_type, feature, value, disposition, order_no, heat_no, supplier)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				r.Machine, r.Line, cal(r.At), parse(r.At), r.Type, r.Feature, r.Value, r.Disposition, r.Order, r.Heat, r.Supplier)
		}
		for _, r := range x.Orders {
			due := parse(r.Due)
			var done *time.Time
			when, status, completed, onTime := r.Issued, "生产中", 0, 0
			if r.Completed != nil {
				t := parse(*r.Completed)
				done, when, completed = &t, *r.Completed, 1
				if !t.After(due) {
					status, onTime = "按期完成", 1
				} else {
					status = "延期完成"
				}
			} else if now.After(due) {
				status = "已逾期"
			}
			b.Queue(`INSERT INTO work_orders (order_no, line_id, part_id, customer, heat_no, qty, good, scrap, issued_at, due_at, completed_at, hour, status, completed, on_time, supplier, part_name, price, promise_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
				ON CONFLICT (order_no) DO UPDATE SET good=$7, scrap=$8, completed_at=$11, hour=$12, status=$13, completed=$14, on_time=$15`,
				r.No, r.Line, r.Part, r.Customer, r.Heat, r.Qty, r.Good, r.Scrap, parse(r.Issued), due, done, cal(when), status, completed, onTime, r.Supplier, r.PartName, r.Price, nullTime(r.Promise))
		}
		for _, r := range x.Purchases {
			late := 0
			if str(r["received"]) > str(r["promised"]) {
				late = 1
			}
			b.Queue(`INSERT INTO purchases VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT DO NOTHING`,
				r["po"], r["heat"], r["supplier"], r["material"], r["line"], r["part"], r["tonnes"], r["price"], r["amount"],
				parse(str(r["ordered"])), parse(str(r["promised"])), parse(str(r["received"])), cal(str(r["received"])), late, r["result"], r["reason"])
		}
		for _, r := range x.Shipments {
			dep := parse(str(r["departed"]))
			var delivered *time.Time
			var transit *float64
			status, on, done := "在途", 0, 0
			if d := str(r["delivered"]); d != "" {
				t := parse(d)
				h := t.Sub(dep).Hours()
				delivered, transit, done, status = &t, &h, 1, "已签收"
				if !t.After(parse(str(r["promise"]))) {
					on = 1
				}
			}
			b.Queue(`INSERT INTO shipments (shipment_no, order_no, line_id, customer, part_id, part_name, units, tonnes, trucks, carrier,
				departed_at, eta_at, promise_at, delivered_at, hour, revenue, transit_h, delivered, on_time, status, material_cost)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
				ON CONFLICT (shipment_no) DO UPDATE SET delivered_at=$14, transit_h=$17, delivered=$18, on_time=$19, status=$20`,
				r["no"], r["order"], r["line"], r["customer"], r["part"], r["partName"], r["units"], r["tonnes"], r["trucks"], r["carrier"],
				dep, parse(str(r["eta"])), parse(str(r["promise"])), delivered, cal(str(r["departed"])), r["revenue"], transit, done, on, status, r["materialCost"])
		}
		for _, r := range x.Complaints {
			var closed *time.Time
			status := "处理中"
			if c := str(r["closed"]); c != "" {
				t := parse(c)
				closed, status = &t, "已关闭"
			}
			b.Queue(`INSERT INTO complaints VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
				ON CONFLICT (complaint_no) DO UPDATE SET closed_at=$11, status=$13`,
				r["no"], r["customer"], r["order"], r["shipment"], r["line"], r["part"], r["partName"], r["type"], r["qty"],
				parse(str(r["opened"])), closed, cal(str(r["opened"])), status)
		}
		for _, r := range x.Lots {
			// a lot arrives in pieces (one per drain): add them up
			b.Queue(`INSERT INTO lots (heat_no, supplier, line_id, part_id, first_at, last_at, hour, produced, good, rework, scrap, part_name) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
				ON CONFLICT (heat_no) DO UPDATE SET last_at=greatest(lots.last_at, $6), produced=lots.produced+$8,
				good=lots.good+$9, rework=lots.rework+$10, scrap=lots.scrap+$11`,
				r.Heat, r.Supplier, r.Line, r.Part, parse(r.First), parse(r.Last), cal(r.First), r.Produced, r.Good, r.Rework, r.Scrap, r.PartName)
		}
	}
	rows = b.Len()
	if rows == 0 {
		return 0, nil
	}
	return rows, s.db.SendBatch(ctx, b).Close()
}

// Summary is a one-line count of what the database holds, for logs.
func (s *Store) Summary(ctx context.Context) string {
	var parts []string
	for _, t := range []string{"line_hours", "machine_hours", "downtime", "tool_changes", "defects", "work_orders"} {
		var n int
		if err := s.db.QueryRow(ctx, "SELECT count(*) FROM "+t).Scan(&n); err == nil {
			parts = append(parts, fmt.Sprintf("%s=%d", t, n))
		}
	}
	var lo, hi *time.Time
	_ = s.db.QueryRow(ctx, "SELECT min(hour), max(hour) FROM calendar").Scan(&lo, &hi)
	if lo != nil && hi != nil {
		parts = append(parts, fmt.Sprintf("%s → %s", lo.Format("01-02 15:04"), hi.Format("01-02 15:04")))
	}
	return strings.Join(parts, " ")
}

// Record keeps one company's history while the server runs: the plants' dimensions once, then
// every closed hour, each minute. An unreachable database is retried; nothing blocks the twin.
func Record(ctx context.Context, eng Engine, company, dsn string, log *slog.Logger) {
	var st *Store
	for st == nil {
		var err error
		if st, err = Open(ctx, dsn); err != nil {
			log.Warn("history database unreachable, retrying", "company", company, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
		}
	}
	defer st.Close()
	if err := st.WriteDims(ctx, eng.HistoryDims(company)); err != nil {
		log.Error("history dims", "company", company, "err", err)
		return
	}
	if !eng.HistoryOn(company, time.Now()) {
		log.Error("history: no such company", "company", company)
		return
	}
	log.Info("history recording", "company", company, "db", st.Summary(ctx))
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// close the hour in progress; a restart adds the rest of it
			wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if _, err := st.Write(wctx, eng.History(company, true), time.Now()); err != nil {
				log.Error("history final write", "company", company, "err", err)
			}
			cancel()
			return
		case <-tick.C:
			if _, err := st.Write(ctx, eng.History(company, false), time.Now()); err != nil {
				log.Error("history write", "company", company, "err", err)
			}
		}
	}
}

// Engine is what recording needs from the simulation.
type Engine interface {
	HistoryOn(company string, now time.Time) bool
	History(company string, flush bool) string
	HistoryDims(company string) string
}
