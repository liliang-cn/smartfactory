package web

import (
	"encoding/json"
	"net/http"

	"smartfactory/internal/guard"
)

// The command guard (/api/guard/{company}): its settings, and the commands that wait for a
// person, which operators approve or deny from the console, the twin or the command line.
func (s *Server) guardRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	mux.HandleFunc("GET /api/guard/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Guard(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("PUT /api/guard/{company}", func(w http.ResponseWriter, r *http.Request) {
		var in guard.Settings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return
		}
		v, err := o.SetGuard(s.caller(r), via(r), r.PathValue("company"), in)
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/guard/{company}/requests", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.CommandRequests(s.caller(r), r.PathValue("company"), r.URL.Query().Get("status"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/guard/{company}/requests/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.ApproveCommand(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/guard/{company}/requests/{id}/deny", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason string `json:"reason"`
		}
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in)
		v, err := o.DenyCommand(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"), in.Reason)
		answer(w, v, err)
	})
}
