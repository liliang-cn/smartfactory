package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"smartfactory/internal/engine"
	"smartfactory/internal/series"
)

// trendTool answers for what the machines did over a span from the readings the equipment sent
// (series): one machine's readings and states, or every machine ranked by how long it stood
// still. A machine only simulated has no history: the tool says so rather than guessing.
func trendTool(rd *series.Store, eng *engine.Engine, company string) tool {
	return tool{name: "machine_trend",
		desc: "查设备过去一段时间的历史数据（来自接入的设备读数，不是仿真）。给 asset：这台设备这段时间每个读数的最低、最高（和出现的时间）、平均、一条粗曲线，各状态各多少分钟，最长的几次停机（故障、停机、保养、换型）各从几点到几点、多久、什么原因，按原因汇总。不给 asset：全公司（或 plant 这个厂）的设备按停机时间从长到短排，问“昨晚哪台停得最久”“这周哪台故障最多”用它。" +
			"时间用 hours（最近几小时，默认 8）或 from/to（如 2026-11-13 20:00，北京时间）。“现在怎么样”用 twin_machines，过去的趋势、停机、读数变化用这个。",
		params: obj(map[string]any{
			"asset":  prop("string", "设备编号，如 HP-101（可省：所有设备按停机时间排名）"),
			"plant":  prop("string", "厂的编号或名称（可省）"),
			"fields": strList("要看的读数：temp 机身温度、power 功率、vib 振动、speed 速度、health 健康度、toolLife 模具/刀具寿命（可省：常用的几项）"),
			"hours":  prop("number", "最近几小时（默认 8，最多 2160）"),
			"from":   prop("string", "开始时间，如 2026-11-13 20:00（北京时间，可省）"),
			"to":     prop("string", "结束时间（可省：现在）"),
		}),
		run: func(_ context.Context, a args) (any, error) {
			from, to, err := trendSpan(a)
			if err != nil {
				return nil, err
			}
			names := twinNames(eng, company)
			plant := ""
			if p := a.str("plant"); p != "" {
				if plant = names.plantID(p); plant == "" {
					return nil, fmt.Errorf("没有叫“%s”的厂", p)
				}
			}
			kept, err := rd.Machines(company)
			if err != nil {
				return nil, err
			}
			if len(kept) == 0 {
				return nil, errors.New("这家公司没有接入设备数据，孪生里只有仿真；仿真不留历史读数，只能用 twin_machines 看现在的状态")
			}
			span := map[string]any{"from": from.Format("2006-01-02 15:04"), "to": to.Format("2006-01-02 15:04")}
			asset := a.str("asset")
			if asset == "" {
				rank, err := rd.Downtime(company, plant, from, to, shanghai)
				if err != nil {
					return nil, err
				}
				out := make([]map[string]any, 0, min(len(rank), 15))
				for _, r := range rank[:min(len(rank), 15)] {
					m := map[string]any{"plant": names.plant(r.Plant), "asset": r.Asset, "name": names.asset(r.Plant, r.Asset),
						"stop_minutes": r.StopMinutes, "stop_count": r.StopCount, "run_share": r.RunShare}
					by := map[string]float64{}
					for k, v := range r.ByState {
						by[cnState(k)] = v
					}
					if len(by) > 0 {
						m["by_state"] = by
					}
					if r.Longest != nil {
						m["longest"] = stopText(*r.Longest)
					}
					if len(r.Reasons) > 0 {
						m["reasons"] = r.Reasons
					}
					out = append(out, m)
				}
				return map[string]any{"span": span, "machines": out, "total": len(rank),
					"note": "停机只算故障、停机、保养、换型；待机、堵料是在等上下游，不算。run_share 是运行时间占比。"}, nil
			}
			p, id, err := names.find(kept, plant, asset)
			if err != nil {
				return nil, err
			}
			fields := a.list("fields")
			if len(fields) == 0 {
				have, err := rd.Fields(company, p, id)
				if err != nil {
					return nil, err
				}
				for _, f := range []string{"temp", "power", "vib", "speed", "health", "toolLife", "measure"} {
					if slices.Contains(have, f) {
						fields = append(fields, f)
					}
				}
			}
			tr, err := rd.Trend(series.TrendQuery{Company: company, Plant: p, Asset: id, Fields: fields, From: from, To: to, Loc: shanghai})
			if err != nil {
				return nil, err
			}
			out := map[string]any{"plant": names.plant(p), "asset": id, "name": names.asset(p, id), "span": span, "fields": tr.Fields}
			if tr.States != nil {
				totals := make([]map[string]any, 0, len(tr.States.Totals))
				for _, x := range tr.States.Totals {
					totals = append(totals, map[string]any{"state": cnState(x.State), "minutes": x.Minutes, "share": x.Share})
				}
				stops := make([]string, 0, len(tr.States.Stops))
				for _, s := range tr.States.Stops {
					stops = append(stops, stopText(s))
				}
				out["states"] = map[string]any{"totals": totals, "longest_stops": stops, "stop_count": tr.States.StopCount,
					"stop_minutes": tr.States.StopMinutes, "reasons": tr.States.Reasons, "changes": tr.States.Changes}
			}
			if tr.Note != "" {
				out["note"] = tr.Note
			}
			out["legend"] = "fields：min/max 是最低/最高和出现的那一分钟，curve 每点是 [时间, 平均, 最低, 最高]；temp 是设备机身温度，不是工件温度；flagged 是不可信、已剔除的读数个数。"
			return out, nil
		}}
}

// trendSpan reads hours or from/to (Beijing time).
func trendSpan(a args) (time.Time, time.Time, error) {
	to := now()
	if v := a.str("to"); v != "" {
		t, err := parseLocal(v)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = t
	}
	if v := a.str("from"); v != "" {
		t, err := parseLocal(v)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return t, to, nil
	}
	h := 8.0
	if v, ok := a["hours"].(float64); ok && v > 0 {
		h = v
	} else if n := a.num("hours", 0); n > 0 {
		h = float64(n)
	}
	h = min(h, series.MaxTrendSpan.Hours())
	return to.Add(-time.Duration(h * float64(time.Hour))), to, nil
}

func parseLocal(v string) (time.Time, error) {
	v = strings.TrimSpace(strings.ReplaceAll(v, "T", " "))
	if t, err := time.Parse(time.RFC3339, strings.Replace(v, " ", "T", 1)); err == nil {
		return t.In(shanghai), nil
	}
	for _, f := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(f, v, shanghai); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("时间“%s”看不懂，写成 2026-11-13 20:00", v)
}

func cnState(s string) string {
	if t, ok := stateText[s]; ok {
		return t
	}
	return s
}

func stopText(s series.Stop) string {
	end := s.To.Format("15:04")
	if s.To.Format("01-02") != s.From.Format("01-02") {
		end = s.To.Format("01-02 15:04")
	}
	txt := fmt.Sprintf("%s %s～%s %s %.1f 分钟", s.From.Format("01-02"), s.From.Format("15:04"), end, cnState(s.State), s.Minutes)
	if s.Reason != "" {
		txt += "：" + s.Reason
	}
	if s.Open {
		txt += "（到现在还没恢复）"
	}
	return txt
}

// names are the twin's names for a company's plants and machines.
type names struct {
	plants map[string]string            // id → name
	assets map[string]map[string]string // plant → asset → name
}

func twinNames(eng *engine.Engine, company string) names {
	n := names{plants: map[string]string{}, assets: map[string]map[string]string{}}
	if eng == nil {
		return n
	}
	var snap snapshot
	if json.Unmarshal([]byte(eng.Snapshot(company)), &snap) != nil {
		return n
	}
	for _, p := range snap.Plants {
		n.plants[p.ID] = p.Name
		n.assets[p.ID] = map[string]string{}
		for _, a := range p.Assets {
			id, _ := a["id"].(string)
			name, _ := a["name"].(string)
			n.assets[p.ID][id] = name
		}
	}
	return n
}

func (n names) plant(id string) string {
	if v := n.plants[id]; v != "" {
		return v
	}
	return id
}

func (n names) asset(plant, id string) string { return n.assets[plant][id] }

func (n names) plantID(q string) string {
	for id, name := range n.plants {
		if strings.EqualFold(id, q) || name == q {
			return id
		}
	}
	for id, name := range n.plants {
		if strings.Contains(name, q) {
			return id
		}
	}
	return ""
}

// squash keeps letters and digits, lower case: hp101 finds HP-101.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// find is the one machine with stored readings that q names (its number, or its name when only
// one has it), in plant when given.
func (n names) find(kept [][2]string, plant, q string) (string, string, error) {
	var hits [][2]string
	for _, m := range kept {
		if (plant == "" || m[0] == plant) && squash(m[1]) == squash(q) {
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 {
		for _, m := range kept {
			if (plant == "" || m[0] == plant) && n.asset(m[0], m[1]) != "" && strings.Contains(n.asset(m[0], m[1]), q) {
				hits = append(hits, m)
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0][0], hits[0][1], nil
	case 0:
		for p, as := range n.assets {
			for id := range as {
				if (plant == "" || p == plant) && squash(id) == squash(q) {
					return "", "", fmt.Errorf("%s 在孪生里只有仿真，没有接入设备数据；仿真不留历史读数，现在的状态用 twin_machines 查", id)
				}
			}
		}
		return "", "", fmt.Errorf("没有找到设备“%s”；用设备编号（如 HP-101）", q)
	}
	var list []string
	for _, h := range hits[:min(len(hits), 12)] {
		list = append(list, fmt.Sprintf("%s %s %s", n.plant(h[0]), h[1], n.asset(h[0], h[1])))
	}
	return "", "", fmt.Errorf("“%s”对得上好几台：%s；给出设备编号，必要时再给 plant", q, strings.Join(list, "、"))
}
