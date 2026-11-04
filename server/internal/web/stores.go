package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"smartfactory/internal/ops"
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
	// ?status=open: the purchase lines (what is on order and when it is expected)
	mux.HandleFunc("GET /api/stores/{company}/purchases", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Purchases(s.caller(r), r.PathValue("company"), r.URL.Query().Get("status"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/stores/{company}/purchases", func(w http.ResponseWriter, r *http.Request) {
		var p stores.Purchase
		if read(w, r, &p) {
			v, err := o.SavePurchase(s.caller(r), via(r), r.PathValue("company"), p)
			answer(w, v, err)
		}
	})
	// {"qty": what came (the rest by default), "heat", "mill", "cert" for bar}
	mux.HandleFunc("POST /api/stores/{company}/purchases/{id}/receive", func(w http.ResponseWriter, r *http.Request) {
		var in stores.MoveInput
		if r.ContentLength == 0 || read(w, r, &in) {
			v, err := o.ReceivePurchase(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"), in)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("DELETE /api/stores/{company}/purchases/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.CancelPurchase(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
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
	// {bom?, drawing?, why?}; an empty body signs off without a BOM
	mux.HandleFunc("POST /api/rnd/{company}/{id}/release", func(w http.ResponseWriter, r *http.Request) {
		var so stores.SignOff
		if r.ContentLength != 0 && !read(w, r, &so) {
			return
		}
		v, err := o.ReleaseProject(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"), so)
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/rnd/{company}/{id}/bom", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.DraftProjectBOM(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
		answer(w, v, err)
	})

	// BOMs (/api/bom/{company}): part numbers and their revisions, change records, explosion,
	// where a component is used, and how the open orders' materials stand
	mux.HandleFunc("GET /api/bom/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.BOMOverview(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/bom/{company}", func(w http.ResponseWriter, r *http.Request) {
		var b stores.BOM
		if read(w, r, &b) {
			v, err := o.SaveBOM(s.caller(r), via(r), r.PathValue("company"), b)
			answer(w, v, err)
		}
	})
	// {part, name, rev?, drawing?, file, data (base64)}: an Excel or CSV sheet as a new draft
	mux.HandleFunc("POST /api/bom/{company}/import", func(w http.ResponseWriter, r *http.Request) {
		var in ops.BOMImport
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12<<20)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对（文件最大 8 MB）："+err.Error())
			return
		}
		v, err := o.ImportBOM(s.caller(r), via(r), r.PathValue("company"), in)
		answer(w, v, err)
	})
	// ?part=&rev=&qty=
	mux.HandleFunc("GET /api/bom/{company}/explode", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		qty, _ := strconv.ParseFloat(q.Get("qty"), 64)
		v, err := o.ExplodeBOM(s.caller(r), r.PathValue("company"), q.Get("part"), q.Get("rev"), qty)
		answer(w, v, err)
	})
	// ?q= a code, name, grade or spec
	mux.HandleFunc("GET /api/bom/{company}/where", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.WhereUsed(s.caller(r), r.PathValue("company"), r.URL.Query().Get("q"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/bom/{company}/kitting", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Kitting(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/bom/{company}/parts/{part}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.BOMPart(s.caller(r), r.PathValue("company"), r.PathValue("part"))
		answer(w, v, err)
	})
	mux.HandleFunc("PUT /api/bom/{company}/{id}", func(w http.ResponseWriter, r *http.Request) {
		var b stores.BOM
		if read(w, r, &b) {
			b.ID = r.PathValue("id")
			v, err := o.SaveBOM(s.caller(r), via(r), r.PathValue("company"), b)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("DELETE /api/bom/{company}/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.DeleteBOM(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/bom/{company}/{id}/copy", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.CopyBOM(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
		answer(w, v, err)
	})
	// {why, effective?, drawing?}
	mux.HandleFunc("POST /api/bom/{company}/{id}/release", func(w http.ResponseWriter, r *http.Request) {
		var rel stores.Release
		if read(w, r, &rel) {
			v, err := o.ReleaseBOM(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"), rel)
			answer(w, v, err)
		}
	})
	mux.HandleFunc("POST /api/bom/{company}/{id}/retire", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.RetireBOM(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id"))
		answer(w, v, err)
	})
}
