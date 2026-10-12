// Package guard stands between whoever sends a command and the machine that would carry it
// out. A command for a machine fed by real equipment (not simulated, not emulated demo
// devices) is judged against the company's rules on the machine's state in the twin, and,
// when an AI sends it (an MCP client, an assistant), against the company's level: advisory —
// a person confirms every one; guarded — a person confirms the listed kinds; autonomous —
// the rules alone. A command that waits is kept as a request an operator approves or denies
// before it lapses. Every command to a machine, carried out or not, goes into the activity log
// with what was true at the time.
package guard

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// Command is what is asked of a machine.
type Command struct {
	Company string   `json:"company"`
	Plant   string   `json:"plant"`
	Asset   string   `json:"asset"`
	Cmd     string   `json:"cmd"`
	Arg     *float64 `json:"arg,omitempty"`
}

// Origin is who sends a command and from where.
type Origin struct {
	Who auth.Who
	// Via is where it came from, as the activity log says it: live, mcp, assistant.
	Via string
	// By is what the equipment adapters are told: operator, mcp, assistant.
	By string
	// AI: an MCP client or an assistant, not a person.
	AI bool
	// Browser is the page that sent it, so that page can skip the echo.
	Browser string
}

// Outcome is what became of a command.
type Outcome string

const (
	Sent    Outcome = "sent"
	Refused Outcome = "refused"
	Waiting Outcome = "waiting"
	// Failed: allowed, but the twin would not do it (no such machine, a command it cannot take).
	Failed Outcome = "failed"
)

// Verdict is what the guard did with a command.
type Verdict struct {
	Outcome Outcome `json:"outcome"`
	// Msg is the twin's answer when it was carried out, or why not.
	Msg string `json:"msg"`
	// Real: the command reaches a machine (it was judged).
	Real bool `json:"real"`
	// Request is the command waiting for a person.
	Request *Request `json:"request,omitempty"`
}

// Request is a command an AI sent that waits for a person.
type Request struct {
	ID      string   `json:"id"`
	Company string   `json:"company"`
	Plant   string   `json:"plant"`
	Asset   string   `json:"asset"`
	Cmd     string   `json:"cmd"`
	Arg     *float64 `json:"arg,omitempty"`
	// Who sent it, and how (mcp, assistant).
	Who string `json:"who"`
	Via string `json:"via"`
	// Why it waits, and the machine's state when it was sent.
	Reason  string    `json:"reason"`
	Context string    `json:"context"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
	// Status: waiting | sent | refused | denied | expired | failed
	Status    string     `json:"status"`
	DecidedBy string     `json:"decidedBy,omitempty"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
	// Result is the twin's answer, or why it was refused or denied.
	Result string `json:"result,omitempty"`

	by, key string
}

// Guard keeps every company's settings and waiting requests in {data}/guard.db.
type Guard struct {
	Eng   *engine.Engine
	Hub   *tags.Hub
	Audit *audit.Log
	Log   *slog.Logger
	// Now is the clock (tests set it).
	Now func() time.Time
	// Changed is told the company an AI's command went to, or whose request was decided or
	// lapsed (nil: no one).
	Changed func(company string)

	db      *sql.DB
	mu      sync.Mutex
	cache   map[string]Settings
	waiters map[string][]chan Request
}

func Open(dir string, eng *engine.Engine, hub *tags.Hub, trail *audit.Log, log *slog.Logger) (*Guard, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "guard.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (company TEXT PRIMARY KEY, json TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS requests (id TEXT PRIMARY KEY, company TEXT NOT NULL, plant TEXT NOT NULL, asset TEXT NOT NULL,
			cmd TEXT NOT NULL, arg REAL, who TEXT NOT NULL, who_key TEXT NOT NULL, via TEXT NOT NULL, by TEXT NOT NULL,
			reason TEXT NOT NULL, context TEXT NOT NULL, created INTEGER NOT NULL, expires INTEGER NOT NULL, status TEXT NOT NULL,
			decided_by TEXT NOT NULL, decided_at INTEGER NOT NULL, result TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS requests_company ON requests (company, status, created);
		CREATE TABLE IF NOT EXISTS machines (company TEXT NOT NULL, plant TEXT NOT NULL, asset TEXT NOT NULL, seen INTEGER NOT NULL,
			PRIMARY KEY (company, plant, asset))`); err != nil {
		db.Close()
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Guard{Eng: eng, Hub: hub, Audit: trail, Log: log, Now: time.Now, db: db, cache: map[string]Settings{}, waiters: map[string][]chan Request{}}, nil
}

func (g *Guard) Close() error { return g.db.Close() }

// Settings are a company's guard: its own, or the defaults.
func (g *Guard) Settings(company string) Settings {
	g.mu.Lock()
	s, ok := g.cache[company]
	g.mu.Unlock()
	if ok {
		return s
	}
	s = Defaults()
	var js string
	if err := g.db.QueryRow(`SELECT json FROM settings WHERE company=?`, company).Scan(&js); err == nil {
		json.Unmarshal([]byte(js), &s)
	}
	g.mu.Lock()
	g.cache[company] = s
	g.mu.Unlock()
	return s
}

// Problems is settings the guard refuses, and why.
type Problems struct{ List []string }

func (p *Problems) Error() string { return strings.Join(p.List, "；") }

// SetSettings changes a company's guard.
func (g *Guard) SetSettings(company string, s Settings) error {
	if s.Expire == 0 {
		s.Expire = Defaults().Expire
	}
	if s.Approve == nil {
		s.Approve = []string{}
	}
	if s.Rules == nil {
		s.Rules = []Rule{}
	}
	if p := s.Check(); len(p) > 0 {
		return &Problems{List: p}
	}
	b, _ := json.Marshal(s)
	if _, err := g.db.Exec(`INSERT INTO settings VALUES (?,?) ON CONFLICT(company) DO UPDATE SET json=excluded.json`, company, string(b)); err != nil {
		return err
	}
	g.mu.Lock()
	g.cache[company] = s
	g.mu.Unlock()
	return nil
}

// Real reports whether a command for the asset reaches a machine: real equipment feeds it or
// takes its commands now, or did once (a device that has not reported since a restart is still
// a machine).
func (g *Guard) Real(company, plant, asset string) bool {
	if g.Hub != nil && g.Hub.Real(company, plant, asset) {
		g.remember(company, plant+"/"+asset)
		return true
	}
	var n int
	g.db.QueryRow(`SELECT count(*) FROM machines WHERE company=? AND plant=? AND asset=?`, company, plant, asset).Scan(&n)
	return n > 0
}

// Machines are the company's machines (plant/asset) real equipment feeds now or did once.
func (g *Guard) Machines(company string) []string {
	if g.Hub != nil {
		for _, m := range g.Hub.RealAssets(company) {
			g.remember(company, m)
		}
	}
	out := []string{}
	rows, err := g.db.Query(`SELECT plant || '/' || asset FROM machines WHERE company=? ORDER BY plant, asset`, company)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		rows.Scan(&m)
		out = append(out, m)
	}
	return out
}

func (g *Guard) remember(company, plantAsset string) {
	plant, asset, _ := strings.Cut(plantAsset, "/")
	g.db.Exec(`INSERT INTO machines VALUES (?,?,?,?) ON CONFLICT DO UPDATE SET seen=excluded.seen`, company, plant, asset, g.Now().UnixMilli())
}

// Send judges a command and carries it out when it may go: the twin first, then the equipment.
func (g *Guard) Send(o Origin, c Command) Verdict {
	if o.AI {
		defer g.changed(c.Company)
	}
	real := g.Real(c.Company, c.Plant, c.Asset)
	if !real {
		v := g.exec(o, c, "")
		g.record(o.Who, o.Via, c, "command", v, "")
		return v
	}
	s := g.Settings(c.Company)
	state, ok := g.Eng.AssetState(c.Company, c.Plant, c.Asset)
	if !ok {
		v := Verdict{Outcome: Failed, Msg: fmt.Sprintf("找不到 %s/%s", c.Plant, c.Asset), Real: true}
		g.record(o.Who, o.Via, c, "command", v, "")
		return v
	}
	ctx := describe(state)
	if rule, why := s.refusal(c.Cmd, c.Arg, state); rule != nil {
		v := Verdict{Outcome: Refused, Msg: why, Real: true}
		g.record(o.Who, o.Via, c, "command.refused", v, ctx+"；规则："+rule.When)
		return v
	}
	if o.AI && (s.Level == Advisory || (s.Level == Guarded && slices.Contains(s.Approve, c.Cmd))) {
		reason := "这家公司的 AI 只能提出指令，由人确认"
		if s.Level == Guarded {
			reason = "这家公司让 AI 发的「" + c.Cmd + "」指令由人确认"
		}
		r, err := g.hold(o, c, reason, ctx, s.Expire)
		if err != nil {
			v := Verdict{Outcome: Failed, Msg: err.Error(), Real: true}
			g.record(o.Who, o.Via, c, "command.pending", v, ctx)
			return v
		}
		v := Verdict{Outcome: Waiting, Msg: fmt.Sprintf("等操作员批准（%s，%d 分钟内有效）", r.ID, s.Expire), Real: true, Request: r}
		g.record(o.Who, o.Via, c, "command.pending", v, ctx+"；"+reason)
		return v
	}
	v := g.exec(o, c, "")
	v.Real = true
	g.record(o.Who, o.Via, c, "command", v, ctx)
	return v
}

// exec carries a command out: the twin, then every adapter that can act on it.
func (g *Guard) exec(o Origin, c Command, name string) Verdict {
	r := g.Eng.Command(c.Company, c.Plant, c.Asset, c.Cmd, c.Arg)
	if !r.OK {
		return Verdict{Outcome: Failed, Msg: r.Msg}
	}
	if g.Hub != nil {
		g.Hub.Command(tags.Command{Company: c.Company, Plant: c.Plant, Asset: c.Asset, Cmd: c.Cmd, Arg: c.Arg, By: o.By, Name: cmpOr(name, o.Who.Name), Origin: o.Browser})
	}
	return Verdict{Outcome: Sent, Msg: r.Msg}
}

func (g *Guard) record(who auth.Who, via string, c Command, action string, v Verdict, ctx string) {
	var err error
	if v.Outcome == Failed || v.Outcome == Refused {
		err = errors.New(v.Msg)
	}
	target := c.Plant + "/" + c.Asset
	if v.Request != nil {
		target += " " + v.Request.ID
	}
	g.Audit.RecordWith(who, via, c.Company, action, target, Text(c.Cmd, c.Arg), ctx, err)
}

// Text is a command as the activity log writes it: "speed 1.2", "stop".
func Text(cmd string, arg *float64) string {
	if arg == nil {
		return cmd
	}
	return cmd + " " + strconv.FormatFloat(*arg, 'g', -1, 64)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "CQ-" + strings.ToUpper(hex.EncodeToString(b))
}

func (g *Guard) hold(o Origin, c Command, reason, ctx string, minutes int) (*Request, error) {
	now := g.Now()
	r := &Request{ID: newID(), Company: c.Company, Plant: c.Plant, Asset: c.Asset, Cmd: c.Cmd, Arg: c.Arg, Who: o.Who.Name, Via: o.Via,
		Reason: reason, Context: ctx, Created: now, Expires: now.Add(time.Duration(minutes) * time.Minute), Status: "waiting", by: o.By, key: o.Who.Key}
	var arg any
	if c.Arg != nil {
		arg = *c.Arg
	}
	_, err := g.db.Exec(`INSERT INTO requests VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Company, r.Plant, r.Asset, r.Cmd, arg,
		r.Who, r.key, r.Via, r.by, r.Reason, r.Context, now.UnixMilli(), r.Expires.UnixMilli(), r.Status, "", 0, "")
	return r, err
}

const reqCols = `id, company, plant, asset, cmd, arg, who, who_key, via, by, reason, context, created, expires, status, decided_by, decided_at, result`

func scan(sc interface{ Scan(...any) error }) (Request, error) {
	var r Request
	var arg sql.NullFloat64
	var created, expires, decided int64
	err := sc.Scan(&r.ID, &r.Company, &r.Plant, &r.Asset, &r.Cmd, &arg, &r.Who, &r.key, &r.Via, &r.by, &r.Reason, &r.Context,
		&created, &expires, &r.Status, &r.DecidedBy, &decided, &r.Result)
	if arg.Valid {
		r.Arg = &arg.Float64
	}
	r.Created, r.Expires = time.UnixMilli(created), time.UnixMilli(expires)
	if decided > 0 {
		t := time.UnixMilli(decided)
		r.DecidedAt = &t
	}
	return r, err
}

// Get is one request, or nil.
func (g *Guard) Get(company, id string) (*Request, error) {
	g.lapse()
	r, err := scan(g.db.QueryRow(`SELECT `+reqCols+` FROM requests WHERE company=? AND id=?`, company, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// List is a company's requests, waiting ones first, then the newest; status narrows it.
func (g *Guard) List(company, status string, limit int) ([]Request, error) {
	g.lapse()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + reqCols + ` FROM requests WHERE company=?`
	args := []any{company}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	rows, err := g.db.Query(q+` ORDER BY status='waiting' DESC, created DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Between is every request of a company made at or after from and before to, oldest first.
func (g *Guard) Between(company string, from, to time.Time) ([]Request, error) {
	g.lapse()
	rows, err := g.db.Query(`SELECT `+reqCols+` FROM requests WHERE company=? AND created>=? AND created<? ORDER BY created`,
		company, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ErrDecided is a request that is no longer waiting.
var ErrDecided = errors.New("这条指令已经处理过了")

// Approve carries a waiting command out as the person who approved it, if the rules still let
// it go on the machine as it is now.
func (g *Guard) Approve(who auth.Who, via, company, id string) (*Request, error) {
	r, err := g.take(company, id)
	if err != nil || r == nil {
		return r, err
	}
	c := Command{Company: r.Company, Plant: r.Plant, Asset: r.Asset, Cmd: r.Cmd, Arg: r.Arg}
	s := g.Settings(company)
	state, ok := g.Eng.AssetState(c.Company, c.Plant, c.Asset)
	ctx := describe(state) + "；" + r.Who + " 发起，" + who.Name + " 批准"
	var v Verdict
	status := "sent"
	switch rule, why := s.refusal(c.Cmd, c.Arg, state); {
	case !ok:
		v, status = Verdict{Outcome: Failed, Msg: fmt.Sprintf("找不到 %s/%s", c.Plant, c.Asset)}, "failed"
	case rule != nil:
		// the machine changed while the command waited
		v, status = Verdict{Outcome: Refused, Msg: why}, "refused"
		ctx += "；规则：" + rule.When
	default:
		v = g.exec(Origin{Who: who, Via: r.Via, By: r.by}, c, r.Who+"（"+who.Name+" 批准）")
		if v.Outcome == Failed {
			status = "failed"
		}
	}
	v.Request = r
	g.record(who, via, c, "command.approved", v, ctx)
	return g.decide(r, status, who.Name, v.Msg)
}

// Deny turns a waiting command down.
func (g *Guard) Deny(who auth.Who, via, company, id, reason string) (*Request, error) {
	r, err := g.take(company, id)
	if err != nil || r == nil {
		return r, err
	}
	msg := cmpOr(strings.TrimSpace(reason), "操作员没有批准")
	c := Command{Company: r.Company, Plant: r.Plant, Asset: r.Asset, Cmd: r.Cmd, Arg: r.Arg}
	g.record(who, via, c, "command.denied", Verdict{Outcome: Refused, Msg: msg, Request: r}, r.Who+" 发起；"+msg)
	return g.decide(r, "denied", who.Name, msg)
}

// take claims a waiting request for a decision: nil when there is none, ErrDecided when
// it was decided (or lapsed) already.
func (g *Guard) take(company, id string) (*Request, error) {
	r, err := g.Get(company, id)
	if err != nil || r == nil {
		return nil, err
	}
	if r.Status != "waiting" {
		return r, ErrDecided
	}
	res, err := g.db.Exec(`UPDATE requests SET status='deciding' WHERE id=? AND status='waiting'`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return r, ErrDecided
	}
	return r, nil
}

func (g *Guard) decide(r *Request, status, by, result string) (*Request, error) {
	now := g.Now()
	if _, err := g.db.Exec(`UPDATE requests SET status=?, decided_by=?, decided_at=?, result=? WHERE id=?`, status, by, now.UnixMilli(), result, r.ID); err != nil {
		return nil, err
	}
	r.Status, r.DecidedBy, r.DecidedAt, r.Result = status, by, &now, result
	g.notify(*r)
	g.changed(r.Company)
	return r, nil
}

func (g *Guard) changed(company string) {
	if g.Changed != nil {
		g.Changed(company)
	}
}

// lapse lets every request past its time expire.
func (g *Guard) lapse() {
	now := g.Now().UnixMilli()
	rows, err := g.db.Query(`SELECT `+reqCols+` FROM requests WHERE status='waiting' AND expires<=?`, now)
	if err != nil {
		return
	}
	var gone []Request
	for rows.Next() {
		if r, err := scan(rows); err == nil {
			gone = append(gone, r)
		}
	}
	rows.Close()
	for _, r := range gone {
		res, err := g.db.Exec(`UPDATE requests SET status='expired', decided_at=?, result=? WHERE id=? AND status='waiting'`, now, "没有人在时限内批准", r.ID)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		r.Status, r.Result = "expired", "没有人在时限内批准"
		c := Command{Company: r.Company, Plant: r.Plant, Asset: r.Asset, Cmd: r.Cmd, Arg: r.Arg}
		g.record(auth.Who{Name: "指令把关"}, "guard", c, "command.expired", Verdict{Outcome: Refused, Msg: r.Result, Request: &r}, r.Who+" 发起")
		g.notify(r)
		g.changed(r.Company)
	}
}

// Run lets requests lapse on time until ctx ends.
func (g *Guard) Run(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.lapse()
			if g.Hub != nil && g.Eng != nil {
				for _, c := range g.Eng.CompanyIDs() {
					for _, m := range g.Hub.RealAssets(c) {
						g.remember(c, m)
					}
				}
			}
		}
	}
}

func (g *Guard) notify(r Request) {
	g.mu.Lock()
	ws := g.waiters[r.ID]
	delete(g.waiters, r.ID)
	g.mu.Unlock()
	for _, w := range ws {
		w <- r
	}
}

// Wait is the request once it is decided, or as it is when ctx ends.
func (g *Guard) Wait(ctx context.Context, company, id string) (*Request, error) {
	ch := make(chan Request, 1)
	g.mu.Lock()
	g.waiters[id] = append(g.waiters[id], ch)
	g.mu.Unlock()
	r, err := g.Get(company, id)
	if err != nil || r == nil || (r.Status != "waiting" && r.Status != "deciding") {
		return r, err
	}
	select {
	case got := <-ch:
		return &got, nil
	case <-ctx.Done():
		g.mu.Lock()
		g.waiters[id] = slices.DeleteFunc(g.waiters[id], func(c chan Request) bool { return c == ch })
		if len(g.waiters[id]) == 0 {
			delete(g.waiters, id)
		}
		g.mu.Unlock()
		return g.Get(company, id)
	}
}
