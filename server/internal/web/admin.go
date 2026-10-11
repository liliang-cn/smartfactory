package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"smartfactory/internal/agents"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/mcptools"
)

// The console (/admin) where administrators build what the twin shows: companies with their
// plants and lines, industry packs, and who may do what. An administrator of some companies
// manages those; one of every company also creates new companies and industry packs.
func (s *Server) adminRoutes(mux *http.ServeMux) {
	// admin answers 401 without a token and 403 for anyone who administers nothing
	admin := func(w http.ResponseWriter, r *http.Request) (auth.Who, bool) {
		who := s.caller(r)
		if who.Key == "" {
			writeError(w, http.StatusUnauthorized, "用管理员令牌登录")
			return who, false
		}
		if who.Role != auth.Admin {
			writeError(w, http.StatusForbidden, who.Name+"的令牌是"+who.Role.Label()+"，后台要管理员令牌")
			return who, false
		}
		return who, true
	}
	read := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return false
		}
		return true
	}
	// verdict answers a config the engine turned down with each problem it found
	verdict := func(w http.ResponseWriter, res engine.ConfigResult) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{"error": "配置没有通过校验：" + strings.Join(res.Errors, "；"), "errors": res.Errors, "where": res.Where})
	}

	mux.HandleFunc("GET /api/admin", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		var list []adminCompany
		if err := json.Unmarshal([]byte(s.Engine.List()), &list); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := []adminCompany{}
		for _, c := range list {
			if !who.In(c.ID) {
				continue
			}
			c.LiveTags = len(s.Hub.Last(c.ID, "", ""))
			c.Advisor = s.Advisors[c.ID] != ""
			out = append(out, c)
		}
		writeJSON(w, map[string]any{
			"me": who, "companies": out, "industries": json.RawMessage(s.Engine.IndustryList()),
			"issue": s.Keys.CanIssue(), "assistants": s.Agents != nil, "version": s.Version,
		})
	})

	mux.HandleFunc("POST /api/admin/companies", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		var cfg map[string]any
		if !read(w, r, &cfg) {
			return
		}
		id, _ := cfg["id"].(string)
		if !who.Can(id, auth.Admin) {
			writeError(w, http.StatusForbidden, "新建公司要所有公司的管理员令牌；你只管理 "+strings.Join(who.Companies, "、"))
			return
		}
		raw, _ := json.Marshal(cfg)
		res := s.Engine.CreateCompany(raw)
		var err error
		if !res.OK {
			err = errors.New(strings.Join(res.Errors, "；"))
		}
		name, _ := cfg["name"].(string)
		s.Audit.Record(who, "page", id, "company.create", id, name, err)
		if !res.OK {
			verdict(w, res)
			return
		}
		writeJSON(w, res.Detail)
	})

	mux.HandleFunc("DELETE /api/admin/companies/{id}", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !who.Can(id, auth.Admin) {
			writeError(w, http.StatusForbidden, "你不是这家公司的管理员")
			return
		}
		res := s.Engine.DeleteCompany(id)
		var err error
		if !res.OK {
			err = errors.New(strings.Join(res.Errors, "；"))
		}
		s.Audit.Record(who, "page", id, "company.delete", id, "", err)
		if !res.OK {
			verdict(w, res)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/admin/industries/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := admin(w, r); !ok {
			return
		}
		js := s.Engine.Industry(r.PathValue("id"))
		if js == "null" {
			writeError(w, http.StatusNotFound, "没有这个行业包")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(js))
	})

	mux.HandleFunc("POST /api/admin/industries", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		if !who.Everywhere() {
			writeError(w, http.StatusForbidden, "行业包是所有公司共用的，要所有公司的管理员令牌")
			return
		}
		var cfg map[string]any
		if !read(w, r, &cfg) {
			return
		}
		raw, _ := json.Marshal(cfg)
		res := s.Engine.CreateIndustry(raw)
		var err error
		if !res.OK {
			err = errors.New(strings.Join(res.Errors, "；"))
		}
		id, _ := cfg["id"].(string)
		name, _ := cfg["name"].(string)
		s.Audit.Record(who, "page", "*", "industry.create", id, name, err)
		if !res.OK {
			verdict(w, res)
			return
		}
		writeJSON(w, res.Detail)
	})

	mux.HandleFunc("GET /api/admin/tokens", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		list, err := s.Keys.Accounts(who)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, list)
	})

	mux.HandleFunc("POST /api/admin/tokens", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		var in struct {
			Name string `json:"name"`
			Role string `json:"role"`
			// Companies it covers; null is every company
			Companies []string `json:"companies"`
			Expires   string   `json:"expires"`
		}
		if !read(w, r, &in) {
			return
		}
		role, err := auth.ParseRole(in.Role)
		if err != nil {
			writeError(w, http.StatusBadRequest, "角色要是成员、操作员或管理员")
			return
		}
		known := s.Engine.CompanyIDs()
		for _, c := range in.Companies {
			if !slices.Contains(known, c) {
				writeError(w, http.StatusBadRequest, "没有公司 "+c)
				return
			}
		}
		token, acc, err := s.Keys.Issue(who, in.Name, role, in.Companies, in.Expires)
		if err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, auth.ErrNoStore) {
				code = http.StatusServiceUnavailable
			}
			writeError(w, code, err.Error())
			return
		}
		s.Audit.Record(who, "page", scopeOf(acc.Companies), "token.issue", acc.Key+" "+acc.Name, acc.Role.String(), nil)
		writeJSON(w, map[string]any{"token": token, "account": acc})
	})

	mux.HandleFunc("DELETE /api/admin/tokens/{key}", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		acc, err := s.Keys.Revoke(who, r.PathValue("key"))
		if acc.Key == "" {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		s.Audit.Record(who, "page", scopeOf(acc.Companies), "token.revoke", acc.Key+" "+acc.Name, acc.Role.String(), err)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// the scene architect: a description in, a checked company config out (nothing is created)
	mux.HandleFunc("POST /api/admin/compose", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		if s.Agents == nil || !s.Agents.Ready() {
			writeError(w, http.StatusServiceUnavailable, agents.ErrNoModel.Error())
			return
		}
		var in struct {
			Brief string          `json:"brief"`
			Base  json.RawMessage `json:"base"`
		}
		if !read(w, r, &in) {
			return
		}
		in.Brief = strings.TrimSpace(in.Brief)
		if in.Brief == "" || utf8.RuneCountInString(in.Brief) > 4000 {
			writeError(w, http.StatusBadRequest, "描述要 1～4000 字")
			return
		}
		if _, busy := agentBusy.LoadOrStore("compose:"+who.Key, true); busy {
			writeError(w, http.StatusTooManyRequests, "上一次还没搭完")
			return
		}
		defer agentBusy.Delete("compose:" + who.Key)
		send := stream(w)
		if err := s.Agents.Compose(r.Context(), who, in.Brief, in.Base, mcptools.SceneGuide(), send); err != nil {
			send(agents.Event{Kind: "error", Text: err.Error()})
		}
	})

	// every company's changes, or one company's, for the console
	mux.HandleFunc("GET /api/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		who, ok := admin(w, r)
		if !ok {
			return
		}
		q := r.URL.Query()
		company := q.Get("company")
		if company == "" && !who.Everywhere() {
			writeError(w, http.StatusForbidden, "看所有公司的记录要所有公司的管理员令牌，选一家公司看")
			return
		}
		if company != "" && company != "*" && !who.In(company) {
			writeError(w, http.StatusForbidden, "你的令牌不包括这家公司")
			return
		}
		if company == "*" && !who.Everywhere() {
			writeError(w, http.StatusForbidden, "共用的改动（行业包、全局令牌）要所有公司的管理员令牌")
			return
		}
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		list, err := s.Audit.List(r.Context(), company, before, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, list)
	})
}

// adminCompany is a company as the console lists it.
type adminCompany struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Short    string `json:"short"`
	Industry string `json:"industry"`
	// Builtin companies ship with the app; the others were created in the console or over MCP.
	Builtin bool `json:"builtin"`
	Plants  []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Hall     string `json:"hall"`
		Site     string `json:"site"`
		Lines    int    `json:"lines"`
		Machines int    `json:"machines"`
	} `json:"plants"`
	LiveTags int  `json:"liveTags"`
	Advisor  bool `json:"advisor"`
}

// scopeOf is the company an account's audit entry belongs to: its only company, or "*".
func scopeOf(companies []string) string {
	if len(companies) == 1 {
		return companies[0]
	}
	return "*"
}
