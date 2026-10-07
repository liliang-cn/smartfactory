package web

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Advisor is the consulting service (DataIntelligence) that reads one company's history. The
// twin talks to it for its viewers: they never need a login of their own there.
type Advisor struct {
	// API is the service's base URL, e.g. http://192.168.123.64:43610
	API string
	// Token is the identity viewers' questions and reads go out as (an analyst).
	Token string
	// Approver is the identity an operator's decisions go out as.
	Approver string
}

var advisorClient = &http.Client{Timeout: 0}

// one question at a time per viewer address: a run is minutes of model calls
var asking sync.Map

func (s *Server) advisorRoutes(mux *http.ServeMux) {
	pass := func(method, path string, write bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			a, ok := s.AdvisorAPI[r.PathValue("company")]
			if !ok || a.API == "" {
				http.NotFound(w, r)
				return
			}
			token := a.Token
			if write {
				if !s.operator(r) {
					http.Error(w, `{"error":"需要运营令牌"}`, http.StatusForbidden)
					return
				}
				token = a.Approver
			}
			target := a.API + strings.NewReplacer("{plan}", r.PathValue("plan"), "{verb}", r.PathValue("verb"), "{name}", r.PathValue("name"), "{id}", r.PathValue("id")).Replace(path)
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			req, err := http.NewRequestWithContext(r.Context(), method, target, r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			res, err := advisorClient.Do(req)
			if err != nil {
				http.Error(w, `{"error":"顾问服务不可用"}`, http.StatusBadGateway)
				return
			}
			defer res.Body.Close()
			for _, h := range []string{"Content-Type", "Cache-Control"} {
				if v := res.Header.Get(h); v != "" {
					w.Header().Set(h, v)
				}
			}
			w.WriteHeader(res.StatusCode)
			// stream as it comes: the copilot sends its loop as server-sent events
			fl, _ := w.(http.Flusher)
			buf := make([]byte, 16<<10)
			for {
				n, err := res.Body.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
					if fl != nil {
						fl.Flush()
					}
				}
				if err != nil {
					if err != io.EOF {
						s.Log.Warn("advisor proxy", "path", path, "err", err)
					}
					return
				}
			}
		}
	}
	mux.HandleFunc("GET /api/advisor/{company}/metrics", pass("GET", "/v1/metrics", false))
	mux.HandleFunc("GET /api/advisor/{company}/dimensions", pass("GET", "/v1/dimensions", false))
	mux.HandleFunc("GET /api/advisor/{company}/consult", pass("GET", "/v1/consult", false))
	mux.HandleFunc("POST /api/advisor/{company}/query", pass("POST", "/v1/query", false))
	ask := pass("POST", "/v1/copilot/stream", false)
	mux.HandleFunc("POST /api/advisor/{company}/ask", func(w http.ResponseWriter, r *http.Request) {
		who := clientIP(r)
		if _, busy := asking.LoadOrStore(who, time.Now()); busy {
			http.Error(w, `{"error":"上一个问题还没答完"}`, http.StatusTooManyRequests)
			return
		}
		defer asking.Delete(who)
		ask(w, r)
	})
	// standing reviews (the group's weekly diagnosis): reports for everyone, starting one for operators
	mux.HandleFunc("GET /api/advisor/{company}/reviews", pass("GET", "/v1/reviews", false))
	mux.HandleFunc("GET /api/advisor/{company}/reviews/runs/{id}", pass("GET", "/v1/reviews/runs/{id}", false))
	mux.HandleFunc("POST /api/advisor/{company}/reviews/{name}/run", pass("POST", "/v1/reviews/{name}/run", true))
	mux.HandleFunc("POST /api/advisor/{company}/plans/{plan}/{verb}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("verb") {
		case "adopt", "reject", "measure", "accept":
			pass("POST", "/v1/consult/plans/{plan}/{verb}", true)(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	if i := strings.LastIndex(r.RemoteAddr, ":"); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
