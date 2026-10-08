// Package web serves the twin's front end, its live feed and the MCP endpoint from one port.
package web

import (
	"cmp"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/sfconv"
	"smartfactory/internal/tags"
)

// Protocol is the version of the browser protocol this server speaks (protocol/README.md); it
// must match PROTOCOL in src/protocol/sfp.ts.
const Protocol = 1

type Server struct {
	Hub     *tags.Hub
	Engine  *engine.Engine
	Token   string
	Version string
	Static  fs.FS
	MCP     http.Handler
	Log     *slog.Logger
	// Advisors is the advisor console (DataIntelligence) of each company that has one.
	Advisors map[string]string
	// AdvisorAPI is how the twin reaches each company's advisor on its viewers' behalf.
	AdvisorAPI map[string]Advisor
	// Prefix is the MQTT topic convention's first segment (protocol/devices.md).
	Prefix string

	namesOnce sync.Once
	names     map[string]string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/live", s.live)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "version": s.Version, "companies": s.Engine.CompanyIDs(), "stepMs": s.Engine.StepCost().Seconds() * 1000, "savedAt": s.Engine.SavedAt(), "sources": s.sources()})
	})
	mux.HandleFunc("GET /api/advisors", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, s.Advisors)
	})
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		writeJSON(w, s.Hub.Last(q.Get("company"), q.Get("plant"), q.Get("asset")))
	})
	// companies created at run time (MCP), for browsers
	mux.HandleFunc("GET /api/config/directory", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, s.Engine.RuntimeDirectory())
	})
	mux.HandleFunc("GET /api/config/company/{id}", func(w http.ResponseWriter, r *http.Request) {
		company, industries, ok := s.Engine.RuntimeCompany(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "找不到这家公司")
			return
		}
		writeJSON(w, map[string]any{"company": company, "industries": industries})
	})
	mux.HandleFunc("GET /api/devices/{company}/{plant}", s.deviceMap)
	if s.MCP != nil {
		mux.Handle("/mcp", s.requireToken(s.MCP))
	}
	s.advisorRoutes(mux)
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
	Type    string   `json:"type"`
	Company string   `json:"company"`
	Plant   string   `json:"plant"`
	Asset   string   `json:"asset"`
	Cmd     string   `json:"cmd"`
	Arg     *float64 `json:"arg"`
	Scale   float64  `json:"scale"`
	// O is the sending browser, echoed on broadcasts so it can skip its own
	O string `json:"o"`
}

// Visitors may operate the simulated plant (it is a demo), but their actions undo themselves
// after a while so one visitor cannot leave the plant stopped for everyone.
const visitorRevert = 90 * time.Second

var revertOf = map[string]string{"stop": "start", "fault": "reset", "speed": "speed"}

// live is the browser link. The server runs the simulation; a browser watches one company and
// gets frames of the plant it is looking at (and the others now and then), sends commands, and
// with the operator token can also change the plant clock and reach real equipment.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	op := s.operator(r)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	out := make(chan any, 64)
	send := func(v any) {
		select {
		case out <- v:
		default:
		}
	}
	send(map[string]any{"type": "hello", "protocol": Protocol, "operator": op, "sim": true, "version": s.Version, "sources": s.sources()})

	var (
		company  string
		frames   <-chan []byte
		setPlant func(string)
		unwatch  = func() {}
		bucket   = 5.0
		refill   = time.Now()
	)
	defer func() { unwatch() }()

	// The reader only reads; everything is handled on this goroutine.
	in := make(chan clientMsg, 16)
	go func() {
		defer cancel()
		for {
			var m clientMsg
			if err := wsjson.Read(ctx, c, &m); err != nil {
				return
			}
			select {
			case in <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	handle := func(m clientMsg) {
		switch m.Type {
		case "watch":
			if m.Company != company {
				unwatch()
				company = m.Company
				frames, setPlant, unwatch = s.Engine.Watch(company, m.Plant)
			} else if setPlant != nil {
				setPlant(m.Plant)
			}
		case "plant":
			if setPlant != nil {
				setPlant(m.Plant)
			}
		case "cmd":
			if company == "" || m.Asset == "" {
				return
			}
			if !op {
				// token bucket: a burst of 5, then one every 3 s
				bucket = min(5, bucket+time.Since(refill).Seconds()/3)
				refill = time.Now()
				if bucket < 1 {
					send(map[string]any{"type": "result", "ok": false, "msg": "操作太频繁，稍后再试"})
					return
				}
				bucket--
			}
			res := s.Engine.Command(company, m.Plant, m.Asset, m.Cmd, m.Arg)
			if res.OK {
				// Adapters decide what reaches equipment: operators reach everything, visitors
				// only emulated equipment (public sources).
				by := "visitor"
				if op {
					by = "operator"
				} else {
					s.scheduleRevert(company, m)
				}
				s.Hub.Command(tags.Command{Company: company, Plant: m.Plant, Asset: m.Asset, Cmd: m.Cmd, Arg: m.Arg, By: by, Origin: m.O})
			}
			send(map[string]any{"type": "result", "ok": res.OK, "msg": res.Msg})
		case "time":
			if !op {
				send(map[string]any{"type": "result", "ok": false, "msg": "全厂时钟是共享的，只有操作员可以调整"})
				return
			}
			if company != "" {
				s.Engine.SetTimeScale(company, m.Scale)
			}
		case "hist":
			if company != "" {
				send(map[string]any{"type": "hist", "plant": m.Plant, "asset": m.Asset, "data": json.RawMessage(s.Engine.Hist(company, m.Plant, m.Asset))})
			}
		}
	}

	sub := s.Hub.Subscribe()
	defer s.Hub.Unsubscribe(sub)
	status := time.NewTicker(5 * time.Second)
	defer status.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-in:
			handle(m)
		case f := <-frames:
			if err := c.Write(ctx, websocket.MessageText, f); err != nil {
				return
			}
		case v := <-out:
			if err := wsjson.Write(ctx, c, v); err != nil {
				return
			}
		case e := <-sub.C:
			// operator / MCP actions on the watched company, for the "who did what" toast
			if e.Type == "cmd" && e.Cmd.Company == company && (e.Cmd.By == "operator" || e.Cmd.By == "mcp") {
				if err := wsjson.Write(ctx, c, e); err != nil {
					return
				}
			}
		case <-status.C:
			if err := wsjson.Write(ctx, c, map[string]any{"type": "sources", "sources": s.sources()}); err != nil {
				return
			}
		}
	}
}

func (s *Server) scheduleRevert(company string, m clientMsg) {
	back, ok := revertOf[m.Cmd]
	if !ok {
		return
	}
	one := 1.0
	time.AfterFunc(visitorRevert, func() {
		var arg *float64
		if back == "speed" {
			arg = &one
		}
		if s.Engine.Command(company, m.Plant, m.Asset, back, arg).OK {
			s.Hub.Command(tags.Command{Company: company, Plant: m.Plant, Asset: m.Asset, Cmd: back, Arg: arg, By: "auto"})
		}
	})
}

// sources adds readable names (company and plant names from the simulation) to each target.
func (s *Server) sources() []tags.SourceStatus {
	out := s.Hub.Sources()
	if s.Engine == nil {
		return out
	}
	s.namesOnce.Do(func() {
		var list []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Plants []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"plants"`
		}
		s.names = map[string]string{}
		if json.Unmarshal([]byte(s.Engine.List()), &list) == nil {
			for _, c := range list {
				for _, p := range c.Plants {
					s.names[c.ID+"/"+p.ID] = c.Name + " " + p.Name
				}
			}
		}
	})
	for i := range out {
		if out[i].Targets == nil {
			out[i].Targets = []string{}
		}
		for _, t := range out[i].Targets {
			parts := strings.SplitN(t, "/", 3)
			label := t
			if len(parts) == 3 {
				if n, ok := s.names[parts[0]+"/"+parts[1]]; ok {
					label = n + " · " + parts[2]
				}
			}
			out[i].Labels = append(out[i].Labels, label)
		}
	}
	return out
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

// deviceMap tells an integrator where every value of a plant lives under the sf convention:
// the MQTT topics, the Modbus registers and the OPC UA nodes, derived from the plant's machines.
func (s *Server) deviceMap(w http.ResponseWriter, r *http.Request) {
	company, plant := r.PathValue("company"), r.PathValue("plant")
	machines := s.Engine.Machines(company, plant)
	if len(machines) == 0 {
		writeError(w, http.StatusNotFound, "找不到这个厂")
		return
	}
	fleet := s.Engine.Fleet(company, plant)
	prefix := cmp.Or(s.Prefix, "sf")
	base := strings.Join([]string{prefix, company, plant}, "/")
	type mbPoint struct {
		Asset    string         `json:"asset"`
		Field    string         `json:"field"`
		Register string         `json:"register"`
		Address  uint16         `json:"address"`
		Kind     string         `json:"kind"`
		Scale    *float64       `json:"scale,omitempty"`
		Map      map[string]any `json:"map,omitempty"`
	}
	type mbCmd struct {
		Asset      string  `json:"asset"`
		Address    uint16  `json:"address"`
		ArgAddress uint16  `json:"argAddress"`
		ArgScale   float64 `json:"argScale"`
	}
	type uaPoint struct {
		Asset string `json:"asset"`
		Field string `json:"field"`
		Node  string `json:"node"`
	}
	type uaCmd struct {
		Asset   string `json:"asset"`
		Node    string `json:"node"`
		ArgNode string `json:"argNode"`
	}
	mb := sfconv.Expand(config.Source{Type: "modbus", Company: company, Plant: plant}, machines, fleet)
	ua := sfconv.Expand(config.Source{Type: "opcua", Company: company, Plant: plant}, machines, fleet)
	mbPoints, uaPoints := []mbPoint{}, []uaPoint{}
	mbCmds, uaCmds := []mbCmd{}, []uaCmd{}
	for _, p := range mb.Points {
		mbPoints = append(mbPoints, mbPoint{p.Asset, p.Field, p.Register, p.Address, p.Kind, p.Scale, p.Map})
	}
	for _, p := range ua.Points {
		uaPoints = append(uaPoints, uaPoint{p.Asset, p.Field, p.Node})
	}
	// every command of a machine goes through the same register / node: list each machine once
	for _, c := range mb.Commands {
		if len(mbCmds) == 0 || mbCmds[len(mbCmds)-1].Asset != c.Asset {
			mbCmds = append(mbCmds, mbCmd{c.Asset, c.Address, *c.ArgAddress, c.ArgScale})
		}
	}
	for _, c := range ua.Commands {
		if len(uaCmds) == 0 || uaCmds[len(uaCmds)-1].Asset != c.Asset {
			uaCmds = append(uaCmds, uaCmd{c.Asset, c.Node, c.ArgNode})
		}
	}
	writeJSON(w, map[string]any{
		"company": company, "plant": plant,
		"mqtt": map[string]string{"data": base + "/{asset}", "field": base + "/{asset}/{field}", "cmd": base + "/{asset}/cmd"},
		"modbus": map[string]any{"points": mbPoints, "commands": mbCmds,
			"codes": map[string]any{"state": sfconv.States, "command": sfconv.Commands}},
		"opcua": map[string]any{"points": uaPoints, "commands": uaCmds},
	})
}

// writeError answers with the protocol's error body, {"error": msg}.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
