package erp

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"smartfactory/internal/engine"
)

// Config is how a company's ERP is connected.
type Config struct {
	Enabled bool `json:"enabled"`
	// WhenEmpty is what a line with no order from the ERP does: idle (waits for one) or
	// simulate (makes up its own, as the twin does with no ERP).
	WhenEmpty string  `json:"whenEmpty"`
	Pull      *Pull   `json:"pull,omitempty"`
	Notify    *Notify `json:"notify,omitempty"`
}

// Pull fetches orders from the ERP's API: a request, where the list is in the answer, and
// where each field is in an item (dotted paths; a number indexes an array, for ERPs that
// answer rows as arrays).
type Pull struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Body    string            `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Items   string            `json:"items,omitempty"`
	// Fields maps no, partNo, product, name, qty, due, customer, price, plant, line to paths.
	Fields map[string]string `json:"fields"`
	// Every is minutes between pulls; 0 pulls only when asked.
	Every int `json:"every,omitempty"`
}

// Notify is where started and finished orders are posted, signed with Secret.
type Notify struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

// Status is how the connection last went.
type Status struct {
	LastPull     *time.Time `json:"lastPull,omitempty"`
	PullError    string     `json:"pullError,omitempty"`
	PullFetched  int        `json:"pullFetched"`
	PullProblems []string   `json:"pullProblems,omitempty"`
	LastNotify   *time.Time `json:"lastNotify,omitempty"`
	NotifyError  string     `json:"notifyError,omitempty"`
	Delivered    int        `json:"delivered"`
	Failed       int        `json:"failed"`
}

// Fields are the order fields a pull maps.
var Fields = []string{"no", "partNo", "product", "name", "qty", "due", "customer", "price", "plant", "line"}

// Service keeps the orders moving between the store, the lines and the ERP.
type Service struct {
	Store *Store
	Eng   *engine.Engine
	Log   *slog.Logger
	HTTP  *http.Client
	// Every is how often the lines and the store are brought in step (2 s).
	Every time.Duration

	mu      sync.Mutex
	dirty   map[string]bool
	on      map[string]bool
	pulling map[string]bool
	cfgs    map[string]Config
	status  map[string]Status
}

func New(store *Store, eng *engine.Engine, log *slog.Logger) *Service {
	return &Service{
		Store: store, Eng: eng, Log: log, HTTP: &http.Client{Timeout: 20 * time.Second}, Every: 2 * time.Second,
		dirty: map[string]bool{}, on: map[string]bool{}, pulling: map[string]bool{}, cfgs: map[string]Config{}, status: map[string]Status{},
	}
}

// Config is a company's connection, as stored (secrets included).
func (s *Service) Config(company string) (Config, Status, error) {
	s.mu.Lock()
	cfg, ok := s.cfgs[company]
	st := s.status[company]
	s.mu.Unlock()
	if ok {
		return cfg, st, nil
	}
	cfg, st, err := s.Store.settings(company)
	if err != nil {
		return cfg, st, err
	}
	cfg.normalize()
	s.mu.Lock()
	s.cfgs[company], s.status[company] = cfg, st
	s.mu.Unlock()
	return cfg, st, nil
}

func (c *Config) normalize() {
	if c.WhenEmpty != "simulate" {
		c.WhenEmpty = "idle"
	}
	if c.Pull != nil {
		c.Pull.Method = strings.ToUpper(cmp.Or(strings.TrimSpace(c.Pull.Method), "GET"))
	}
}

// Check says what is wrong with a config.
func (c Config) Check() []string {
	var out []string
	if p := c.Pull; p != nil {
		if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
			out = append(out, "拉取地址要以 http:// 或 https:// 开头")
		}
		if p.Method != "GET" && p.Method != "POST" {
			out = append(out, "拉取方法只能是 GET 或 POST")
		}
		if p.Fields["no"] == "" || p.Fields["qty"] == "" {
			out = append(out, "字段映射至少要有工单号（no）和数量（qty）")
		}
		if p.Fields["partNo"] == "" && p.Fields["product"] == "" && p.Fields["line"] == "" {
			out = append(out, "字段映射要有料号（partNo）、产品（product）或产线（line）之一，才知道在哪条线做")
		}
		for k := range p.Fields {
			if !slices.Contains(Fields, k) {
				out = append(out, fmt.Sprintf("没有字段 %q，可以映射的是 %s", k, strings.Join(Fields, "、")))
			}
		}
		if p.Every < 0 || p.Every > 24*60 {
			out = append(out, "拉取间隔是 0～1440 分钟")
		}
	}
	if n := c.Notify; n != nil && !strings.HasPrefix(n.URL, "http://") && !strings.HasPrefix(n.URL, "https://") {
		out = append(out, "回调地址要以 http:// 或 https:// 开头")
	}
	return out
}

// SetConfig stores a company's connection; header values left empty keep what they were, and a
// callback gets a signing secret the first time (returned, the only time it is shown).
func (s *Service) SetConfig(company string, in Config) (secret string, err error) {
	old, _, err := s.Config(company)
	if err != nil {
		return "", err
	}
	in.normalize()
	if in.Pull != nil {
		if in.Pull.Fields == nil {
			in.Pull.Fields = map[string]string{}
		}
		maps.DeleteFunc(in.Pull.Fields, func(_, v string) bool { return strings.TrimSpace(v) == "" })
		for k, v := range in.Pull.Headers {
			if v == "" && old.Pull != nil && old.Pull.Headers[k] != "" {
				in.Pull.Headers[k] = old.Pull.Headers[k]
			}
		}
	}
	if in.Notify != nil {
		in.Notify.Secret = ""
		if old.Notify != nil {
			in.Notify.Secret = old.Notify.Secret
		}
		if in.Notify.Secret == "" {
			secret = newSecret()
			in.Notify.Secret = secret
		}
	}
	if problems := in.Check(); len(problems) > 0 {
		return "", &Problems{List: problems}
	}
	if err := s.Store.saveConfig(company, in); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.cfgs[company] = in
	s.dirty[company] = true
	s.mu.Unlock()
	return secret, nil
}

// RotateSecret gives the callback a new signing secret.
func (s *Service) RotateSecret(company string) (string, error) {
	cfg, _, err := s.Config(company)
	if err != nil {
		return "", err
	}
	if cfg.Notify == nil {
		return "", &Problems{List: []string{"还没有设回调地址"}}
	}
	cfg.Notify.Secret = newSecret()
	if err := s.Store.saveConfig(company, cfg); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.cfgs[company] = cfg
	s.mu.Unlock()
	return cfg.Notify.Secret, nil
}

func newSecret() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "whsec_" + hex.EncodeToString(b)
}

// Problems is a config or order the service refuses, and why.
type Problems struct{ List []string }

func (p *Problems) Error() string { return strings.Join(p.List, "；") }

// Result is what became of one order sent.
type Result struct {
	No       string  `json:"no"`
	OK       bool    `json:"ok"`
	Outcome  Outcome `json:"outcome,omitempty"`
	Plant    string  `json:"plant,omitempty"`
	Line     string  `json:"line,omitempty"`
	LineName string  `json:"lineName,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// Submit stores orders the ERP sent, each routed to a line; those that cannot be (no line makes
// the part, the line it names does not exist) are refused one by one.
func (s *Service) Submit(company string, ins []Input, source string) []Result {
	now := time.Now()
	out := make([]Result, len(ins))
	changed := false
	for i, in := range ins {
		r := Result{No: in.No}
		if err := in.check(); err != nil {
			r.Error = err.Error()
			out[i] = r
			continue
		}
		route, res := s.route(company, in)
		if !res.OK {
			r.Error = strings.Join(res.Errors, "；")
			out[i] = r
			continue
		}
		oc, err := s.Store.put(company, in, route, source, now)
		switch {
		case errors.Is(err, errFinished):
			r.OK, r.Outcome = true, Unchanged
		case err != nil:
			r.Error = err.Error()
		default:
			r.OK, r.Outcome = true, oc
			changed = changed || oc != Unchanged
		}
		r.Plant, r.Line, r.LineName = route.Plant, route.Line, route.LineName
		out[i] = r
	}
	if changed {
		s.touch(company)
	}
	return out
}

// route picks the line for an order, counting what is queued in the store for each line (the
// lines' own queues lag it by a sync).
func (s *Service) route(company string, in Input) (engine.Route, engine.ConfigResult) {
	ahead, err := s.Store.ahead(company, in.No)
	if err != nil {
		return engine.Route{}, engine.ConfigResult{Errors: []string{err.Error()}}
	}
	return s.Eng.RouteOrder(company, struct {
		Input
		Ahead map[string]int `json:"ahead"`
	}{in, ahead})
}

func (in *Input) check() error {
	in.No = strings.TrimSpace(in.No)
	switch {
	case in.No == "":
		return errors.New("缺工单号（no）")
	case len(in.No) > 64:
		return errors.New("工单号最长 64 个字符")
	case in.Qty <= 0:
		return errors.New("数量（qty）要大于 0")
	case in.Qty > 10_000_000:
		return errors.New("数量（qty）太大")
	}
	return nil
}

// Cancel withdraws an order; a line running it moves on to its next one.
func (s *Service) Cancel(company, no string) (*Order, error) {
	o, err := s.Store.cancel(company, no, time.Now())
	if err == nil && o != nil {
		s.touch(company)
	}
	return o, err
}

func (s *Service) touch(company string) {
	s.mu.Lock()
	s.dirty[company] = true
	s.mu.Unlock()
}

// Reroute routes the queued orders again, after the company's factory changed: those no line
// makes any more stay queued with the reason.
func (s *Service) Reroute(company string) {
	ins, err := s.Store.queuedRoutes(company)
	if err != nil {
		s.Log.Error("erp: reroute", "company", company, "err", err)
		return
	}
	now := time.Now()
	for _, in := range ins {
		r, res := s.route(company, in)
		if err := s.Store.reroute(company, in.No, r, strings.Join(res.Errors, "；"), now); err != nil {
			s.Log.Error("erp: reroute", "company", company, "order", in.No, "err", err)
		}
	}
	s.touch(company)
}

// Run keeps the lines' queues and the store in step every couple of seconds, and pulls from the
// ERPs that are pulled on a schedule.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, c := range s.Eng.CompanyIDs() {
			cfg, st, err := s.Config(c)
			if err != nil {
				s.Log.Error("erp: settings", "company", c, "err", err)
				continue
			}
			s.sync(ctx, c, cfg)
			if p := cfg.Pull; cfg.Enabled && p != nil && p.Every > 0 && (st.LastPull == nil || time.Since(*st.LastPull) >= time.Duration(p.Every)*time.Minute) {
				go func() {
					if _, err := s.Pull(ctx, c, false); err != nil && !errors.Is(err, errBusy) {
						s.Log.Warn("erp: pull", "company", c, "err", err)
					}
				}()
			}
		}
	}
}

func (s *Service) sync(ctx context.Context, c string, cfg Config) {
	s.mu.Lock()
	on, dirty := s.on[c], s.dirty[c]
	s.dirty[c] = false
	if !cfg.Enabled {
		s.on[c] = false
	}
	s.mu.Unlock()
	if !cfg.Enabled {
		if on {
			s.Eng.StopOrders(c)
		}
		return
	}
	var q *engine.OrderQueue
	if !on || dirty {
		var err error
		if q, err = s.Store.queue(c, cfg.WhenEmpty); err != nil {
			s.Log.Error("erp: queue", "company", c, "err", err)
			s.touch(c)
			return
		}
	}
	res, ok := s.Eng.SyncOrders(c, q)
	if !ok {
		return
	}
	now := time.Now()
	for _, e := range res.Events {
		var changed bool
		var err error
		event := "order.started"
		if e.Kind == "done" {
			event = "order.done"
			changed, err = s.Store.finished(c, e, now)
		} else {
			changed, err = s.Store.started(c, e, now)
		}
		if err != nil {
			s.Log.Error("erp: order", "company", c, "order", e.No, "err", err)
			continue
		}
		if changed && cfg.Notify != nil {
			if o, _ := s.Store.Get(c, e.No); o != nil {
				go s.notify(ctx, c, *cfg.Notify, event, *o)
			}
		}
	}
	for _, r := range res.Running {
		// a line runs an order the store still has queued (the twin carried it over a restart): it started
		if changed, err := s.Store.started(c, r, now); err == nil && changed {
			if cfg.Notify != nil {
				if o, _ := s.Store.Get(c, r.No); o != nil {
					go s.notify(ctx, c, *cfg.Notify, "order.started", *o)
				}
			}
		}
		if err := s.Store.progress(c, r); err != nil {
			s.Log.Error("erp: progress", "company", c, "order", r.No, "err", err)
		}
	}
	if !on {
		// orders the store has running that no line runs (the twin's state was lost) go back in the queue
		running := map[string]bool{}
		for _, r := range res.Running {
			running[r.No] = true
		}
		for _, e := range res.Events {
			running[e.No] = true
		}
		if n, err := s.Store.requeueLost(c, running, now); err != nil {
			s.Log.Error("erp: requeue", "company", c, "err", err)
		} else if n > 0 {
			s.touch(c)
		}
		s.mu.Lock()
		s.on[c] = true
		s.mu.Unlock()
	}
}

// notify posts an order's news to the ERP's callback, signed: X-SF-Signature is
// sha256=HMAC-SHA256(secret, body) in hex. Three tries.
func (s *Service) notify(ctx context.Context, company string, n Notify, event string, o Order) {
	body, _ := json.Marshal(map[string]any{"event": event, "company": company, "at": time.Now().Format(time.RFC3339), "order": o})
	mac := hmac.New(sha256.New, []byte(n.Secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	var last error
	for try, wait := range []time.Duration{0, 5 * time.Second, 30 * time.Second} {
		if try > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SF-Event", event)
		req.Header.Set("X-SF-Signature", sig)
		res, err := s.HTTP.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			if res.StatusCode < 300 {
				last = nil
				break
			}
			err = fmt.Errorf("回调返回 %s", res.Status)
		}
		last = err
	}
	s.mu.Lock()
	st := s.status[company]
	now := time.Now()
	st.LastNotify = &now
	if last != nil {
		st.Failed++
		st.NotifyError = last.Error()
		s.Log.Warn("erp: callback", "company", company, "order", o.No, "err", last)
	} else {
		st.Delivered++
		st.NotifyError = ""
	}
	s.status[company] = st
	s.mu.Unlock()
	s.Store.saveStatus(company, st)
}

// PullItem is one order a pull read, and where it would go.
type PullItem struct {
	Order    Input  `json:"order"`
	LineName string `json:"lineName,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	Error    string `json:"error,omitempty"`
}

// PullResult is what a pull read and did.
type PullResult struct {
	Fetched int        `json:"fetched"`
	Items   []PullItem `json:"items"`
	Created int        `json:"created"`
	Updated int        `json:"updated"`
	Failed  int        `json:"failed"`
}

var errBusy = errors.New("上一次拉取还没结束")

// Pull fetches the ERP's orders and stores them; dry only shows what it read and where each
// would go.
func (s *Service) Pull(ctx context.Context, company string, dry bool) (*PullResult, error) {
	cfg, _, err := s.Config(company)
	if err != nil {
		return nil, err
	}
	if cfg.Pull == nil {
		return nil, &Problems{List: []string{"还没有设拉取地址"}}
	}
	s.mu.Lock()
	if s.pulling[company] {
		s.mu.Unlock()
		return nil, errBusy
	}
	s.pulling[company] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pulling, company); s.mu.Unlock() }()

	res, err := s.fetch(ctx, *cfg.Pull)
	if !dry {
		s.mu.Lock()
		st := s.status[company]
		now := time.Now()
		st.LastPull = &now
		st.PullError, st.PullProblems = "", nil
		if err != nil {
			st.PullError = err.Error()
		}
		s.status[company] = st
		s.mu.Unlock()
		defer func() { s.mu.Lock(); st := s.status[company]; s.mu.Unlock(); s.Store.saveStatus(company, st) }()
	}
	if err != nil {
		return nil, err
	}
	var problems []string
	if dry {
		for i := range res.Items {
			it := &res.Items[i]
			if it.Error != "" {
				continue
			}
			if r, v := s.route(company, it.Order); v.OK {
				it.LineName = r.PlantName + " " + r.LineName
			} else {
				it.Error = strings.Join(v.Errors, "；")
			}
		}
	} else {
		var ins []Input
		var at []int
		for i, it := range res.Items {
			if it.Error == "" {
				ins = append(ins, it.Order)
				at = append(at, i)
			}
		}
		for j, r := range s.Submit(company, ins, "pull") {
			it := &res.Items[at[j]]
			it.LineName, it.Outcome, it.Error = r.LineName, string(r.Outcome), r.Error
		}
	}
	for _, it := range res.Items {
		switch {
		case it.Error != "":
			res.Failed++
			if len(problems) < 20 {
				problems = append(problems, cmp.Or(it.Order.No, "（没有工单号）")+"："+it.Error)
			}
		case it.Outcome == string(Created) || it.Outcome == string(Requeued):
			res.Created++
		case it.Outcome == string(Updated):
			res.Updated++
		}
	}
	if !dry {
		s.mu.Lock()
		st := s.status[company]
		st.PullFetched, st.PullProblems = res.Fetched, problems
		s.status[company] = st
		s.mu.Unlock()
	}
	return res, nil
}

func (s *Service) fetch(ctx context.Context, p Pull) (*PullResult, error) {
	var body io.Reader
	if p.Body != "" {
		body = strings.NewReader(p.Body)
	}
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	if p.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 ERP：%w", err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("ERP 返回 %s：%s", res.Status, clip(string(b), 200))
	}
	var doc any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		return nil, fmt.Errorf("ERP 返回的不是 JSON：%s", clip(string(b), 200))
	}
	list, ok := At(doc, p.Items).([]any)
	if !ok {
		return nil, fmt.Errorf("在返回的 JSON 里 %q 处没有找到列表", cmp.Or(p.Items, "（根）"))
	}
	out := &PullResult{Fetched: len(list), Items: make([]PullItem, len(list))}
	for i, item := range list {
		m := map[string]any{}
		for k, path := range p.Fields {
			m[k] = At(item, path)
		}
		in, err := InputFrom(m)
		out.Items[i] = PullItem{Order: in}
		if err == nil {
			err = in.check()
		}
		if err != nil {
			out.Items[i].Error = err.Error()
		}
	}
	return out, nil
}

func clip(s string, n int) string {
	if r := []rune(strings.TrimSpace(s)); len(r) > n {
		return string(r[:n]) + "…"
	}
	return strings.TrimSpace(s)
}

// At is the value at a dotted path ("data.rows", "FMaterialId.FNumber", "0"); "" is v itself.
func At(v any, path string) any {
	if path = strings.TrimSpace(path); path == "" {
		return v
	}
	for _, seg := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			v = x[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

// InputFrom reads an order from loosely typed fields: numbers may come as strings, due dates
// as RFC 3339, "2006-01-02 15:04:05", "2006-01-02" (Beijing time) or epoch seconds or milliseconds.
func InputFrom(m map[string]any) (Input, error) {
	str := func(k string) string {
		switch v := m[k].(type) {
		case nil:
			return ""
		case string:
			return strings.TrimSpace(v)
		default:
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	in := Input{No: str("no"), PartNo: str("partNo"), Product: str("product"), Name: str("name"), Customer: str("customer"), Plant: str("plant"), Line: str("line")}
	if s := str("qty"); s != "" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return in, fmt.Errorf("数量 %q 不是数字", s)
		}
		in.Qty = int(math.Round(f))
	}
	if s := str("price"); s != "" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return in, fmt.Errorf("单价 %q 不是数字", s)
		}
		in.Price = f
	}
	if s := str("due"); s != "" {
		t, err := ParseTime(s)
		if err != nil {
			return in, err
		}
		in.Due = &t
	}
	return in, nil
}

var shanghai = time.FixedZone("CST", 8*3600)

// ParseTime reads a due date the ways ERPs write them.
func ParseTime(s string) (time.Time, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e12 {
			return time.UnixMilli(n), nil
		}
		return time.Unix(n, 0), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, f := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006/01/02 15:04:05", "2006/01/02"} {
		if t, err := time.ParseInLocation(f, s, shanghai); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("交期 %q 看不懂，用 2026-10-20 或 2026-10-20 17:00 这样的写法", s)
}
