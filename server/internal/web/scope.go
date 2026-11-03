package web

import (
	"encoding/json"
	"net/http"

	"smartfactory/internal/agents"
	"smartfactory/internal/ops"
	"smartfactory/internal/scoped"
)

// Changing one part of a built company (/api/admin/companies/{id}/scope…): the part picked with
// the config to change it in, what a change would do, saving it, the AI's proposal, and undoing
// the last change.
func (s *Server) scopeRoutes(mux *http.ServeMux) {
	o := s.ops()
	read := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return false
		}
		return true
	}
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	// ?kind=line&plant=p1&line=L2 (area=<code>, asset=<tag>)
	mux.HandleFunc("GET /api/admin/companies/{id}/scope", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		n := scoped.Node{Kind: q.Get("kind"), Plant: q.Get("plant"), Area: q.Get("area"), Line: q.Get("line"), Asset: q.Get("asset")}
		v, err := o.Scope(s.caller(r), r.PathValue("id"), n)
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/companies/{id}/scope/preview", func(w http.ResponseWriter, r *http.Request) {
		var in ops.ScopeRequest
		if !read(w, r, &in) {
			return
		}
		v, err := o.ScopePreview(s.caller(r), r.PathValue("id"), in)
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/companies/{id}/scope/apply", func(w http.ResponseWriter, r *http.Request) {
		var in ops.ScopeRequest
		if !read(w, r, &in) {
			return
		}
		v, err := o.ScopeApply(s.caller(r), via(r), r.PathValue("id"), in)
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/companies/{id}/undo", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Undo(s.caller(r), via(r), r.PathValue("id"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/companies/{id}/scope/ai", func(w http.ResponseWriter, r *http.Request) {
		who := s.caller(r)
		var in struct {
			Node  scoped.Node `json:"node"`
			Brief string      `json:"brief"`
		}
		if !read(w, r, &in) {
			return
		}
		if err := ops.Admin(who); err != nil {
			writeOpError(w, err)
			return
		}
		if _, busy := agentBusy.LoadOrStore("scope:"+who.Key, true); busy {
			writeError(w, http.StatusTooManyRequests, "上一次还没改完")
			return
		}
		defer agentBusy.Delete("scope:" + who.Key)
		var send func(agents.Event)
		emit := func(e agents.Event) {
			if send == nil {
				send = stream(w)
			}
			send(e)
		}
		if err := o.ScopeAI(r.Context(), who, r.PathValue("id"), in.Node, in.Brief, emit); err != nil {
			if send == nil {
				writeOpError(w, err)
				return
			}
			send(agents.Event{Kind: "error", Text: err.Error()})
		}
	})
}
