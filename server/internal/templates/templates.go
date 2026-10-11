// Package templates keeps factory templates: a company's structure (plants, lines, machines)
// with the industry packs it needs that are not built in, so it can start a factory on any
// installation. An installation keeps the ones its administrators save or import; exported as a
// file, a template moves to another installation. A template holds no data: no state,
// documents, notes, tickets or records.
package templates

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// Author is who made it.
	Author  string    `json:"author,omitempty"`
	Created time.Time `json:"created"`
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
	// Source: builtin | local (saved or imported on this installation)
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
