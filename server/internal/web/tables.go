package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"smartfactory/internal/tables"
)

// A restaurant's tables. Reporters (a camera box, the POS, a table sensor, the waiter page) send
// states (/api/report/{company}/tables) and dining-session events (/api/report/{company}/sessions),
// one or a list; the twin and anyone may read the tables now, a table's day and the history.
func (s *Server) tableRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	// one report or a list of them: {…} or [{…}, …] or {"reports": [...]}
	list := func(w http.ResponseWriter, r *http.Request, key string, out any) bool {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "一次最多 4 MB")
			return false
		}
		body = bytes.TrimSpace(body)
		if len(body) > 0 && body[0] == '{' {
			var wrap map[string]json.RawMessage
			if json.Unmarshal(body, &wrap) == nil {
				if inner, ok := wrap[key]; ok {
					body = inner
				} else {
					body = append(append([]byte{'['}, body...), ']')
				}
			}
		}
		if err := json.Unmarshal(body, out); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return false
		}
		return true
	}
	mux.HandleFunc("POST /api/report/{company}/tables", func(w http.ResponseWriter, r *http.Request) {
		var in []tables.Report
		if !list(w, r, "reports", &in) {
			return
		}
		v, err := o.ReportTables(s.caller(r), via(r), r.PathValue("company"), in)
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/report/{company}/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in []tables.Event
		if !list(w, r, "events", &in) {
			return
		}
		v, err := o.ReportSessions(s.caller(r), via(r), r.PathValue("company"), in)
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/tables/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.TablesNow(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/tables/{company}/history", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		v, err := o.TableHistory(s.caller(r), r.PathValue("company"), q.Get("from"), q.Get("to"), q.Get("by") == "day")
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/tables/{company}/{plant}/{table}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.TableNow(s.caller(r), r.PathValue("company"), r.PathValue("plant"), r.PathValue("table"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/tables/{company}/{plant}/{table}/sessions", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.TableSessions(s.caller(r), r.PathValue("company"), r.PathValue("plant"), r.PathValue("table"), r.URL.Query().Get("day"))
		answer(w, v, err)
	})
}
