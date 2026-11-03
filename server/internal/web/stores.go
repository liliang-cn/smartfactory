package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"smartfactory/internal/stores"
)

// A company's stores (/api/stores/{company}): items and their receipts, issues and counts, heats
// of bar and where they went; its dies (/api/dies/{company}); its new parts in development
// (/api/rnd/{company}).
func (s *Server) storeRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	read := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/stores/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.StoresOverview(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/stores/{company}/categories", func(w http.ResponseWriter, r *http.Request) {
		var c stores.Category
		if read(w, r, &c) {
			v, err := o.SaveStoreCategory(s.caller(r), via(r), r.PathValue("company"), c)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("POST /api/stores/{company}/items", func(w http.ResponseWriter, r *http.Request) {
		var it stores.Item
		if read(w, r, &it) {
			v, err := o.SaveStoreItem(s.caller(r), via(r), r.PathValue("company"), it)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("POST /api/stores/{company}/moves", func(w http.ResponseWriter, r *http.Request) {
		var in stores.MoveInput
		if read(w, r, &in) {
			v, err := o.RecordMove(s.caller(r), via(r), r.PathValue("company"), in)
			answer(w, v, err)
		}
	})
	// ?item=&heat=&limit=
	mux.HandleFunc("GET /api/stores/{company}/moves", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		n, _ := strconv.Atoi(q.Get("limit"))
		v, err := o.StoreMoves(s.caller(r), r.PathValue("company"), q.Get("item"), q.Get("heat"), n)
		answer(w, v, err)
	})
	// ?q= a heat number or an order number
	mux.HandleFunc("GET /api/stores/{company}/trace", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.TraceHeat(s.caller(r), r.PathValue("company"), r.URL.Query().Get("q"))
		answer(w, v, err)
	})

	mux.HandleFunc("GET /api/dies/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.DiesOverview(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/dies/{company}", func(w http.ResponseWriter, r *http.Request) {
		var d stores.Die
		if read(w, r, &d) {
			v, err := o.SaveDie(s.caller(r), via(r), r.PathValue("company"), d, true)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("GET /api/dies/{company}/on/{plant}/{asset}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.DieOn(s.caller(r), r.PathValue("company"), r.PathValue("plant"), r.PathValue("asset"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/dies/{company}/{no}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.DieDetail(s.caller(r), r.PathValue("company"), r.PathValue("no"))
		answer(w, v, err)
	})
	mux.HandleFunc("PUT /api/dies/{company}/{no}", func(w http.ResponseWriter, r *http.Request) {
		var d stores.Die
		if read(w, r, &d) {
			d.No = r.PathValue("no")
			v, err := o.SaveDie(s.caller(r), via(r), r.PathValue("company"), d, false)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("POST /api/dies/{company}/{no}/act", func(w http.ResponseWriter, r *http.Request) {
		var a stores.DieAction
		if read(w, r, &a) {
			v, err := o.DieAct(s.caller(r), via(r), r.PathValue("company"), r.PathValue("no"), a)
			answer(w, v, err)
		}
	})

	mux.HandleFunc("GET /api/rnd/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.RnDOverview(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/rnd/{company}", func(w http.ResponseWriter, r *http.Request) {
		var p stores.Project
		if read(w, r, &p) {
			v, err := o.SaveProject(s.caller(r), via(r), r.PathValue("company"), p)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("POST /api/rnd/{company}/{id}/trials", func(w http.ResponseWriter, r *http.Request) {
		var t stores.Trial
		if read(w, r, &t) {
			t.Project = r.PathValue("id")
			v, err := o.AddTrial(s.caller(r), via(r), r.PathValue("company"), t)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("POST /api/rnd/{company}/{id}/release", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.ReleaseProject(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
		answer(w, v, err)
	})
}
