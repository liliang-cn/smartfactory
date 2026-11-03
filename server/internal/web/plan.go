package web

import (
	"encoding/json"
	"net/http"
	"time"

	"smartfactory/internal/plan"
)

// A company's production plan (/api/plan/{company}): the plan, the draft and how they differ;
// re-planning, moving and locking orders in the draft, publishing it, and checking a plan on the
// simulation.
func (s *Server) planRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	read := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if r.ContentLength == 0 {
			return true
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/plan/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.PlanOverview(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	// {"goal": "due" | "balance" | "setup"}
	mux.HandleFunc("POST /api/plan/{company}/replan", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Goal plan.Goal `json:"goal"`
		}
		if read(w, r, &in) {
			v, err := o.Replan(s.caller(r), r.PathValue("company"), in.Goal)
			answer(w, v, err)
		}
	})
	// {"no", "line": "plant/line", "at": place on the line, 0 first}
	mux.HandleFunc("POST /api/plan/{company}/move", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			No   string `json:"no"`
			Line string `json:"line"`
			At   int    `json:"at"`
		}
		if read(w, r, &in) {
			v, err := o.MovePlanned(s.caller(r), r.PathValue("company"), in.No, in.Line, in.At)
			answer(w, v, err)
		}
	})
	// {"no", "lock": true | false}
	mux.HandleFunc("POST /api/plan/{company}/lock", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			No   string `json:"no"`
			Lock bool   `json:"lock"`
		}
		if read(w, r, &in) {
			v, err := o.LockPlanned(s.caller(r), via(r), r.PathValue("company"), in.No, in.Lock)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("PUT /api/plan/{company}/settings", func(w http.ResponseWriter, r *http.Request) {
		var in plan.Settings
		if read(w, r, &in) {
			v, err := o.SetPlanSettings(s.caller(r), via(r), r.PathValue("company"), in)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("DELETE /api/plan/{company}/draft", func(w http.ResponseWriter, r *http.Request) {
		err := o.DiscardPlan(s.caller(r), r.PathValue("company"))
		answer(w, map[string]bool{"ok": true}, err)
	})
	mux.HandleFunc("POST /api/plan/{company}/publish", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.PublishPlan(s.caller(r), via(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/plan/{company}/history", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.PlanHistory(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	// {"plan": "draft" | "published", "days": 0.5–7}
	mux.HandleFunc("POST /api/plan/{company}/whatif", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Plan string  `json:"plan"`
			Days float64 `json:"days"`
		}
		if read(w, r, &in) {
			v, err := o.PlanWhatIf(s.caller(r), r.PathValue("company"), in.Plan, in.Days)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("GET /api/plan/{company}/whatif", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.PlanWhatIfStatus(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	// {"part", "qty", "due"}: what an urgent order would do to the plan, nothing stored
	mux.HandleFunc("POST /api/plan/{company}/rush", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Part     string `json:"part"`
			Qty      int    `json:"qty"`
			Due      string `json:"due"`
			Customer string `json:"customer"`
		}
		if read(w, r, &in) {
			ri := plan.RushInput{Part: in.Part, Qty: in.Qty, Customer: in.Customer}
			if in.Due != "" {
				t, err := parseDue(in.Due)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				ri.Due = t
			}
			v, err := o.PlanRush(s.caller(r), r.PathValue("company"), ri)
			answer(w, v, err)
		}
	})
}

func parseDue(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	cst := time.FixedZone("CST", 8*3600)
	for _, f := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(f, s, cst); err == nil {
			if f == "2006-01-02" {
				t = t.Add(17 * time.Hour)
			}
			return t, nil
		}
	}
	return time.Time{}, errBadDue
}

type badDue struct{}

func (badDue) Error() string {
	return "交期看不懂，用 2026-11-11 或 2026-11-11 17:00 这样的写法"
}

var errBadDue = badDue{}
