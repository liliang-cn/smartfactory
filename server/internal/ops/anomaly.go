package ops

import (
	"fmt"
	"slices"
	"strings"

	"smartfactory/internal/anomaly"
	"smartfactory/internal/auth"
)

// Hints on a company's measured machines: readings plausible but unusual for the machine. Anyone
// watching the twin sees a machine's (its live readings are as public); members see the company's
// list and what came before; administrators reset a machine's normal (after maintenance it reads
// differently, and should be learned again).

func (o *Ops) hints(who auth.Who, company string, need auth.Role) error {
	if o.Hints == nil {
		return fail(503, "这台服务器没有学设备读数（没有数据目录，或关掉了异常检测）")
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

// AnomalyReport is a company's hints now and of the last days, and every measured machine's
// normals.
type AnomalyReport struct {
	anomaly.Report
	// Admin: the caller may reset a machine's normal.
	Admin bool `json:"admin"`
}

func (o *Ops) Anomalies(who auth.Who, company string) (*AnomalyReport, error) {
	if err := o.hints(who, company, auth.Member); err != nil {
		return nil, err
	}
	r, err := o.Hints.Report(company)
	if err != nil {
		return nil, err
	}
	return &AnomalyReport{Report: *r, Admin: who.Can(company, auth.Admin)}, nil
}

// MachineAnomaly is one measured machine's fields: how far each is from its normal now.
func (o *Ops) MachineAnomaly(who auth.Who, company, plant, asset string) (*anomaly.Machine, error) {
	if err := o.hints(who, company, auth.Visitor); err != nil {
		return nil, err
	}
	m, ok := o.Hints.Machine(company, plant, asset)
	if !ok {
		return nil, fail(404, "%s/%s 还没有收到过设备读数", plant, asset)
	}
	return &m, nil
}

// ResetOutcome is one machine's reset, or why there was none.
type ResetOutcome struct {
	Machine string `json:"machine"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// ResetBaseline forgets what was learned of the named machines ("plant/asset" or the asset), so
// their normal is learned again from now on, and ends their hints.
func (o *Ops) ResetBaseline(who auth.Who, via, company string, machines []string) ([]ResetOutcome, error) {
	if err := o.hints(who, company, auth.Admin); err != nil {
		return nil, err
	}
	if len(machines) == 0 {
		return nil, fail(400, "说要重置哪台设备")
	}
	known := o.Hints.Machines(company)
	out := []ResetOutcome{}
	var done []string
	for _, name := range machines {
		var hit []anomaly.Machine
		for _, m := range known {
			if name == m.Plant+"/"+m.Asset || name == m.Asset {
				hit = append(hit, m)
			}
		}
		if len(hit) == 0 {
			out = append(out, ResetOutcome{Machine: name, Error: "没有学过这台设备的读数"})
			continue
		}
		for _, m := range hit {
			k := m.Plant + "/" + m.Asset
			ok, err := o.Hints.Reset(company, m.Plant, m.Asset)
			if err != nil {
				return out, err
			}
			if ok {
				done = append(done, k)
			}
			out = append(out, ResetOutcome{Machine: k, OK: ok})
		}
	}
	if len(done) > 0 {
		o.Audit.Record(who, via, company, "anomaly.reset", strings.Join(clipList(done, 5), "、"), fmt.Sprintf("%d 台", len(done)), nil)
	}
	return out, nil
}
