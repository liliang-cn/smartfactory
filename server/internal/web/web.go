// Package web serves the twin's front end, its live feed and the MCP endpoint from one port.
package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"smartfactory/internal/tags"
)

type Server struct {
	Hub     *tags.Hub
	Token   string
	Version string
	Static  fs.FS
	MCP     http.Handler
	Log     *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/live", s.live)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "version": s.Version, "sources": s.Hub.Sources()})
	})
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		writeJSON(w, s.Hub.Last(q.Get("company"), q.Get("plant"), q.Get("asset")))
	})
	if s.MCP != nil {
		mux.Handle("/mcp", s.requireToken(s.MCP))
	}
	mux.Handle("/", s.static())
	return mux
}

func (s *Server) operator(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	got := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" {
			http.Error(w, "MCP is disabled: set a token in the server config or SF_TOKEN", http.StatusServiceUnavailable)
			return
		}
		if !s.operator(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="smartfactory"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type clientMsg struct {
	Type    string          `json:"type"`
	Company string          `json:"company"`
	Data    json.RawMessage `json:"data"`
	Cmd     *tags.Command   `json:"cmd"`
}

// live is the browser feed: every tag and operator command as it happens. Browsers send back
// a snapshot of their simulation so MCP clients can see KPIs and alarms, and — with the operator
// token — commands that should reach the devices.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	op := s.operator(r)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sub := s.Hub.Subscribe()
	defer s.Hub.Unsubscribe(sub)
	hello := map[string]any{"type": "hello", "operator": op, "version": s.Version, "sources": s.Hub.Sources(), "tags": s.Hub.Last("", "", "")}
	if err := wsjson.Write(ctx, c, hello); err != nil {
		return
	}

	go func() {
		defer cancel()
		for {
			var m clientMsg
			if err := wsjson.Read(ctx, c, &m); err != nil {
				return
			}
			switch m.Type {
			case "snapshot":
				if m.Company != "" && len(m.Data) > 0 {
					s.Hub.SetSnapshot(m.Company, m.Data)
				}
			case "cmd":
				if op && m.Cmd != nil && m.Cmd.Asset != "" {
					cmd := *m.Cmd
					cmd.By = "operator"
					s.Log.Info("operator command", "company", cmd.Company, "plant", cmd.Plant, "asset", cmd.Asset, "cmd", cmd.Cmd)
					s.Hub.Command(cmd)
				}
			}
		}
	}()

	status := time.NewTicker(5 * time.Second)
	defer status.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-sub.C:
			if err := wsjson.Write(ctx, c, e); err != nil {
				return
			}
		case <-status.C:
			if err := wsjson.Write(ctx, c, map[string]any{"type": "sources", "sources": s.Hub.Sources()}); err != nil {
				return
			}
		}
	}
}

// static serves the embedded single-page app: hashed assets cached for a year, the page itself
// revalidated every time, and unknown paths falling back to the page.
func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.Static, p); err != nil {
			if path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			p = "index.html"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
