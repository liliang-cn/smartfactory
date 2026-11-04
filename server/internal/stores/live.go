package stores

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"smartfactory/internal/engine"
)

func jsonOf(v any) (string, error) {
	raw, err := json.Marshal(v)
	return string(raw), err
}

// Run keeps the stores in step with the twin every few seconds, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		s.Sync()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Sync looks at the twin once: what the lines forged of which heat, the strokes the presses struck
// with the dies on them; and gives the lines the heats the store holds.
func (s *Service) Sync() {
	if s.Twin == nil {
		return
	}
	for _, c := range s.Twin.CompanyIDs() {
		if err := s.Seed(c); err != nil {
			s.Log.Warn("stores: seed", "company", c, "err", err)
		}
		if err := s.SeedBOM(c); err != nil {
			s.Log.Warn("stores: seed BOM", "company", c, "err", err)
		}
		if err := s.SeedPurchases(c); err != nil {
			s.Log.Warn("stores: seed purchases", "company", c, "err", err)
		}
		mats := s.Twin.Materials(c)
		if mats == nil {
			continue
		}
		if err := s.syncHeats(c, mats); err != nil {
			s.Log.Warn("stores: heats", "company", c, "err", err)
		}
		if err := s.syncDies(c, mats); err != nil {
			s.Log.Warn("stores: dies", "company", c, "err", err)
		}
	}
}

func (s *Service) syncHeats(company string, mats []engine.PlantMaterial) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	reserved := map[string]float64{}
	for _, p := range mats {
		for _, l := range p.Lines {
			key := company + "/" + p.ID + "/" + l.ID
			if l.Lot == nil {
				delete(s.seen, key)
				continue
			}
			prev, ok := s.seen[key]
			s.seen[key] = seenLine{heat: l.Lot.No, left: l.Lot.Left}
			reserved[l.Lot.No] += float64(l.Lot.Left) * l.Input
			if !ok || prev.heat != l.Lot.No || prev.left <= l.Lot.Left {
				continue
			}
			n := prev.left - l.Lot.Left
			order := ""
			if l.Job != nil {
				order = l.Job.No
			}
			if err := s.used(company, l.Lot.No, l.Lot.Supplier, p.ID, l.ID, p.Name+" "+l.Name, order, n, float64(n)*l.Input, now); err != nil {
				return err
			}
		}
	}
	// the heats the store holds, for the lines' next lots: what is left less what lines already took
	hs, err := s.Heats(company, "")
	if err != nil {
		return err
	}
	feed := []engine.Heat{}
	for _, h := range hs {
		f := kgPer(h.Unit)
		kg := h.Left*f - reserved[h.No]
		if f == 0 || h.Grade == "" || kg <= 0 {
			continue
		}
		feed = append(feed, engine.Heat{No: h.No, Mill: h.Mill, Material: h.Grade, Kg: kg})
	}
	s.Twin.SetHeats(company, feed)
	return nil
}

func (s *Service) syncDies(company string, mats []engine.PlantMaterial) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ds, err := s.Dies(company)
	if err != nil {
		return err
	}
	for _, p := range mats {
		for _, l := range p.Lines {
			for _, m := range l.Presses {
				key := p.ID + "/" + m.ID
				prev, seen := s.hit[key]
				s.hit[key] = seenPress{cycles: m.Cycles, used: m.Tool.Used}
				i := slices.IndexFunc(ds, func(d Die) bool { return d.Place == OnPress && d.Plant == p.ID && d.Asset == m.ID })
				if i < 0 {
					continue
				}
				d := &ds[i]
				if !seen {
					// after a restart, or right after mounting: the twin's tool is this die
					s.Twin.MountTool(company, p.ID, m.ID, d.Strokes, d.Life)
					s.hit[key] = seenPress{cycles: m.Cycles, used: d.Strokes}
					s.warn(company, d)
					continue
				}
				// the twin changed its tool by itself: this die came off for re-sinking
				if prev.used > 0 && m.Tool.Used < prev.used && !m.Changing {
					if err := s.twinChange(company, d, p, l); err != nil {
						return err
					}
					continue
				}
				dc := m.Cycles - prev.cycles
				if dc <= 0 || dc > 100_000 {
					continue
				}
				d.Strokes += dc
				share := float64(d.Strokes) / float64(max(d.Life, 1))
				for _, mark := range []float64{0.9, 1} {
					if share >= mark && d.Warned < mark {
						d.Warned = mark
						text := fmt.Sprintf("%s 上的模具 %s 寿命 %.0f%%（%d/%d 次）", m.ID, d.No, share*100, d.Strokes, d.Life)
						if mark == 1 {
							text = fmt.Sprintf("%s 上的模具 %s 到了设计寿命（%d 次）", m.ID, d.No, d.Life)
						}
						if err := s.dieEvent(company, d, "warn", text, "孪生"); err != nil {
							return err
						}
						if s.Event != nil {
							s.Event(company, "die.life", d.No, text)
						}
						s.warn(company, d)
					}
				}
				if err := s.putDie(company, d); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// twinChange books a die change the twin made on its own: the die off for re-sinking, and the
// next ready die of its kind for this part (if the store has one) on.
func (s *Service) twinChange(company string, d *Die, p engine.PlantMaterial, l engine.LineMaterial) error {
	plant, asset := d.Plant, d.Asset
	if _, err := s.act(company, "孪生", d.No, DieAction{Do: "unmount", To: Repair, Note: "到计划换模点，下机翻新"}); err != nil {
		return err
	}
	ds, err := s.Dies(company)
	if err != nil {
		return err
	}
	for _, o := range ds {
		if o.Place == InStore && o.Kind == d.Kind && (o.Product == "" || strings.Contains(l.Product, o.Product) || strings.Contains(o.Product, l.Product)) {
			_, err := s.act(company, "孪生", o.No, DieAction{Do: "mount", Plant: plant, Asset: asset, Note: "换上替换模"})
			return err
		}
	}
	return nil
}
