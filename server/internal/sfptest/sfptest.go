// Package sfptest checks messages against the protocol standard, protocol/sfp.schema.json
// (written by `npm run schema` from src/protocol/sfp.ts), for the conformance tests.
package sfptest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

type Standard struct {
	t    testing.TB
	Root jsonschema.Schema
}

// Load reads the standard from the repository.
func Load(t testing.TB) *Standard {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(here), "../../../protocol/sfp.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Standard{t: t}
	if err := json.Unmarshal(b, &s.Root); err != nil {
		t.Fatal(err)
	}
	return s
}

// Check validates one message or body (JSON) against the definition named def.
func (s *Standard) Check(def string, raw []byte) {
	s.t.Helper()
	root := s.Root
	root.Ref = "#/$defs/" + def
	r, err := root.Resolve(nil)
	if err != nil {
		s.t.Fatalf("%s: %v", def, err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		s.t.Fatalf("%s: not JSON: %v\n%.300s", def, err, raw)
	}
	if err := r.Validate(v); err != nil {
		s.t.Errorf("%s does not conform: %v\n%.300s", def, err, raw)
	}
}
