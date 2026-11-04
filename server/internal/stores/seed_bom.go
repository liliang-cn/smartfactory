package stores

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// SeedBOM gives the demo company (the other demo companies, seed_bom_packs.go) the BOMs of the part numbers its lines make, once its stores
// are seeded: each finished part over its forging, the forging over the bar it is cut from, by the
// cut weight its config gives, and the shot, die lubricant and rust oil it takes; a crankshaft
// also takes its timing gear (forged here from 40Cr bar: a sub-assembly with a BOM of its own),
// its key, plugs and dowels, a connecting rod its bolts and bush. The standard parts and
// consumables go into the stores with their stock, some of it too little for a big order. The
// 2.0T crankshaft has a revision before the one in effect, so it has a change record; the new
// part in trial forging has a draft.
func (s *Service) SeedBOM(company string) error {
	if company != Demo {
		return s.seedPack(company)
	}
	if s.Products == nil {
		return nil
	}
	mark := company + "/bom"
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM seeded WHERE company = ?`, mark).Scan(&n)
	if n > 0 {
		return nil
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM seeded WHERE company = ?`, company).Scan(&n)
	if n == 0 {
		return nil // the stores first: the bar the BOMs are cut from
	}
	refs := s.Products(company)
	if len(refs) == 0 {
		return nil
	}
	// once: a seed that fails half way is not tried again on top of what it wrote
	if _, err := s.db.Exec(`INSERT INTO seeded (company) VALUES (?)`, mark); err != nil {
		return err
	}
	cats, err := s.Cats(company)
	if err != nil {
		return err
	}
	key := map[string]string{}
	for _, c := range cats {
		if key[c.Kind] == "" {
			key[c.Kind] = c.Key
		}
	}
	now := s.Now
	defer func() { s.Now = now }()
	back := func(days float64) func() time.Time {
		at := time.Now().Add(-time.Duration(days * 24 * float64(time.Hour)))
		return func() time.Time { return at }
	}
	s.Now = back(60)
	items, err := s.Items(company, "")
	if err != nil {
		return err
	}
	// what the BOMs take that the stores do not keep yet: the gear bar, standard parts, consumables
	aux := ""
	if c, err := s.SaveCat(company, Category{Key: "aux", Label: "辅料", Kind: Other, Unit: "kg", Fields: []string{"规格", "用在"}}); err == nil {
		aux = c.Key
	}
	type keep struct {
		cat, code, name   string
		attrs             map[string]string
		unit              string
		safety, stock     float64
		heat, mill, place string
	}
	keeps := []keep{
		{key[Bar], "GB-40Cr-90", "40Cr 圆钢 φ90", map[string]string{"牌号": "40Cr", "规格": "φ90"}, "t", 1, 0.8, "26F02117", "华东特钢", "原料库 A9 区"},
		{key[Part], "BZ-KEY-5x7.5", "半圆键", map[string]string{"规格": "GB/T 1099.1 5×7.5", "用在": "曲轴正时齿轮"}, "件", 500, 2400, "", "", "标准件库 D1"},
		{key[Part], "BZ-PLUG-M10", "油道螺塞", map[string]string{"规格": "M10×1 GB/T 3", "用在": "曲轴油道"}, "件", 1000, 2600, "", "", "标准件库 D2"},
		{key[Part], "BZ-PIN-8x16", "圆柱定位销", map[string]string{"规格": "GB/T 119.2 8×16", "用在": "曲轴飞轮端"}, "件", 800, 5200, "", "", "标准件库 D3"},
		{key[Part], "BZ-BOLT-M9", "连杆螺栓", map[string]string{"规格": "M9×1×52 10.9 级", "用在": "连杆大头"}, "件", 2000, 9000, "", "", "标准件库 D4"},
		{key[Part], "WG-BUSH-22", "连杆小头衬套", map[string]string{"规格": "CuSn8 φ22×26", "用在": "连杆小头"}, "件", 1000, 1500, "", "", "标准件库 D5"},
		{aux, "FL-S330", "抛丸钢丸", map[string]string{"规格": "S330", "用在": "抛丸机"}, "kg", 500, 2400, "", "", "辅料库 E1"},
		{aux, "FL-GRAPH", "水基石墨脱模剂", map[string]string{"规格": "固含量 18%", "用在": "热模锻喷涂"}, "kg", 200, 650, "", "", "辅料库 E2"},
		{aux, "FL-OIL", "防锈油", map[string]string{"规格": "薄层型", "用在": "成品包装"}, "kg", 100, 320, "", "", "辅料库 E3"},
	}
	for _, k := range keeps {
		if k.cat == "" || itemFor(items, Need{Code: k.code}) != nil {
			continue
		}
		it, err := s.SaveItem(company, Item{Cat: k.cat, Code: k.code, Name: k.name, Attrs: k.attrs, Unit: k.unit, Safety: k.safety, Place: k.place})
		if err != nil {
			return err
		}
		in := MoveInput{Item: it.ID, Kind: In, Qty: k.stock, Ref: "采购入库"}
		if k.heat != "" {
			in.Heat, in.Mill, in.Cert, in.Ref = k.heat, k.mill, "ZB-26F-0577", "送货单 SH26388"
		}
		if _, _, err := s.Record(company, "仓管 王建国", in); err != nil {
			return err
		}
	}
	if items, err = s.Items(company, ""); err != nil {
		return err
	}
	bar := func(grade string, kg float64) BOMLine {
		l := BOMLine{Code: "GB-" + grade, Name: grade + " 圆钢", Kind: Raw, Grade: grade, Qty: round(kg), Unit: "kg", Scrap: 1.5, Op: "下料"}
		if it := itemFor(items, Need{Grade: grade}); it != nil {
			l.Code, l.Name, l.Spec = it.Code, it.Name, it.Attrs["规格"]
		}
		return l
	}
	consumables := func(parent string, kg float64) []BOMLine {
		return []BOMLine{
			{Parent: parent, Code: "FL-GRAPH", Name: "水基石墨脱模剂", Kind: Aux, Qty: kgOf(kg * 0.004), Unit: "kg", Op: "热模锻"},
			{Parent: parent, Code: "FL-S330", Name: "抛丸钢丸", Kind: Aux, Spec: "S330", Qty: kgOf(kg * 0.006), Unit: "kg", Op: "抛丸"},
			{Code: "FL-OIL", Name: "防锈油", Kind: Aux, Qty: kgOf(kg * 0.0015), Unit: "kg", Op: "包装"},
		}
	}
	release := func(b BOM, why, who string, days float64) (*BOM, error) {
		s.Now = back(days)
		saved, err := s.SaveBOM(company, who, b)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", b.Part, b.Rev, err)
		}
		saved, _, err = s.ReleaseBOM(company, "总工 刘海涛", saved.ID, Release{Why: why, Effective: s.Now().In(cst).Format("2006-01-02")})
		return saved, err
	}

	// the timing gear every crankshaft takes, forged and machined here
	gear := BOM{Part: "ZSCL-28T", Name: "曲轴正时齿轮 28 齿", Drawing: "A", Lines: []BOMLine{
		{Code: "GB-40Cr-90", Name: "40Cr 圆钢", Kind: Raw, Grade: "40Cr", Spec: "φ90", Qty: 1.25, Unit: "kg", Scrap: 3, Op: "下料"},
		{Code: "FL-OIL", Name: "防锈油", Kind: Aux, Qty: 0.004, Unit: "kg", Op: "包装"},
	}, Note: "调质 + 高频淬火，齿面 50–55 HRC"}
	cranks := false
	for _, r := range refs {
		if r.Product == "crankshaft" || strings.Contains(r.Name, "曲轴") {
			cranks = true
		}
	}
	if cranks {
		if _, err := release(gear, "首次发布", "技术部 陈工", 58); err != nil {
			return err
		}
	}
	for i, r := range refs {
		if r.Material == "" {
			continue
		}
		input := r.Input
		if input <= 0 {
			input = r.Weight * 1.15
		}
		forging := BOMLine{ID: "F", Code: r.Part + "-DJ", Name: strings.TrimSpace(r.Name) + "锻件", Kind: Make, Qty: 1, Unit: "件", Op: "热模锻"}
		b := BOM{Part: r.Part, Name: r.Name, Drawing: "B", Lines: []BOMLine{forging, func() BOMLine { l := bar(r.Material, input); l.Parent = "F"; return l }()}}
		b.Lines = append(b.Lines, consumables("F", r.Weight)...)
		crank := r.Product == "crankshaft" || strings.Contains(r.Name, "曲轴")
		rod := r.Product == "conrod" || strings.Contains(r.Name, "连杆")
		switch {
		case crank:
			b.Lines = append(b.Lines,
				BOMLine{Code: "ZSCL-28T", Name: "曲轴正时齿轮 28 齿", Kind: Make, Qty: 1, Unit: "件", Op: "装配"},
				BOMLine{Code: "BZ-KEY-5x7.5", Name: "半圆键", Kind: Std, Spec: "GB/T 1099.1 5×7.5", Qty: 1, Unit: "件", Op: "装配"},
				BOMLine{Code: "BZ-PLUG-M10", Name: "油道螺塞", Kind: Std, Spec: "M10×1 GB/T 3", Qty: 2, Unit: "件", Op: "装配"},
				BOMLine{Code: "BZ-PIN-8x16", Name: "圆柱定位销", Kind: Std, Spec: "GB/T 119.2 8×16", Qty: 2, Unit: "件", Op: "装配"},
			)
		case rod:
			b.Lines = append(b.Lines,
				BOMLine{Code: "BZ-BOLT-M9", Name: "连杆螺栓", Kind: Std, Spec: "M9×1×52 10.9 级", Qty: 2, Unit: "件", Op: "胀断装配"},
				BOMLine{Code: "WG-BUSH-22", Name: "连杆小头衬套", Kind: Buy, Spec: "CuSn8 φ22×26", Qty: 1, Unit: "件", Op: "压衬套"},
			)
		}
		days := 50 - float64(i)
		if !crank || !strings.Contains(r.Part, "B20") {
			if _, err := release(b, "首次发布", "技术部 陈工", days); err != nil {
				return err
			}
			continue
		}
		// the 2.0T crankshaft: revision A, then B for the customer's drawing C with one more oil way
		if _, err := release(b, "首次发布", "技术部 陈工", days); err != nil {
			return err
		}
		s.Now = back(12)
		next, err := s.CopyBOM(company, "技术部 陈工", r.Part)
		if err != nil {
			return err
		}
		for j := range next.Lines {
			switch next.Lines[j].Code {
			case "BZ-PLUG-M10":
				next.Lines[j].Qty = 3
			case "FL-S330":
				next.Lines[j].Qty = kgOf(r.Weight * 0.005)
			}
			if next.Lines[j].Kind == Raw {
				next.Lines[j].Scrap = 1.2
			}
		}
		next.Drawing, next.Note = "C", "客户图纸 C 版：加一条斜油道"
		saved, err := s.SaveBOM(company, "技术部 陈工", *next)
		if err != nil {
			return err
		}
		s.Now = back(10)
		if _, _, err := s.ReleaseBOM(company, "总工 刘海涛", saved.ID, Release{Why: "远岳动力图纸 C 版加了一条斜油道，螺塞由 2 个改 3 个；抛丸改用新机，钢丸消耗降下来；下料改带锯后损耗按 1.2% 算",
			Effective: s.Now().In(cst).Format("2006-01-02"), Drawing: "C"}); err != nil {
			return err
		}
	}

	// the new part in trial forging: a draft from its project, with the cut weight worked out
	if p, err := s.ProjectOf(company, "ZXJ-2201"); err == nil {
		s.Now = back(5)
		if d, err := s.DraftBOM(company, "技术部 陈工", p.ID); err == nil {
			d.Lines[0].Qty, d.Lines[0].Note, d.Lines[0].Scrap = 8.1, "第二次试锻后坯料加了 0.2 kg", 1.5
			if it := itemFor(items, Need{Grade: p.Grade}); it != nil {
				d.Lines[0].Code, d.Lines[0].Name, d.Lines[0].Spec = it.Code, it.Name, it.Attrs["规格"]
			}
			d.Lines = append(d.Lines, consumables("", p.Weight)...)
			d.Lines = append(d.Lines, BOMLine{Code: "BZ-BUSH-ZXJ", Name: "转向节衬套", Kind: Buy, Spec: "φ38×42", Qty: 2, Unit: "件", Op: "压装", Note: "供应商样件在送检"})
			if _, err := s.SaveBOM(company, "技术部 陈工", *d); err != nil {
				return err
			}
		}
	}
	return nil
}

// kgOf rounds a consumable's kilograms to what a person would write (0.0864 → 0.086).
func kgOf(v float64) float64 {
	if v <= 0 {
		return 0.001
	}
	p := math.Pow(10, 2-math.Floor(math.Log10(v)))
	return math.Round(v*p) / p
}

// SeedPurchases gives the demo company the deliveries it is waiting for: the connecting rod bushes
// and bolts that its stock of is too little for a big order are on order, and the shot is coming.
func (s *Service) SeedPurchases(company string) error {
	if company != Demo {
		return nil
	}
	mark := company + "/po"
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM seeded WHERE company = ?`, mark).Scan(&n)
	if n > 0 {
		return nil
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM seeded WHERE company = ?`, company+"/bom").Scan(&n)
	if n == 0 {
		return nil // the items the orders are for come first
	}
	if _, err := s.db.Exec(`INSERT INTO seeded (company) VALUES (?)`, mark); err != nil {
		return err
	}
	items, err := s.Items(company, "")
	if err != nil {
		return err
	}
	at := time.Now()
	lines := []struct {
		code, supplier, ref string
		qty, hours          float64
	}{
		{"BZ-BOLT-M9", "宁波紧固件厂", "CG-26-0412", 30000, 20},
		{"WG-BUSH-22", "常州精工衬套", "CG-26-0415", 12000, 40},
		{"WG-BUSH-22", "常州精工衬套", "CG-26-0431", 12000, 120},
		{"FL-S330", "洛阳磨料", "CG-26-0420", 2000, 72},
	}
	for _, l := range lines {
		it := itemFor(items, Need{Code: l.code})
		if it == nil {
			continue
		}
		if _, err := s.SavePurchase(company, "采购 周敏", Purchase{Item: it.ID, Ref: l.ref, Qty: l.qty, Supplier: l.supplier,
			Expected: at.Add(time.Duration(l.hours * float64(time.Hour)))}); err != nil {
			return err
		}
	}
	return nil
}
