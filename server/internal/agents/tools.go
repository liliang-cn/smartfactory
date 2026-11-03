package agents

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
)

// caller is the person a run acts for (auth.With on the run's context).
func caller(ctx context.Context) auth.Who { return auth.From(ctx) }

type tool struct {
	name, desc string
	params     map[string]any
	write      bool
	run        func(ctx context.Context, a args) (any, error)
}

type args map[string]any

func (a args) str(k string) string {
	switch v := a[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func (a args) num(k string, def int) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return def
}

func (a args) list(k string) []string {
	var out []string
	switch v := a[k].(type) {
	case []any:
		for _, x := range v {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '，' || r == '、' || r == ' ' }) {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func enum(desc string, values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

// tools are every tool an assistant can be given, for one company.
func tools(st *Store, eng *engine.Engine, company string) map[string]tool {
	ts := []tool{
		{name: "list_docs", desc: "列出公司资料库里的全部文档（编号、标题、篇幅）。",
			params: obj(map[string]any{}),
			run: func(context.Context, args) (any, error) {
				return st.Docs(company)
			}},
		{name: "search_docs", desc: "在公司资料库里按关键词搜索，返回最相关的段落（文档编号、文档名、章节、段号、原文）。关键词用空格分开，越短越好。",
			params: obj(map[string]any{"query": prop("string", "关键词，空格分隔"), "limit": prop("integer", "最多返回几段，默认 6")}, "query"),
			run: func(_ context.Context, a args) (any, error) {
				return st.SearchDocs(company, a.str("query"), max(1, min(a.num("limit", 6), 12)))
			}},
		{name: "read_doc", desc: "按段号连续读一篇文档，用来看搜索命中段落的前后文。",
			params: obj(map[string]any{"doc": prop("string", "文档编号"), "from": prop("integer", "从第几段开始（0 起）"), "count": prop("integer", "读几段，默认 3")}, "doc"),
			run: func(_ context.Context, a args) (any, error) {
				parts, total, err := st.ReadDoc(company, a.str("doc"), a.num("from", 0), max(1, min(a.num("count", 3), 8)))
				if errors.Is(err, ErrNotFound) {
					return nil, fmt.Errorf("没有编号为 %s 的文档；先用 list_docs 或 search_docs", a.str("doc"))
				}
				return map[string]any{"parts": parts, "total": total}, err
			}},
		{name: "search_notes", desc: "按关键词搜索研发笔记（标题、正文、标签、设备编号），返回最相关的几条全文。",
			params: obj(map[string]any{"query": prop("string", "关键词，空格分隔"), "limit": prop("integer", "最多几条，默认 5")}, "query"),
			run: func(_ context.Context, a args) (any, error) {
				return st.SearchNotes(company, a.str("query"), max(1, min(a.num("limit", 5), 10)))
			}},
		{name: "list_notes", desc: "最近更新的研发笔记（标题、标签、设备、日期，不含正文）。",
			params: obj(map[string]any{"limit": prop("integer", "最多几条，默认 10")}),
			run: func(_ context.Context, a args) (any, error) {
				ns, err := st.Notes(company)
				out := []map[string]any{}
				for _, n := range ns[:min(len(ns), max(1, a.num("limit", 10)))] {
					out = append(out, map[string]any{"id": n.ID, "title": n.Title, "tags": n.Tags, "assets": n.Assets, "updated": n.Updated.Format("2006-01-02")})
				}
				return out, err
			}},
		{name: "save_note", desc: "新建一条研发笔记。",
			params: obj(map[string]any{
				"title":  prop("string", "标题，一句话说清是什么试验或问题"),
				"body":   prop("string", "正文，Markdown，按 目的/条件/过程/结果/结论/下一步 分段"),
				"tags":   strList("2～4 个标签"),
				"assets": strList("涉及的设备编号，如 HP-101"),
			}, "title", "body"),
			write: true,
			run: func(ctx context.Context, a args) (any, error) {
				return st.SaveNote(company, Note{Title: a.str("title"), Body: a.str("body"), Tags: a.list("tags"), Assets: a.list("assets"), By: caller(ctx).Name})
			}},
		{name: "update_note", desc: "修改一条已有笔记：给出的字段替换原值；append 追加到正文末尾（带日期小标题）。",
			params: obj(map[string]any{
				"id":     prop("string", "笔记编号"),
				"title":  prop("string", "新标题（可省）"),
				"body":   prop("string", "整段替换的正文（可省）"),
				"append": prop("string", "追加到末尾的内容，小标题用 ###（可省）"),
				"tags":   strList("新标签（可省）"),
				"assets": strList("新设备编号（可省）"),
			}, "id"),
			write: true,
			run: func(ctx context.Context, a args) (any, error) {
				n, err := st.Note(company, a.str("id"))
				if errors.Is(err, ErrNotFound) {
					return nil, fmt.Errorf("没有编号为 %s 的笔记", a.str("id"))
				} else if err != nil {
					return nil, err
				}
				if v := a.str("title"); v != "" {
					n.Title = v
				}
				if v := a.str("body"); v != "" {
					n.Body = v
				}
				if v := a.str("append"); v != "" {
					n.Body = strings.TrimRight(n.Body, "\n") + "\n\n## " + today() + " 追加\n\n" + v
				}
				if v := a.list("tags"); v != nil {
					n.Tags = v
				}
				if v := a.list("assets"); v != nil {
					n.Assets = v
				}
				return st.SaveNote(company, n)
			}},
		{name: "twin_machines", desc: "孪生里设备现在的状态（实时）：按设备编号、名称、产线、车间找，或按状态找（“故障”“停”“待机”“报警”），不填看全部。返回各状态各几台（states），出问题的排在前面：哪个厂、状态（state_text）、故障原因、机身温度（设备本体的温度和它自己的报警线、跳停线，不是工件、炉料或出炉温度）、功率、振动、健康度、稼动率、正在响的报警。问“现在怎么样、哪台停了、哪台最慢”都用它。",
			params: obj(map[string]any{"query": prop("string", "设备编号（HP-101）、名称（热模锻压力机）、产线（曲轴 1 线）、车间（压铸车间）或状态（故障、停）；空字符串看全部")}, "query"),
			run: func(_ context.Context, a args) (any, error) {
				return machines(eng, company, a.str("query"))
			}},
		{name: "search_tickets", desc: "搜索售后工单（编号、客户、产品、批次、问题、答复），可按状态筛选；query 为空时按时间倒序列出。",
			params: obj(map[string]any{"query": prop("string", "关键词，空格分隔"), "status": enum("只看这个状态（可省）", Statuses), "limit": prop("integer", "最多几条，默认 8")}),
			run: func(_ context.Context, a args) (any, error) {
				return st.SearchTickets(company, a.str("query"), a.str("status"), max(1, min(a.num("limit", 8), 20)))
			}},
		{name: "get_ticket", desc: "读一张工单的全部内容和处理记录。",
			params: obj(map[string]any{"id": prop("string", "工单编号，如 SV-2026-0003")}, "id"),
			run: func(_ context.Context, a args) (any, error) {
				t, err := st.Ticket(company, a.str("id"))
				if errors.Is(err, ErrNotFound) {
					return nil, fmt.Errorf("没有工单 %s", a.str("id"))
				}
				return t, err
			}},
		{name: "create_ticket", desc: "登记一张新的售后工单，返回工单编号。",
			params: obj(map[string]any{
				"customer": prop("string", "客户名称"),
				"product":  prop("string", "产品名称和零件号"),
				"batch":    prop("string", "批次号或炉号（不知道就留空）"),
				"title":    prop("string", "一句话问题"),
				"detail":   prop("string", "客户原话要点、数量、发现地点、附带数据"),
				"severity": enum("严重程度", Severities),
			}, "customer", "product", "title", "detail", "severity"),
			write: true,
			run: func(ctx context.Context, a args) (any, error) {
				sev := a.str("severity")
				if !slices.Contains(Severities, sev) {
					return nil, fmt.Errorf("severity 只能是 %v", Severities)
				}
				return st.CreateTicket(company, Ticket{Customer: a.str("customer"), Product: a.str("product"), Batch: a.str("batch"),
					Title: a.str("title"), Detail: a.str("detail"), Severity: sev,
					Log: []TicketLog{{At: now(), By: caller(ctx).Name, Text: "登记"}}})
			}},
		{name: "update_ticket", desc: "处理一张工单：改状态、存答复草稿、记一笔处理过程（三者都可省，至少给一个）。",
			params: obj(map[string]any{
				"id":     prop("string", "工单编号"),
				"status": enum("新状态（可省）", Statuses),
				"reply":  prop("string", "给客户的答复草稿，整段替换（可省）"),
				"note":   prop("string", "记入处理记录的一句话（可省）"),
			}, "id"),
			write: true,
			run: func(ctx context.Context, a args) (any, error) {
				status := a.str("status")
				if status != "" && !slices.Contains(Statuses, status) {
					return nil, fmt.Errorf("status 只能是 %v", Statuses)
				}
				note := a.str("note")
				if note == "" && a.str("reply") != "" && status == "" {
					note = "更新答复草稿"
				}
				t, err := st.UpdateTicket(company, a.str("id"), status, a.str("reply"), caller(ctx).Name, note)
				if errors.Is(err, ErrNotFound) {
					return nil, fmt.Errorf("没有工单 %s", a.str("id"))
				}
				return t, err
			}},
	}
	out := map[string]tool{}
	for _, t := range ts {
		out[t.name] = t
	}
	return out
}

type snapshot struct {
	Plants []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Alarms []struct {
			Asset  string `json:"asset"`
			Text   string `json:"text"`
			At     string `json:"at"`
			Active bool   `json:"active"`
		} `json:"alarms"`
		Assets []map[string]any `json:"assets"`
	} `json:"plants"`
}

// stateText is how a machine's state reads in an answer.
var stateText = map[string]string{
	"running": "运行", "idle": "待机", "blocked": "堵料等下游", "stopped": "停机", "fault": "故障", "maint": "保养",
	"changeover": "换型", "moving": "行驶", "waiting": "等待", "handling": "装卸", "away": "离开",
}

// stateWords are the states a question can ask for ("哪台故障了", "停了哪些").
var stateWords = []struct {
	word   string
	states []string
}{
	{"故障", []string{"fault"}}, {"坏", []string{"fault"}}, {"报警", nil}, {"停", []string{"fault", "stopped", "maint", "changeover"}},
	{"待机", []string{"idle"}}, {"空闲", []string{"idle"}}, {"保养", []string{"maint"}}, {"换型", []string{"changeover"}},
	{"堵", []string{"blocked"}}, {"运行", []string{"running"}}, {"在跑", []string{"running"}},
}

// trouble orders machines for an answer: faults first, then stopped, in maintenance or changeover,
// then held up, then the rest.
func trouble(state string) int {
	switch state {
	case "fault":
		return 0
	case "stopped", "maint", "changeover":
		return 1
	case "blocked":
		return 2
	}
	return 3
}

// Machines finds the company's machines matching query in the twin's current state: a machine
// (HP-101, 热模锻压力机), a line, an area (压铸车间), or a state ("故障", "停"); empty for all. The machines in
// trouble come first, with how many machines are in each state across what matched.
func Machines(eng *engine.Engine, company, query string) (any, error) {
	return machines(eng, company, query)
}

func machines(eng *engine.Engine, company, query string) (any, error) {
	if eng == nil {
		return nil, errors.New("孪生不可用")
	}
	raw := eng.Snapshot(company)
	var snap snapshot
	if raw == "null" || json.Unmarshal([]byte(raw), &snap) != nil {
		return nil, errors.New("孪生里没有这家公司")
	}
	q := strings.ToLower(strings.Join(strings.Fields(query), ""))
	var states []string
	alarmed := false
	for _, w := range stateWords {
		if strings.Contains(q, w.word) {
			states = append(states, w.states...)
			alarmed = alarmed || w.states == nil
			q = strings.ReplaceAll(q, w.word, "")
		}
	}
	// what is left of the question after the state words, minus filler, names the machines
	for _, f := range []string{"现在", "哪台", "哪些", "设备", "机器", "了", "的", "吗", "？", "?", "有", "在"} {
		q = strings.ReplaceAll(q, f, "")
	}
	type found struct {
		m     map[string]any
		order int
	}
	var all []found
	count := map[string]int{}
	for _, p := range snap.Plants {
		for _, a := range p.Assets {
			text := strings.ToLower(strings.ReplaceAll(fmt.Sprint(a["id"], a["name"], a["line"], a["area"], a["type"]), " ", ""))
			if q != "" && !strings.Contains(text, q) {
				continue
			}
			state, _ := a["state"].(string)
			var alarms []string
			for _, al := range p.Alarms {
				if al.Asset == a["id"] && al.Active && len(alarms) < 5 {
					alarms = append(alarms, al.At+" "+al.Text)
				}
			}
			if (len(states) > 0 || alarmed) && !slices.Contains(states, state) && !(alarmed && len(alarms) > 0) {
				continue
			}
			m := map[string]any{"plant": p.Name}
			for _, k := range []string{"id", "name", "area", "line", "state", "fault", "temp", "power", "vib", "health", "util", "speed", "tool", "live"} {
				if v, ok := a[k]; ok && v != "" && v != nil {
					m[k] = v
				}
			}
			// the reading is the machine's body, not the work in it: say so, with its own limits
			if t, ok := a["temp"].(float64); ok {
				txt := fmt.Sprintf("机身温度 %.1f ℃", t)
				if w, _ := a["tempWarn"].(float64); w != 0 {
					txt += fmt.Sprintf("（本机报警线 %g ℃", w)
					if tr, _ := a["tempTrip"].(float64); tr != 0 {
						txt += fmt.Sprintf("，跳停线 %g ℃", tr)
					}
					txt += "）"
				}
				m["temp"] = txt
			}
			if t, ok := stateText[state]; ok {
				m["state_text"] = t
			}
			if alarms != nil {
				m["alarms"] = alarms
			}
			count[cmp.Or(stateText[state], state)]++
			all = append(all, found{m, trouble(state)})
		}
	}
	if len(all) == 0 {
		if len(states) > 0 || alarmed {
			return map[string]any{"machines": []any{}, "note": "现在没有处在这种状态的设备"}, nil
		}
		return nil, fmt.Errorf("孪生里没有和“%s”对得上的设备；试试编号前缀（HP、IH、RR）或产线名", query)
	}
	slices.SortStableFunc(all, func(a, b found) int { return a.order - b.order })
	out := make([]map[string]any, 0, min(len(all), 20))
	for _, f := range all[:min(len(all), 20)] {
		out = append(out, f.m)
	}
	res := map[string]any{"machines": out, "states": count, "total": len(all)}
	if len(all) > len(out) {
		res["more"] = len(all) - len(out)
	}
	return res, nil
}
