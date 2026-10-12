package ops

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"smartfactory/internal/auth"
	"smartfactory/internal/checks"
)

// A company's forms filled in by hand — patrol rounds, first-article checks, lab results: members
// read the forms and what was written, operators fill them in, administrators set them up.

func (o *Ops) checks(who auth.Who, company string, need auth.Role) error {
	if o.Checks == nil {
		return fail(503, "这台服务器不保存人工记录（没有数据目录）")
	}
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return fail(404, "没有公司 %s", company)
	}
	if !who.Can(company, need) {
		if who.Key == "" {
			return fail(401, "用令牌登录")
		}
		return fail(403, "要公司 %s %s以上的令牌", company, need.Label())
	}
	return nil
}

func checkError(err error) error {
	var p *checks.Problems
	switch {
	case errors.As(err, &p):
		return &Error{Code: 422, Msg: p.Error(), Problems: p.List}
	case errors.Is(err, checks.ErrNotFound):
		return &Error{Code: 404, Msg: "没有这一项"}
	}
	return err
}

// ChecksOverview is a company's forms and every machine one is for, with its rounds.
type ChecksOverview struct {
	Forms    []checks.Form    `json:"forms"`
	Machines []checks.Machine `json:"machines"`
	// Kinds name the forms' kinds; TwinFields the machine fields a number may stand for.
	Kinds      map[string]string `json:"kinds"`
	TwinFields map[string]string `json:"twinFields"`
	// Who is the caller; Operator: they may fill forms in; Admin: set them up.
	Who      string    `json:"who"`
	Operator bool      `json:"operator"`
	Admin    bool      `json:"admin"`
	Now      time.Time `json:"now"`
}

func (o *Ops) ChecksOverview(who auth.Who, company string) (*ChecksOverview, error) {
	if err := o.checks(who, company, auth.Member); err != nil {
		return nil, err
	}
	forms, err := o.Checks.Forms(company)
	if err != nil {
		return nil, err
	}
	ms, err := o.Checks.Machines(company)
	if err != nil {
		return nil, err
	}
	return &ChecksOverview{Forms: forms, Machines: ms, Kinds: checks.Kinds, TwinFields: checks.TwinFields,
		Who: who.Name, Operator: who.Can(company, auth.Operator), Admin: who.Can(company, auth.Admin), Now: o.Checks.Now()}, nil
}

// CheckTemplates are forms to start one from.
func (o *Ops) CheckTemplates(who auth.Who, company string) ([]checks.Form, error) {
	if err := o.checks(who, company, auth.Member); err != nil {
		return nil, err
	}
	return checks.Templates(), nil
}

// SaveCheckForm sets a form up, or changes the one f names.
func (o *Ops) SaveCheckForm(who auth.Who, via, company string, f checks.Form) (*checks.Form, error) {
	if err := o.checks(who, company, auth.Admin); err != nil {
		return nil, err
	}
	saved, err := o.Checks.SaveForm(company, f, who.Name)
	target, name := f.ID, f.Name
	if saved != nil {
		target, name = saved.ID, saved.Name
	}
	o.Audit.Record(who, via, company, "check.form", target, name, err)
	return saved, checkError(err)
}

// DeleteCheckForm takes a form out of use; what was filled in on it stays.
func (o *Ops) DeleteCheckForm(who auth.Who, via, company, id string) error {
	if err := o.checks(who, company, auth.Admin); err != nil {
		return err
	}
	f, err := o.Checks.DeleteForm(company, id)
	name := ""
	if f != nil {
		name = f.Name
	}
	o.Audit.Record(who, via, company, "check.form.delete", id, name, err)
	return checkError(err)
}

// CheckRounds are the company's rounds with the statuses (none: all), overdue ones first.
func (o *Ops) CheckRounds(who auth.Who, company string, status []string) ([]checks.Round, error) {
	if err := o.checks(who, company, auth.Member); err != nil {
		return nil, err
	}
	for _, s := range status {
		if !slices.Contains([]string{"overdue", "due", "done", "open"}, s) {
			return nil, fail(400, "status 是 overdue、due、done 或 open")
		}
	}
	return o.Checks.Rounds(company, status...)
}

// MachineChecks is what a machine's page shows: its rounds, the forms for it, and the last time
// each was filled in.
type MachineChecks struct {
	Machine  checks.Machine      `json:"machine"`
	Forms    []checks.Form       `json:"forms"`
	Last     []checks.Submission `json:"last"`
	Operator bool                `json:"operator"`
}

func (o *Ops) MachineChecks(who auth.Who, company, plant, asset string) (*MachineChecks, error) {
	if err := o.checks(who, company, auth.Member); err != nil {
		return nil, err
	}
	m, ok, err := o.Checks.Machine(company, plant, asset)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fail(404, "%s/%s 没有要填的表", plant, asset)
	}
	out := &MachineChecks{Machine: *m, Forms: []checks.Form{}, Operator: who.Can(company, auth.Operator)}
	for _, r := range m.Rounds {
		if f, err := o.Checks.Form(company, r.Form); err == nil {
			out.Forms = append(out.Forms, *f)
		}
	}
	if out.Last, err = o.Checks.Latest(company, plant, asset); err != nil {
		return nil, err
	}
	return out, nil
}

// SubmitCheck keeps a form filled in by who, and logs it with every value.
func (o *Ops) SubmitCheck(who auth.Who, via, company string, in checks.Input) (*checks.Submission, error) {
	if err := o.checks(who, company, auth.Operator); err != nil {
		return nil, err
	}
	sub, err := o.Checks.Submit(company, in, checks.Author{Name: who.Name, Via: via})
	if sub != nil && sub.Duplicate {
		return sub, nil
	}
	target, detail, context := in.Plant+"/"+in.Asset, in.Form, ""
	if sub != nil {
		target = sub.Plant + "/" + sub.Asset + " " + sub.ID
		detail = fmt.Sprintf("%s：%d 项", sub.FormName, len(sub.Values))
		if sub.Bad > 0 {
			detail += fmt.Sprintf("，%d 项异常", sub.Bad)
		}
		context = submissionText(sub, o.Checks.Loc)
	}
	o.Audit.RecordWith(who, via, company, "check.submit", target, detail, context, err)
	return sub, checkError(err)
}

// submissionText is a submission as the activity log keeps it: every value, what was wrong with
// it, and when it was taken when that was not just now.
func submissionText(sub *checks.Submission, loc *time.Location) string {
	var parts []string
	for _, v := range sub.Values {
		val := v.Text
		switch {
		case v.Kind == checks.Photo:
			val = "照片"
		case v.Num != nil:
			val = strings.TrimSpace(strconv.FormatFloat(*v.Num, 'f', -1, 64) + " " + v.Unit)
		}
		if v.Bad && v.Kind == checks.Number {
			val += "（" + v.Why + "）"
		}
		if v.Flag != "" {
			val += "（读数不可信：" + v.Flag + "）"
		}
		parts = append(parts, v.Label+" "+val)
	}
	if sub.Note != "" {
		parts = append(parts, "备注 "+sub.Note)
	}
	if sub.Late {
		parts = append(parts, fmt.Sprintf("%s 记的，%s 才收到（手机当时没网）", sub.Taken.In(loc).Format("01-02 15:04"), sub.Received.In(loc).Format("01-02 15:04")))
	}
	return strings.Join(parts, "；")
}

// CheckSubmissions are what was filled in, the latest first.
func (o *Ops) CheckSubmissions(who auth.Who, company string, f checks.Filter) ([]checks.Submission, error) {
	if err := o.checks(who, company, auth.Member); err != nil {
		return nil, err
	}
	switch {
	case f.Limit <= 0:
		f.Limit = 200
	case f.Limit > 20000:
		f.Limit = 20000
	}
	return o.Checks.Submissions(company, f)
}

// CheckPhoto is a photo taken with a submission.
func (o *Ops) CheckPhoto(who auth.Who, company, id string) (string, []byte, error) {
	if err := o.checks(who, company, auth.Member); err != nil {
		return "", nil, err
	}
	mime, data, err := o.Checks.Photo(company, id)
	return mime, data, checkError(err)
}
