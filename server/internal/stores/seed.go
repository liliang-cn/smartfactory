package stores

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Demo is the built-in demo company whose stores start with a plant's worth of history; every
// other company, a factory built from its template included, starts with empty stores.
const Demo = "huaduan"

// Seed gives the demo company its stores the first time the twin runs it: bar by heat, standard
// parts (a bolt below its safety stock), finished parts, dies on the presses (one near the end of
// its life), spares and one in repair, and a new part in trial forging.
func (s *Service) Seed(company string) error {
	if company != Demo {
		return nil
	}
	var done int
	s.db.QueryRow(`SELECT COUNT(*) FROM seeded WHERE company = ?`, company).Scan(&done)
	if done > 0 {
		return nil
	}
	mats := s.Twin.Materials(company)
	if len(mats) == 0 {
		return nil
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
	if key[Bar] == "" {
		return nil // the pack declares no bar: nothing that fits
	}
	who := "仓管 王建国"
	back := func(days float64) func() time.Time {
		at := time.Now().Add(-time.Duration(days * 24 * float64(time.Hour)))
		return func() time.Time { return at }
	}
	now := s.Now
	defer func() { s.Now = now }()

	// bar: each grade the lines forge, one or two heats from the mill that supplies it
	type grade struct{ mat, mill string }
	var grades []grade
	for _, p := range mats {
		for _, l := range p.Lines {
			if l.Material == "" || slices.ContainsFunc(grades, func(g grade) bool { return g.mat == l.Material }) {
				continue
			}
			// the mill that supplies the line, or one that makes the grade (bearing steel from a bearing-steel mill)
			mill := []string{"华东特钢", "北方重钢"}[len(grades)%2]
			if strings.HasPrefix(l.Material, "GCr") {
				mill = "兴华轴承钢"
			}
			if l.Lot != nil && l.Lot.Supplier != "" {
				mill = l.Lot.Supplier
			}
			grades = append(grades, grade{l.Material, mill})
		}
	}
	sizes := []string{"φ80", "φ65", "φ120", "φ90", "φ200"}
	for i, g := range grades {
		s.Now = back(30)
		it, err := s.SaveItem(company, Item{Cat: key[Bar], Code: fmt.Sprintf("GB-%s-%s", g.mat, sizes[i%len(sizes)][len("φ"):]), Name: g.mat + " 圆钢 " + sizes[i%len(sizes)],
			Attrs: map[string]string{"牌号": g.mat, "规格": sizes[i%len(sizes)]}, Unit: "t", Safety: 25, Place: fmt.Sprintf("原料库 A%d 区", i+1)})
		if err != nil {
			return err
		}
		heats := 1
		if i == 0 {
			heats = 2
		}
		for h := range heats {
			s.Now = back(float64(25 - h*9 - i))
			no := fmt.Sprintf("26%c%05d", "ABCDEFGHJK"[(i+h)%10], 10231+i*977+h*311)
			if _, _, err := s.Record(company, who, MoveInput{Item: it.ID, Kind: In, Qty: float64(38 + (i*7+h*3)%6), Heat: no, Mill: g.mill,
				Cert: fmt.Sprintf("ZB-%s-%04d", no[:3], 412+i*31+h*7), Ref: fmt.Sprintf("送货单 SH%d", 26100+i*13+h), Note: "进厂复检：低倍、超声合格"}); err != nil {
				return err
			}
		}
	}

	// standard and spare parts for the presses and their hydraulics
	parts := []struct {
		code, name, spec, use string
		safety, stock         float64
		unit                  string
	}{
		{"BZ-M24-120", "高强度螺栓", "M24×120 10.9 级", "模座紧固", 40, 12, "件"},
		{"BZ-6316", "深沟球轴承", "6316-2RS", "辊锻机主轴", 6, 10, "件"},
		{"BZ-MF-200", "液压缸密封圈", "Φ200 聚氨酯", "压力机主缸", 10, 16, "套"},
		{"BZ-4WE10", "电磁换向阀", "4WE10E", "压力机液压站", 2, 3, "件"},
		{"BZ-TQ-30", "感应圈", "Φ90 曲轴坯用", "中频加热炉", 2, 4, "个"},
	}
	if key[Part] == "" {
		parts = nil
	}
	for i, x := range parts {
		s.Now = back(20 - float64(i))
		it, err := s.SaveItem(company, Item{Cat: key[Part], Code: x.code, Name: x.name, Attrs: map[string]string{"规格": x.spec, "用在": x.use}, Unit: x.unit, Safety: x.safety, Place: "备件库 B" + fmt.Sprint(i+1)})
		if err != nil {
			return err
		}
		if _, _, err := s.Record(company, who, MoveInput{Item: it.ID, Kind: In, Qty: x.stock + 8, Ref: "采购入库"}); err != nil {
			return err
		}
		s.Now = back(6 - float64(i))
		if _, _, err := s.Record(company, "维修班 李强", MoveInput{Item: it.ID, Kind: Out, Qty: 8, Ref: "设备维修领用"}); err != nil {
			return err
		}
	}

	// finished parts of the first lines' products
	for i, p := range mats[0].Lines[:min(2, len(mats[0].Lines))] {
		if key[Product] == "" {
			break
		}
		s.Now = back(2)
		it, err := s.SaveItem(company, Item{Cat: key[Product], Code: fmt.Sprintf("CP-%s-%d", p.ID, i+1), Name: p.Product, Unit: "件", Place: "成品库 C" + fmt.Sprint(i+1)})
		if err != nil {
			return err
		}
		if _, _, err := s.Record(company, "成品库 赵敏", MoveInput{Item: it.ID, Kind: In, Qty: float64(360 + i*140), Ref: "完工入库"}); err != nil {
			return err
		}
	}

	// dies: on every press of the first plant's first two lines, a spare of each kind, one in repair
	s.Now = back(40)
	kinds := map[string]string{}
	for li, l := range mats[0].Lines[:min(2, len(mats[0].Lines))] {
		for pi, m := range l.Presses {
			share := []float64{0.92, 0.55, 0.31, 0.68, 0.12}[(li*3+pi)%5]
			d := Die{No: fmt.Sprintf("M-%s-1", m.ID), Kind: m.Tool.Label, Product: l.Product, Drawing: "A1", Life: m.Tool.Life, Strokes: int64(share * float64(m.Tool.Life))}
			if _, err := s.SaveDie(company, "模具库 孙磊", d, true); err != nil {
				return err
			}
			if _, err := s.Act(company, "模具库 孙磊", d.No, DieAction{Do: "mount", Plant: mats[0].ID, Asset: m.ID}); err != nil {
				return err
			}
			if _, ok := kinds[m.Tool.Label]; !ok {
				kinds[m.Tool.Label] = l.Product
				spare := Die{No: fmt.Sprintf("M-%s-2", m.ID), Kind: m.Tool.Label, Product: l.Product, Drawing: "A1", Life: m.Tool.Life, Strokes: m.Tool.Life / 4, Resinks: 1}
				if _, err := s.SaveDie(company, "模具库 孙磊", spare, true); err != nil {
					return err
				}
			}
		}
	}
	if len(mats[0].Lines) > 0 && len(mats[0].Lines[0].Presses) > 0 {
		m := mats[0].Lines[0].Presses[0]
		r := Die{No: fmt.Sprintf("M-%s-3", m.ID), Kind: m.Tool.Label, Product: mats[0].Lines[0].Product, Drawing: "A1", Life: m.Tool.Life, Strokes: m.Tool.Life}
		if d, err := s.SaveDie(company, "模具库 孙磊", r, true); err == nil {
			s.Now = back(3)
			d.Place = Repair
			if err := s.putDie(company, d); err != nil {
				return err
			}
			if err := s.dieEvent(company, d, "unmount", "到寿命下机，送模具车间翻新（型腔补焊后重新加工）", "模具库 孙磊"); err != nil {
				return err
			}
		}
	}

	// a new part in trial forging, with its trial die
	s.Now = back(18)
	pr, err := s.SaveProject(company, "技术部 陈工", Project{Part: "ZXJ-2201", Name: "新能源车转向节", Customer: "衡川汽车", Grade: "42CrMo", Weight: 6.8,
		Drawing: "B", DieDesign: "A2", Stage: "模具设计", Owner: "技术部 陈工", Due: time.Now().AddDate(0, 0, 24).Format("2006-01-02"),
		Note: "客户要求 PPAP 3 级；R 角充不满是难点"})
	if err != nil {
		return err
	}
	var trialDie string
	for k := range kinds {
		trialDie = "M-ZXJ2201-T1"
		if _, err := s.SaveDie(company, "技术部 陈工", Die{No: trialDie, Kind: k, Product: "新能源车转向节", Drawing: "A2", Life: 8000, Place: InTrial, Project: pr.ID}, true); err != nil {
			return err
		}
		break
	}
	trials := []Trial{
		{Die: trialDie, Params: "加热 1180°C，始锻 1150°C，预锻一次 + 终锻一次", Qty: 12, Good: 7, Result: "需改模", Findings: "法兰 R 角充不满 5 件；飞边偏大"},
		{Die: trialDie, Params: "加热 1190°C，终锻模 R 角修大 1.5 mm，加大坯料 0.2 kg", Qty: 20, Good: 18, Result: "需改工艺", Findings: "2 件折叠（磁粉探伤发现），尺寸全部合格"},
	}
	for i, t := range trials {
		s.Now = back(float64(9 - i*5))
		t.Project = pr.ID
		if _, err := s.AddTrial(company, "试制班 周伟", t); err != nil {
			return err
		}
	}
	s.Now = now
	_, err = s.db.Exec(`INSERT INTO seeded (company) VALUES (?)`, company)
	return err
}
