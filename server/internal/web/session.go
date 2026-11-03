package web

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"smartfactory/internal/auth"
)

// Settings kept with the accounts.
const (
	// SettingMustLogin: "1" when the twin, the check page and the live feed need a sign-in.
	SettingMustLogin = "must_login"
)

// MustLogin reports whether visitors are kept out: only people signed in, and systems with a
// token, see the factory.
func (s *Server) MustLogin() bool {
	st := s.Keys.Store()
	if st == nil {
		return false
	}
	v, _ := st.Setting(SettingMustLogin)
	return v == "1"
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求的内容读不懂")
		return false
	}
	return true
}

// firstCode is the installation code a new server wrote for its first administrator, "" when
// there is none (any more).
func (s *Server) firstCode() string {
	if s.FirstToken == "" {
		return ""
	}
	b, err := os.ReadFile(s.FirstToken)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

type authState struct {
	// Setup: nobody has an account yet, so the first administrator is set up now.
	Setup bool `json:"setup"`
	// Code: setting up needs the installation code (the file at CodeFile), since the page
	// was not opened on the server itself.
	Code     bool   `json:"code"`
	CodeFile string `json:"codeFile,omitempty"`
	// Tokens: the server has accounts kept nowhere (no data directory) or older tokens, so
	// a token can still sign in.
	MustLogin bool      `json:"mustLogin"`
	Me        *auth.Who `json:"me,omitempty"`
	// MustChange: the signed-in person's password was set by an administrator.
	MustChange bool `json:"mustChange,omitempty"`
	// Accounts: the server keeps accounts (it has a data directory).
	Accounts bool `json:"accounts"`
}

func (s *Server) authState(r *http.Request) authState {
	st := s.Keys.Store()
	out := authState{Accounts: st != nil, MustLogin: s.MustLogin()}
	if st != nil && !st.HasUsers() {
		out.Setup = true
		if code := s.firstCode(); code != "" {
			out.Code, out.CodeFile = !auth.Loopback(r), s.FirstToken
		} else if !auth.Loopback(r) {
			// an older installation: its administrator signs in with their token and makes accounts
			out.Setup = false
		}
	}
	if who := s.caller(r); who.Key != "" {
		out.Me = &who
		if who.User != "" && st != nil {
			if u, err := st.User(who.User); err == nil {
				out.MustChange = u.MustChange
			}
		}
	}
	return out
}

// sameSite reports whether a request that changes something came from this server's own pages.
// A browser marks where a request came from (Sec-Fetch-Site, Origin); a page elsewhere riding on
// the session cookie is refused.
func sameSite(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	return true
}

// gate is in front of every route: requests riding on a session cookie must come from these
// pages, and when the factory is private, a caller with neither a session nor a token sees only
// the sign-in.
func (s *Server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if unsafe && auth.Token(r) == "" && auth.SessionOf(r) != "" && !sameSite(r) {
			writeError(w, http.StatusForbidden, "这个请求不是从工作台页面发出的")
			return
		}
		if s.MustLogin() && needsSignIn(r.URL.Path) && s.caller(r).Key == "" {
			writeError(w, http.StatusUnauthorized, "要先登录")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// needsSignIn: the paths a private factory keeps from visitors. Pages and their assets load (they
// show the sign-in); the sign-in itself, what the server is, and MCP (its own token) stay open.
func needsSignIn(path string) bool {
	switch {
	case !strings.HasPrefix(path, "/api/"):
		return false
	case strings.HasPrefix(path, "/api/auth/"), path == "/api/site":
		return false
	}
	return true
}

func (s *Server) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.authState(r))
	})

	// the first administrator, on a server nobody has an account on yet
	mux.HandleFunc("POST /api/auth/setup", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code string `json:"code"`
			auth.NewUser
		}
		if !readJSON(w, r, &in) {
			return
		}
		st := s.Keys.Store()
		state := s.authState(r)
		if st == nil || !state.Setup {
			writeError(w, http.StatusConflict, "已经设置过管理员了，用账号登录")
			return
		}
		if state.Code {
			code := s.firstCode()
			if code == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(in.Code)), []byte(code)) != 1 {
				writeError(w, http.StatusForbidden, "安装码不对：在服务器上运行 sudo cat "+s.FirstToken+" 看")
				return
			}
		}
		in.Role = auth.Admin.String()
		in.Companies = nil
		u, err := st.CreateUser("设置", in.NewUser, false)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// the installation code has done its job
		if s.FirstToken != "" {
			os.Remove(s.FirstToken)
		}
		s.Keys.RevokeBootstrap(u.Name)
		id, _, err := s.Keys.SignIn(in.Login, in.Password, auth.ClientIP(r), r.UserAgent())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		auth.SetSession(w, r, id)
		who, _ := s.Keys.Session(id)
		s.Audit.Record(who, "page", "", "user.setup", u.Login, u.Name, nil)
		writeJSON(w, s.authStateFor(r, who))
	})

	mux.HandleFunc("POST /api/auth/signin", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Login    string `json:"login"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		id, u, err := s.Keys.SignIn(in.Login, in.Password, auth.ClientIP(r), r.UserAgent())
		switch {
		case errors.Is(err, auth.ErrLocked):
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		case errors.Is(err, auth.ErrNoStore):
			writeError(w, http.StatusServiceUnavailable, "这台服务器没有数据目录，只能用令牌登录")
			return
		case err != nil:
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		auth.SetSession(w, r, id)
		who, _ := s.Keys.Session(id)
		s.Audit.Record(who, "page", "", "user.signin", u.Login, "", nil)
		writeJSON(w, s.authStateFor(r, who))
	})

	mux.HandleFunc("POST /api/auth/signout", func(w http.ResponseWriter, r *http.Request) {
		s.Keys.SignOut(auth.SessionOf(r))
		auth.SetSession(w, r, "")
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/auth/password", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Old  string `json:"old"`
			Next string `json:"next"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		who := s.caller(r)
		st := s.Keys.Store()
		if who.User == "" || st == nil {
			writeError(w, http.StatusUnauthorized, "要先用账号登录")
			return
		}
		if err := st.ChangePassword(who, in.Old, in.Next, auth.SessionOf(r)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.Audit.Record(who, "page", "", "user.password", who.Name, "", nil)
		writeJSON(w, map[string]bool{"ok": true})
	})

	// the accounts of the people, for administrators
	mux.HandleFunc("GET /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		who := s.caller(r)
		if who.Role != auth.Admin {
			writeError(w, http.StatusForbidden, "只有管理员能管理账号")
			return
		}
		list, err := s.Keys.Store().Users(who)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"users": list, "mustLogin": s.MustLogin()})
	})
	mux.HandleFunc("POST /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		who := s.caller(r)
		var in auth.NewUser
		if !readJSON(w, r, &in) {
			return
		}
		cover := in.Companies
		if len(cover) == 0 {
			cover = nil
		}
		if who.Role != auth.Admin || !who.Manages(cover) {
			writeError(w, http.StatusForbidden, "只有管理员能建账号")
			return
		}
		u, err := s.Keys.Store().CreateUser(who.Name, in, true)
		s.Audit.Record(who, "page", "", "user.create", u.Login, u.Name+" "+u.Role.Label(), err)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, u)
	})
	mux.HandleFunc("PATCH /api/admin/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		who := s.caller(r)
		if who.Role != auth.Admin {
			writeError(w, http.StatusForbidden, "只有管理员能管理账号")
			return
		}
		var in auth.UserChange
		if !readJSON(w, r, &in) {
			return
		}
		u, err := s.Keys.Store().UpdateUser(who, r.PathValue("id"), in)
		what := []string{}
		if in.Disabled != nil {
			what = append(what, map[bool]string{true: "停用", false: "启用"}[*in.Disabled])
		}
		if in.Password != nil {
			what = append(what, "重设密码")
		}
		if in.Role != nil {
			what = append(what, "角色 "+*in.Role)
		}
		s.Audit.Record(who, "page", "", "user.update", u.Login, strings.Join(what, "，"), err)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, u)
	})
	mux.HandleFunc("PUT /api/admin/settings", func(w http.ResponseWriter, r *http.Request) {
		who := s.caller(r)
		if who.Role != auth.Admin || !who.Everywhere() {
			writeError(w, http.StatusForbidden, "只有管理员能改")
			return
		}
		var in struct {
			MustLogin *bool `json:"mustLogin"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if in.MustLogin != nil {
			v := map[bool]string{true: "1", false: "0"}[*in.MustLogin]
			err := s.Keys.Store().SetSetting(SettingMustLogin, v)
			s.Audit.Record(who, "page", "", "settings.must_login", "", map[bool]string{true: "必须登录", false: "访客可看"}[*in.MustLogin], err)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, map[string]bool{"mustLogin": s.MustLogin()})
	})
}

func (s *Server) authStateFor(r *http.Request, who auth.Who) authState {
	out := s.authState(r)
	out.Setup, out.Code, out.CodeFile = false, false, ""
	out.Me = &who
	if st := s.Keys.Store(); st != nil {
		if u, err := st.User(who.User); err == nil {
			out.MustChange = u.MustChange
		}
	}
	return out
}
