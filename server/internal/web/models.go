package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"smartfactory/internal/models"
)

// Ready-made 3D models (package models) as the twin loads them: /models/<id>.glb. Those the
// built-in templates use ship in the program (web/public/models); any other a look uses is kept
// in the data directory the first time somebody opens the twin, and served from there after.
// Only models a look on this server uses are fetched, so a visitor cannot have the server
// download the catalogue. /models/credits lists every model's attribution (CC BY 4.0).

// usedModels are the models the looks of the industry packs and companies use, worked out again
// at most every few seconds: asking for any other costs a look-up, and a look saved just now is
// seen soon after.
type usedModels struct {
	mu  sync.Mutex
	at  time.Time
	ids map[string]bool
}

const usedFor = 5 * time.Second

var modelRe = regexp.MustCompile(`"model":"([a-z0-9][a-z0-9-]{1,95})"`)

// usedModel reports whether a look of an industry pack or a company uses model id.
func (s *Server) usedModel(id string) bool {
	u := &s.used
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ids == nil || time.Since(u.at) > usedFor {
		u.ids = map[string]bool{}
		add := func(js string) {
			for _, m := range modelRe.FindAllStringSubmatch(js, -1) {
				u.ids[m[1]] = true
			}
		}
		var list []struct {
			ID string `json:"id"`
		}
		json.Unmarshal([]byte(s.Engine.IndustryList()), &list)
		for _, in := range list {
			add(s.Engine.Industry(in.ID))
		}
		for _, c := range s.Engine.CompanyIDs() {
			add(s.Engine.CompanyConfig(c))
		}
		u.at = time.Now()
	}
	return u.ids[id]
}

func (s *Server) modelFile(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	id, ok := strings.CutSuffix(file, ".glb")
	if !ok || !models.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	// the program's own
	if s.Static != nil {
		if _, err := fs.Stat(s.Static, "models/"+file); err == nil {
			w.Header().Set("Content-Type", "model/gltf-binary")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			http.ServeFileFS(w, r, s.Static, "models/"+file)
			return
		}
	}
	if s.Models == nil {
		http.NotFound(w, r)
		return
	}
	p := s.Models.Path(id)
	if p == "" {
		if !s.usedModel(id) {
			http.NotFound(w, r)
			return
		}
		var err error
		if p, err = s.Models.Fetch(r.Context(), id); err != nil {
			s.Log.Warn("model not fetched", "id", id, "err", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "model/gltf-binary")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, p)
}

// modelCredits is the attribution of every model the twin may draw: the program's own and the
// ones kept here, as plain text.
func (s *Server) modelCredits(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	b.WriteString("3D models in this SmartFactory\n\n")
	for _, f := range []string{"simready/CREDITS.txt", "models/CREDITS.txt"} {
		if s.Static == nil {
			break
		}
		if t, err := fs.ReadFile(s.Static, f); err == nil {
			b.Write(t)
			b.WriteString("\n")
		}
	}
	if s.Models != nil {
		if kept := s.Models.Credits(); len(kept) > 0 {
			b.WriteString("Kept on this server from the Polyfork catalogue (https://polyfork.dev), CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/), unchanged:\n\n")
			for _, in := range kept {
				fmt.Fprintf(&b, "%s.glb: %s\n", in.ID, in.Credit)
			}
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(b.String()))
}
