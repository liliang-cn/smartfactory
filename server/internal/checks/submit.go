package checks

import (
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"smartfactory/internal/tags"
)

// Input is a form filled in, as the phone sends it.
type Input struct {
	// ID is the phone's own name for it, so a submission sent twice (the answer lost on a bad
	// network) is kept once.
	ID    string `json:"id,omitempty"`
	Form  string `json:"form"`
	Plant string `json:"plant"`
	Asset string `json:"asset"`
	// Taken is when the values were taken; a phone that was offline sends them later.
	Taken *time.Time `json:"taken,omitempty"`
	// Values by field key: a number, an answer (choice, 是/否), a text, or a photo as a data URL.
	Values map[string]any `json:"values"`
	Note   string         `json:"note,omitempty"`
}

// Value is one answer as it was kept and judged.
type Value struct {
	Key   string   `json:"key"`
	Label string   `json:"label"`
	Kind  string   `json:"kind"`
	Num   *float64 `json:"num,omitempty"`
	// Text is an answer, a note, or a photo's id.
	Text string `json:"text,omitempty"`
	Unit string `json:"unit,omitempty"`
	// Lo and Hi are the spec the number was judged by.
	Lo *float64 `json:"lo,omitempty"`
	Hi *float64 `json:"hi,omitempty"`
	// Bad: out of spec, or an abnormal answer; Why says how.
	Bad bool   `json:"bad,omitempty"`
	Why string `json:"why,omitempty"`
	// Field is the series field a number was stored under; Flag why the store keeps it but does
	// not believe it (out of the plausible range: a slip of the finger, most likely).
	Field string `json:"field,omitempty"`
	Flag  string `json:"flag,omitempty"`
}

// Submission is a form filled in on one machine.
type Submission struct {
	ID        string    `json:"id"`
	Form      string    `json:"form"`
	FormName  string    `json:"formName"`
	Kind      string    `json:"kind"`
	Plant     string    `json:"plant"`
	PlantName string    `json:"plantName"`
	Asset     string    `json:"asset"`
	AssetName string    `json:"assetName"`
	Taken     time.Time `json:"taken"`
	Received  time.Time `json:"received"`
	// By is who filled it in, Via where it came from (page, cli …).
	By     string  `json:"by"`
	Via    string  `json:"via"`
	Values []Value `json:"values"`
	Note   string  `json:"note,omitempty"`
	// Bad is how many values were out of spec or abnormal.
	Bad int `json:"bad"`
	// Late: sent well after it was taken (from a phone that was offline); its readings are
	// stored with their time but do not move the twin, which is past them.
	Late bool `json:"late,omitempty"`
	// Duplicate: this was sent before; nothing was done again.
	Duplicate bool `json:"duplicate,omitempty"`
}

// Author is who fills a form in.
type Author struct{ Name, Via string }

// MaxAge is how old a submission may be when it arrives.
const MaxAge = 30 * 24 * time.Hour

// maxPhoto is the largest photo kept (the phone shrinks them well below).
const maxPhoto = 2 << 20

// Submit checks a filled-in form and keeps it: its numbers go to the readings (source manual, at
// the time they were taken), and an abnormal value raises a quality alarm on the machine in the
// twin — or a normal one clears it — when this is the latest time the form was filled in there.
func (s *Service) Submit(company string, in Input, by Author) (*Submission, error) {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	if in.ID == "" {
		in.ID = newID("CK-")
	} else if !idRe.MatchString(in.ID) {
		return nil, &Problems{List: []string{"编号只能用字母、数字、- 和 _，最多 64 个"}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, err := s.Submission(company, in.ID); err == nil {
		old.Duplicate = true
		return old, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	f, err := s.Form(company, in.Form)
	if errors.Is(err, ErrNotFound) {
		return nil, &Problems{List: []string{"没有表单 " + in.Form}}
	} else if err != nil {
		return nil, err
	}
	// the machine, as the twin has it now
	var m *Machine
	for _, ps := range s.Eng.Stations(company) {
		for _, l := range ps.Lines {
			for _, st := range l.Stations {
				if ps.ID == in.Plant && st.ID == in.Asset {
					m = &Machine{Plant: ps.ID, PlantName: ps.Name, Line: l.ID, LineName: l.Name, Asset: st.ID, Name: st.Name, Measure: st.Measure}
				}
			}
		}
	}
	if m == nil {
		return nil, &Problems{List: []string{fmt.Sprintf("没有设备 %s/%s", in.Plant, in.Asset)}}
	}
	if !f.applies(m.Plant, m.Line, m.Asset) {
		add("「%s」不用于 %s", f.Name, m.Name)
	}
	now := s.Now()
	taken := now
	if in.Taken != nil && !in.Taken.IsZero() {
		taken = *in.Taken
	}
	if taken.After(now) {
		// a phone whose clock runs ahead: it was taken just now
		taken = now
	}
	if now.Sub(taken) > MaxAge {
		add("记录时间 %s 太早了，只收 30 天以内的", taken.In(s.Loc).Format("2006-01-02 15:04"))
	}
	in.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(in.Note) > 500 {
		add("备注最多 500 字")
	}

	sub := &Submission{ID: in.ID, Form: f.ID, FormName: f.Name, Kind: f.Kind, Plant: m.Plant, PlantName: m.PlantName, Asset: m.Asset, AssetName: m.Name,
		Taken: taken.Truncate(time.Millisecond), Received: now.Truncate(time.Millisecond), By: by.Name, Via: by.Via, Note: in.Note, Values: []Value{}}
	type photo struct {
		id, mime string
		data     []byte
	}
	var photos []photo
	for _, fd := range f.Fields {
		raw, given := in.Values[fd.Key]
		if t, ok := raw.(string); ok && strings.TrimSpace(t) == "" || raw == nil {
			given = false
		}
		if !given {
			if fd.Required {
				add("%s 要填", fd.Label)
			}
			continue
		}
		v := Value{Key: fd.Key, Label: fd.Label, Kind: fd.Kind, Unit: fd.Unit}
		switch fd.Kind {
		case Number:
			x, ok := number(raw)
			if !ok {
				add("%s 要填数字", fd.Label)
				continue
			}
			v.Num = &x
			v.Lo, v.Hi = fd.Lo, fd.Hi
			if fd.Twin == "measure" && v.Lo == nil && v.Hi == nil && m.Measure != nil {
				// the machine's own quality spec
				v.Lo, v.Hi = m.Measure.Lo, m.Measure.Hi
				if v.Unit == "" {
					v.Unit = m.Measure.Unit
				}
			}
			switch {
			case v.Hi != nil && x > *v.Hi:
				v.Bad, v.Why = true, "超上限 "+num(*v.Hi)
			case v.Lo != nil && x < *v.Lo:
				v.Bad, v.Why = true, "低于下限 "+num(*v.Lo)
			}
			v.Field = fd.Key
			if fd.Twin != "" {
				v.Field = fd.Twin
			}
		case Choice, YesNo:
			a, _ := raw.(string)
			if b, ok := raw.(bool); ok && fd.Kind == YesNo {
				a = map[bool]string{true: "是", false: "否"}[b]
			}
			a = strings.TrimSpace(a)
			if !slices.Contains(fd.Options, a) {
				add("%s 要从 %s 里选", fd.Label, strings.Join(fd.Options, "、"))
				continue
			}
			v.Text = a
			if slices.Contains(fd.Bad, a) {
				v.Bad, v.Why = true, a
			}
		case Text:
			t, _ := raw.(string)
			if t = strings.TrimSpace(t); utf8.RuneCountInString(t) > 500 {
				add("%s 最多 500 字", fd.Label)
				continue
			}
			v.Text = t
		case Photo:
			mime, data, ok := dataURL(raw)
			if !ok {
				add("%s 要是 JPEG、PNG 或 WebP 照片", fd.Label)
				continue
			}
			if len(data) > maxPhoto {
				add("%s 照片太大（最多 2 MB）", fd.Label)
				continue
			}
			ph := photo{newID("P-"), mime, data}
			photos = append(photos, ph)
			v.Text = ph.id
		}
		if v.Bad {
			sub.Bad++
		}
		sub.Values = append(sub.Values, v)
	}
	if len(sub.Values) == 0 && len(p) == 0 {
		add("什么都没填")
	}
	if len(p) > 0 {
		return nil, &Problems{List: p}
	}

	// the numbers, checked and stored like an instrument's readings, at the time they were taken
	sub.Late = now.Sub(taken) >= tags.Late
	if s.Hub != nil {
		for i := range sub.Values {
			v := &sub.Values[i]
			if v.Num == nil {
				continue
			}
			q, why := s.Hub.Put(tags.Tag{Company: company, Plant: m.Plant, Asset: m.Asset, Field: v.Field, Value: *v.Num, Source: Source, At: sub.Taken})
			if q&tags.Doubtful != 0 {
				v.Flag = why
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, ph := range photos {
		if _, err := tx.Exec(`INSERT INTO photos VALUES (?,?,?,?)`, company, ph.id, ph.mime, ph.data); err != nil {
			return nil, err
		}
	}
	b, _ := json.Marshal(sub)
	if _, err := tx.Exec(`INSERT INTO submissions VALUES (?,?,?,?,?,?,?,?,?,?)`, company, sub.ID, sub.Form, sub.Plant, sub.Asset,
		sub.Taken.UnixMilli(), sub.Received.UnixMilli(), sub.By, sub.Bad, string(b)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.Changed != nil {
		s.Changed(company)
	}

	// the machine's quality alarms follow the latest time this form was filled in on it
	var newer int
	s.db.QueryRow(`SELECT count(*) FROM submissions WHERE company=? AND form=? AND plant=? AND asset=? AND taken>?`,
		company, sub.Form, sub.Plant, sub.Asset, sub.Taken.UnixMilli()).Scan(&newer)
	if newer == 0 {
		for _, v := range sub.Values {
			if v.Kind == Text || v.Kind == Photo {
				continue
			}
			text := ""
			if v.Bad && v.Flag == "" {
				text = alarmText(f, v, by.Name, sub.Taken.In(s.Loc))
			}
			s.Eng.Alert(company, sub.Plant, sub.Asset, "check:"+f.ID+":"+v.Key, text)
		}
	}
	return sub, nil
}

// alarmText is how a value out of spec reads in the twin's alarms: 点检 油位：异常（张工 08:15）.
func alarmText(f *Form, v Value, by string, at time.Time) string {
	what := v.Text
	if v.Num != nil {
		what = strings.TrimSpace(num(*v.Num)+" "+v.Unit) + "，" + v.Why
	}
	return fmt.Sprintf("%s %s：%s（%s %s 记）", f.Name, v.Label, what, by, at.Format("01-02 15:04"))
}

func num(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

func number(v any) (float64, bool) {
	var x float64
	switch n := v.(type) {
	case float64:
		x = n
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		x = f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(n, "，", ".")), 64)
		if err != nil {
			return 0, false
		}
		x = f
	default:
		return 0, false
	}
	return x, !math.IsNaN(x) && !math.IsInf(x, 0)
}

// dataURL reads a photo sent as data:image/…;base64,…
func dataURL(v any) (mime string, data []byte, ok bool) {
	s, _ := v.(string)
	head, body, found := strings.Cut(s, ",")
	if !found || !strings.HasPrefix(head, "data:") || !strings.HasSuffix(head, ";base64") {
		return "", nil, false
	}
	mime = strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
		return "", nil, false
	}
	data, err := base64.StdEncoding.DecodeString(body)
	return mime, data, err == nil && len(data) > 0
}

func scanSubmission(sc interface{ Scan(...any) error }) (*Submission, error) {
	var js string
	if err := sc.Scan(&js); err != nil {
		return nil, err
	}
	var sub Submission
	return &sub, json.Unmarshal([]byte(js), &sub)
}

// Submission is one filled-in form.
func (s *Service) Submission(company, id string) (*Submission, error) {
	sub, err := scanSubmission(s.db.QueryRow(`SELECT json FROM submissions WHERE company=? AND id=?`, company, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sub, err
}

// Filter picks submissions; zero fields match everything.
type Filter struct {
	Form, Plant, Asset string
	// From and To bound when they were taken.
	From, To time.Time
	// Bad: only those with an abnormal value.
	Bad   bool
	Limit int
}

// Submissions are the company's filled-in forms the filter picks, the latest taken first.
func (s *Service) Submissions(company string, f Filter) ([]Submission, error) {
	q := `SELECT json FROM submissions WHERE company=?`
	args := []any{company}
	for _, c := range []struct{ col, v string }{{"form", f.Form}, {"plant", f.Plant}, {"asset", f.Asset}} {
		if c.v != "" {
			q += " AND " + c.col + "=?"
			args = append(args, c.v)
		}
	}
	if !f.From.IsZero() {
		q += " AND taken>=?"
		args = append(args, f.From.UnixMilli())
	}
	if !f.To.IsZero() {
		q += " AND taken<?"
		args = append(args, f.To.UnixMilli())
	}
	if f.Bad {
		q += " AND bad>0"
	}
	if f.Limit <= 0 {
		f.Limit = 200
	}
	q += " ORDER BY taken DESC LIMIT ?"
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Submission{}
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// Latest is the last time each form was filled in on a machine, the latest first.
func (s *Service) Latest(company, plant, asset string) ([]Submission, error) {
	rows, err := s.db.Query(`SELECT s.json FROM submissions s JOIN (SELECT form, max(taken) t FROM submissions WHERE company=? AND plant=? AND asset=? GROUP BY form) l
		ON s.form=l.form AND s.taken=l.t WHERE s.company=? AND s.plant=? AND s.asset=? ORDER BY s.taken DESC`, company, plant, asset, company, plant, asset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Submission{}
	seen := map[string]bool{}
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		if !seen[sub.Form] {
			seen[sub.Form] = true
			out = append(out, *sub)
		}
	}
	return out, rows.Err()
}

// Photo is a photo taken with a submission.
func (s *Service) Photo(company, id string) (mime string, data []byte, err error) {
	err = s.db.QueryRow(`SELECT mime, data FROM photos WHERE company=? AND id=?`, company, id).Scan(&mime, &data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// WriteCSV writes submissions one value a row, for a spreadsheet: with a byte-order mark, so
// Excel reads the Chinese right.
func (s *Service) WriteCSV(w io.Writer, list []Submission) error {
	io.WriteString(w, "\xef\xbb\xbf")
	c := csv.NewWriter(w)
	c.Write([]string{"记录编号", "记录时间", "收到时间", "厂", "设备编号", "设备", "表单", "记录人", "项目", "值", "单位", "下限", "上限", "结果", "说明", "备注"})
	at := func(t time.Time) string { return t.In(s.Loc).Format("2006-01-02 15:04:05") }
	opt := func(x *float64) string {
		if x == nil {
			return ""
		}
		return num(*x)
	}
	for _, sub := range list {
		for _, v := range sub.Values {
			val := v.Text
			if v.Num != nil {
				val = num(*v.Num)
			}
			result := "正常"
			switch {
			case v.Kind == Text || v.Kind == Photo:
				result = ""
			case v.Bad:
				result = "异常"
			}
			why := v.Why
			if v.Flag != "" {
				why = strings.TrimPrefix(why+"；读数不可信："+v.Flag, "；")
			}
			c.Write([]string{sub.ID, at(sub.Taken), at(sub.Received), sub.PlantName, sub.Asset, sub.AssetName, sub.FormName, sub.By,
				v.Label, val, v.Unit, opt(v.Lo), opt(v.Hi), result, why, sub.Note})
		}
	}
	c.Flush()
	return c.Error()
}
