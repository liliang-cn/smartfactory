// Package checks keeps what people write down by hand: lab results, patrol rounds, first-article
// measurements. An administrator sets up a form — what it asks, which machines it is for, how
// often a round is due — and operators fill it in on a phone at the machine. Every number becomes
// a reading like an instrument's (source manual), checked and stored in the series store with the
// time it was taken; one that stands for a machine's own field (its quality measure) reaches the
// twin, and a value out of spec or an abnormal answer raises a quality alarm on the machine.
package checks

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// Field kinds.
const (
	Number = "number"
	Choice = "choice"
	YesNo  = "yesno"
	Text   = "text"
	Photo  = "photo"
)

// Form kinds.
var Kinds = map[string]string{"patrol": "点检", "first": "首件检验", "lab": "化验", "other": "记录"}

// Source is what a reading written down by a person carries as its source.
const Source = "manual"

// Field is one thing a form asks.
type Field struct {
	// Key names the value: the series field a number is stored under when it stands for no field
	// of the machine's own (letters, digits and _).
	Key   string `json:"key"`
	Label string `json:"label"`
	// Kind: number | choice | yesno | text | photo
	Kind string `json:"kind"`
	Unit string `json:"unit,omitempty"`
	// Lo, Hi and Target are a number's spec.
	Lo     *float64 `json:"lo,omitempty"`
	Hi     *float64 `json:"hi,omitempty"`
	Target *float64 `json:"target,omitempty"`
	// Options are a choice's answers.
	Options []string `json:"options,omitempty"`
	// Bad are the answers that are abnormal (a choice's, or 是 / 否 for a yes/no question).
	Bad []string `json:"bad,omitempty"`
	// Twin is the machine's own field a number stands for (measure, temp, power, vib): it is
	// stored with the machine's readings of that field, and a measure reaches the twin's quality
	// chart. A measure without a spec of its own is judged by the machine's.
	Twin     string `json:"twin,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// Schedule says when a round of a form is due on each of its machines.
type Schedule struct {
	// Every: "" (no schedule: filled in when needed) | shift | day | hours
	Every string `json:"every,omitempty"`
	// Hours between rounds, for every = hours.
	Hours int `json:"hours,omitempty"`
	// At is when rounds start, HH:MM: the shifts' starts (shift; default 08:00 and 20:00), the
	// day's round (day; default 08:00), the first round of the day (hours; default 00:00).
	At []string `json:"at,omitempty"`
	// Within is how many minutes from the start a round must be done in; 0: before the next starts.
	Within int `json:"within,omitempty"`
}

// Form is what a person fills in at a machine.
type Form struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind: patrol (点检) | first (首件检验) | lab (化验) | other
	Kind string `json:"kind"`
	// Plant is the plant it is for ("" every plant); Lines (ids) and Assets (ids) narrow it to
	// those lines and machines of the plant, none of either: every station of the plant.
	Plant    string   `json:"plant,omitempty"`
	Lines    []string `json:"lines,omitempty"`
	Assets   []string `json:"assets,omitempty"`
	Fields   []Field  `json:"fields"`
	Schedule Schedule `json:"schedule"`
	// Note is what the person filling it in reads first.
	Note    string    `json:"note,omitempty"`
	By      string    `json:"by,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Problems are what is wrong with a form or a submission.
type Problems struct{ List []string }

func (p *Problems) Error() string { return strings.Join(p.List, "；") }

// ErrNotFound: no such form or submission.
var ErrNotFound = errors.New("not found")

// shanghai is where rounds are timed: shift starts are wall-clock times at the plant.
var shanghai = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return l
	}
	return time.FixedZone("CST", 8*3600)
}()

// Service keeps every company's forms and what was filled in, in {data}/checks.db.
type Service struct {
	Eng *engine.Engine
	Hub *tags.Hub
	Log *slog.Logger
	// Now is the clock and Loc where rounds are timed (tests set them).
	Now func() time.Time
	Loc *time.Location
	// Changed is told the company whose machine a form was just filled in on (nil: no one).
	Changed func(company string)

	db *sql.DB
	// mu keeps two copies of one submission (a phone resending) from both going in.
	mu sync.Mutex
}

func Open(dir string, eng *engine.Engine, hub *tags.Hub, log *slog.Logger) (*Service, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "checks.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS forms (company TEXT NOT NULL, id TEXT NOT NULL, json TEXT NOT NULL,
			deleted INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (company, id));
		CREATE TABLE IF NOT EXISTS submissions (company TEXT NOT NULL, id TEXT NOT NULL, form TEXT NOT NULL, plant TEXT NOT NULL,
			asset TEXT NOT NULL, taken INTEGER NOT NULL, received INTEGER NOT NULL, who TEXT NOT NULL, bad INTEGER NOT NULL, json TEXT NOT NULL,
			PRIMARY KEY (company, id));
		CREATE INDEX IF NOT EXISTS submissions_round ON submissions (company, form, plant, asset, taken);
		CREATE INDEX IF NOT EXISTS submissions_taken ON submissions (company, taken);
		CREATE TABLE IF NOT EXISTS photos (company TEXT NOT NULL, id TEXT NOT NULL, mime TEXT NOT NULL, data BLOB NOT NULL,
			PRIMARY KEY (company, id))`); err != nil {
		db.Close()
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{Eng: eng, Hub: hub, Log: log, Now: time.Now, Loc: shanghai, db: db}, nil
}

func (s *Service) Close() error { return s.db.Close() }

func newID(prefix string) string {
	b := make([]byte, 5)
	rand.Read(b)
	return prefix + strings.ToUpper(hex.EncodeToString(b))
}

// Forms are the company's forms, by name.
func (s *Service) Forms(company string) ([]Form, error) {
	rows, err := s.db.Query(`SELECT json FROM forms WHERE company=? AND deleted=0`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Form{}
	for rows.Next() {
		var js string
		var f Form
		if rows.Scan(&js) == nil && json.Unmarshal([]byte(js), &f) == nil {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b Form) int { return strings.Compare(a.Name, b.Name) })
	return out, rows.Err()
}

// Form is one form; deleted ones too (their submissions still name them).
func (s *Service) Form(company, id string) (*Form, error) {
	var js string
	if err := s.db.QueryRow(`SELECT json FROM forms WHERE company=? AND id=?`, company, id).Scan(&js); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var f Form
	return &f, json.Unmarshal([]byte(js), &f)
}

// SaveForm checks f and keeps it: a new form when it has no id, else it replaces the one it names.
func (s *Service) SaveForm(company string, f Form, by string) (*Form, error) {
	f.Name = strings.TrimSpace(f.Name)
	f.Note = strings.TrimSpace(f.Note)
	if p := s.CheckForm(company, &f); len(p) > 0 {
		return nil, &Problems{List: p}
	}
	now := s.Now()
	if f.ID == "" {
		f.ID, f.Created = newID("F-"), now
	} else if old, err := s.Form(company, f.ID); err == nil {
		f.Created = old.Created
	} else if errors.Is(err, ErrNotFound) {
		f.Created = now
	} else {
		return nil, err
	}
	f.Updated, f.By = now, by
	b, _ := json.Marshal(f)
	if _, err := s.db.Exec(`INSERT INTO forms VALUES (?,?,?,0) ON CONFLICT (company, id) DO UPDATE SET json=excluded.json, deleted=0`, company, f.ID, string(b)); err != nil {
		return nil, err
	}
	return &f, nil
}

// DeleteForm takes a form out of use; what was filled in on it is kept.
func (s *Service) DeleteForm(company, id string) (*Form, error) {
	f, err := s.Form(company, id)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`UPDATE forms SET deleted=1 WHERE company=? AND id=?`, company, id)
	return f, err
}

var (
	keyRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,39}$`)
	idRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
)

// TwinFields are the machine's own fields a number on a form may stand for.
var TwinFields = map[string]string{"measure": "质量检测值", "temp": "温度", "power": "功率", "vib": "振动"}

// reserved are the fields equipment reports (protocol/devices.md): a form's own number would be
// mixed into the machine's readings of them.
var reserved = []string{"state", "fault", "faultCode", "running", "temp", "power", "vib", "speed", "health", "measure", "lastMeasure",
	"mcount", "toolUsed", "toolLife", "stock", "count", "cycles", "battery", "x", "z", "heading", "task", "inv", "cap"}

// CheckForm says what is wrong with f, filling in what it leaves to defaults.
func (s *Service) CheckForm(company string, f *Form) []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	if f.ID != "" && !idRe.MatchString(f.ID) {
		add("编号只能用字母、数字、- 和 _")
	}
	if f.Name == "" || utf8.RuneCountInString(f.Name) > 40 {
		add("名称要 1～40 字")
	}
	if f.Kind == "" {
		f.Kind = "other"
	}
	if _, ok := Kinds[f.Kind]; !ok {
		add("类型要是 patrol（点检）、first（首件检验）、lab（化验）或 other")
	}
	if utf8.RuneCountInString(f.Note) > 500 {
		add("说明最多 500 字")
	}
	// where it applies: the plant's own lines and machines
	if f.Plant == "" && (len(f.Lines) > 0 || len(f.Assets) > 0) {
		add("选了产线或设备，要先选厂")
	}
	if f.Plant != "" && s.Eng != nil {
		var plant *engine.PlantStations
		for _, ps := range s.Eng.Stations(company) {
			if ps.ID == f.Plant {
				plant = &ps
			}
		}
		if plant == nil {
			add("没有厂 %s", f.Plant)
		} else {
			for _, l := range f.Lines {
				if !slices.ContainsFunc(plant.Lines, func(x engine.LineStations) bool { return x.ID == l }) {
					add("%s 没有产线 %s", plant.Name, l)
				}
			}
			for _, a := range f.Assets {
				if !slices.ContainsFunc(plant.Lines, func(x engine.LineStations) bool {
					return slices.ContainsFunc(x.Stations, func(st engine.Station) bool { return st.ID == a })
				}) {
					add("%s 的产线上没有设备 %s", plant.Name, a)
				}
			}
		}
	}
	f.Lines, f.Assets = nonNil(f.Lines), nonNil(f.Assets)
	// what it asks
	if len(f.Fields) == 0 {
		add("至少要有一项")
	}
	if len(f.Fields) > 40 {
		add("最多 40 项")
	}
	keys := map[string]bool{}
	twins := map[string]bool{}
	for i := range f.Fields {
		fd := &f.Fields[i]
		fd.Label, fd.Key, fd.Unit = strings.TrimSpace(fd.Label), strings.TrimSpace(fd.Key), strings.TrimSpace(fd.Unit)
		name := fd.Label
		if name == "" {
			name = fmt.Sprintf("第 %d 项", i+1)
			add("第 %d 项没有名称", i+1)
		}
		if !keyRe.MatchString(fd.Key) {
			add("%s：标识要以字母开头，只用字母、数字和 _，最多 40 个", name)
		} else if keys[fd.Key] {
			add("%s：标识 %s 和前面的重复了", name, fd.Key)
		}
		keys[fd.Key] = true
		switch fd.Kind {
		case Number:
			if fd.Lo != nil && fd.Hi != nil && *fd.Lo > *fd.Hi {
				add("%s：下限比上限大", name)
			}
			for _, v := range []*float64{fd.Lo, fd.Hi, fd.Target} {
				if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
					add("%s：标准要是数", name)
				}
			}
			if fd.Twin != "" {
				if _, ok := TwinFields[fd.Twin]; !ok {
					add("%s：对应的设备字段只能是 measure、temp、power 或 vib", name)
				} else if twins[fd.Twin] {
					add("%s：设备字段 %s 已经有一项对应了", name, fd.Twin)
				}
				twins[fd.Twin] = true
			} else if slices.Contains(reserved, fd.Key) {
				add("%s：标识 %s 是设备上报用的字段名，换一个；要让孪生用它，设「对应设备字段」", name, fd.Key)
			}
			fd.Options, fd.Bad = nil, nil
		case Choice, YesNo:
			if fd.Kind == YesNo {
				fd.Options = []string{"是", "否"}
			}
			opts := []string{}
			for _, o := range fd.Options {
				if o = strings.TrimSpace(o); o != "" && !slices.Contains(opts, o) {
					opts = append(opts, o)
				}
			}
			fd.Options = opts
			if len(opts) < 2 || len(opts) > 12 {
				add("%s：选项要 2～12 个", name)
			}
			if fd.Bad == nil && fd.Kind == Choice {
				// 异常, 不合格 and the like are the abnormal answers unless the form says otherwise
				for _, o := range opts {
					if strings.Contains(o, "异常") || strings.Contains(o, "不合格") || o == "NG" {
						fd.Bad = append(fd.Bad, o)
					}
				}
			}
			for _, b := range fd.Bad {
				if !slices.Contains(opts, b) {
					add("%s：异常的答案「%s」不在选项里", name, b)
				}
			}
			fd.Unit, fd.Lo, fd.Hi, fd.Target, fd.Twin = "", nil, nil, nil, ""
		case Text, Photo:
			fd.Unit, fd.Lo, fd.Hi, fd.Target, fd.Twin, fd.Options, fd.Bad = "", nil, nil, nil, "", nil, nil
		default:
			add("%s：类型要是 number、choice、yesno、text 或 photo", name)
		}
		if len(fd.Bad) == 0 {
			fd.Bad = nil
		}
	}
	// when it is due
	sc := &f.Schedule
	switch sc.Every {
	case "":
		sc.Hours, sc.At, sc.Within = 0, nil, 0
	case "shift", "day", "hours":
		if sc.Every == "hours" && (sc.Hours < 1 || sc.Hours > 24) {
			add("每几小时一次：1～24 小时")
		}
		if sc.Every != "hours" {
			sc.Hours = 0
		}
		for _, t := range sc.At {
			if !timeRe.MatchString(t) {
				add("时间 %q 要写成 08:00 这样", t)
			}
		}
		if sc.Every != "shift" && len(sc.At) > 1 {
			add("每天、每几小时只要一个开始时间")
		}
		if len(sc.At) > 6 {
			add("一天最多 6 个班")
		}
		at := slices.Clone(sc.At)
		slices.Sort(at)
		if len(slices.Compact(at)) != len(sc.At) {
			add("班次的开始时间重复了")
		}
		if sc.Within < 0 || sc.Within > 7*24*60 {
			add("要在几分钟内做完：0～10080")
		}
	default:
		add("周期要是不定期、每班（shift）、每天（day）或每几小时（hours）")
	}
	return p
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// applies reports whether f is for the station a of line l in plant.
func (f *Form) applies(plant, line, asset string) bool {
	if f.Plant != "" && f.Plant != plant {
		return false
	}
	if len(f.Lines) == 0 && len(f.Assets) == 0 {
		return true
	}
	return slices.Contains(f.Lines, line) || slices.Contains(f.Assets, asset)
}
