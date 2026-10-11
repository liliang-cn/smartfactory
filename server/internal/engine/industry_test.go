package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// An industry pack is checked without being loaded: a copy of a built-in pack under its own id
// passes, the built-in id and a broken pack do not, and a company on a pack that is not loaded
// yet checks only together with it (as the scene architect proposes them).
func TestCheckIndustry(t *testing.T) {
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	pack := func() map[string]any {
		var p map[string]any
		json.Unmarshal([]byte(e.Industry("forging")), &p)
		p["id"], p["name"] = "my-forging", "自己的锻造"
		return p
	}
	raw := func(v any) []byte { b, _ := json.Marshal(v); return b }

	mine := pack()
	if r := e.CheckIndustry(raw(mine)); !r.OK {
		t.Fatalf("a copy under its own id: %v", r.Errors)
	}
	if strings.Contains(e.IndustryList(), `"my-forging"`) {
		t.Error("checking the pack loaded it")
	}
	builtin := pack()
	builtin["id"] = "forging"
	if r := e.CheckIndustry(raw(builtin)); r.OK {
		t.Error("a pack took a built-in pack's id")
	}
	broken := pack()
	for _, m := range broken["machines"].(map[string]any) {
		m.(map[string]any)["code"] = "bad code"
		break
	}
	if r := e.CheckIndustry(raw(broken)); r.OK || !strings.Contains(strings.Join(r.Errors, " "), "code") {
		t.Errorf("a broken pack: %+v", r)
	}

	var company map[string]any
	json.Unmarshal([]byte(e.BuiltinTemplate("huaduan")), &company)
	company["id"], company["industry"] = "jianghe", "my-forging"
	if r := e.CheckCompany(raw(company)); r.OK {
		t.Error("a company checked on a pack that is not loaded")
	}
	if r := e.CheckTemplate(raw(map[string]any{"company": company, "industries": map[string]any{"my-forging": mine}})); !r.OK {
		t.Errorf("a company with the pack it brings: %v", r.Errors)
	}

	// the pack went through a Go map, so its stages are in alphabetical order now; its lines
	// still take billets in and send finished parts out
	if r := e.CreateIndustry(raw(mine)); !r.OK {
		t.Fatal(r.Errors)
	}
	if r := e.CreateCompany(raw(company)); !r.OK {
		t.Fatal(r.Errors)
	}
	var model struct {
		Plants []struct {
			Lines []struct {
				Product struct {
					Key    string
					Stages []string
				}
			}
		}
	}
	json.Unmarshal([]byte(e.ProcessModel("jianghe")), &model)
	for _, p := range model.Plants {
		for _, l := range p.Lines {
			want := strings.Join(orderedKeys(t, e.Industry("forging"), l.Product.Key), " ")
			// packers add a carton stage at the end of products that have none
			if got := strings.TrimSuffix(strings.Join(l.Product.Stages, " "), " packed"); got != want {
				t.Errorf("%s stages %s, want %s", l.Product.Key, got, want)
			}
		}
	}
}

// orderedKeys is a product's stage keys in the order a pack's JSON writes them.
func orderedKeys(t *testing.T, pack, product string) []string {
	t.Helper()
	var p struct {
		Products map[string]struct{ Stages json.RawMessage }
	}
	json.Unmarshal([]byte(pack), &p)
	d := json.NewDecoder(strings.NewReader(string(p.Products[product].Stages)))
	d.Token()
	var out []string
	for d.More() {
		k, _ := d.Token()
		out = append(out, k.(string))
		var skip json.RawMessage
		d.Decode(&skip)
	}
	return out
}
