package ops

import (
	"fmt"
	"math"
	"strings"

	"smartfactory/internal/auth"
	"smartfactory/internal/erp"
	"smartfactory/internal/extract"
	"smartfactory/internal/stores"
)

// A company's BOMs: members read them, explode them and see how the open orders' materials
// stand; operators (the engineers) draft, import, release and retire revisions.

// BOMOverview is the company's part numbers with how their BOMs stand, and the latest changes.
type BOMOverview struct {
	Parts    []stores.PartRow `json:"parts"`
	ECOs     []stores.ECO     `json:"ecos"`
	Kinds    [][2]string      `json:"kinds"`
	Operator bool             `json:"operator"`
}

func (o *Ops) BOMOverview(who auth.Who, company string) (*BOMOverview, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	out := &BOMOverview{Kinds: stores.LineKinds, Operator: who.Can(company, auth.Operator)}
	var err error
	if out.Parts, err = o.Stores.Parts(company); err != nil {
		return nil, err
	}
	out.ECOs, err = o.Stores.ECOs(company, "", 20)
	return out, storesError(err)
}

// BOMPart is a part's revisions, its change records, and where it goes in as a sub-assembly.
type BOMPart struct {
	Part      string         `json:"part"`
	Revisions []stores.BOM   `json:"revisions"`
	ECOs      []stores.ECO   `json:"ecos"`
	UsedIn    []stores.Usage `json:"usedIn"`
}

func (o *Ops) BOMPart(who auth.Who, company, part string) (*BOMPart, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	out := &BOMPart{Part: part}
	var err error
	if out.Revisions, err = o.Stores.BOMs(company, part); err != nil {
		return nil, err
	}
	if out.ECOs, err = o.Stores.ECOs(company, part, 100); err != nil {
		return nil, err
	}
	uses, err := o.Stores.WhereUsed(company, part)
	out.UsedIn = []stores.Usage{}
	for _, u := range uses {
		if strings.EqualFold(u.Code, part) {
			out.UsedIn = append(out.UsedIn, u)
		}
	}
	return out, storesError(err)
}

func (o *Ops) SaveBOM(who auth.Who, via, company string, b stores.BOM) (*stores.BOM, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.SaveBOM(company, who.Name, b)
	rev := b.Rev
	if v != nil {
		rev = v.Rev
	}
	o.Audit.Record(who, via, company, "bom.save", b.Part, fmt.Sprintf("%s 版，%d 行", rev, len(b.Lines)), err)
	return v, storesError(err)
}

func (o *Ops) DeleteBOM(who auth.Who, via, company, id string) (*stores.BOM, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.DeleteBOM(company, id)
	target, detail := id, ""
	if v != nil {
		target, detail = v.Part, v.Rev+" 版草稿"
	}
	o.Audit.Record(who, via, company, "bom.delete", target, detail, err)
	return v, storesError(err)
}

func (o *Ops) CopyBOM(who auth.Who, via, company, id string) (*stores.BOM, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.CopyBOM(company, who.Name, id)
	target, detail := id, ""
	if v != nil {
		target, detail = v.Part, "新版本 "+v.Rev
	}
	o.Audit.Record(who, via, company, "bom.copy", target, detail, err)
	return v, storesError(err)
}

// BOMReleased is a revision put in effect and its change record.
type BOMReleased struct {
	BOM *stores.BOM `json:"bom"`
	ECO *stores.ECO `json:"eco"`
}

func (o *Ops) ReleaseBOM(who auth.Who, via, company, id string, r stores.Release) (*BOMReleased, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	b, e, err := o.Stores.ReleaseBOM(company, who.Name, id, r)
	target, detail := id, r.Why
	if b != nil {
		target, detail = b.Part, fmt.Sprintf("%s 版生效：%s", b.Rev, r.Why)
	}
	o.Audit.Record(who, via, company, "bom.release", target, detail, err)
	if err != nil {
		return nil, storesError(err)
	}
	return &BOMReleased{BOM: b, ECO: e}, nil
}

func (o *Ops) RetireBOM(who auth.Who, via, company, id string) (*stores.BOM, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.RetireBOM(company, who.Name, id)
	target, detail := id, ""
	if v != nil {
		target, detail = v.Part, v.Rev+" 版停用"
	}
	o.Audit.Record(who, via, company, "bom.retire", target, detail, err)
	return v, storesError(err)
}

// BOMImport is a spreadsheet sent to import: the part it is a BOM of (or the sheet says), and the file.
type BOMImport struct {
	Part    string `json:"part"`
	Name    string `json:"name"`
	Rev     string `json:"rev,omitempty"`
	Drawing string `json:"drawing,omitempty"`
	File    string `json:"file"`
	// Data is the file's bytes (base64 in JSON).
	Data []byte `json:"data"`
}

// BOMImported is the draft an import made and what it could not use.
type BOMImported struct {
	BOM      *stores.BOM `json:"bom"`
	Lines    int         `json:"lines"`
	Columns  []string    `json:"columns"`
	Problems []string    `json:"problems"`
}

func (o *Ops) ImportBOM(who auth.Who, via, company string, in BOMImport) (*BOMImported, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	rows, err := extract.Table(in.File, in.Data)
	if err != nil {
		o.Audit.Record(who, via, company, "bom.import", in.Part, in.File, err)
		return nil, &Error{Code: 422, Msg: err.Error(), Problems: []string{err.Error()}}
	}
	b, im, err := o.Stores.ImportBOM(company, who.Name, stores.BOM{Part: in.Part, Name: in.Name, Rev: in.Rev, Drawing: in.Drawing}, rows)
	detail := in.File
	if b != nil {
		detail = fmt.Sprintf("%s → %s 版草稿，%d 行", in.File, b.Rev, len(b.Lines))
	}
	o.Audit.Record(who, via, company, "bom.import", in.Part, detail, err)
	if err != nil {
		return nil, storesError(err)
	}
	out := &BOMImported{BOM: b, Lines: len(b.Lines), Columns: im.Columns, Problems: im.Problems}
	if out.Problems == nil {
		out.Problems = []string{}
	}
	return out, nil
}

func (o *Ops) ExplodeBOM(who auth.Who, company, part, rev string, qty float64) (*stores.Explosion, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	v, err := o.Stores.Explode(company, part, rev, qty)
	if err != nil {
		if err == stores.ErrNotFound {
			return nil, fail(404, "%s 没有生效的 BOM", part)
		}
		return nil, err
	}
	return v, nil
}

func (o *Ops) WhereUsed(who auth.Who, company, q string) ([]stores.Usage, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	v, err := o.Stores.WhereUsed(company, q)
	return v, storesError(err)
}

// OpenDemand is what the company's open orders still have to make, in the order they run.
func OpenDemand(prod *erp.Service, company string) ([]stores.Demand, error) {
	if prod == nil {
		return nil, nil
	}
	list, err := prod.Store.List(company, erp.Filter{Status: []string{"running", "queued"}, Limit: 1000})
	if err != nil {
		return nil, err
	}
	out := make([]stores.Demand, 0, len(list))
	for _, x := range list {
		out = append(out, stores.Demand{No: x.No, Part: x.PartNo, Product: x.Product, Qty: math.Max(0, float64(x.Qty-x.Good))})
	}
	return out, nil
}

// Kitting is how the open orders' materials stand: each order, in the order the lines run them,
// takes what the stores hold before the next one does.
func (o *Ops) Kitting(who auth.Who, company string) (*stores.Kitting, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	ds, err := OpenDemand(o.Production, company)
	if err != nil {
		return nil, err
	}
	v, err := o.Stores.Kit(company, ds)
	return v, storesError(err)
}

func (o *Ops) DraftProjectBOM(who auth.Who, via, company, id string) (*stores.BOM, error) {
	if err := o.stores(who, company, auth.Operator); err != nil {
		return nil, err
	}
	v, err := o.Stores.DraftBOM(company, who.Name, id)
	target, detail := id, ""
	if v != nil {
		target, detail = v.Part, v.Rev+" 版草稿（研发）"
	}
	o.Audit.Record(who, via, company, "bom.save", target, detail, err)
	return v, storesError(err)
}
