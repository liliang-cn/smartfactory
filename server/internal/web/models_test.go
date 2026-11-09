package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/models"
	"smartfactory/internal/tags"
)

// The twin's models: the program's own are served as they ship; another is fetched from the
// catalogue the first time somebody asks for it, but only when a look uses it, and kept; the
// credits page names every one; the console's search is the administrators'.
func TestModelFiles(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.NewOffline(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()

	var downloads atomic.Int32
	glb := append([]byte("glTF"), make([]byte, 40)...)
	cat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/assets":
			w.Write([]byte(`{"coverage":"ok","assets":[{"id":"camp-chair-1","title":"Camp Chair","free":true,"size_m":{"x":0.6,"y":0.9,"z":0.6}}]}`))
		case "/api/assets/camp-chair-1":
			w.Write([]byte(`{"id":"camp-chair-1","title":"Camp Chair","credit":"Camp Chair by Polyfork (https://polyfork.dev/asset/camp-chair-1), CC BY 4.0","size_m":{"x":0.6,"y":0.9,"z":0.6}}`))
		case "/api/assets/lone-rock-1":
			w.Write([]byte(`{"id":"lone-rock-1","title":"Lone Rock","size_m":{"x":1,"y":1,"z":1}}`))
		case "/cdn/camp-chair-1.glb", "/cdn/lone-rock-1.glb":
			downloads.Add(1)
			w.Write(glb)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cat.Close()
	kept, err := models.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	kept.Base = cat.URL

	// a chair drawn into the built-in camping pack's canopy
	overlay := `{"id":"camping","machines":{"canopy":{"look":{"archetype":"frame","parts":[
{"shape":"model","model":"camp-chair-1","size":[0.6,0.9,0.6],"at":[0,0,0],"rotate":[0,0,0],"color":"white"}]}}}}`
	if r := eng.SetShapeOverlay([]byte(overlay)); !r.OK {
		t.Fatalf("%v", r.Errors)
	}
	static := fstest.MapFS{
		"models/own-tent-1.glb": {Data: glb},
		"models/CREDITS.txt":    {Data: []byte("own-tent-1.glb: Own Tent by Polyfork (https://polyfork.dev/asset/own-tent-1), CC BY 4.0\n")},
		"simready/CREDITS.txt":  {Data: []byte("3D models by NVIDIA SimReady\n")},
		"index.html":            {Data: []byte("<html></html>")},
	}
	admin, ak := auth.NewToken("厂长", auth.Admin, nil, "")
	srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", ak), Log: log, Static: static, Models: kept}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	get := func(path, token string) (int, string, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Content-Type"), string(b)
	}

	if code, ctype, body := get("/models/own-tent-1.glb", ""); code != 200 || ctype != "model/gltf-binary" || !strings.HasPrefix(body, "glTF") {
		t.Fatalf("the program's own model: %d %s", code, ctype)
	}
	if code, ctype, body := get("/models/camp-chair-1.glb", ""); code != 200 || ctype != "model/gltf-binary" || !strings.HasPrefix(body, "glTF") {
		t.Fatalf("a model a look uses: %d %s", code, ctype)
	}
	if code, _, _ := get("/models/camp-chair-1.glb", ""); code != 200 || downloads.Load() != 1 {
		t.Fatalf("kept after the first time: %d, %d downloads", code, downloads.Load())
	}
	// a model no look uses is not fetched for a visitor
	for _, p := range []string{"/models/lone-rock-1.glb", "/models/Bad%20Id.glb", "/models/camp-chair-1", "/models/..%2fsecret.glb"} {
		if code, _, _ := get(p, ""); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if downloads.Load() != 1 {
		t.Fatalf("downloaded what no look uses: %d", downloads.Load())
	}

	code, ctype, body := get("/models/credits", "")
	if code != 200 || !strings.HasPrefix(ctype, "text/plain") {
		t.Fatalf("credits: %d %s", code, ctype)
	}
	for _, want := range []string{"NVIDIA SimReady", "own-tent-1.glb: Own Tent by Polyfork", "camp-chair-1.glb: Camp Chair by Polyfork (https://polyfork.dev/asset/camp-chair-1), CC BY 4.0"} {
		if !strings.Contains(body, want) {
			t.Errorf("credits leave out %q:\n%s", want, body)
		}
	}

	code, _, body = get("/api/admin/models?q=chair", admin)
	var found models.Found
	if json.Unmarshal([]byte(body), &found) != nil || code != 200 || len(found.Models) != 1 || found.Models[0].Size != [3]float64{0.6, 0.9, 0.6} {
		t.Fatalf("the console's search: %d %s", code, body)
	}
	if code, _, _ := get("/api/admin/models?q=chair", ""); code != 401 && code != 403 {
		t.Fatalf("a visitor searched: %d", code)
	}
}
