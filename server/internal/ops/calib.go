package ops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"smartfactory/internal/auth"
	"smartfactory/internal/calib"
)

// A company's calibration: members read how its models do against the measurements,
// administrators fit machines from their readings and put the fitted parameters in force (or
// take them back).

func (o *Ops) calib(who auth.Who, company string, need auth.Role) error {
	if o.Calib == nil {
		return fail(503, "这台服务器没有存设备读数，没法校准（没有数据目录）")
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

func calibError(err error) error {
	var m *calib.Missing
	if errors.As(err, &m) {
		return &Error{Code: 404, Msg: m.Error()}
	}
	return err
}

// Calibration is every machine of the company: its parameters (template, fitted, in force),
// how the last fit played back and how its model has done over the last day.
func (o *Ops) Calibration(who auth.Who, company string) ([]calib.Machine, error) {
	if err := o.calib(who, company, auth.Admin); err != nil {
		return nil, err
	}
	return o.Calib.Status(company)
}

// FitCalibration fits the named machines (or every machine with stored readings) from the last
// hours of readings; nothing is put in force.
func (o *Ops) FitCalibration(ctx context.Context, who auth.Who, via, company string, machines []string, hours float64) ([]calib.Machine, error) {
	if err := o.calib(who, company, auth.Admin); err != nil {
		return nil, err
	}
	if hours < 0 || hours > 24*30 {
		return nil, fail(400, "hours 是 1～720 小时")
	}
	ms, err := o.Calib.Fit(ctx, company, machines, hours)
	detail := ""
	if err == nil {
		detail = fmt.Sprintf("%d 台", len(ms))
	}
	o.Audit.Record(who, via, company, "calib.fit", strings.Join(clipList(machines, 5), "、"), detail, err)
	return ms, calibError(err)
}

// ApplyCalibration puts the fitted parameters of the named machines (or of every fitted one) in force.
func (o *Ops) ApplyCalibration(who auth.Who, via, company string, machines []string) ([]calib.Outcome, error) {
	if err := o.calib(who, company, auth.Admin); err != nil {
		return nil, err
	}
	out, err := o.Calib.Apply(company, machines, who.Name)
	o.Audit.Record(who, via, company, "calib.apply", strings.Join(clipList(outcomeNames(out), 5), "、"), outcomeDetail(out), err)
	return out, calibError(err)
}

// RevertCalibration puts the templates' parameters back on the named machines (or on all).
func (o *Ops) RevertCalibration(who auth.Who, via, company string, machines []string) ([]calib.Outcome, error) {
	if err := o.calib(who, company, auth.Admin); err != nil {
		return nil, err
	}
	out, err := o.Calib.Revert(company, machines)
	o.Audit.Record(who, via, company, "calib.revert", strings.Join(clipList(outcomeNames(out), 5), "、"), outcomeDetail(out), err)
	return out, calibError(err)
}

func outcomeNames(out []calib.Outcome) []string {
	var names []string
	for _, x := range out {
		if x.OK {
			names = append(names, x.Machine)
		}
	}
	return names
}

func outcomeDetail(out []calib.Outcome) string {
	ok := len(outcomeNames(out))
	if bad := len(out) - ok; bad > 0 {
		return fmt.Sprintf("%d 台，%d 台没有", ok, bad)
	}
	return fmt.Sprintf("%d 台", ok)
}

// CalibrationResiduals is how far a machine's model was from its measurements in one field, by
// step (one minute at least).
func (o *Ops) CalibrationResiduals(who auth.Who, company, machine, field string, from, to time.Time, step time.Duration) ([]calib.ResidualPoint, error) {
	if err := o.calib(who, company, auth.Member); err != nil {
		return nil, err
	}
	if !slices.Contains([]string{"temp", "power", "vib"}, field) {
		return nil, fail(400, "field 是 temp、power 或 vib")
	}
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	if !from.Before(to) {
		return nil, fail(400, "from 要早于 to")
	}
	ps, err := o.Calib.Residuals(company, machine, field, from, to, step)
	return ps, calibError(err)
}
