package ops

import (
	"strings"

	"smartfactory/internal/auth"
	"smartfactory/internal/stores"
)

// What the assistants over MCP ask of the BOMs: read-only, for members of the company.

// BOMRevision is one revision of a part as MCP lists them.
type BOMRevision struct {
	Rev        string `json:"rev"`
	Status     string `json:"status"`
	Drawing    string `json:"drawing,omitempty"`
	Effective  string `json:"effective,omitempty"`
	Lines      int    `json:"lines"`
	By         string `json:"by,omitempty"`
	ReleasedBy string `json:"releasedBy,omitempty"`
}

// BOMLookup is a part's BOM found by number or name: its revisions, and the one asked for (or in
// effect) exploded for qty. With no part, the company's part numbers and how their BOMs stand.
type BOMLookup struct {
	Part      string            `json:"part,omitempty"`
	Name      string            `json:"name,omitempty"`
	Revisions []BOMRevision     `json:"revisions,omitempty"`
	Explosion *stores.Explosion `json:"explosion,omitempty"`
	// Parts are all part numbers (no part asked for), or the several a name matched.
	Parts []stores.PartRow `json:"parts,omitempty"`
}

// QueryBOM finds a part by its number (or part of its name) and explodes the revision rev (empty:
// the one in effect) for qty.
func (o *Ops) QueryBOM(who auth.Who, company, part, rev string, qty float64) (*BOMLookup, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	parts, err := o.Stores.Parts(company)
	if err != nil {
		return nil, err
	}
	part = strings.TrimSpace(part)
	if part == "" {
		return &BOMLookup{Parts: parts}, nil
	}
	key := strings.ToLower(strings.Join(strings.Fields(part), ""))
	var hits []stores.PartRow
	for _, p := range parts {
		if strings.EqualFold(p.Part, part) {
			hits = []stores.PartRow{p}
			break
		}
		if strings.Contains(strings.ToLower(strings.ReplaceAll(p.Part+p.Name+p.Family, " ", "")), key) {
			hits = append(hits, p)
		}
	}
	switch {
	case len(hits) == 0:
		return nil, fail(404, "没有和“%s”对得上的零件号或名称", part)
	case len(hits) > 1:
		return &BOMLookup{Parts: hits}, nil
	}
	out := &BOMLookup{Part: hits[0].Part, Name: hits[0].Name, Revisions: []BOMRevision{}}
	revs, err := o.Stores.BOMs(company, out.Part)
	if err != nil {
		return nil, err
	}
	for _, b := range revs {
		out.Revisions = append(out.Revisions, BOMRevision{Rev: b.Rev, Status: b.Status, Drawing: b.Drawing, Effective: b.Effective, Lines: len(b.Lines), By: b.By, ReleasedBy: b.ReleasedBy})
	}
	if qty <= 0 {
		qty = 1
	}
	out.Explosion, err = o.Stores.Explode(company, out.Part, rev, qty)
	if err == stores.ErrNotFound {
		if rev != "" {
			return nil, fail(404, "%s 没有 %s 版", out.Part, rev)
		}
		return out, nil // revisions only: none is in effect
	}
	return out, err
}

// ChangeRecords are a company's engineering changes (ECO), newest first, of one part or all.
func (o *Ops) ChangeRecords(who auth.Who, company, part string, limit int) ([]stores.ECO, error) {
	if err := o.stores(who, company, auth.Member); err != nil {
		return nil, err
	}
	v, err := o.Stores.ECOs(company, strings.TrimSpace(part), limit)
	return v, storesError(err)
}

// OrderKit is how the open orders' materials stand: all of them, or the one order asked for with
// what it gets of each item.
func (o *Ops) OrderKit(who auth.Who, company, order string) (any, error) {
	k, err := o.Kitting(who, company)
	if err != nil {
		return nil, err
	}
	order = strings.TrimSpace(order)
	if order == "" {
		return k, nil
	}
	for _, x := range k.Orders {
		if strings.EqualFold(x.No, order) {
			return map[string]any{"order": x}, nil
		}
	}
	return nil, fail(404, "没有在做或排队的工单 %s（只算在做和排队的）", order)
}
