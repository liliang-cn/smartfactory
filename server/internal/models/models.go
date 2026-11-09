// Package models is the catalogue of ready-made 3D models a look can use beside its primitives:
// the free low-poly models of Polyfork (https://polyfork.dev), searched by the shape drawer and
// kept on this server once a look uses one, so the twin draws them without reaching out again.
//
// Their mesh files are CC BY 4.0: each file is kept with the credit line it came with
// (<id>.json beside <id>.glb), and Credits lists them for the twin's credits page. Polyfork asks
// that a model be fetched when somebody uses it, not the catalogue in bulk, under a User-Agent that
// names the product, backing off when it answers 429; Fetch does exactly that.
//
// A model rests on y = 0, centred on x and z, its front towards +z, at its real size in metres.
package models

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBase is where the catalogue lives.
const DefaultBase = "https://polyfork.dev"

// UserAgent names this product to the catalogue, as it asks.
const UserAgent = "SmartFactory (+https://github.com/liliang-cn/smartfactory)"

// MaxFile is the largest model file kept (a free model is tens to hundreds of kilobytes).
const MaxFile = 16 << 20

// MaxTriangles keeps searches to models a plant full of them can draw.
const MaxTriangles = 6000

// Info is one model as a look needs it.
type Info struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Class string `json:"class,omitempty"`
	// Size is how big it really is: [width along x, height along y, depth along z] in metres.
	Size      [3]float64 `json:"size"`
	Triangles int        `json:"triangles,omitempty"`
	Thumbnail string     `json:"thumbnail,omitempty"`
	Page      string     `json:"page,omitempty"`
	// Credit is the attribution its licence asks for; it goes with the file.
	Credit string `json:"credit"`
}

// Found is what a search found. Coverage is the catalogue's own verdict on the query ("none",
// "poor", "ok", "filtered_out"): on none or poor nothing fitting is there, and a look should
// be built from primitives rather than from something unrelated.
type Found struct {
	Coverage  string   `json:"coverage,omitempty"`
	Models    []Info   `json:"models"`
	Unmatched []string `json:"unmatched,omitempty"`
}

// ErrBusy is the catalogue asking to be left alone for a while (429).
var ErrBusy = errors.New("模型库请求太频繁，过一会儿再试")

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,95}$`)

// ValidID reports whether id can name a model (and a file).
func ValidID(id string) bool { return idRe.MatchString(id) }

// Catalog searches the catalogue and keeps the models looks use in Dir.
type Catalog struct {
	Dir  string
	Base string
	UA   string
	HTTP *http.Client
	Log  *slog.Logger

	mu    sync.Mutex
	known map[string]Info
	busy  map[string]*fetch
	until time.Time // no requests before this (after a 429)
}

type fetch struct {
	done chan struct{}
	path string
	err  error
}

// Open keeps models in dir, reading the credits of those already there.
func Open(dir string, log *slog.Logger) (*Catalog, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Catalog{Dir: dir, Base: DefaultBase, UA: UserAgent, HTTP: &http.Client{}, Log: log, known: map[string]Info{}, busy: map[string]*fetch{}}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		var in Info
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &in) == nil && ValidID(in.ID) {
			c.known[in.ID] = in
		}
	}
	return c, nil
}

// row is a model as the catalogue lists it.
type row struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Class     string `json:"class"`
	Triangles int    `json:"triangles"`
	Free      *bool  `json:"free"`
	Page      string `json:"page"`
	Thumbnail string `json:"thumbnail"`
	Credit    string `json:"credit"`
	Size      *struct {
		X, Y, Z float64
	} `json:"size_m"`
}

func (r row) info() Info {
	in := Info{ID: r.ID, Title: cmp.Or(r.Title, r.ID), Class: r.Class, Triangles: r.Triangles, Thumbnail: r.Thumbnail,
		Page: cmp.Or(r.Page, DefaultBase+"/asset/"+r.ID), Credit: r.Credit}
	if r.Size != nil {
		in.Size = [3]float64{r.Size.X, r.Size.Y, r.Size.Z}
	}
	if in.Credit == "" {
		// the line the catalogue gives every model, in its own words
		in.Credit = fmt.Sprintf("%s by Polyfork (%s), CC BY 4.0", in.Title, in.Page)
	}
	return in
}

// Search looks for up to n free models matching q.
func (c *Catalog) Search(ctx context.Context, q string, n int) (*Found, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("要搜什么")
	}
	n = min(max(n, 1), 24)
	v := url.Values{"q": {q}, "free": {"1"}, "per_page": {strconv.Itoa(n)}, "max_triangles": {strconv.Itoa(MaxTriangles)}}
	var out struct {
		Coverage  string   `json:"coverage"`
		Unmatched []string `json:"unmatched_terms"`
		Assets    []row    `json:"assets"`
	}
	if err := c.getJSON(ctx, "/api/assets?"+v.Encode(), 45*time.Second, &out); err != nil {
		return nil, err
	}
	f := &Found{Coverage: out.Coverage, Unmatched: out.Unmatched, Models: []Info{}}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range out.Assets {
		if !ValidID(r.ID) || r.Free != nil && !*r.Free || r.Size == nil {
			continue
		}
		in := r.info()
		if old, ok := c.known[in.ID]; ok && strings.Contains(old.Credit, "polyfork.dev/asset/") {
			in.Credit = old.Credit
		}
		c.known[in.ID] = in
		f.Models = append(f.Models, in)
	}
	return f, nil
}

// Known is a model this server has seen (searched, or kept), without asking the catalogue.
func (c *Catalog) Known(id string) (Info, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	in, ok := c.known[id]
	return in, ok
}

// Path is the kept file of a model, "" when it is not here.
func (c *Catalog) Path(id string) string {
	if !ValidID(id) {
		return ""
	}
	p := filepath.Join(c.Dir, id+".glb")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// Fetch keeps a model on this server (once; callers asking for the same one wait for it) and
// returns its file.
func (c *Catalog) Fetch(ctx context.Context, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("模型 id %q 不对", id)
	}
	if p := c.Path(id); p != "" {
		return p, nil
	}
	c.mu.Lock()
	if f, ok := c.busy[id]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.path, f.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f := &fetch{done: make(chan struct{})}
	c.busy[id] = f
	c.mu.Unlock()
	// the download is the server's, not the request's: a viewer who leaves does not waste it
	f.path, f.err = c.download(context.WithoutCancel(ctx), id)
	c.mu.Lock()
	delete(c.busy, id)
	c.mu.Unlock()
	close(f.done)
	return f.path, f.err
}

func (c *Catalog) download(ctx context.Context, id string) (string, error) {
	in, ok := c.Known(id)
	if !ok || !strings.Contains(in.Credit, "/asset/") {
		var r row
		if err := c.getJSON(ctx, "/api/assets/"+id, 60*time.Second, &r); err != nil {
			if !ok {
				return "", err
			}
		} else if r.ID == id {
			in = r.info()
		}
	}
	if in.ID == "" {
		return "", fmt.Errorf("模型库里没有 %s", id)
	}
	b, err := c.get(ctx, "/cdn/"+id+".glb", 150*time.Second, MaxFile)
	if err != nil {
		return "", err
	}
	if len(b) < 20 || !bytes.Equal(b[:4], []byte("glTF")) {
		return "", fmt.Errorf("%s 不是 glTF 模型文件", id)
	}
	meta, _ := json.MarshalIndent(in, "", "  ")
	if err := write(filepath.Join(c.Dir, id+".json"), meta); err != nil {
		return "", err
	}
	p := filepath.Join(c.Dir, id+".glb")
	if err := write(p, b); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.known[id] = in
	c.mu.Unlock()
	if c.Log != nil {
		c.Log.Info("model kept", "id", id, "bytes", len(b), "credit", in.Credit)
	}
	return p, nil
}

// Credits are the models kept on this server, with the attribution each needs.
func (c *Catalog) Credits() []Info {
	files, _ := filepath.Glob(filepath.Join(c.Dir, "*.glb"))
	out := []Info{}
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".glb")
		if in, ok := c.Known(id); ok {
			out = append(out, in)
		}
	}
	slices.SortFunc(out, func(a, b Info) int { return strings.Compare(a.Title, b.Title) })
	return out
}

func write(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (c *Catalog) getJSON(ctx context.Context, path string, wait time.Duration, v any) error {
	b, err := c.get(ctx, path, wait, 8<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("模型库的回答读不懂：%w", err)
	}
	return nil
}

// get fetches path from the catalogue, trying once more when the network or the catalogue
// failed, and holding off every request for as long as a 429 asks.
func (c *Catalog) get(ctx context.Context, path string, wait time.Duration, limit int64) ([]byte, error) {
	var last error
	for try := range 2 {
		if try > 0 {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		c.mu.Lock()
		hold := time.Until(c.until)
		c.mu.Unlock()
		if hold > 0 {
			return nil, ErrBusy
		}
		b, retry, err := c.once(ctx, path, wait, limit)
		if err == nil {
			return b, nil
		}
		last = err
		if !retry {
			break
		}
	}
	return nil, last
}

func (c *Catalog) once(ctx context.Context, path string, wait time.Duration, limit int64) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Base, "/")+path, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", cmp.Or(c.UA, UserAgent))
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil || errors.Is(ctx.Err(), context.DeadlineExceeded), fmt.Errorf("模型库连不上：%w", err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		hold := time.Minute
		if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 {
			hold = min(time.Duration(s)*time.Second, 10*time.Minute)
		}
		c.mu.Lock()
		c.until = time.Now().Add(hold)
		c.mu.Unlock()
		return nil, false, ErrBusy
	case res.StatusCode == http.StatusNotFound:
		return nil, false, errors.New("模型库里没有这个")
	case res.StatusCode >= 500:
		return nil, true, fmt.Errorf("模型库出错（%d）", res.StatusCode)
	case res.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("模型库拒绝了（%d）", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, true, fmt.Errorf("模型库的回答没收完：%w", err)
	}
	if int64(len(b)) > limit {
		return nil, false, fmt.Errorf("文件超过 %d MB", limit>>20)
	}
	return b, false, nil
}
