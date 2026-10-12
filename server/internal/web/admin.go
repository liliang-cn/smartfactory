package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"smartfactory/internal/agents"
	"smartfactory/internal/ops"
	"smartfactory/internal/templates"
)

// The console's API (/api/admin, /api/knowledge): each route reads the request, calls the
// operation in package ops — the same one the command line and the console agent call — and
// writes its answer.
func (s *Server) adminRoutes(mux *http.ServeMux) {
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
	done := map[string]bool{"ok": true}

	mux.HandleFunc("GET /api/admin", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Overview(s.caller(r))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/admin/companies/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.CompanyConfig(s.caller(r), r.PathValue("id"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/companies", func(w http.ResponseWriter, r *http.Request) {
		var cfg map[string]any
		if !read(w, r, &cfg) {
			return
		}
		// ?check=1: what creating it would say, nothing created
		if r.URL.Query().Get("check") != "" {
			v, err := o.CheckCompany(s.caller(r), cfg)
			answer(w, v, err)
			return
		}
		v, err := o.CreateCompany(s.caller(r), via(r), cfg)
		answer(w, v, err)
	})
	mux.HandleFunc("DELETE /api/admin/companies/{id}", func(w http.ResponseWriter, r *http.Request) {
		answer(w, done, o.DeleteCompany(s.caller(r), via(r), r.PathValue("id")))
	})
	mux.HandleFunc("GET /api/admin/industries/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Industry(s.caller(r), r.PathValue("id"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/industries", func(w http.ResponseWriter, r *http.Request) {
		var cfg map[string]any
		if !read(w, r, &cfg) {
			return
		}
		v, err := o.CreateIndustry(s.caller(r), via(r), cfg)
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/admin/tokens", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Accounts(s.caller(r))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/tokens", func(w http.ResponseWriter, r *http.Request) {
		var in ops.IssueRequest
		if !read(w, r, &in) {
			return
		}
		v, err := o.Issue(s.caller(r), via(r), in)
		answer(w, v, err)
	})
	mux.HandleFunc("DELETE /api/admin/tokens/{key}", func(w http.ResponseWriter, r *http.Request) {
		answer(w, done, o.Revoke(s.caller(r), via(r), r.PathValue("key")))
	})
	mux.HandleFunc("GET /api/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		v, err := o.Activity(r.Context(), s.caller(r), q.Get("company"), before, limit)
		answer(w, v, err)
	})

	// the scene architect: a description in, a checked company config out (nothing is created)
	mux.HandleFunc("POST /api/admin/compose", func(w http.ResponseWriter, r *http.Request) {
		who := s.caller(r)
		var in struct {
			Kind  string          `json:"kind"`
			Brief string          `json:"brief"`
			Base  json.RawMessage `json:"base"`
		}
		if !read(w, r, &in) {
			return
		}
		if err := ops.Admin(who); err != nil {
			writeOpError(w, err)
			return
		}
		if _, busy := agentBusy.LoadOrStore("compose:"+who.Key, true); busy {
			writeError(w, http.StatusTooManyRequests, "上一次还没搭完")
			return
		}
		defer agentBusy.Delete("compose:" + who.Key)
		var send func(agents.Event)
		emit := func(e agents.Event) {
			if send == nil {
				send = stream(w)
			}
			send(e)
		}
		if err := o.Compose(r.Context(), who, in.Kind, in.Brief, in.Base, emit); err != nil {
			if send == nil {
				writeOpError(w, err)
				return
			}
			send(agents.Event{Kind: "error", Text: err.Error()})
		}
	})

	// the console agent: a conversation in words; changes come back as actions to confirm
	mux.HandleFunc("POST /api/admin/agent", func(w http.ResponseWriter, r *http.Request) {
		who := s.caller(r)
		var in struct {
			Conversation string `json:"conversation"`
			Text         string `json:"text"`
		}
		if !read(w, r, &in) {
			return
		}
		if err := ops.Admin(who); err != nil {
			writeOpError(w, err)
			return
		}
		if _, err := uuid.Parse(in.Conversation); err != nil {
			in.Conversation = uuid.NewString()
		}
		if _, busy := agentBusy.LoadOrStore("console:"+who.Key, true); busy {
			writeError(w, http.StatusTooManyRequests, "上一句还没答完")
			return
		}
		defer agentBusy.Delete("console:" + who.Key)
		var send func(agents.Event)
		emit := func(e agents.Event) {
			if send == nil {
				send = stream(w)
				send(agents.Event{Kind: "conversation", ID: in.Conversation})
			}
			send(e)
		}
		// the conversation is the person's own: agent-go keeps it under their token
		if err := o.ConsoleAgent(r.Context(), who, who.Key+":"+in.Conversation, in.Text, emit); err != nil {
			if send == nil {
				writeOpError(w, err)
				return
			}
			send(agents.Event{Kind: "error", Text: err.Error()})
		}
	})
	// carry out an action the console agent proposed (or any action), as the person confirming it
	mux.HandleFunc("POST /api/admin/actions", func(w http.ResponseWriter, r *http.Request) {
		var a ops.Action
		if !read(w, r, &a) {
			return
		}
		v, err := o.Do(r.Context(), s.caller(r), via(r), a)
		answer(w, v, err)
	})

	// templates: built in, and saved or imported here
	mux.HandleFunc("GET /api/admin/templates", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Templates(r.Context(), s.caller(r))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/admin/templates/{source}/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Template(r.Context(), s.caller(r), r.PathValue("source"), r.PathValue("id"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/templates", func(w http.ResponseWriter, r *http.Request) {
		var in ops.SaveRequest
		if !read(w, r, &in) {
			return
		}
		v, err := o.SaveTemplate(s.caller(r), via(r), in)
		answer(w, v, err)
	})
	mux.HandleFunc("DELETE /api/admin/templates/{id}", func(w http.ResponseWriter, r *http.Request) {
		answer(w, done, o.DeleteTemplate(s.caller(r), via(r), r.PathValue("id")))
	})
	// a template exported from another installation
	mux.HandleFunc("POST /api/admin/templates/import", func(w http.ResponseWriter, r *http.Request) {
		var t templates.Template
		if !read(w, r, &t) {
			return
		}
		v, err := o.ImportTemplate(s.caller(r), via(r), &t)
		answer(w, v, err)
	})

	// a company's knowledge base
	mux.HandleFunc("GET /api/knowledge/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.KnowledgeStats(r.Context(), s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/knowledge/{company}/search", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		v, err := o.KnowledgeSearch(r.Context(), s.caller(r), r.PathValue("company"), r.URL.Query().Get("q"), max(limit, 6))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/knowledge/{company}/entity", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.KnowledgeEntity(r.Context(), s.caller(r), r.PathValue("company"), r.URL.Query().Get("name"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/knowledge/{company}/graph", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.KnowledgeGraph(r.Context(), s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/knowledge/{company}/rebuild", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.KnowledgeRebuild(r.Context(), s.caller(r), via(r), r.PathValue("company"))
		answer(w, v, err)
	})
}

// via is where a console request came from: the command line says so (X-SF-Via: cli), the
// rest are the page.
func via(r *http.Request) string {
	if r.Header.Get("X-SF-Via") == "cli" {
		return "cli"
	}
	return "page"
}

// writeOpError answers with what an operation said about why it was refused.
func writeOpError(w http.ResponseWriter, err error) {
	var e *ops.Error
	if !errors.As(err, &e) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Code)
	body := map[string]any{"error": e.Msg}
	if len(e.Problems) > 0 {
		body["errors"] = e.Problems
		body["where"] = e.Where
	}
	json.NewEncoder(w).Encode(body)
}

// ops is the operations behind the console, built from the server's parts unless given.
func (s *Server) ops() *ops.Ops {
	if s.Ops == nil {
		s.Ops = &ops.Ops{Engine: s.Engine, Keys: s.Keys, Audit: s.Audit, Agents: s.Agents, KB: s.Knowledge, Hub: s.Hub, Advisors: s.Advisors, Version: s.Version,
			Saved: s.Saved, Production: s.Production, Calib: s.Calib, Commands: s.Guard, Devices: s.Devices, MQTT: s.MQTT, Checks: s.Checks}
	}
	return s.Ops
}
