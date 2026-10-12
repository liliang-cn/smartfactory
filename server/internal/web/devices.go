package web

import (
	"encoding/json"
	"net/http"

	"smartfactory/internal/ops"
)

// Devices on the embedded broker (/api/admin/devices/{company}): each registered with a token or
// a certificate of its own, shown once, and held to its own assets' topics; administrators
// register, renew and revoke them.
func (s *Server) deviceRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	mux.HandleFunc("GET /api/admin/devices/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.DeviceOverview(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/devices/{company}", func(w http.ResponseWriter, r *http.Request) {
		var in ops.DeviceInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return
		}
		v, err := o.AddDevice(s.caller(r), via(r), r.PathValue("company"), in)
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/devices/{company}/{id}/renew", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.RenewDevice(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
		answer(w, v, err)
	})
	mux.HandleFunc("DELETE /api/admin/devices/{company}/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.RevokeDevice(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
		answer(w, v, err)
	})
}
