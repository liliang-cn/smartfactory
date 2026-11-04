package stores

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The other demo companies' BOMs: written against the part numbers their industry packs give
// the products (and the stores each pack declares), so every finished part has a BOM in effect
// whose leaves the stores all keep, a sub-assembly opens into its own, and there is a second
// revision with a change record, a draft, a low item and something too little for a big order.
func TestSeedBOMPacks(t *testing.T) {
	industry := map[string]string{"jinggong": "machining", "nanfang": "aircon", "ruineng": "battery", "yuanlan": "automotive", "hetian": "food", "qianyu": "chuanchuan"}
	for company, file := range industry {
		t.Run(company, func(t *testing.T) {
			raw, err := os.ReadFile("../../../web/src/config/industries/" + file + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var pack struct {
				Products map[string]struct {
					Name   string `json:"name"`
					PartNo string `json:"partNo"`
				} `json:"products"`
				Stores struct {
					Categories []Category `json:"categories"`
				} `json:"stores"`
			}
			if err := json.Unmarshal(raw, &pack); err != nil {
				t.Fatal(err)
			}
			p := packs[company]
			// the finished parts: the part numbers the products declare, each with a BOM in the seed
			var refs []ProductRef
			var tops []string
			for k, pr := range pack.Products {
				if pr.PartNo == "" {
					continue
				}
				refs = append(refs, ProductRef{Part: pr.PartNo, Name: pr.Name, Product: k})
				tops = append(tops, pr.PartNo)
				if !slices.ContainsFunc(p.boms, func(b pBOM) bool { return b.part == pr.PartNo }) {
					t.Errorf("the seed has no BOM for %s (%s)", pr.PartNo, pr.Name)
				}
			}
			// the pack's own categories cover every item the seed keeps
			for _, it := range p.items {
				if !slices.ContainsFunc(pack.Stores.Categories, func(c Category) bool { return c.Key == it.cat }) {
					t.Errorf("item %s is in category %q the pack does not declare", it.code, it.cat)
				}
			}
			s, _ := open(t)
			s.Products = func(string) []ProductRef { return refs }
			s.Categories = func(string) []Category { return pack.Stores.Categories }
			for range 2 { // once
				if err := s.SeedBOM(company); err != nil {
					t.Fatal(err)
				}
			}
			parts, _ := s.Parts(company)
			active := map[string]PartRow{}
			drafts := 0
			for _, r := range parts {
				active[r.Part] = r
				drafts += r.Drafts
			}
			for _, b := range p.boms {
				if active[b.part].Active == "" {
					t.Errorf("%s has no BOM in effect", b.part)
				}
			}
			for _, top := range tops {
				if !active[top].Listed {
					t.Errorf("%s is not listed as a part its lines make", top)
				}
				x, err := s.Explode(company, top, "", 1000)
				if err != nil {
					t.Fatal(err)
				}
				if x.Unkept != 0 || len(x.Needs) < 4 {
					t.Errorf("%s: %d needs, %d the stores keep none of: %+v", top, len(x.Needs), x.Unkept, x.Needs)
				}
				for _, n := range x.Needs {
					if n.Problem != "" {
						t.Errorf("%s: %s %s", top, n.Code, n.Problem)
					}
				}
			}
			ecos, _ := s.ECOs(company, "", 0)
			if len(ecos) <= len(p.boms) || drafts == 0 {
				t.Errorf("%d change records for %d parts, %d drafts", len(ecos), len(p.boms), drafts)
			}
			var low, big bool
			items, _ := s.Items(company, "")
			for _, it := range items {
				low = low || it.Low
			}
			for _, top := range tops {
				if x, _ := s.Explode(company, top, "", 100000); x.Short > 0 {
					big = true
				}
			}
			if !low || !big {
				t.Errorf("an item below its safety stock %v, a big order that goes short %v", low, big)
			}
			var ds []Demand
			for _, top := range tops {
				ds = append(ds, Demand{No: "T-" + top, Part: top, Qty: 3000})
			}
			k, err := s.Kit(company, ds)
			if err != nil || len(k.Orders) != len(tops) || len(k.Totals) == 0 {
				t.Fatalf("kitting: %+v %v", k, err)
			}
		})
	}
}
