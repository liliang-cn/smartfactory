package web

import "net/http"

// The plant breakdown (/api/pbs/{company}): company › plant › area › line › machine, with how many
// machines under each are in which state now, for the console's structure view.
func (s *Server) pbsRoutes(mux *http.ServeMux) {
	o := s.ops()
	mux.HandleFunc("GET /api/pbs/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.PBS(s.caller(r), r.PathValue("company"))
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	})
}
