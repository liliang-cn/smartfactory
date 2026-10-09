package agents

import (
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
		{name: "twin_machines", desc: "在数字孪生里按编号、名称、产线或类型找设备，返回它在哪个厂、当前状态、温度、功率、振动、健康度、稼动率、故障和最近的报警。",
			params: obj(map[string]any{"query": prop("string", "设备编号（HP-101）、名称（热模锻压力机）或产线（曲轴 1 线）")}, "query"),
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

// machines finds the company's machines matching query in the twin's current state.
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
	var out []map[string]any
	for _, p := range snap.Plants {
		for _, a := range p.Assets {
			text := strings.ToLower(strings.ReplaceAll(fmt.Sprint(a["id"], a["name"], a["line"], a["type"]), " ", ""))
			if q != "" && !strings.Contains(text, q) {
				continue
			}
			m := map[string]any{"plant": p.Name}
			for _, k := range []string{"id", "name", "line", "state", "fault", "temp", "power", "vib", "health", "util", "speed", "tool", "live"} {
				if v, ok := a[k]; ok && v != "" && v != nil {
					m[k] = v
				}
			}
			var alarms []string
			for _, al := range p.Alarms {
				if al.Asset == a["id"] && len(alarms) < 5 {
					alarms = append(alarms, al.At+" "+al.Text)
				}
			}
			if alarms != nil {
				m["alarms"] = alarms
			}
			out = append(out, m)
			if len(out) == 12 {
				return map[string]any{"machines": out, "more": true}, nil
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("孪生里没有和“%s”对得上的设备；试试编号前缀（HP、IH、RR）或产线名", query)
	}
	return map[string]any{"machines": out}, nil
}
