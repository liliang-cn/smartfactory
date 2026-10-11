package calib

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"smartfactory/internal/engine"
	"smartfactory/internal/series"
)

// Service collects the models' residuals, fits machines from their stored readings and keeps the
// applied calibrations on the engine.
type Service struct {
	Store  *Store
	Eng    *engine.Engine
	Series *series.Store
	Log    *slog.Logger
	// Every is how often residuals are collected (30 s); Keep how long they are kept (90 days).
	Every, Keep time.Duration
	// Now is the clock (tests set it).
	Now func() time.Time
}

func New(st *Store, eng *engine.Engine, readings *series.Store, log *slog.Logger) *Service {
	return &Service{Store: st, Eng: eng, Series: readings, Log: log, Every: 30 * time.Second, Keep: 90 * 24 * time.Hour, Now: time.Now}
}

// Run puts the applied calibrations on the engine, then collects residuals until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for _, c := range s.Eng.CompanyIDs() {
		if err := s.Push(c); err != nil {
			s.Log.Error("calib: apply", "company", c, "err", err)
		}
	}
	t := time.NewTicker(s.Every)
	defer t.Stop()
	pruned := time.Time{}
	for {
		select {
		case <-ctx.Done():
			s.Collect()
			return
		case <-t.C:
		}
		s.Collect()
		if now := s.Now(); now.Sub(pruned) > time.Hour {
			if err := s.Store.prune(now.Add(-s.Keep)); err != nil {
				s.Log.Error("calib: prune", "err", err)
			}
			pruned = now
		}
	}
}

// Collect stores what the models got wrong since the last collection.
func (s *Service) Collect() {
	now := s.Now()
	for _, c := range s.Eng.CompanyIDs() {
		if err := s.Store.addResiduals(c, s.Eng.Residuals(c), now); err != nil {
			s.Log.Error("calib: residuals", "company", c, "err", err)
		}
	}
}

// Push puts a company's applied calibrations on its machines (after a restart, or its factory
// was rebuilt).
func (s *Service) Push(company string) error {
	ap, err := s.Store.applied(company)
	if err != nil {
		return err
	}
	m := map[string]engine.Params{}
	for k, a := range ap {
		m[k] = a.Params
	}
	missing, res := s.Eng.Calibrate(company, m)
	if !res.OK {
		return errors.New(strings.Join(res.Errors, "；"))
	}
	if len(missing) > 0 {
		s.Log.Warn("calib: machines no longer in the factory", "company", company, "machines", missing)
	}
	return nil
}

// Machine is a machine as the console shows calibration: its parameters (template, fitted,
// applied), how the fit replayed and how its model has been doing.
type Machine struct {
	Plant     string `json:"plant"`
	PlantName string `json:"plantName"`
	Line      string `json:"line"`
	Asset     string `json:"asset"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	// Live: reporting now; Stored: has readings stored to fit from.
	Live   bool     `json:"live"`
	Stored bool     `json:"stored"`
	Params []Param  `json:"params"`
	Replay []Replay `json:"replay"`
	// FittedAt and the period it was fitted over.
	FittedAt *time.Time `json:"fittedAt,omitempty"`
	From     *time.Time `json:"from,omitempty"`
	To       *time.Time `json:"to,omitempty"`
	// AppliedAt / AppliedBy: a calibration is in force.
	AppliedAt *time.Time `json:"appliedAt,omitempty"`
	AppliedBy string     `json:"appliedBy,omitempty"`
	// Residual is how far its model was from the measurements over the last day.
	Residual []Summary `json:"residual"`
}

func key(plant, asset string) string { return plant + "/" + asset }

func val(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func templateOf(mp engine.MachineParams) Template {
	t := mp.Template
	tpl := Template{
		Thermal: Thermal{val(t.IdleRise), val(t.RunRise), val(t.Tau)},
		Power:   Power{val(t.PIdle), val(t.PRun)},
		Vib:     val(t.Vib), Cycle: val(t.Cycle), MTBF: val(t.MTBF),
	}
	if t.Repair != nil {
		tpl.RepairMin, tpl.RepairMax = t.Repair[0], t.Repair[1]
	}
	if tpl.Thermal.Tau == 0 {
		tpl.Thermal.Tau = 40
	}
	return tpl
}

// blank is a machine's parameters before any fit: the template's.
func blank(mp engine.MachineParams) []Param {
	ps, _ := Fit(Data{}, templateOf(mp))
	for i := range ps {
		ps[i].Note = ""
	}
	return ps
}

func appliedValue(p engine.Params, name string) *float64 {
	switch name {
	case "idleRise":
		return p.IdleRise
	case "runRise":
		return p.RunRise
	case "tau":
		return p.Tau
	case "pIdle":
		return p.PIdle
	case "pRun":
		return p.PRun
	case "vib":
		return p.Vib
	case "cycle":
		return p.Cycle
	case "mtbf":
		return p.MTBF
	case "repairMin":
		if p.Repair != nil {
			return f64(p.Repair[0])
		}
	case "repairMax":
		if p.Repair != nil {
			return f64(p.Repair[1])
		}
	}
	return nil
}

// Status is every machine of the company with what calibration knows of it.
func (s *Service) Status(company string) ([]Machine, error) {
	mps := s.Eng.MachineParams(company)
	if mps == nil {
		return nil, fmt.Errorf("没有公司 %s", company)
	}
	fits, err := s.Store.fits(company)
	if err != nil {
		return nil, err
	}
	ap, err := s.Store.applied(company)
	if err != nil {
		return nil, err
	}
	sums, err := s.Store.summaries(company, s.Now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	stored := map[string]bool{}
	if s.Series != nil {
		as, err := s.Series.Assets(company)
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			stored[a] = true
		}
	}
	out := make([]Machine, 0, len(mps))
	for _, mp := range mps {
		k := key(mp.Plant, mp.Asset)
		m := Machine{Plant: mp.Plant, PlantName: mp.PlantName, Line: mp.Line, Asset: mp.Asset, Name: mp.Name, Label: mp.Label,
			Live: mp.Live, Stored: stored[k], Residual: sums[k], Replay: []Replay{}}
		if m.Residual == nil {
			m.Residual = []Summary{}
		}
		if f, ok := fits[k]; ok {
			m.Params, m.Replay = f.Params, f.Replay
			at, from, to := f.At, f.From, f.To
			m.FittedAt, m.From, m.To = &at, &from, &to
		} else {
			m.Params = blank(mp)
		}
		if a, ok := ap[k]; ok {
			at := a.At
			m.AppliedAt, m.AppliedBy = &at, a.By
			for i := range m.Params {
				m.Params[i].Applied = appliedValue(a.Params, m.Params[i].Name)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// pick are the machines named ("plant/asset" or the asset alone), or all when none are.
func pick(mps []engine.MachineParams, names []string) ([]engine.MachineParams, error) {
	if len(names) == 0 {
		return mps, nil
	}
	var out []engine.MachineParams
	for _, n := range names {
		found := false
		for _, mp := range mps {
			if n == key(mp.Plant, mp.Asset) || n == mp.Asset {
				out = append(out, mp)
				found = true
			}
		}
		if !found {
			return nil, &Missing{Name: n}
		}
	}
	return out, nil
}

// Missing is a machine named that the company does not have.
type Missing struct{ Name string }

func (m *Missing) Error() string { return "没有设备 " + m.Name }

// Fit fits the named machines (or every machine with stored readings) from the last hours of
// their readings, and keeps the fits.
func (s *Service) Fit(ctx context.Context, company string, names []string, hours float64) ([]Machine, error) {
	if s.Series == nil {
		return nil, errors.New("这台服务器没有存设备读数（没有数据目录）")
	}
	mps := s.Eng.MachineParams(company)
	if mps == nil {
		return nil, fmt.Errorf("没有公司 %s", company)
	}
	targets, err := pick(mps, names)
	if err != nil {
		return nil, err
	}
	if hours <= 0 {
		hours = 24
	}
	stored, err := s.Series.Assets(company)
	if err != nil {
		return nil, err
	}
	to := s.Now()
	from := to.Add(-time.Duration(hours * float64(time.Hour)))
	for _, mp := range targets {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !slices.Contains(stored, key(mp.Plant, mp.Asset)) {
			if len(names) == 0 {
				continue // nothing measured: nothing to fit
			}
		}
		d, err := s.load(company, mp.Plant, mp.Asset, from, to)
		if err != nil {
			return nil, err
		}
		ps, rp := Fit(d, templateOf(mp))
		if rp == nil {
			rp = []Replay{}
		}
		if err := s.Store.saveFit(company, mp.Plant, mp.Asset, Fitted{At: to, From: from, To: to, Params: ps, Replay: rp}); err != nil {
			return nil, err
		}
	}
	all, err := s.Status(company)
	if err != nil {
		return nil, err
	}
	var out []Machine
	for _, m := range all {
		for _, mp := range targets {
			if m.Plant == mp.Plant && m.Asset == mp.Asset && m.FittedAt != nil && !m.FittedAt.Before(to) {
				out = append(out, m)
			}
		}
	}
	if out == nil {
		out = []Machine{}
	}
	return out, nil
}

// load reads a machine's period from the series store, a few hours at a time; a span that comes
// back with series.MaxRaw readings of a field (cut short) is read again in halves.
func (s *Service) load(company, plant, asset string, from, to time.Time) (Data, error) {
	var d Data
	fields := []string{"temp", "power", "vib", "speed", "health", "cycles", "state"}
	dst := map[string]*[]Sample{"temp": &d.Temp, "power": &d.Power, "vib": &d.Vib, "speed": &d.Speed, "health": &d.Health, "cycles": &d.Cycles}
	var read func(t, end time.Time) error
	read = func(t, end time.Time) error {
		a, err := s.Series.Read(series.Query{Company: company, Plant: plant, Asset: asset, Fields: fields, From: t, To: end, Step: series.Raw})
		if err != nil {
			return err
		}
		if end.Sub(t) > 5*time.Minute {
			for _, f := range a.Series {
				if len(f.Points) >= series.MaxRaw {
					mid := t.Add(end.Sub(t) / 2)
					if err := read(t, mid); err != nil {
						return err
					}
					return read(mid, end)
				}
			}
		}
		for _, f := range a.Series {
			out, ok := dst[f.Field]
			if !ok {
				continue
			}
			for _, p := range f.Points {
				if len(p) < 3 || num(p[2]) != 0 {
					continue // flagged: not believed
				}
				*out = append(*out, Sample{T: time.UnixMilli(int64(num(p[0]))), V: num(p[1])})
			}
		}
		for _, e := range a.Events {
			if e.Field == "state" && e.Q == 0 {
				d.States = append(d.States, StateChange{T: time.UnixMilli(e.T), State: e.V})
			}
		}
		return nil
	}
	for t := from; t.Before(to); t = t.Add(4 * time.Hour) {
		end := t.Add(4 * time.Hour)
		if end.After(to) {
			end = to
		}
		if err := read(t, end); err != nil {
			return d, err
		}
	}
	return d, nil
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case uint8:
		return float64(x)
	case float32:
		return float64(x)
	}
	return 0
}

// Applied machine, or why it was not.
type Outcome struct {
	Machine string `json:"machine"`
	OK      bool   `json:"ok"`
	Params  int    `json:"params,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Apply puts each named machine's (or every fitted machine's) fitted parameters in force.
func (s *Service) Apply(company string, names []string, by string) ([]Outcome, error) {
	mps := s.Eng.MachineParams(company)
	if mps == nil {
		return nil, fmt.Errorf("没有公司 %s", company)
	}
	targets, err := pick(mps, names)
	if err != nil {
		return nil, err
	}
	fits, err := s.Store.fits(company)
	if err != nil {
		return nil, err
	}
	out := []Outcome{}
	now := s.Now()
	for _, mp := range targets {
		k := key(mp.Plant, mp.Asset)
		f, ok := fits[k]
		if !ok {
			if len(names) > 0 {
				out = append(out, Outcome{Machine: k, Error: "还没有拟合"})
			}
			continue
		}
		var p engine.Params
		n := 0
		var rmin, rmax *float64
		for _, x := range f.Params {
			if x.Fitted == nil {
				continue
			}
			v := *x.Fitted
			switch x.Name {
			case "idleRise":
				p.IdleRise = &v
			case "runRise":
				p.RunRise = &v
			case "tau":
				p.Tau = &v
			case "pIdle":
				p.PIdle = &v
			case "pRun":
				p.PRun = &v
			case "vib":
				p.Vib = &v
			case "cycle":
				p.Cycle = &v
			case "mtbf":
				p.MTBF = &v
			case "repairMin":
				rmin = &v
			case "repairMax":
				rmax = &v
			default:
				continue
			}
			n++
		}
		if rmin != nil && rmax != nil {
			p.Repair = &[2]float64{*rmin, *rmax}
		} else if rmin != nil || rmax != nil {
			n--
		}
		if n == 0 {
			if len(names) > 0 {
				out = append(out, Outcome{Machine: k, Error: "没有拟合出能用的参数"})
			}
			continue
		}
		if err := s.Store.apply(company, mp.Plant, mp.Asset, Applied{Params: p, At: now, By: by}); err != nil {
			return nil, err
		}
		out = append(out, Outcome{Machine: k, OK: true, Params: n})
	}
	return out, s.Push(company)
}

// Revert puts the named machines' (or every machine's) template parameters back.
func (s *Service) Revert(company string, names []string) ([]Outcome, error) {
	mps := s.Eng.MachineParams(company)
	if mps == nil {
		return nil, fmt.Errorf("没有公司 %s", company)
	}
	targets, err := pick(mps, names)
	if err != nil {
		return nil, err
	}
	out := []Outcome{}
	for _, mp := range targets {
		ok, err := s.Store.revert(company, mp.Plant, mp.Asset)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, Outcome{Machine: key(mp.Plant, mp.Asset), OK: true})
		} else if len(names) > 0 {
			out = append(out, Outcome{Machine: key(mp.Plant, mp.Asset), Error: "没有在用的校准"})
		}
	}
	return out, s.Push(company)
}

// Residuals is a machine's residual in one field over a range, by step (a minute at least).
func (s *Service) Residuals(company, machine, field string, from, to time.Time, step time.Duration) ([]ResidualPoint, error) {
	plant, asset, ok := strings.Cut(machine, "/")
	if !ok {
		mps := s.Eng.MachineParams(company)
		for _, mp := range mps {
			if mp.Asset == machine {
				plant, asset, ok = mp.Plant, mp.Asset, true
				break
			}
		}
		if !ok {
			return nil, &Missing{Name: machine}
		}
	}
	return s.Store.residuals(company, plant, asset, field, from, to, step)
}
