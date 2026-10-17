package ops

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"smartfactory/internal/auth"
	"smartfactory/internal/tables"
)

// A restaurant's tables: anyone may see which are taken (the twin shows it to visitors too);
// a reporter token (a camera box, the POS), operators and administrators report them.

func (o *Ops) tablesFor(company string) error {
	if o.Tables == nil {
		return fail(503, "这台服务器不保存桌态（没有数据目录）")
	}
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return fail(404, "没有公司 %s", company)
	}
	return nil
}

func tableError(err error) error {
	switch {
	case errors.Is(err, tables.ErrNoTable):
		return fail(404, "没有这张桌")
	case errors.Is(err, tables.ErrAmbiguous):
		return fail(400, "几个门店都有这张桌，写上门店（plant）")
	case errors.Is(err, tables.ErrUnknownCompany):
		return fail(404, "没有这家公司")
	}
	return err
}

func (o *Ops) mayReport(who auth.Who, company string) error {
	if who.Reports(company) {
		return nil
	}
	if who.Key == "" {
		return fail(401, "上报桌态要令牌：桌态上报令牌，或操作员以上")
	}
	return fail(403, "%s的令牌是%s，上报桌态要这家公司的桌态上报令牌或操作员以上", who.Name, who.Role.Label())
}

// TablesNow is a company's tables, each with who last reported it, and today's figures.
func (o *Ops) TablesNow(who auth.Who, company string) (*tables.Overview, error) {
	if err := o.tablesFor(company); err != nil {
		return nil, err
	}
	if who.Role.Narrow() && !who.Reports(company) {
		return nil, fail(403, "这个令牌只用来上报")
	}
	ov, err := o.Tables.Overview(company)
	return ov, tableError(err)
}

// ReportTables keeps a batch of table states (a camera box, a table sensor, a waiter's phone).
func (o *Ops) ReportTables(who auth.Who, via, company string, in []tables.Report) (*tables.Result, error) {
	if err := o.tablesFor(company); err != nil {
		return nil, err
	}
	if err := o.mayReport(who, company); err != nil {
		return nil, err
	}
	if len(in) == 0 || len(in) > 1000 {
		return nil, fail(400, "一次报 1～1000 条")
	}
	r, err := o.Tables.Report(company, in, who.Name)
	if err != nil {
		return nil, tableError(err)
	}
	if r.Accepted+r.Late > 0 && !who.Role.Narrow() {
		// a person reporting by hand (the waiter page) is on the record; a device's stream is in the store
		o.Audit.Record(who, via, company, "table.report", reportTarget(in), fmt.Sprintf("%d 条，%d 条补报", r.Accepted+r.Late, r.Late), nil)
	}
	return &r, nil
}

func reportTarget(in []tables.Report) string {
	if len(in) == 1 {
		return in[0].Table + " " + in[0].State
	}
	return strconv.Itoa(len(in)) + " 张桌"
}

// ReportSessions keeps a POS's session events (seated, ordered, paid, left).
func (o *Ops) ReportSessions(who auth.Who, via, company string, in []tables.Event) (*tables.Result, error) {
	if err := o.tablesFor(company); err != nil {
		return nil, err
	}
	if err := o.mayReport(who, company); err != nil {
		return nil, err
	}
	if len(in) == 0 || len(in) > 1000 {
		return nil, fail(400, "一次报 1～1000 条")
	}
	r, err := o.Tables.Events(company, in, who.Name)
	if err != nil {
		return nil, tableError(err)
	}
	return &r, nil
}

// TableDay is one table's sessions on a business day (the day of `day`, YYYY-MM-DD; "" today),
// with its reports and session events of that day.
type TableDay struct {
	Day      string           `json:"day"`
	Sessions []tables.Session `json:"sessions"`
	Marks    []tables.Mark    `json:"marks"`
}

func (o *Ops) TableSessions(who auth.Who, company, plant, table, day string) (*TableDay, error) {
	if err := o.tablesFor(company); err != nil {
		return nil, err
	}
	at := time.Time{}
	if day != "" {
		t, err := time.ParseInLocation("2006-01-02", day, shanghai)
		if err != nil {
			return nil, fail(400, "day 的格式是 YYYY-MM-DD")
		}
		at = t.Add(12 * time.Hour)
	}
	if at.IsZero() {
		at = o.Tables.Now()
	}
	list, err := o.Tables.Day(company, plant, table, at)
	if err != nil {
		return nil, err
	}
	from := tables.DayStart(at)
	marks, err := o.Tables.Marks(company, plant, table, from, 100)
	if err != nil {
		return nil, err
	}
	marks = slices.DeleteFunc(marks, func(m tables.Mark) bool { return m.At >= from.AddDate(0, 0, 1).UnixMilli() })
	return &TableDay{Day: from.Format("2006-01-02"), Sessions: list, Marks: marks}, nil
}

// TableNow is one table's state, its session in progress and today's: "A03 坐了多久".
func (o *Ops) TableNow(who auth.Who, company, plant, table string) (*tables.Current, error) {
	if err := o.tablesFor(company); err != nil {
		return nil, err
	}
	c, err := o.Tables.Table(company, plant, table)
	return c, tableError(err)
}

// TableHistory is the company's sessions by hour or day over a range (from/to: YYYY-MM-DD or a time; default the last 7 days).
func (o *Ops) TableHistory(who auth.Who, company, from, to string, byDay bool) ([]tables.Bucket, error) {
	if err := o.tablesFor(company); err != nil {
		return nil, err
	}
	f, t, err := tables.Range(o.Tables.Now(), from, to)
	if err != nil {
		return nil, fail(400, "%s", err.Error())
	}
	return o.Tables.History(company, f, t, byDay)
}
