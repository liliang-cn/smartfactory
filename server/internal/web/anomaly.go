package web

import (
	"encoding/json"
	"net/http"
)

// Readings unusual for their machine (/api/anomalies/{company}): the company's hints and normals
// for members; one machine's fields, for the twin's details of it, for anyone watching; and an
// administrator's reset of a machine's normal.
func (s *Server) anomalyRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	mux.HandleFunc("GET /api/anomalies/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Anomalies(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/anomalies/{company}/{plant}/{asset}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.MachineAnomaly(s.caller(r), r.PathValue("company"), r.PathValue("plant"), r.PathValue("asset"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/anomalies/{company}/reset", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Machines []string `json:"machines"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return
		}
		v, err := o.ResetBaseline(s.caller(r), via(r), r.PathValue("company"), in.Machines)
		answer(w, v, err)
	})
}
