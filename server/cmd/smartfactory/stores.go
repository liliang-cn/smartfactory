package main

import (
	"encoding/json"
	"slices"
	"strings"

	"smartfactory/internal/engine"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/stores"
)

// storeCategories are the store categories a company's industry packs declare.
func storeCategories(eng *engine.Engine, company string) []stores.Category {
	var cfg struct {
		Industry string `json:"industry"`
		Plants   []struct {
			Industry string `json:"industry"`
		} `json:"plants"`
	}
	if json.Unmarshal([]byte(eng.CompanyConfig(company)), &cfg) != nil {
		return nil
	}
	ids := []string{cfg.Industry}
	for _, p := range cfg.Plants {
		if p.Industry != "" && !slices.Contains(ids, p.Industry) {
			ids = append(ids, p.Industry)
		}
	}
	var out []stores.Category
	for _, id := range ids {
		var ind struct {
			Stores *struct {
				Categories []stores.Category `json:"categories"`
			} `json:"stores"`
		}
		if id == "" || json.Unmarshal([]byte(eng.Industry(id)), &ind) != nil || ind.Stores == nil {
			continue
		}
		for _, c := range ind.Stores.Categories {
			if !slices.ContainsFunc(out, func(o stores.Category) bool { return o.Key == c.Key }) {
				out = append(out, c)
			}
		}
	}
	return out
}

// bomInfos are a company's BOMs in effect as its knowledge base keeps them: every line with how
// much goes into one of the part, and the sub-assembly it goes in through.
func bomInfos(store *stores.Service, company string) ([]knowledge.BOMInfo, error) {
	xs, err := store.InEffect(company)
	if err != nil {
		return nil, err
	}
	family := map[string]string{}
	if store.Products != nil {
		for _, p := range store.Products(company) {
			family[strings.ToUpper(p.Part)] = p.Family
		}
	}
	out := make([]knowledge.BOMInfo, 0, len(xs))
	for _, x := range xs {
		info := knowledge.BOMInfo{Part: x.Part, Name: x.Name, Rev: x.Rev, Drawing: x.Drawing, Effective: x.Effective, Family: family[strings.ToUpper(x.Part)]}
		var via []string
		for _, r := range x.Rows {
			via = append(via[:min(len(via), r.Level-1)], r.Name)
			l := knowledge.BOMLineInfo{Level: r.Level, Code: r.Code, Label: r.Label(), Kind: stores.KindLabel(r.Kind), Per: r.Per, Unit: r.Unit, Op: r.Op}
			if r.Level > 1 {
				l.Via = strings.Join(via[:r.Level-1], " / ")
			}
			info.Lines = append(info.Lines, l)
		}
		out = append(out, info)
	}
	return out, nil
}

// storeProducts are the part numbers a company's lines make: each line's product from its
// plant's industry pack, by its variants (a part number each) or by its own part number.
func storeProducts(eng *engine.Engine, company string) []stores.ProductRef {
	var cfg struct {
		Industry string `json:"industry"`
		Plants   []struct {
			Industry string `json:"industry"`
			Lines    []struct {
				Product string `json:"product"`
			} `json:"lines"`
		} `json:"plants"`
	}
	if json.Unmarshal([]byte(eng.CompanyConfig(company)), &cfg) != nil {
		return nil
	}
	type product struct {
		Name     string  `json:"name"`
		PartNo   string  `json:"partNo"`
		Material string  `json:"material"`
		Weight   float64 `json:"weight"`
		Input    float64 `json:"input"`
		Variants []struct {
			PartNo string  `json:"partNo"`
			Name   string  `json:"name"`
			Weight float64 `json:"weight"`
			Input  float64 `json:"input"`
		} `json:"variants"`
	}
	packs := map[string]map[string]product{}
	pack := func(id string) map[string]product {
		if p, ok := packs[id]; ok {
			return p
		}
		var ind struct {
			Products map[string]product `json:"products"`
		}
		json.Unmarshal([]byte(eng.Industry(id)), &ind)
		packs[id] = ind.Products
		return ind.Products
	}
	var out []stores.ProductRef
	add := func(r stores.ProductRef) {
		if r.Part != "" && !slices.ContainsFunc(out, func(o stores.ProductRef) bool { return o.Part == r.Part }) {
			out = append(out, r)
		}
	}
	for _, p := range cfg.Plants {
		ind := p.Industry
		if ind == "" {
			ind = cfg.Industry
		}
		for _, l := range p.Lines {
			pr, ok := pack(ind)[l.Product]
			if !ok {
				continue
			}
			if len(pr.Variants) == 0 {
				add(stores.ProductRef{Part: pr.PartNo, Name: pr.Name, Product: l.Product, Family: pr.Name, Material: pr.Material, Weight: pr.Weight, Input: pr.Input})
				continue
			}
			for _, v := range pr.Variants {
				r := stores.ProductRef{Part: v.PartNo, Name: v.Name, Product: l.Product, Family: pr.Name, Material: pr.Material, Weight: v.Weight, Input: v.Input}
				if r.Weight == 0 {
					r.Weight, r.Input = pr.Weight, pr.Input
				}
				add(r)
			}
		}
	}
	return out
}
