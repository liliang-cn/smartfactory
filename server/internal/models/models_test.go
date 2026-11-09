package models

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// a catalogue with two tents, one of them too big to fetch, and a broken file
func fake(t *testing.T) (*Catalog, *httptest.Server, *atomic.Int32) {
	t.Helper()
	var downloads atomic.Int32
	glb := append([]byte("glTF"), make([]byte, 60)...)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/assets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != UserAgent {
			http.Error(w, "name yourself", 400)
			return
		}
		if r.URL.Query().Get("free") != "1" || r.URL.Query().Get("max_triangles") == "" {
			http.Error(w, "filters", 400)
			return
		}
		if r.URL.Query().Get("q") == "lunar base" {
			w.Write([]byte(`{"coverage":"none","unmatched_terms":["lunar"],"assets":[]}`))
			return
		}
		w.Write([]byte(`{"coverage":"ok","assets":[
			{"id":"dome-tent-9ee59d","title":"Dome Tent","class":"prop","triangles":751,"free":true,"page":"https://polyfork.dev/asset/dome-tent-9ee59d","size_m":{"x":3,"y":1.31,"z":3}},
			{"id":"paid-thing-1","title":"Paid","free":false,"size_m":{"x":1,"y":1,"z":1}},
			{"id":"Bad Id","title":"Bad","free":true,"size_m":{"x":1,"y":1,"z":1}}]}`))
	})
	mux.HandleFunc("GET /api/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "camp-chair-1":
			w.Write([]byte(`{"id":"camp-chair-1","title":"Camp Chair","credit":"Camp Chair by Polyfork (https://polyfork.dev/asset/camp-chair-1), CC BY 4.0","size_m":{"x":0.6,"y":0.9,"z":0.6}}`))
		case "not-glb-1":
			w.Write([]byte(`{"id":"not-glb-1","title":"Odd","size_m":{"x":1,"y":1,"z":1}}`))
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /cdn/{file}", func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		switch r.PathValue("file") {
		case "dome-tent-9ee59d.glb", "camp-chair-1.glb":
			time.Sleep(50 * time.Millisecond)
			w.Write(glb)
		case "not-glb-1.glb":
			w.Write([]byte("<html>nope</html>"))
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Base = ts.URL
	return c, ts, &downloads
}

func TestSearchAndFetch(t *testing.T) {
	c, _, downloads := fake(t)
	ctx := context.Background()
	f, err := c.Search(ctx, "tent", 8)
	if err != nil {
		t.Fatal(err)
	}
	if f.Coverage != "ok" || len(f.Models) != 1 {
		t.Fatalf("only the free model with a good id is offered: %+v", f)
	}
	tent := f.Models[0]
	if tent.Size != [3]float64{3, 1.31, 3} || tent.Credit != "Dome Tent by Polyfork (https://polyfork.dev/asset/dome-tent-9ee59d), CC BY 4.0" {
		t.Fatalf("size and credit: %+v", tent)
	}
	if _, ok := c.Known("dome-tent-9ee59d"); !ok {
		t.Fatal("a searched model is known to the shape drawer")
	}
	if c.Path("dome-tent-9ee59d") != "" {
		t.Fatal("searching keeps nothing")
	}

	// many viewers asking at once: one download
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := c.Fetch(ctx, "dome-tent-9ee59d"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := downloads.Load(); n != 1 {
		t.Fatalf("downloaded %d times", n)
	}
	p := c.Path("dome-tent-9ee59d")
	if p == "" {
		t.Fatal("not kept")
	}
	var meta Info
	b, _ := os.ReadFile(strings.TrimSuffix(p, ".glb") + ".json")
	if json.Unmarshal(b, &meta) != nil || meta.Credit != tent.Credit {
		t.Fatalf("the credit goes with the file: %s", b)
	}
	// once kept, it is served from here
	if _, err := c.Fetch(ctx, "dome-tent-9ee59d"); err != nil || downloads.Load() != 1 {
		t.Fatalf("fetched again: %v %d", err, downloads.Load())
	}

	// a model never searched: its credit comes from the catalogue's own page of it
	if _, err := c.Fetch(ctx, "camp-chair-1"); err != nil {
		t.Fatal(err)
	}
	cr := c.Credits()
	if len(cr) != 2 || cr[0].Title != "Camp Chair" || !strings.Contains(cr[0].Credit, "CC BY 4.0") {
		t.Fatalf("credits: %+v", cr)
	}

	// a server restarted knows what it keeps
	again, _ := Open(c.Dir, nil)
	if len(again.Credits()) != 2 {
		t.Fatal("credits lost on restart")
	}
}

func TestFetchRefuses(t *testing.T) {
	c, _, _ := fake(t)
	ctx := context.Background()
	for _, id := range []string{"../etc/passwd", "Bad Id", "", "x"} {
		if _, err := c.Fetch(ctx, id); err == nil {
			t.Errorf("fetched %q", id)
		}
	}
	if _, err := c.Fetch(ctx, "missing-model-1"); err == nil {
		t.Error("fetched a model the catalogue does not have")
	}
	if _, err := c.Fetch(ctx, "not-glb-1"); err == nil || c.Path("not-glb-1") != "" {
		t.Errorf("kept a file that is not a model: %v", err)
	}
	if f, err := c.Search(ctx, "lunar base", 5); err != nil || f.Coverage != "none" || len(f.Models) != 0 {
		t.Errorf("nothing fitting is said so: %+v %v", f, err)
	}
	files, _ := filepath.Glob(filepath.Join(c.Dir, "*"))
	if len(files) != 0 {
		t.Errorf("left files behind: %v", files)
	}
}

// After a 429 nothing is asked of the catalogue until the time it gave is over.
func TestBackOff(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()
	c, _ := Open(t.TempDir(), nil)
	c.Base = ts.URL
	if _, err := c.Search(context.Background(), "tent", 5); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v", err)
	}
	if _, err := c.Search(context.Background(), "chair", 5); !errors.Is(err, ErrBusy) || hits.Load() != 1 {
		t.Fatalf("asked again while told to wait: %v, %d requests", err, hits.Load())
	}
}

// A file larger than a model can be is not kept.
func TestTooBig(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Write([]byte(`{"id":"huge-1","title":"Huge","size_m":{"x":1,"y":1,"z":1}}`))
			return
		}
		w.Write([]byte("glTF"))
		w.Write(make([]byte, MaxFile))
	}))
	defer ts.Close()
	c, _ := Open(t.TempDir(), nil)
	c.Base = ts.URL
	if _, err := c.Fetch(context.Background(), "huge-1"); err == nil || c.Path("huge-1") != "" {
		t.Fatalf("kept an oversized file: %v", err)
	}
}
