package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"smartfactory/internal/agents"
)

// one answer at a time per person: a run is a minute of model calls
var agentBusy sync.Map

// who is the person asking: operators share one identity; a visitor is the id their browser
// keeps (X-SF-Visitor), or their address.
func (s *Server) who(r *http.Request) (owner string, c agents.Caller) {
	if s.operator(r) {
		return "operator", agents.Caller{Operator: true, Name: "运营人员"}
	}
	v := strings.TrimSpace(r.Header.Get("X-SF-Visitor"))
	if v == "" || len(v) > 64 {
		v = clientIP(r)
	}
	return "visitor:" + v, agents.Caller{Name: "访客"}
}

func (s *Server) agentRoutes(mux *http.ServeMux) {
	a := s.Agents
	// company resolves the company of the path, seeding its first assistants
	company := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		if a == nil {
			writeError(w, http.StatusServiceUnavailable, "这台服务器没有开启助手")
			return "", false
		}
		c := r.PathValue("company")
		if !slices.Contains(s.Engine.CompanyIDs(), c) {
			writeError(w, http.StatusNotFound, "找不到这家公司")
			return "", false
		}
		if err := a.Ensure(c); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return "", false
		}
		return c, true
	}
	operator := func(w http.ResponseWriter, r *http.Request) bool {
		if !s.operator(r) {
			writeError(w, http.StatusForbidden, "需要运营令牌")
			return false
		}
		return true
	}
	found := func(w http.ResponseWriter, err error, what string) bool {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "没有这个"+what)
			return false
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return false
		}
		return true
	}
	read := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(v); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return false
		}
		return true
	}

	mux.HandleFunc("GET /api/agents/{company}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		list, err := a.Store.Agents(c)
		if !found(w, err, "公司") {
			return
		}
		writeJSON(w, map[string]any{"ready": a.Ready(), "operator": s.operator(r), "templates": agents.Templates, "agents": list,
			"severities": agents.Severities, "statuses": agents.Statuses})
	})
	type agentIn struct {
		Kind         string `json:"kind"`
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
	}
	valid := func(w http.ResponseWriter, in *agentIn) bool {
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || utf8.RuneCountInString(in.Name) > 20 {
			writeError(w, http.StatusBadRequest, "名称要 1～20 个字")
			return false
		}
		if utf8.RuneCountInString(in.Instructions) > 4000 {
			writeError(w, http.StatusBadRequest, "要求最多 4000 字")
			return false
		}
		return true
	}
	mux.HandleFunc("POST /api/agents/{company}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok || !operator(w, r) {
			return
		}
		var in agentIn
		if !read(w, r, &in) || !valid(w, &in) {
			return
		}
		if !slices.ContainsFunc(agents.Templates, func(t agents.Template) bool { return t.Kind == in.Kind }) {
			writeError(w, http.StatusBadRequest, "没有这种助手")
			return
		}
		ag, err := a.Store.CreateAgent(c, in.Kind, in.Name, strings.TrimSpace(in.Instructions))
		if found(w, err, "助手") {
			writeJSON(w, ag)
		}
	})
	mux.HandleFunc("PUT /api/agents/{company}/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok || !operator(w, r) {
			return
		}
		var in agentIn
		if !read(w, r, &in) || !valid(w, &in) {
			return
		}
		ag, err := a.Store.UpdateAgent(c, r.PathValue("id"), in.Name, strings.TrimSpace(in.Instructions))
		if found(w, err, "助手") {
			a.Forget(ag.ID)
			writeJSON(w, ag)
		}
	})
	mux.HandleFunc("DELETE /api/agents/{company}/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok || !operator(w, r) {
			return
		}
		if found(w, a.Store.DeleteAgent(c, r.PathValue("id")), "助手") {
			a.Forget(r.PathValue("id"))
			writeJSON(w, map[string]bool{"ok": true})
		}
	})

	// conversations are the asker's own
	mux.HandleFunc("GET /api/agents/{company}/{id}/conversations", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		owner, _ := s.who(r)
		list, err := a.Store.Conversations(c, r.PathValue("id"), owner)
		if found(w, err, "助手") {
			writeJSON(w, list)
		}
	})
	mux.HandleFunc("GET /api/agents/{company}/{id}/conversations/{conv}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		owner, _ := s.who(r)
		conv, err := a.Store.Conversation(c, r.PathValue("conv"), owner)
		if err == nil && conv.Agent != r.PathValue("id") {
			err = agents.ErrNotFound
		}
		if !found(w, err, "对话") {
			return
		}
		msgs, err := a.Store.Messages(conv.ID)
		if found(w, err, "对话") {
			writeJSON(w, map[string]any{"conversation": conv, "messages": msgs})
		}
	})
	mux.HandleFunc("DELETE /api/agents/{company}/{id}/conversations/{conv}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		owner, _ := s.who(r)
		if found(w, a.Store.DeleteConversation(c, r.PathValue("conv"), owner), "对话") {
			writeJSON(w, map[string]bool{"ok": true})
		}
	})
	mux.HandleFunc("POST /api/agents/{company}/{id}/ask", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		var in struct {
			Conversation string `json:"conversation"`
			Text         string `json:"text"`
		}
		if !read(w, r, &in) {
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		if in.Text == "" || utf8.RuneCountInString(in.Text) > 4000 {
			writeError(w, http.StatusBadRequest, "问题要 1～4000 字")
			return
		}
		ag, err := a.Store.Agent(c, r.PathValue("id"))
		if !found(w, err, "助手") {
			return
		}
		if !a.Ready() {
			writeError(w, http.StatusServiceUnavailable, agents.ErrNoModel.Error())
			return
		}
		owner, who := s.who(r)
		var conv agents.Conversation
		if in.Conversation != "" {
			conv, err = a.Store.Conversation(c, in.Conversation, owner)
			if !found(w, err, "对话") {
				return
			}
			if conv.Agent != ag.ID {
				writeError(w, http.StatusBadRequest, "这段对话不属于这个助手")
				return
			}
		}
		if _, busy := agentBusy.LoadOrStore(owner, true); busy {
			writeError(w, http.StatusTooManyRequests, "上一个问题还没答完")
			return
		}
		defer agentBusy.Delete(owner)
		if conv.ID == "" {
			title := []rune(strings.Join(strings.Fields(in.Text), " "))
			conv, err = a.Store.CreateConversation(c, ag.ID, owner, string(title[:min(len(title), 40)]))
			if !found(w, err, "助手") {
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		fl, _ := w.(http.Flusher)
		send := func(e agents.Event) {
			b, _ := json.Marshal(e)
			w.Write([]byte("data: "))
			w.Write(b)
			w.Write([]byte("\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		send(agents.Event{Kind: "conversation", ID: conv.ID, Title: conv.Title})
		if err := a.Ask(r.Context(), ag, conv.ID, in.Text, who, send); err != nil {
			send(agents.Event{Kind: "error", Text: err.Error()})
		}
	})

	// what the assistants work on: documents (operators add and remove them), notes, tickets
	mux.HandleFunc("GET /api/library/{company}/docs", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		list, err := a.Store.Docs(c)
		if found(w, err, "公司") {
			writeJSON(w, list)
		}
	})
	mux.HandleFunc("GET /api/library/{company}/docs/{doc}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		d, body, err := a.Store.DocBody(c, r.PathValue("doc"))
		if found(w, err, "文档") {
			writeJSON(w, map[string]any{"doc": d, "body": body})
		}
	})
	mux.HandleFunc("POST /api/library/{company}/docs", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok || !operator(w, r) {
			return
		}
		var in struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if !read(w, r, &in) {
			return
		}
		in.Title = strings.TrimSpace(in.Title)
		if in.Title == "" || utf8.RuneCountInString(in.Title) > 60 {
			writeError(w, http.StatusBadRequest, "标题要 1～60 个字")
			return
		}
		if strings.TrimSpace(in.Body) == "" || !utf8.ValidString(in.Body) {
			writeError(w, http.StatusBadRequest, "文档是空的，或者不是 UTF-8 文本")
			return
		}
		d, err := a.Store.AddDoc(c, in.Title, in.Body, "运营人员")
		if found(w, err, "公司") {
			writeJSON(w, d)
		}
	})
	mux.HandleFunc("DELETE /api/library/{company}/docs/{doc}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok || !operator(w, r) {
			return
		}
		if found(w, a.Store.DeleteDoc(c, r.PathValue("doc")), "文档") {
			writeJSON(w, map[string]bool{"ok": true})
		}
	})
	mux.HandleFunc("GET /api/library/{company}/notes", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		list, err := a.Store.Notes(c)
		if found(w, err, "公司") {
			writeJSON(w, list)
		}
	})
	mux.HandleFunc("DELETE /api/library/{company}/notes/{note}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok || !operator(w, r) {
			return
		}
		if found(w, a.Store.DeleteNote(c, r.PathValue("note")), "笔记") {
			writeJSON(w, map[string]bool{"ok": true})
		}
	})
	mux.HandleFunc("GET /api/library/{company}/tickets", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		list, err := a.Store.Tickets(c)
		if found(w, err, "公司") {
			writeJSON(w, list)
		}
	})
	mux.HandleFunc("PATCH /api/library/{company}/tickets/{ticket}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok || !operator(w, r) {
			return
		}
		var in struct {
			Status string `json:"status"`
		}
		if !read(w, r, &in) {
			return
		}
		if !slices.Contains(agents.Statuses, in.Status) {
			writeError(w, http.StatusBadRequest, "没有这个状态")
			return
		}
		t, err := a.Store.UpdateTicket(c, r.PathValue("ticket"), in.Status, "", "运营人员", "")
		if found(w, err, "工单") {
			writeJSON(w, t)
		}
	})
}
