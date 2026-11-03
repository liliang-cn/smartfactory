package stores

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Where a die is.
const (
	OnPress = "press"  // mounted on a machine
	InStore = "store"  // in the die store, ready
	Repair  = "repair" // being re-sunk or repaired
	InTrial = "trial"  // a new die still in trial forging (研发)
	Scrap   = "scrap"  // worn out
)

// PlaceLabel names where a die is.
var PlaceLabel = map[string]string{OnPress: "在机", InStore: "在库", Repair: "返修中", InTrial: "试制中", Scrap: "已报废"}

// Die is one die (a set of dies for one operation): what it forges, how many strokes it is good
// for, how many it has struck, and where it is.
type Die struct {
	No string `json:"no"`
	// Kind is the tool it is (热锻模, 辊锻模 …): the machines whose tool it is can take it.
	Kind string `json:"kind"`
	// Product is the part it forges, Drawing the die design's revision.
	Product string `json:"product,omitempty"`
	Drawing string `json:"drawing,omitempty"`
	// Life is its design life in strokes, Strokes what it has struck (re-sinking does not reset it:
	// Resinks counts the times it was re-sunk).
	Life    int64 `json:"life"`
	Strokes int64 `json:"strokes"`
	Resinks int   `json:"resinks,omitempty"`
	// Place: press | store | repair | trial | scrap; Plant and Asset the machine it is on.
	Place     string `json:"place"`
	Plant     string `json:"plant,omitempty"`
	Asset     string `json:"asset,omitempty"`
	AssetName string `json:"assetName,omitempty"`
	// Project is the development project it was made for (研发).
	Project string `json:"project,omitempty"`
	Note    string `json:"note,omitempty"`
	// Warned is the share of life last warned about (0.9, 1).
	Warned  float64   `json:"warned,omitempty"`
	Updated time.Time `json:"updated"`
	// Left is the strokes it has left (computed).
	Left int64 `json:"left"`
}

// DieEvent is one thing that happened to a die.
type DieEvent struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text"`
	Who     string    `json:"who"`
	Strokes int64     `json:"strokes"`
}

func (d *Die) fill() *Die {
	d.Left = max(0, d.Life-d.Strokes)
	return d
}

// Dies are the company's dies, those on machines first.
func (s *Service) Dies(company string) ([]Die, error) {
	ds, err := getAll[Die](s.db, `SELECT json FROM dies WHERE company = ?`, company)
	if err != nil {
		return nil, err
	}
	rank := map[string]int{OnPress: 0, InTrial: 1, InStore: 2, Repair: 3, Scrap: 4}
	for i := range ds {
		ds[i].fill()
	}
	slices.SortFunc(ds, func(a, b Die) int {
		if r := rank[a.Place] - rank[b.Place]; r != 0 {
			return r
		}
		return strings.Compare(a.No, b.No)
	})
	if ds == nil {
		ds = []Die{}
	}
	return ds, nil
}

// Die is one die.
func (s *Service) Die(company, no string) (*Die, error) {
	ds, err := getAll[Die](s.db, `SELECT json FROM dies WHERE company = ? AND no = ?`, company, no)
	if err != nil {
		return nil, err
	}
	if len(ds) == 0 {
		return nil, ErrNotFound
	}
	return ds[0].fill(), nil
}

// DieOn is the die on a machine, nil when none is booked on it.
func (s *Service) DieOn(company, plant, asset string) (*Die, error) {
	ds, err := s.Dies(company)
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		if d.Place == OnPress && d.Plant == plant && d.Asset == asset {
			return &d, nil
		}
	}
	return nil, nil
}

// DieHistory is what happened to a die, the latest first.
func (s *Service) DieHistory(company, no string, limit int) ([]DieEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT at, kind, text, who, strokes FROM die_events WHERE company = ? AND die = ? ORDER BY at DESC, id DESC LIMIT ?`, company, no, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DieEvent{}
	for rows.Next() {
		var e DieEvent
		var at int64
		if err := rows.Scan(&at, &e.Kind, &e.Text, &e.Who, &e.Strokes); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) dieEvent(company string, d *Die, kind, text, who string) error {
	_, err := s.db.Exec(`INSERT INTO die_events (company, die, at, kind, text, who, strokes) VALUES (?,?,?,?,?,?,?)`,
		company, d.No, ms(s.Now()), kind, text, who, d.Strokes)
	return err
}

func (s *Service) putDie(company string, d *Die) error {
	d.Updated = s.Now()
	d.fill()
	return s.putJSON("dies", company, d.No, d)
}

// SaveDie books a new die, or changes what is written about one (not where it is or its strokes).
func (s *Service) SaveDie(company, who string, d Die, isNew bool) (*Die, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.No, d.Kind, d.Product, d.Drawing, d.Note = strings.TrimSpace(d.No), strings.TrimSpace(d.Kind), strings.TrimSpace(d.Product), strings.TrimSpace(d.Drawing), strings.TrimSpace(d.Note)
	var p []string
	if d.No == "" {
		p = append(p, "模具要有模具号")
	}
	if d.Kind == "" {
		p = append(p, "填它是什么模（热锻模、辊锻模 …），对得上设备用的模具才能装")
	}
	if d.Life <= 0 {
		p = append(p, "设计寿命（次）要大于 0")
	}
	if d.Strokes < 0 {
		p = append(p, "已打次数不能是负数")
	}
	if len(p) > 0 {
		return nil, &Problems{p}
	}
	old, err := s.Die(company, d.No)
	switch {
	case isNew && err == nil:
		return nil, &Problems{[]string{fmt.Sprintf("模具号 %s 已经有了", d.No)}}
	case !isNew && err != nil:
		return nil, err
	}
	if isNew {
		if d.Place != InTrial {
			d.Place = InStore
		}
		d.Plant, d.Asset, d.AssetName, d.Warned = "", "", "", 0
		if err := s.putDie(company, &d); err != nil {
			return nil, err
		}
		text := fmt.Sprintf("建档：%s，设计寿命 %d 次", d.Kind, d.Life)
		if d.Strokes > 0 {
			text += fmt.Sprintf("，已打 %d 次", d.Strokes)
		}
		return &d, s.dieEvent(company, &d, "new", text, who)
	}
	// what is written about it; where it is and its strokes change by mounting and counting
	old.Kind, old.Product, old.Drawing, old.Life, old.Note, old.Project = d.Kind, d.Product, d.Drawing, d.Life, d.Note, d.Project
	if err := s.putDie(company, old); err != nil {
		return nil, err
	}
	return old, s.dieEvent(company, old, "edit", "改了档案", who)
}

// DieAction is what a person does with a die.
type DieAction struct {
	// Do: mount (on Plant/Asset) | unmount (To store or repair) | repair (back from repair: Resunk if
	// it was re-sunk) | scrap | release (a trial die into the store)
	Do    string `json:"do"`
	Plant string `json:"plant,omitempty"`
	Asset string `json:"asset,omitempty"`
	To    string `json:"to,omitempty"`
	// Life is a new design life after re-sinking (0: the same).
	Life   int64  `json:"life,omitempty"`
	Resunk bool   `json:"resunk,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Act does what a person (or the twin, who "孪生") does with a die.
func (s *Service) Act(company, who, no string, a DieAction) (*Die, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.act(company, who, no, a)
}

func (s *Service) act(company, who, no string, a DieAction) (*Die, error) {
	d, err := s.Die(company, no)
	if err != nil {
		return nil, err
	}
	note := ""
	if a.Note = strings.TrimSpace(a.Note); a.Note != "" {
		note = "：" + a.Note
	}
	switch a.Do {
	case "mount":
		if d.Place == Scrap {
			return nil, &Problems{[]string{"报废的模具不能装"}}
		}
		if d.Place == OnPress {
			return nil, &Problems{[]string{fmt.Sprintf("%s 已经装在 %s 上，先卸下", d.No, d.AssetName)}}
		}
		press, plantName := s.press(company, a.Plant, a.Asset)
		if press == nil {
			return nil, &Problems{[]string{"选一台用模具的设备"}}
		}
		if press.Tool.Label != d.Kind {
			return nil, &Problems{[]string{fmt.Sprintf("%s 用的是%s，%s 是%s", press.ID, press.Tool.Label, d.No, d.Kind)}}
		}
		if on, _ := s.dieOnLocked(company, a.Plant, a.Asset); on != nil {
			return nil, &Problems{[]string{fmt.Sprintf("%s 上装着 %s，先卸下", press.ID, on.No)}}
		}
		d.Place, d.Plant, d.Asset, d.AssetName = OnPress, a.Plant, a.Asset, fmt.Sprintf("%s %s %s", plantName, press.ID, press.Name)
		// strokes count from the press's cycles from now on, and the twin's tool wears as this die
		delete(s.hit, a.Plant+"/"+a.Asset)
		s.Twin.MountTool(company, a.Plant, a.Asset, d.Strokes, d.Life)
		if err := s.putDie(company, d); err != nil {
			return nil, err
		}
		s.warn(company, d)
		return d, s.dieEvent(company, d, "mount", fmt.Sprintf("装上 %s%s", d.AssetName, note), who)
	case "unmount":
		if d.Place != OnPress {
			return nil, &Problems{[]string{d.No + " 没有装在设备上"}}
		}
		to := a.To
		if to != Repair && to != Scrap {
			to = InStore
		}
		at := d.AssetName
		if s.Twin != nil {
			s.Twin.Alert(company, d.Plant, d.Asset, "die-life", "")
		}
		d.Place, d.Plant, d.Asset, d.AssetName = to, "", "", ""
		if err := s.putDie(company, d); err != nil {
			return nil, err
		}
		return d, s.dieEvent(company, d, "unmount", fmt.Sprintf("从 %s 卸下，%s%s", at, PlaceLabel[to], note), who)
	case "repair":
		if d.Place != Repair {
			return nil, &Problems{[]string{d.No + " 不在返修"}}
		}
		text := "修好入库"
		if a.Resunk {
			d.Resinks++
			text = fmt.Sprintf("第 %d 次翻新（重新开型腔）后入库", d.Resinks)
		}
		if a.Life > 0 {
			d.Life = a.Life
			text += fmt.Sprintf("，寿命定为 %d 次", d.Life)
		}
		d.Place, d.Warned = InStore, 0
		if err := s.putDie(company, d); err != nil {
			return nil, err
		}
		return d, s.dieEvent(company, d, "repair", text+note, who)
	case "scrap":
		if d.Place == OnPress {
			return nil, &Problems{[]string{"先从设备上卸下"}}
		}
		d.Place = Scrap
		if err := s.putDie(company, d); err != nil {
			return nil, err
		}
		return d, s.dieEvent(company, d, "scrap", fmt.Sprintf("报废，共打了 %d 次%s", d.Strokes, note), who)
	case "release":
		if d.Place != InTrial {
			return nil, &Problems{[]string{d.No + " 不是试制模具"}}
		}
		d.Place = InStore
		if err := s.putDie(company, d); err != nil {
			return nil, err
		}
		return d, s.dieEvent(company, d, "release", "试制通过，转为量产模具入库"+note, who)
	}
	return nil, &Problems{[]string{"要做什么：装模、卸模、修好入库、报废"}}
}

func (s *Service) dieOnLocked(company, plant, asset string) (*Die, error) {
	return s.DieOn(company, plant, asset)
}

// Presses are the company's machines that take dies, with the die booked on each.
type PressNow struct {
	Plant     string `json:"plant"`
	PlantName string `json:"plantName"`
	Line      string `json:"line"`
	LineName  string `json:"lineName"`
	Product   string `json:"product"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	// Kind is the tool it takes; TwinUsed and TwinLife the twin's own count of its current tool.
	Kind     string `json:"kind"`
	TwinUsed int64  `json:"twinUsed"`
	TwinLife int64  `json:"twinLife"`
	Changing bool   `json:"changing,omitempty"`
	Die      *Die   `json:"die,omitempty"`
}

// Presses are the machines that take dies, and the die on each.
func (s *Service) Presses(company string) ([]PressNow, error) {
	out := []PressNow{}
	if s.Twin == nil {
		return out, nil
	}
	ds, err := s.Dies(company)
	if err != nil {
		return nil, err
	}
	for _, p := range s.Twin.Materials(company) {
		for _, l := range p.Lines {
			for _, m := range l.Presses {
				x := PressNow{Plant: p.ID, PlantName: p.Name, Line: l.ID, LineName: l.Name, Product: l.Product, ID: m.ID, Name: m.Name,
					Kind: m.Tool.Label, TwinUsed: m.Tool.Used, TwinLife: m.Tool.Life, Changing: m.Changing}
				for _, d := range ds {
					if d.Place == OnPress && d.Plant == p.ID && d.Asset == m.ID {
						x.Die = &d
						break
					}
				}
				out = append(out, x)
			}
		}
	}
	return out, nil
}

func (s *Service) press(company, plant, asset string) (*pressInfo, string) {
	if s.Twin == nil {
		return nil, ""
	}
	for _, p := range s.Twin.Materials(company) {
		if p.ID != plant {
			continue
		}
		for _, l := range p.Lines {
			for _, m := range l.Presses {
				if m.ID == asset {
					return &pressInfo{ID: m.ID, Name: m.Name, Tool: m.Tool}, p.Name
				}
			}
		}
	}
	return nil, ""
}

type pressInfo struct {
	ID, Name string
	Tool     struct {
		Label string `json:"label"`
		Life  int64  `json:"life"`
		Used  int64  `json:"used"`
	}
}

// warn raises (or clears) the die-life alarm on the machine a die is on.
func (s *Service) warn(company string, d *Die) {
	if s.Twin == nil || d.Place != OnPress || d.Life <= 0 {
		return
	}
	share := float64(d.Strokes) / float64(d.Life)
	switch {
	case share >= 1:
		s.Twin.Alert(company, d.Plant, d.Asset, "die-life", fmt.Sprintf("模具 %s 已到设计寿命（%d/%d 次），该下机翻新或报废", d.No, d.Strokes, d.Life))
	case share >= 0.9:
		s.Twin.Alert(company, d.Plant, d.Asset, "die-life", fmt.Sprintf("模具 %s 寿命 %.0f%%，还能打约 %d 次，备好替换模", d.No, share*100, d.Left))
	default:
		s.Twin.Alert(company, d.Plant, d.Asset, "die-life", "")
	}
}
