package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"smartfactory/internal/agents"
	"smartfactory/internal/auth"
	"smartfactory/internal/scoped"
)

// ---- changing one part of a company ----
//
// A built company is changed one part at a time — a plant, an area, a line or a machine — by a
// form or by the AI. Either way the change comes in as the company's whole config (the form's)
// or a list of changes (the AI's), is laid out by the engine, and is refused as a whole when it
// touches anything outside the part picked: a change half applied (a station taken off one line
// but not put on the other) is worse than none, and saying what was out of bounds lets the
// person (or the AI) ask again for just this part. Saving goes the way a builder save goes (the
// same check, the twin rebuilt, the earlier config kept for 撤销), recorded as company.edit.

// ScopeView is the part picked, with the company's config to change it in.
type ScopeView struct {
	Scope *scoped.Scope `json:"scope"`
	Model *scoped.Model `json:"model"`
	// Base stamps the config the view is of: a save against an older one is refused.
	Base string `json:"base"`
	// Versions is how many earlier configs are kept to undo to; Last when the latest was kept.
	Versions int        `json:"versions"`
	Last     *time.Time `json:"last,omitempty"`
}

// ScopeRequest is a change to a part: the whole config as the form left it, or the AI's changes.
type ScopeRequest struct {
	Node    scoped.Node     `json:"node"`
	Config  map[string]any  `json:"config,omitempty"`
	Changes []scoped.Change `json:"changes,omitempty"`
	Base    string          `json:"base"`
}

// ScopeResult is what a change does (and, saved, did).
type ScopeResult struct {
	Title  string `json:"title"`
	Shared string `json:"shared,omitempty"`
	// Split says that this copy of a repeated line is made a line of its own, the others left as they are
	Split   string         `json:"split,omitempty"`
	Preview scoped.Preview `json:"preview"`
	// Config is the company's config with the change, as it would be kept.
	Config map[string]any `json:"config"`
	Base   string         `json:"base"`
	Saved  bool           `json:"saved"`
}

func stamp(cfg string) string {
	h := sha256.Sum256([]byte(cfg))
	return hex.EncodeToString(h[:8])
}

// scopeBase is the company's config now, as a scope model, for an administrator of it.
func (o *Ops) scopeBase(who auth.Who, company string) (*scoped.Model, string, error) {
	if err := Admin(who); err != nil {
		return nil, "", err
	}
	if !who.Can(company, auth.Admin) {
		return nil, "", fail(403, "你不是公司 %s 的管理员", company)
	}
	cfg := o.Engine.CompanyConfig(company)
	if cfg == "null" {
		return nil, "", fail(404, "没有公司 %s", company)
	}
	if o.Engine.BuiltinTemplate(company) != "null" {
		return nil, "", fail(409, "%s 是内置的示例公司，不能改；先用它做模板建一个自己的公司", company)
	}
	res := o.Engine.ScopeModel([]byte(cfg))
	if !res.OK {
		return nil, "", rejected(res)
	}
	m, err := scoped.Parse(res.Detail)
	if err != nil {
		return nil, "", err
	}
	return m, stamp(cfg), nil
}

// splitBase is base with the copy picked made a line entry of its own, when the node asks for
// just that copy of a repeated line; the words say what was done ("" when nothing was).
func (o *Ops) splitBase(base *scoped.Model, n scoped.Node) (*scoped.Model, string, error) {
	if !n.Single {
		return base, "", nil
	}
	cfg, msg, ok := scoped.Split(base, n)
	if !ok {
		return base, "", nil
	}
	raw, _ := json.Marshal(scoped.Compact(base, cfg))
	res := o.Engine.ScopeModel(raw)
	if !res.OK {
		return nil, "", rejected(res)
	}
	m, err := scoped.Parse(res.Detail)
	if err != nil {
		return nil, "", err
	}
	return m, msg, nil
}

// Scope is the part of a company picked, for its editor.
func (o *Ops) Scope(who auth.Who, company string, n scoped.Node) (*ScopeView, error) {
	m, base, err := o.scopeBase(who, company)
	if err != nil {
		return nil, err
	}
	m, split, err := o.splitBase(m, n)
	if err != nil {
		return nil, err
	}
	s, err := scoped.Resolve(m, n)
	if err != nil {
		return nil, fail(404, "%s", err)
	}
	s.Split = split
	v := &ScopeView{Scope: s, Model: m, Base: base}
	vs := o.Engine.Versions(company)
	v.Versions = len(vs)
	if len(vs) > 0 {
		v.Last = &vs[len(vs)-1].At
	}
	return v, nil
}

// proposeScoped lays out the change and says what it does; refused when it touches anything outside.
func (o *Ops) proposeScoped(company string, base *scoped.Model, s *scoped.Scope, req ScopeRequest) (*ScopeResult, *scoped.Model, error) {
	cfg := req.Config
	if len(req.Changes) > 0 {
		var err error
		if cfg, err = scoped.Apply(base.Config, req.Changes); err != nil {
			return nil, nil, &Error{Code: 422, Msg: err.Error(), Problems: []string{err.Error()}}
		}
	}
	if cfg == nil {
		return nil, nil, fail(400, "没有要改的内容")
	}
	keep := scoped.Compact(base, cfg)
	raw, _ := json.Marshal(keep)
	res := o.Engine.ScopeModel(raw)
	if !res.OK {
		return nil, nil, rejected(res)
	}
	prop, err := scoped.Parse(res.Detail)
	if err != nil {
		return nil, nil, err
	}
	if out := s.Outside(base, prop); len(out) > 0 {
		msg := fmt.Sprintf("只能改%s，这次改动还碰到了：%s。没有采用，范围外的地方要单独选中再改", s.Title, strings.Join(out, "；"))
		return nil, nil, &Error{Code: 422, Msg: msg, Problems: out, Where: "scope"}
	}
	pv := scoped.Describe(base, prop)
	o.attachments(company, pv.Tags)
	return &ScopeResult{Title: s.Title, Shared: s.Shared, Split: s.Split, Preview: pv, Config: keep}, prop, nil
}

// attachments says what is filed under each tag that changes or goes: devices that report for
// it, check forms filled in on it.
func (o *Ops) attachments(company string, tags []scoped.TagChange) {
	for i := range tags {
		t := &tags[i]
		if o.Devices != nil {
			if list, err := o.Devices.List(company); err == nil {
				n := 0
				for _, d := range list {
					if d.Revoked == nil && d.Plant == t.Plant && slices.Contains(d.Assets, t.From) {
						n++
					}
				}
				if n > 0 {
					t.Attached = append(t.Attached, fmt.Sprintf("%d 台设备按这个位号接入", n))
				}
			}
		}
		if o.Checks != nil {
			if subs, err := o.Checks.Latest(company, t.Plant, t.From); err == nil && len(subs) > 0 {
				t.Attached = append(t.Attached, fmt.Sprintf("%d 张点检表填过", len(subs)))
			}
		}
	}
}

func (o *Ops) scopeOf(who auth.Who, company string, req ScopeRequest) (*scoped.Model, *scoped.Scope, string, error) {
	base, stamp, err := o.scopeBase(who, company)
	if err != nil {
		return nil, nil, "", err
	}
	if req.Base != "" && req.Base != stamp {
		return nil, nil, "", fail(409, "公司的配置在你改的这段时间变了（别人保存过或撤销过），重新打开这里再改")
	}
	base, split, err := o.splitBase(base, req.Node)
	if err != nil {
		return nil, nil, "", err
	}
	s, err := scoped.Resolve(base, req.Node)
	if err != nil {
		return nil, nil, "", fail(404, "%s", err)
	}
	s.Split = split
	return base, s, stamp, nil
}

// ScopePreview says what a change to one part would do, saving nothing.
func (o *Ops) ScopePreview(who auth.Who, company string, req ScopeRequest) (*ScopeResult, error) {
	base, s, st, err := o.scopeOf(who, company, req)
	if err != nil {
		return nil, err
	}
	r, _, err := o.proposeScoped(company, base, s, req)
	if err != nil {
		return nil, err
	}
	r.Base = st
	return r, nil
}

// ScopeApply saves a change to one part, the way a builder save goes, the earlier config kept.
func (o *Ops) ScopeApply(who auth.Who, via, company string, req ScopeRequest) (*ScopeResult, error) {
	base, s, _, err := o.scopeOf(who, company, req)
	if err != nil {
		return nil, err
	}
	r, _, err := o.proposeScoped(company, base, s, req)
	if err != nil {
		o.Audit.Record(who, via, company, "company.edit", s.Title, "", err)
		return nil, err
	}
	if len(r.Preview.Changes) == 0 {
		return nil, fail(400, "没有改动")
	}
	raw, _ := json.Marshal(r.Config)
	res := o.Engine.CreateCompany(raw)
	changes := strings.Join(r.Preview.Changes, "；")
	if r.Split != "" {
		changes = r.Split + changes
	}
	o.Audit.Record(who, via, company, "company.edit", s.Title, changes, errOf(res))
	if !res.OK {
		return nil, rejected(res)
	}
	r.Saved, r.Base = true, stamp(o.Engine.CompanyConfig(company))
	return r, nil
}

// Versions is how many earlier configs of the company are kept to undo to, and when the latest was.
func (o *Ops) Versions(who auth.Who, company string) (map[string]any, error) {
	if _, _, err := o.scopeBase(who, company); err != nil {
		return nil, err
	}
	vs := o.Engine.Versions(company)
	out := map[string]any{"versions": len(vs)}
	if len(vs) > 0 {
		out["last"] = vs[len(vs)-1].At
	}
	return out, nil
}

// Undo puts the company back on the config it ran before its last change.
func (o *Ops) Undo(who auth.Who, via, company string) (*ScopeView, error) {
	if _, _, err := o.scopeBase(who, company); err != nil {
		return nil, err
	}
	vs := o.Engine.Versions(company)
	if len(vs) == 0 {
		return nil, fail(409, "没有更早的版本可以撤销")
	}
	res := o.Engine.Undo(company)
	o.Audit.Record(who, via, company, "company.undo", company, "回到 "+vs[len(vs)-1].At.In(time.Local).Format("01-02 15:04:05")+" 之前的配置", errOf(res))
	if !res.OK {
		return nil, rejected(res)
	}
	m, base, err := o.scopeBase(who, company)
	if err != nil {
		return nil, err
	}
	v := &ScopeView{Model: m, Base: base}
	vs = o.Engine.Versions(company)
	v.Versions = len(vs)
	if len(vs) > 0 {
		v.Last = &vs[len(vs)-1].At
	}
	return v, nil
}

// ScopeAI has the AI change one part of a company from a few words: it sees the whole config,
// proposes changes, and each proposal goes through the same check (the guard included) a form
// does. The last proposal that passed arrives as a "proposal" event; nothing is saved.
func (o *Ops) ScopeAI(ctx context.Context, who auth.Who, company string, n scoped.Node, brief string, emit func(agents.Event)) error {
	if o.Agents == nil || !o.Agents.Ready() {
		if err := Admin(who); err != nil {
			return err
		}
		return fail(503, "%s", agents.ErrNoModel)
	}
	brief = strings.TrimSpace(brief)
	if brief == "" || utf8.RuneCountInString(brief) > 2000 {
		return fail(400, "要改什么，用 1～2000 字说")
	}
	base, st, err := o.scopeBase(who, company)
	if err != nil {
		return err
	}
	base, split, err := o.splitBase(base, n)
	if err != nil {
		return err
	}
	s, err := scoped.Resolve(base, n)
	if err != nil {
		return fail(404, "%s", err)
	}
	s.Split = split
	cfg, _ := json.Marshal(base.Config)
	machines, _ := json.Marshal(base.Machines)
	check := func(changes []scoped.Change) (any, error) {
		r, _, err := o.proposeScoped(company, base, s, ScopeRequest{Node: n, Changes: changes})
		if err != nil {
			if e, ok := err.(*Error); ok && e.Where == "scope" {
				emit(agents.Event{Kind: "refused", Text: e.Msg, Result: e.Problems})
			}
			return nil, err
		}
		if len(r.Preview.Changes) == 0 {
			return nil, fmt.Errorf("这些改动没有改变任何东西")
		}
		r.Base = st
		emit(agents.Event{Kind: "proposal", Result: r})
		return map[string]any{"ok": true, "changes": r.Preview.Changes, "tags": r.Preview.Tags}, nil
	}
	return o.Agents.Scoped(ctx, who, agents.ScopedTask{
		Brief: brief, Title: s.Title, Path: s.Path, Shared: s.Shared, Config: cfg, Machines: machines, Tags: tagsOf(base, s),
	}, check, emit)
}

// tagsOf lists the stations in the scope with their tags, for the AI: 「L2 曲轴 2 线：HP-201 锻压 …」.
func tagsOf(m *scoped.Model, s *scoped.Scope) string {
	var b strings.Builder
	p := m.Plants[s.Plant]
	for _, l := range p.Lines {
		fmt.Fprintf(&b, "%s（第 %d 项 lines[%d]）%s：", l.ID, l.Cfg+1, l.Cfg, l.Name)
		for i, st := range l.Stations {
			if i > 0 {
				b.WriteString("，")
			}
			fmt.Fprintf(&b, "steps[%d] %s %s", i, st.ID, st.Name)
		}
		b.WriteString("\n")
	}
	return b.String()
}
