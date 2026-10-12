package agents

import (
	"context"
	"strconv"
	"strings"
	"time"

	"smartfactory/internal/checks"
)

// checksTool finds what people wrote down by hand at the machines: patrol rounds, first-article
// checks, lab results, and which rounds are overdue.
func checksTool(svc *checks.Service, company string) tool {
	return tool{name: "manual_records",
		desc: "查人工记录（点检、首件检验、化验）：某台设备或某张表最近填了什么、哪些超差或异常、谁记的、什么时候；overdue=true 时列出逾期没做的点检。",
		params: obj(map[string]any{
			"asset":   prop("string", "设备编号，如 HP-101（可省）"),
			"form":    prop("string", "表单名称里的字，如 点检、化验（可省）"),
			"days":    prop("integer", "看最近几天，默认 3"),
			"bad":     prop("boolean", "只看有超差或异常的"),
			"overdue": prop("boolean", "列出逾期没做的轮次，不看记录"),
		}),
		run: func(_ context.Context, a args) (any, error) {
			day := func(t time.Time) string { return t.In(shanghai).Format("2006-01-02 15:04") }
			if a["overdue"] == true {
				rs, err := svc.Rounds(company, "overdue")
				out := []map[string]any{}
				for _, r := range rs {
					if a.str("asset") != "" && r.Asset != a.str("asset") {
						continue
					}
					m := map[string]any{"form": r.FormName, "machine": r.Asset + " " + r.AssetName, "line": r.PlantName + " " + r.LineName}
					if r.Since != nil {
						m["overdueSince"] = day(*r.Since)
					}
					if r.Last != nil {
						m["last"] = day(*r.Last) + " " + r.LastBy
					}
					out = append(out, m)
				}
				return map[string]any{"overdue": out}, err
			}
			forms := map[string]bool{}
			if q := a.str("form"); q != "" {
				fs, err := svc.Forms(company)
				if err != nil {
					return nil, err
				}
				for _, f := range fs {
					if strings.Contains(f.Name, q) {
						forms[f.ID] = true
					}
				}
			}
			days := max(1, min(a.num("days", 3), 30))
			list, err := svc.Submissions(company, checks.Filter{Asset: a.str("asset"), Bad: a["bad"] == true, Limit: 200, From: time.Now().AddDate(0, 0, -days)})
			if err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, s := range list {
				if len(forms) > 0 && !forms[s.Form] {
					continue
				}
				var vals []string
				for _, v := range s.Values {
					if v.Kind == checks.Photo {
						continue
					}
					x := v.Text
					if v.Num != nil {
						x = strings.TrimSpace(strconv.FormatFloat(*v.Num, 'f', -1, 64) + " " + v.Unit)
					}
					switch {
					case v.Bad && v.Num != nil:
						x += "（" + v.Why + "）"
					case v.Bad:
						x += "（异常）"
					}
					vals = append(vals, v.Label+" "+x)
				}
				out = append(out, map[string]any{"form": s.FormName, "machine": s.Asset + " " + s.AssetName, "by": s.By, "taken": day(s.Taken), "values": strings.Join(vals, "；"), "bad": s.Bad, "note": s.Note})
				if len(out) == 30 {
					break
				}
			}
			if len(out) == 0 {
				return map[string]any{"records": out, "note": "这段时间没有对得上的人工记录"}, nil
			}
			return map[string]any{"records": out}, nil
		}}
}
