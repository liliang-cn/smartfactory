package ops

import (
	"errors"
	"slices"

	"smartfactory/internal/auth"
	"smartfactory/internal/stores"
)

// A company's stores, dies and new parts: members read them, operators (storekeepers, the die shop,
// the development engineers) record what happens.

func (o *Ops) stores(who auth.Who, company string, need auth.Role) error {
	if o.Stores == nil {
		return fail(503, "这台服务器不保存库房记录（没有数据目录）")
	}
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return fail(404, "没有公司 %s", company)
	}
	if !who.Can(company, need) {
		if who.Key == "" {
			return fail(401, "用令牌登录")
		}
		return fail(403, "要公司 %s %s以上的令牌", company, need.Label())
	}
	return nil
}

func storesError(err error) error {
	var p *stores.Problems
	switch {
	case errors.As(err, &p):
		return &Error{Code: 422, Msg: p.Error(), Problems: p.List}
	case errors.Is(err, stores.ErrNotFound):
		return &Error{Code: 404, Msg: "没有这一项"}
	}
	return err
}

// StoresOverview is a company's categories and items, its heats, and whether the caller may record.
type StoresOverview struct {
	Categories []stores.Category  `json:"categories"`
	Items      []stores.Item      `json:"items"`
	Heats      []stores.HeatStock `json:"heats"`
	Moves      []stores.Move      `json:"moves"`
	Operator   bool               `json:"operator"`
}

func (o *Ops) StoresOverview(who auth.Who, company string) (*StoresOverview, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	out := &StoresOverview{Operator: who.Can(company, auth.Operator)}
	var err error
	if out.Categories, err = o.Stores.Cats(company); err != nil {
		return nil, err
	}
	if out.Items, err = o.Stores.Items(company, ""); err != nil {
		return nil, err
	}
	if out.Heats, err = o.Stores.Heats(company, ""); err != nil {
		return nil, err
	}
	if out.Moves, err = o.Stores.Moves(company, "", "", 30); err != nil {
		return nil, err
	}
	return out, nil
}

func (o *Ops) SaveStoreCategory(who auth.Who, via, company string, c stores.Category) (*stores.Category, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.SaveCat(company, c)
	o.Audit.Record(who, via, company, "stores.category", c.Key, c.Label, err)
	return v, storesError(err)
}

func (o *Ops) SaveStoreItem(who auth.Who, via, company string, it stores.Item) (*stores.Item, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.SaveItem(company, it)
	o.Audit.Record(who, via, company, "stores.item", it.Code, it.Name, err)
	return v, storesError(err)
}

func (o *Ops) RecordMove(who auth.Who, via, company string, in stores.MoveInput) (*stores.Move, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	m, it, err := o.Stores.Record(company, who.Name, in)
	name := in.Item
	if it != nil {
		name = it.Label()
	}
	o.Audit.Record(who, via, company, "stores."+in.Kind, in.Item, name, err)
	return m, storesError(err)
}

func (o *Ops) StoreMoves(who auth.Who, company, item, heat string, limit int) ([]stores.Move, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	v, err := o.Stores.Moves(company, item, heat, limit)
	return v, storesError(err)
}

func (o *Ops) TraceHeat(who auth.Who, company, q string) (*stores.Trace, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	v, err := o.Stores.TraceOf(company, q)
	return v, storesError(err)
}

// DiesOverview is a company's dies and the machines that take them.
type DiesOverview struct {
	Dies     []stores.Die      `json:"dies"`
	Presses  []stores.PressNow `json:"presses"`
	Places   map[string]string `json:"places"`
	Operator bool              `json:"operator"`
}

func (o *Ops) DiesOverview(who auth.Who, company string) (*DiesOverview, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	out := &DiesOverview{Places: stores.PlaceLabel, Operator: who.Can(company, auth.Operator)}
	var err error
	if out.Dies, err = o.Stores.Dies(company); err != nil {
		return nil, err
	}
	if out.Presses, err = o.Stores.Presses(company); err != nil {
		return nil, err
	}
	return out, nil
}

// DieDetail is a die and what happened to it.
type DieDetail struct {
	Die     *stores.Die       `json:"die"`
	History []stores.DieEvent `json:"history"`
}

func (o *Ops) DieDetail(who auth.Who, company, no string) (*DieDetail, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	d, err := o.Stores.Die(company, no)
	if err != nil {
		return nil, storesError(err)
	}
	h, err := o.Stores.DieHistory(company, no, 100)
	return &DieDetail{Die: d, History: h}, storesError(err)
}

// DieOn is the die booked on a machine (nil Die when none is).
func (o *Ops) DieOn(who auth.Who, company, plant, asset string) (*DieDetail, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	d, err := o.Stores.DieOn(company, plant, asset)
	if err != nil || d == nil {
		return &DieDetail{History: []stores.DieEvent{}}, storesError(err)
	}
	return o.DieDetail(who, company, d.No)
}

func (o *Ops) SaveDie(who auth.Who, via, company string, d stores.Die, isNew bool) (*stores.Die, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.SaveDie(company, who.Name, d, isNew)
	o.Audit.Record(who, via, company, "die.save", d.No, d.Kind, err)
	return v, storesError(err)
}

func (o *Ops) DieAct(who auth.Who, via, company, no string, a stores.DieAction) (*stores.Die, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.Act(company, who.Name, no, a)
	o.Audit.Record(who, via, company, "die."+a.Do, no, a.Asset, err)
	return v, storesError(err)
}

// RnDOverview is a company's development projects.
type RnDOverview struct {
	Projects []stores.Project `json:"projects"`
	Stages   []string         `json:"stages"`
	Results  []string         `json:"results"`
	Operator bool             `json:"operator"`
}

func (o *Ops) RnDOverview(who auth.Who, company string) (*RnDOverview, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	ps, err := o.Stores.Projects(company)
	return &RnDOverview{Projects: ps, Stages: stores.Stages, Results: stores.TrialResults, Operator: who.Can(company, auth.Operator)}, storesError(err)
}

func (o *Ops) SaveProject(who auth.Who, via, company string, p stores.Project) (*stores.Project, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.SaveProject(company, who.Name, p)
	o.Audit.Record(who, via, company, "rnd.project", p.Part, p.Name, err)
	return v, storesError(err)
}

func (o *Ops) AddTrial(who auth.Who, via, company string, t stores.Trial) (*stores.Project, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.AddTrial(company, who.Name, t)
	o.Audit.Record(who, via, company, "rnd.trial", t.Project, t.Result, err)
	return v, storesError(err)
}

func (o *Ops) ReleaseProject(who auth.Who, via, company, id string, so stores.SignOff) (*stores.Project, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.Release(company, who.Name, id, so)
	detail := ""
	if so.BOM != "" {
		detail = "BOM 随之生效"
		if so.Drawing != "" {
			detail += "，图纸 " + so.Drawing + " 版"
		}
	}
	o.Audit.Record(who, via, company, "rnd.release", id, detail, err)
	return v, storesError(err)
}
