package stores

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Stages of a new part, in order.
var Stages = []string{"立项", "模具设计", "试锻", "检验", "量产", "暂停"}

// Project is a new part being developed for a customer: from the drawing to a die that forges it
// and a sign-off to make it on a line.
type Project struct {
	ID       string `json:"id"`
	Part     string `json:"part"`
	Name     string `json:"name"`
	Customer string `json:"customer,omitempty"`
	// Grade is the steel it is forged from (牌号), Weight the forging's weight (kg).
	Grade  string  `json:"grade,omitempty"`
	Weight float64 `json:"weight,omitempty"`
	// Drawing is the customer drawing's revision, DieDesign the die design's.
	Drawing   string `json:"drawing,omitempty"`
	DieDesign string `json:"dieDesign,omitempty"`
	Stage     string `json:"stage"`
	Owner     string `json:"owner,omitempty"`
	// Due is when the customer wants the first parts (YYYY-MM-DD).
	Due string `json:"due,omitempty"`
	// Docs are the documents in the library it goes with (titles), Note what else to know.
	Docs []string `json:"docs,omitempty"`
	Note string   `json:"note,omitempty"`
	// Released is when it was signed off for production, by whom.
	Released   *time.Time `json:"released,omitempty"`
	ReleasedBy string     `json:"releasedBy,omitempty"`
	Created    time.Time  `json:"created"`
	Updated    time.Time  `json:"updated"`
	// Dies made for it, and its trial forgings, the latest first (filled in when read).
	Dies   []Die   `json:"dies,omitempty"`
	Trials []Trial `json:"trials,omitempty"`
}

// Trial is one trial forging (试锻): with which die and settings, how many came out good, and
// what inspection found.
type Trial struct {
	ID      string    `json:"id"`
	Project string    `json:"project"`
	At      time.Time `json:"at"`
	Die     string    `json:"die,omitempty"`
	// Params are the settings tried: 加热温度, 始锻温度, 打击能量 … as written.
	Params string `json:"params,omitempty"`
	Qty    int    `json:"qty"`
	Good   int    `json:"good"`
	// Result: 合格 | 需改模 | 需改工艺 | 不合格; Findings what inspection found (尺寸、折叠、充不满 …).
	Result   string `json:"result"`
	Findings string `json:"findings,omitempty"`
	Who      string `json:"who"`
}

// TrialResults are the verdicts a trial may have.
var TrialResults = []string{"合格", "需改模", "需改工艺", "不合格"}

// Projects are the company's development projects, the ones in progress first.
func (s *Service) Projects(company string) ([]Project, error) {
	ps, err := getAll[Project](s.db, `SELECT json FROM projects WHERE company = ?`, company)
	if err != nil {
		return nil, err
	}
	ds, _ := s.Dies(company)
	for i := range ps {
		for _, d := range ds {
			if d.Project == ps[i].ID {
				ps[i].Dies = append(ps[i].Dies, d)
			}
		}
		ps[i].Trials, _ = s.trials(company, ps[i].ID)
	}
	done := func(p Project) bool { return p.Stage == "量产" || p.Stage == "暂停" }
	slices.SortFunc(ps, func(a, b Project) int {
		if done(a) != done(b) {
			if done(a) {
				return 1
			}
			return -1
		}
		return b.Updated.Compare(a.Updated)
	})
	if ps == nil {
		ps = []Project{}
	}
	return ps, nil
}

// ProjectOf is one project, by id or part number.
func (s *Service) ProjectOf(company, id string) (*Project, error) {
	ps, err := s.Projects(company)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.ID == id || strings.EqualFold(p.Part, id) {
			return &p, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Service) trials(company, project string) ([]Trial, error) {
	return getAll[Trial](s.db, `SELECT json FROM trials WHERE company = ? AND project = ? ORDER BY at DESC`, company, project)
}

// SaveProject starts a project or changes one (a stage change to 量产 goes through Release).
func (s *Service) SaveProject(company, who string, p Project) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Part, p.Name, p.Customer = strings.TrimSpace(p.Part), strings.TrimSpace(p.Name), strings.TrimSpace(p.Customer)
	var probs []string
	if p.Name == "" {
		probs = append(probs, "零件要有名称")
	}
	if p.Part == "" {
		probs = append(probs, "填零件号（客户图号也行）")
	}
	if utf8.RuneCountInString(p.Name) > 60 {
		probs = append(probs, "名称太长（最多 60 字）")
	}
	if p.Due != "" {
		if _, err := time.Parse("2006-01-02", p.Due); err != nil {
			probs = append(probs, "交样日期写成 2026-11-30 这样")
		}
	}
	if p.Stage == "" {
		p.Stage = Stages[0]
	}
	if !slices.Contains(Stages, p.Stage) {
		probs = append(probs, "阶段是 "+strings.Join(Stages, "、")+" 之一")
	}
	if len(probs) > 0 {
		return nil, &Problems{probs}
	}
	all, err := getAll[Project](s.db, `SELECT json FROM projects WHERE company = ?`, company)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	if p.ID == "" {
		if slices.ContainsFunc(all, func(o Project) bool { return strings.EqualFold(o.Part, p.Part) }) {
			return nil, &Problems{[]string{fmt.Sprintf("零件号 %s 已经有项目了", p.Part)}}
		}
		p.ID, p.Created = newID("RD"), now
		if p.Owner == "" {
			p.Owner = who
		}
	} else {
		i := slices.IndexFunc(all, func(o Project) bool { return o.ID == p.ID })
		if i < 0 {
			return nil, ErrNotFound
		}
		old := all[i]
		p.Created, p.Released, p.ReleasedBy = old.Created, old.Released, old.ReleasedBy
		if p.Stage == "量产" && old.Stage != "量产" {
			return nil, &Problems{[]string{"转量产要用「批准量产」，它会把试制模具转入库"}}
		}
	}
	p.Updated, p.Dies, p.Trials = now, nil, nil
	if err := s.putJSON("projects", company, p.ID, p); err != nil {
		return nil, err
	}
	return s.projectOf(company, p.ID)
}

func (s *Service) projectOf(company, id string) (*Project, error) { return s.ProjectOf(company, id) }

// AddTrial records a trial forging; the project moves to 试锻 if it was before it.
func (s *Service) AddTrial(company, who string, t Trial) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.ProjectOf(company, t.Project)
	if err != nil {
		return nil, err
	}
	var probs []string
	if t.Qty <= 0 {
		probs = append(probs, "试锻了几件？")
	}
	if t.Good < 0 || t.Good > t.Qty {
		probs = append(probs, "合格件数在 0 和试锻件数之间")
	}
	if !slices.Contains(TrialResults, t.Result) {
		probs = append(probs, "结论是 "+strings.Join(TrialResults, "、")+" 之一")
	}
	if t.Die != "" {
		if _, err := s.Die(company, t.Die); err != nil {
			probs = append(probs, "没有模具 "+t.Die)
		}
	}
	if len(probs) > 0 {
		return nil, &Problems{probs}
	}
	t.ID, t.Project, t.At, t.Who = newID("T"), p.ID, s.Now(), who
	if err := s.putTrial(company, t); err != nil {
		return nil, err
	}
	if i := slices.Index(Stages, p.Stage); i >= 0 && i < 2 {
		p.Stage = "试锻"
	}
	p.Updated, p.Dies, p.Trials = s.Now(), nil, nil
	if err := s.putJSON("projects", company, p.ID, p); err != nil {
		return nil, err
	}
	return s.ProjectOf(company, p.ID)
}

func (s *Service) putTrial(company string, t Trial) error {
	raw, err := jsonOf(t)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO trials (company, project, id, at, json) VALUES (?,?,?,?,?)`, company, t.Project, t.ID, ms(t.At), raw)
	return err
}

// Release signs a project off for production: its trial dies go into the store as production dies.
func (s *Service) Release(company, who, id string) (*Project, error) {
	p, err := s.ProjectOf(company, id)
	if err != nil {
		return nil, err
	}
	if p.Stage == "量产" {
		return nil, &Problems{[]string{"已经批准量产了"}}
	}
	if !slices.ContainsFunc(p.Trials, func(t Trial) bool { return t.Result == "合格" }) {
		return nil, &Problems{[]string{"还没有一次合格的试锻，不能批准量产"}}
	}
	for _, d := range p.Dies {
		if d.Place == InTrial {
			if _, err := s.Act(company, who, d.No, DieAction{Do: "release", Note: "随 " + p.Part + " 批准量产"}); err != nil {
				return nil, err
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	p.Stage, p.Released, p.ReleasedBy, p.Updated, p.Dies, p.Trials = "量产", &now, who, now, nil, nil
	if err := s.putJSON("projects", company, p.ID, p); err != nil {
		return nil, err
	}
	return s.ProjectOf(company, p.ID)
}
