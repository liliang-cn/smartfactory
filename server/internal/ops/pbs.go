package ops

import (
	"errors"
	"slices"

	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
)

// PBS is the company's plant breakdown — plants, their areas (车间), lines and machines by tag —
// with how many machines under each are running or stopped by a fault now.
func (o *Ops) PBS(who auth.Who, company string) (*engine.PBSNode, error) {
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return nil, fail(404, "没有公司 %s", company)
	}
	if !who.Can(company, auth.Member) {
		if who.Key == "" {
			return nil, fail(401, "要先登录")
		}
		return nil, fail(403, "要公司 %s %s以上的令牌", company, auth.Member.Label())
	}
	t, err := o.Engine.PBS(company)
	if errors.Is(err, engine.ErrNoCompany) {
		return nil, fail(404, "没有公司 %s", company)
	}
	return t, err
}
