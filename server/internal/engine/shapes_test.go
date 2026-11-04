package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A shape drawn into a built-in pack is laid over it and kept beside it: the pack itself stays as
// shipped, a restart brings the shape back, and taking it off brings back the shipped look.
func TestShapeOverlayOnBuiltinPack(t *testing.T) {
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	e, err := NewOffline(log)
	if err != nil {
		t.Fatal(err)
	}
	e.Dir = dir
	if !e.IsBuiltinIndustry("forging") || e.IsBuiltinIndustry("nope") {
		t.Fatal("which packs are built in")
	}
	look := func(e *Engine) (arch string, parts int) {
		var p struct {
			Machines map[string]struct {
				Look struct {
					Archetype string
					Parts     []json.RawMessage
				}
			}
		}
		json.Unmarshal([]byte(e.Industry("forging")), &p)
		m := p.Machines["die-press"]
		return m.Look.Archetype, len(m.Look.Parts)
	}
	arch0, parts0 := look(e)
	if arch0 == "frame" {
		t.Skip("the forging press is already a frame")
	}

	overlay := `{"id":"forging","machines":{"die-press":{"look":{"archetype":"frame","parts":[
{"shape":"box","size":[1,1,1],"at":[0,0,0],"rotate":[0,0,0],"color":"blue"}]}}}}`
	if r := e.SetShapeOverlay([]byte(overlay)); !r.OK {
		t.Fatalf("%v", r.Errors)
	}
	if arch, n := look(e); arch != "frame" || n != 1 {
		t.Fatalf("with the shape: %s, %d parts", arch, n)
	}
	if !strings.Contains(string(e.ShapeOverlay("forging")), `"die-press"`) {
		t.Fatal("the overlay was not kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "configs", "shapes", "forging.json")); err != nil {
		t.Fatal(err)
	}

	// a restart: the overlay is read back and laid over the pack again
	e2, _ := NewOffline(log)
	e2.Dir = dir
	e2.LoadConfigs()
	if arch, n := look(e2); arch != "frame" || n != 1 {
		t.Fatalf("after a restart: %s, %d parts", arch, n)
	}

	// a shape that does not validate changes nothing
	bad := `{"id":"forging","machines":{"die-press":{"look":{"archetype":"nonsense"}}}}`
	if r := e.SetShapeOverlay([]byte(bad)); r.OK {
		t.Fatal("a bad shape was taken")
	}
	if arch, _ := look(e); arch != "frame" {
		t.Fatal("a bad shape changed the pack")
	}
	// a pack of your own is not drawn on this way
	if r := e.SetShapeOverlay([]byte(`{"id":"my-pack","machines":{}}`)); r.OK {
		t.Fatal("not a built-in pack")
	}

	// taking it off brings the shipped look back and the file goes
	if r := e.SetShapeOverlay([]byte(`{"id":"forging"}`)); !r.OK {
		t.Fatalf("%v", r.Errors)
	}
	if arch, n := look(e); arch != arch0 || n != parts0 {
		t.Fatalf("restored: %s, %d parts; shipped %s, %d", arch, n, arch0, parts0)
	}
	if e.ShapeOverlay("forging") != nil {
		t.Fatal("the overlay file is still there")
	}
}
