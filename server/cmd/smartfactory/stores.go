package main

import (
	"encoding/json"
	"slices"

	"smartfactory/internal/engine"
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
