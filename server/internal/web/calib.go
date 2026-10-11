package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Calibration (/api/admin/calib/{company}): machines fitted from their stored readings, the fitted
// parameters put in force or taken back; and how far each model is from the measurements
// (/api/calib/{company}/residuals), for members.
func (s *Server) calibRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	type pick struct {
		Machines []string `json:"machines"`
		Hours    float64  `json:"hours"`
	}
	read := func(w http.ResponseWriter, r *http.Request) (pick, bool) {
		var in pick
		if r.ContentLength == 0 {
			return in, true
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return in, false
		}
		return in, true
	}
	mux.HandleFunc("GET /api/admin/calib/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Calibration(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/calib/{company}/fit", func(w http.ResponseWriter, r *http.Request) {
		in, ok := read(w, r)
		if !ok {
			return
		}
		v, err := o.FitCalibration(r.Context(), s.caller(r), via(r), r.PathValue("company"), in.Machines, in.Hours)
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/calib/{company}/apply", func(w http.ResponseWriter, r *http.Request) {
		in, ok := read(w, r)
		if !ok {
			return
		}
		v, err := o.ApplyCalibration(s.caller(r), via(r), r.PathValue("company"), in.Machines)
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/calib/{company}/revert", func(w http.ResponseWriter, r *http.Request) {
		in, ok := read(w, r)
		if !ok {
			return
		}
		v, err := o.RevertCalibration(s.caller(r), via(r), r.PathValue("company"), in.Machines)
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/calib/{company}/residuals", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ms := func(k string) time.Time {
			n, err := strconv.ParseInt(q.Get(k), 10, 64)
			if err != nil || n <= 0 {
				return time.Time{}
			}
			return time.UnixMilli(n)
		}
		step := time.Duration(0)
		if sec, err := strconv.Atoi(q.Get("step")); err == nil && sec > 0 {
			step = time.Duration(sec) * time.Second
		}
		v, err := o.CalibrationResiduals(s.caller(r), r.PathValue("company"), q.Get("machine"), q.Get("field"), ms("from"), ms("to"), step)
		answer(w, v, err)
	})
}
