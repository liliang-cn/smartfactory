// Package templates keeps factory templates: a company's structure (plants, lines, machines)
// with the industry packs it needs that are not built in, so it can start a factory on any
// instance. An instance keeps the ones its administrators save; the template hub (the platform
// server) keeps the ones anyone publishes, for everyone. A template holds no data: no state,
// documents, notes, tickets or records.
package templates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Template is a factory to start from.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Author is who made it; AuthorKey their token (the hub lets only them replace it).
	Author    string    `json:"author,omitempty"`
	AuthorKey string    `json:"authorKey,omitempty"`
	Created   time.Time `json:"created"`
	// Company is the company config; its id is the template's own, replaced when used.
	Company json.RawMessage `json:"company"`
	// Industries are the packs it uses that are not built in.
	Industries map[string]json.RawMessage `json:"industries,omitempty"`
}

// Summary is a template as lists show it.
type Summary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	// Source: builtin | local (saved on this instance) | hub (the public template hub)
	Source       string     `json:"source"`
	Industry     string     `json:"industry"`
	IndustryName string     `json:"industryName"`
	Plants       []Plant    `json:"plants"`
	Created      *time.Time `json:"created,omitempty"`
}

type Plant struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Site     string   `json:"site"`
	Lines    []string `json:"lines"`
	Machines int      `json:"machines"`
}

var ValidID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}$`)

// Store keeps templates as files in a directory.
type Store struct {
	dir string
	mu  sync.Mutex
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// Get is one template, or nil.
func (s *Store) Get(id string) (*Template, error) {
	if !ValidID.MatchString(id) {
		return nil, nil
	}
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var t Template
	return &t, json.Unmarshal(b, &t)
}

// All is every template, newest first.
func (s *Store) All() ([]*Template, error) {
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.json"))
	out := []*Template{}
	for _, f := range files {
		t, err := s.Get(strings.TrimSuffix(filepath.Base(f), ".json"))
		if err != nil {
			return nil, err
		}
		if t != nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func (s *Store) Put(t *Template) error {
	if !ValidID.MatchString(t.ID) {
		return fmt.Errorf("模板标识 %q：小写字母、数字、连字符，2～48 个字符", t.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := s.path(t.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(t.ID))
}

func (s *Store) Delete(id string) error {
	if !ValidID.MatchString(id) {
		return os.ErrNotExist
	}
	return os.Remove(s.path(id))
}

// ---- the hub, from an instance ----

// Hub is the public template hub an instance reads from (and publishes to, with a token).
type Hub struct {
	URL   string
	Token string

	mu     sync.Mutex
	cached []Summary
	at     time.Time
}

var client = &http.Client{Timeout: 6 * time.Second}

// List is the hub's templates, remembered for five minutes; nil when it cannot be reached.
func (h *Hub) List(ctx context.Context) []Summary {
	if h == nil || h.URL == "" {
		return nil
	}
	h.mu.Lock()
	if time.Since(h.at) < 5*time.Minute && h.cached != nil {
		defer h.mu.Unlock()
		return h.cached
	}
	h.mu.Unlock()
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(h.URL, "/")+"/api/hub/templates", nil)
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	var list []Summary
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&list) != nil {
		return nil
	}
	for i := range list {
		list[i].Source = "hub"
	}
	h.mu.Lock()
	h.cached, h.at = list, time.Now()
	h.mu.Unlock()
	return list
}

// Get is one of the hub's templates.
func (h *Hub) Get(ctx context.Context, id string) (*Template, error) {
	if h == nil || h.URL == "" {
		return nil, errors.New("没有配置模板市场")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(h.URL, "/")+"/api/hub/templates/"+id, nil)
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上模板市场：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil, nil
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("模板市场答 %s", res.Status)
	}
	var t Template
	return &t, json.NewDecoder(res.Body).Decode(&t)
}

// Publish puts t on the hub under the hub token's name.
func (h *Hub) Publish(ctx context.Context, t *Template) error {
	if h == nil || h.URL == "" || h.Token == "" {
		return errors.New("要发布到模板市场，先在配置里填 templateHub 和 hubToken（模板市场的令牌）")
	}
	b, _ := json.Marshal(t)
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(h.URL, "/")+"/api/hub/templates", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连不上模板市场：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var e struct{ Error string }
		json.NewDecoder(res.Body).Decode(&e)
		return fmt.Errorf("模板市场没有收下：%s", cmpOr(e.Error, res.Status))
	}
	h.mu.Lock()
	h.cached = nil
	h.mu.Unlock()
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
