package ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"smartfactory/internal/auth"
	"smartfactory/internal/templates"
)

// Templates are factories to start from: built in, saved on this instance, or on the template
// hub, where anyone with a hub account publishes theirs for everyone.

// summarize checks a template with the engine and says what it lays out to.
func (o *Ops) summarize(t *templates.Template, source string) (templates.Summary, error) {
	raw, _ := json.Marshal(map[string]any{"company": t.Company, "industries": t.Industries})
	res := o.Engine.CheckTemplate(raw)
	if !res.OK {
		return templates.Summary{}, rejected(res)
	}
	var s templates.Summary
	json.Unmarshal(res.Detail, &s)
	created := t.Created
	s.ID, s.Name, s.Description, s.Author, s.Source, s.Created = t.ID, t.Name, t.Description, t.Author, source, &created
	return s, nil
}

// Templates lists the templates: built in, this instance's, the hub's.
func (o *Ops) Templates(ctx context.Context, who auth.Who) ([]templates.Summary, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	var out []templates.Summary
	if err := json.Unmarshal([]byte(o.Engine.BuiltinTemplates()), &out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Source = "builtin"
	}
	if o.Saved != nil {
		list, err := o.Saved.All()
		if err != nil {
			return nil, err
		}
		for _, t := range list {
			if s, err := o.summarize(t, "local"); err == nil {
				out = append(out, s)
			}
		}
	}
	return append(out, o.Market.List(ctx)...), nil
}

// Template is one template whole.
func (o *Ops) Template(ctx context.Context, who auth.Who, source, id string) (*templates.Template, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	var t *templates.Template
	var err error
	switch source {
	case "builtin":
		js := o.Engine.BuiltinTemplate(id)
		if js != "null" {
			var c struct{ Name string }
			json.Unmarshal([]byte(js), &c)
			t = &templates.Template{ID: id, Name: c.Name, Company: json.RawMessage(js)}
		}
	case "local":
		if o.Saved != nil {
			t, err = o.Saved.Get(id)
		}
	case "hub":
		t, err = o.Market.Get(ctx, id)
	default:
		return nil, fail(400, "模板来源是 builtin、local 或 hub")
	}
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fail(404, "没有模板 %s", id)
	}
	t.AuthorKey = ""
	return t, nil
}

// SaveRequest makes a template of a running company (From) or of a config (Company).
type SaveRequest struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	From        string                     `json:"from,omitempty"`
	Company     map[string]any             `json:"company,omitempty"`
	Industries  map[string]json.RawMessage `json:"industries,omitempty"`
}

// build turns a request into a template: the company's structure under the template's own id
// and name (the customer's name stays behind), with the packs it needs that are not built in.
func (o *Ops) build(who auth.Who, in SaveRequest) (*templates.Template, error) {
	in.ID, in.Name = strings.TrimSpace(in.ID), strings.TrimSpace(in.Name)
	if !templates.ValidID.MatchString(in.ID) {
		return nil, fail(400, "模板标识：小写字母、数字、连字符，2～48 个字符")
	}
	if in.Name == "" {
		return nil, fail(400, "给模板起个名字")
	}
	cfg := in.Company
	if in.From != "" {
		raw, err := o.CompanyConfig(who, in.From)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
	}
	if cfg == nil {
		return nil, fail(400, "要么给出公司（from），要么给出配置（company）")
	}
	cfg["id"], cfg["name"] = in.ID, in.Name
	short := []rune(in.Name)
	cfg["short"] = string(short[:min(len(short), 2)])
	delete(cfg, "$schema")
	packs := map[string]json.RawMessage{}
	for id, raw := range in.Industries {
		packs[id] = raw
	}
	builtin := map[string]bool{}
	var list []struct {
		ID      string
		Builtin bool
	}
	json.Unmarshal([]byte(o.Engine.IndustryList()), &list)
	for _, i := range list {
		builtin[i.ID] = i.Builtin
	}
	for _, id := range industriesOf(cfg) {
		if _, given := packs[id]; given || builtin[id] {
			continue
		}
		if js := o.Engine.Industry(id); js != "null" {
			packs[id] = json.RawMessage(js)
		}
	}
	company, _ := json.Marshal(cfg)
	t := &templates.Template{ID: in.ID, Name: in.Name, Description: strings.TrimSpace(in.Description), Author: who.Name, Created: time.Now(), Company: company}
	if len(packs) > 0 {
		t.Industries = packs
	}
	return t, nil
}

func industriesOf(cfg map[string]any) []string {
	var out []string
	if s, ok := cfg["industry"].(string); ok && s != "" {
		out = append(out, s)
	}
	if ps, ok := cfg["plants"].([]any); ok {
		for _, p := range ps {
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["industry"].(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// SaveTemplate keeps a template on this instance (same id replaces).
func (o *Ops) SaveTemplate(who auth.Who, via string, in SaveRequest) (*templates.Summary, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	if o.Saved == nil {
		return nil, fail(503, "这台服务器没有数据目录，存不了模板")
	}
	t, err := o.build(who, in)
	if err != nil {
		return nil, err
	}
	s, err := o.summarize(t, "local")
	if err == nil {
		err = o.Saved.Put(t)
	}
	o.Audit.Record(who, via, "*", "template.save", t.ID+" "+t.Name, "", err)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (o *Ops) DeleteTemplate(who auth.Who, via, id string) error {
	if err := Admin(who); err != nil {
		return err
	}
	if o.Saved == nil {
		return fail(404, "没有模板 %s", id)
	}
	err := o.Saved.Delete(id)
	o.Audit.Record(who, via, "*", "template.delete", id, "", err)
	if err != nil {
		return fail(404, "没有模板 %s", id)
	}
	return nil
}

// PublishTemplate puts a template saved here on the template hub.
func (o *Ops) PublishTemplate(ctx context.Context, who auth.Who, via, id string) error {
	if err := Admin(who); err != nil {
		return err
	}
	t, err := o.Template(ctx, who, "local", id)
	if err != nil {
		return err
	}
	err = o.Market.Publish(ctx, t)
	o.Audit.Record(who, via, "*", "template.publish", t.ID+" "+t.Name, "", err)
	if err != nil {
		return &Error{Code: 502, Msg: err.Error()}
	}
	return nil
}

// ---- the hub (on the platform server) ----

// HubTemplates is what the hub offers, for anyone.
func (o *Ops) HubTemplates() ([]templates.Summary, error) {
	out := []templates.Summary{}
	if o.HubStore == nil {
		return out, nil
	}
	list, err := o.HubStore.All()
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		if s, err := o.summarize(t, "hub"); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func (o *Ops) HubTemplate(id string) (*templates.Template, error) {
	if o.HubStore == nil {
		return nil, fail(404, "这里不是模板市场")
	}
	t, err := o.HubStore.Get(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fail(404, "没有模板 %s", id)
	}
	t.AuthorKey = ""
	return t, nil
}

// HubPublish takes a template from anyone with a hub account; only its author replaces it.
func (o *Ops) HubPublish(who auth.Who, t *templates.Template) error {
	if o.HubStore == nil {
		return fail(404, "这里不是模板市场")
	}
	if who.Key == "" {
		return fail(401, "用模板市场的令牌发布")
	}
	if old, _ := o.HubStore.Get(t.ID); old != nil && old.AuthorKey != who.Key {
		return fail(409, "模板 %s 是%s发布的，换一个标识", t.ID, old.Author)
	}
	t.Author, t.AuthorKey, t.Created = who.Name, who.Key, time.Now()
	if _, err := o.summarize(t, "hub"); err != nil {
		return err
	}
	err := o.HubStore.Put(t)
	o.Audit.Record(who, "page", "*", "template.publish", t.ID+" "+t.Name, "", err)
	if err != nil {
		return errors.New(err.Error())
	}
	return nil
}
