package web

import (
	"encoding/json"
	"net/http"
	"time"

	"smartfactory/internal/health"
	"smartfactory/internal/ops"
)

// healthRoutes are the console's 运行状况: how the server and the browsers watching it are doing.
func (s *Server) healthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/health", func(w http.ResponseWriter, r *http.Request) {
		who := s.caller(r)
		if err := ops.Admin(who); err != nil {
			writeOpError(w, err)
			return
		}
		if s.Health == nil {
			writeError(w, http.StatusServiceUnavailable, "这台服务器没有开运行状况")
			return
		}
		writeJSON(w, s.healthReport(func(c string) bool { return who.In(c) }))
	})
	// a twin page says how it is doing once a minute; nothing about who is watching
	mux.HandleFunc("POST /api/health/browser", func(w http.ResponseWriter, r *http.Request) {
		if s.Health == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var rep health.Report
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&rep); err != nil {
			writeError(w, http.StatusBadRequest, "报告读不懂")
			return
		}
		if !s.Health.Browsers.Add(rep, time.Now()) {
			writeError(w, http.StatusBadRequest, "报告不对")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

type plantCost struct {
	Company  string `json:"company"`
	Name     string `json:"name"`
	Plant    string `json:"plant"`
	Lines    int    `json:"lines"`
	Machines int    `json:"machines"`
	// Viewers are the browsers watching the plant's company.
	Viewers int `json:"viewers"`
}

func (s *Server) healthReport(show func(string) bool) map[string]any {
	now := time.Now()
	samples := s.Health.Samples()
	var cur health.Sample
	if len(samples) > 0 {
		cur = samples[len(samples)-1]
	}
	host := health.HostInfo()
	dir := s.Health.Dir()
	last := health.Summarize(samples, now, time.Minute)
	hour := health.Summarize(samples, now, time.Hour)
	browsers := s.Health.Browsers.Summary(now)

	// what each plant asks of the simulation: it steps every plant together, so its cost is the
	// machines simulated
	var list []struct {
		ID, Name string
		Plants   []struct {
			ID, Name        string
			Lines, Machines int
		}
	}
	json.Unmarshal([]byte(s.Engine.List()), &list)
	watching := s.Engine.Watchers()
	plants := []plantCost{}
	machines := 0
	for _, c := range list {
		for _, p := range c.Plants {
			machines += p.Machines
			if show(c.ID) {
				plants = append(plants, plantCost{Company: c.Name, Name: p.Name, Plant: p.ID, Lines: p.Lines, Machines: p.Machines, Viewers: watching[c.ID]})
			}
		}
	}
	model := map[string]any{"configured": false}
	if s.Model != nil {
		if name, ok := s.Model(); ok {
			model = map[string]any{"configured": true, "name": name, "callsHour": hour.Calls, "callsMinute": last.Calls}
		}
	}
	return map[string]any{
		"now": now, "version": s.Version, "started": s.Health.Start, "host": host,
		"process": map[string]any{"cpu": last.CPU, "cpuMax": last.CPUMax, "cpuHour": hour.CPU, "rss": cur.RSS, "heap": cur.Heap, "goroutines": cur.Goroutines},
		"engine":  map[string]any{"stepMs": last.StepMs, "stepMaxMs": hour.StepMaxMs, "budgetMs": health.StepBudgetMs, "machines": machines, "plants": plants},
		"viewers": cur.Viewers, "devices": cur.Devices,
		"disk": dir, "model": model, "browsers": browsers,
		"samples":  samples,
		"warnings": health.Check(host, cur.RSS, dir, last, browsers),
	}
}
