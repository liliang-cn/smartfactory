package agents

import (
	"context"
	"fmt"
	"strings"

	"smartfactory/internal/knowledge"
)

// The library's documents also go into the company's knowledge base, where their passages are
// linked to the process they talk about; the assistants search both at once.

// AddDoc keeps a document in the library and indexes it in the knowledge base. A failure to
// index is logged: the document is kept and can be indexed again (IndexDocs).
func (r *Runner) AddDoc(ctx context.Context, company, title, body, by string) (Doc, error) {
	d, err := r.Store.AddDoc(company, title, body, by)
	if err != nil {
		return d, err
	}
	r.index(ctx, company, d)
	return d, nil
}

// PutFile keeps an uploaded file in the library (a new document or its next version) and puts
// the current version's passages in the knowledge base in place of the previous ones.
func (r *Runner) PutFile(ctx context.Context, company string, u Upload) (Doc, Outcome, error) {
	d, outcome, before, err := r.Store.PutFile(company, u)
	if err != nil {
		return d, outcome, err
	}
	if r.KB != nil && before > 0 {
		r.KB.RemoveDoc(ctx, company, d.ID, before)
	}
	r.index(ctx, company, d)
	return d, outcome, nil
}

// DeleteDoc removes a document from the library and the knowledge base.
func (r *Runner) DeleteDoc(ctx context.Context, company, id string) error {
	_, total, _ := r.Store.ReadDoc(company, id, 0, 1)
	if err := r.Store.DeleteDoc(company, id); err != nil {
		return err
	}
	if r.KB != nil {
		r.KB.RemoveDoc(ctx, company, id, total)
	}
	return nil
}

func (r *Runner) index(ctx context.Context, company string, d Doc) {
	if r.KB == nil {
		return
	}
	parts, _, err := r.Store.ReadDoc(company, d.ID, 0, 1<<20)
	if err != nil {
		return
	}
	in := make([]knowledge.Part, len(parts))
	for i, p := range parts {
		in[i] = knowledge.Part{Seq: p.Seq, Heading: p.Heading, Text: p.Text}
	}
	if err := r.KB.AddDoc(ctx, company, d.ID, d.Title, in); err != nil {
		r.Log.Error("index document", "company", company, "doc", d.ID, "err", err)
	}
}

// IndexDocs indexes the company's documents the knowledge base does not have yet (all of them,
// with again). A company whose assistants were never looked at has no library yet.
func (r *Runner) IndexDocs(ctx context.Context, company string, again bool) (int, error) {
	if r.KB == nil {
		return 0, nil
	}
	docs, err := r.Store.Docs(company)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range docs {
		if again || !r.KB.HasDoc(ctx, company, d.ID) {
			r.index(ctx, company, d)
			n++
		}
	}
	return n, nil
}

// knowledgeTools search the knowledge base: passages from documents and the process, and the
// process graph itself.
func knowledgeTools(kb *knowledge.Base, company string) []tool {
	return []tool{
		{name: "search_docs", desc: "在公司的知识库里搜索：资料库文档的段落，公司工艺（工厂、产线、每个工位的上下游、设备、产品、供应商、客户）的说明，生产工单（ERP 下达的，含客户、产品、产线、进度、交期），和设备最近 30 天发生的事：点检超标或异常的记录、每台设备最近的点检和超期没做的、读数异常提示（偏离这台设备平常值多少σ）、模型校准（拟合和启用）、AI 发给设备的指令（批准、拒绝、被规则拒绝、过期）。问题里提到设备编号、产线、产品、客户或工单号时，会沿着知识图谱找到讲它们的段落；同时问到点检、异常、校准、指令时，这台设备的这类记录排在最前面、最新的在前。返回最相关的段落：from=doc 是文档（带文档编号和段号），from=process 是工艺，from=order 是工单，from=check / anomaly / calibration / command 是点检、读数异常、校准、AI 指令。",
			params: obj(map[string]any{
				"query":    prop("string", "要找的问题，原话即可"),
				"names":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "问题涉及的设备编号、设备名、产线、产品、客户名（可省）"},
				"keywords": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "换几种说法的关键词（可省）"},
				"limit":    prop("integer", "最多返回几段，默认 6"),
			}, "query"),
			run: func(ctx context.Context, a args) (any, error) {
				return kb.Search(ctx, company, a.str("query"), a.list("names"), a.list("keywords"), max(1, min(a.num("limit", 6), 12)))
			}},
		{name: "process_graph", desc: "查知识图谱里的一样东西（工位编号如 HP-101、设备名、产线、产品、供应商、客户、工单号）和它的全部关系：上下游工位、属于哪条线、是什么设备、生产什么、谁供货、卖给谁、客户有哪些工单、工单在哪条线做什么产品，以及哪些段落提到它。查工位时还列出它最近发生的事（kind 为 check 点检、anomaly 读数异常、calibration 校准、command AI 指令，每类最多几条、最新的在前，title 是一句话概要）；再查其中一条的名字可得 detail（点检的各项值和填写人、异常的σ和平常范围、校准参数、指令的结果和处理人）。名字不完整时返回可能的候选。",
			params: obj(map[string]any{"name": prop("string", "名字或编号")}, "name"),
			run: func(ctx context.Context, a args) (any, error) {
				name := a.str("name")
				e, err := kb.Lookup(ctx, company, name)
				if err != nil {
					return nil, err
				}
				if e != nil {
					return e, nil
				}
				cands, err := kb.Find(ctx, company, name, "", 12)
				if err != nil {
					return nil, err
				}
				if len(cands) == 0 {
					return nil, fmt.Errorf("工艺图里没有叫「%s」的东西；换个说法，或用 search_docs", name)
				}
				names := make([]string, len(cands))
				for i, c := range cands {
					names[i] = c.Name + "（" + c.Kind + "）"
				}
				return map[string]any{"candidates": cands, "hint": "没有完全同名的，可能是：" + strings.Join(names, "、")}, nil
			}},
		{name: "ontology_query", desc: "查公司的本体（行业包声明的东西有哪些类、怎么相关、有什么规则和约束），带推理。三种问法：" +
			"不带参数看全部类（每类多少东西）、关系、规则、约束和约束现在的状况（哪台设备的读数正超限、哪些东西不满足要求）；" +
			"给 class 列这一类的全部东西（子类的、按关系推出来的都算），再给 relation 和 value 只要和它这样相关的（比如 class=菜品 relation=含过敏原 value=花生过敏原：哪些菜含花生）；" +
			"给 name 查一样东西（或一个类）：它是哪些类、它的事实，推理出的每条带 why（从给定事实一步步推出来）。回答推理出的结论时把 why 说给用户。",
			params: obj(map[string]any{
				"name":     prop("string", "一样东西或一个类的名字（可省）"),
				"class":    prop("string", "类名（可省）"),
				"relation": prop("string", "关系名，和 value 一起用（可省）"),
				"value":    prop("string", "关系的值（可省）"),
			}),
			run: func(ctx context.Context, a args) (any, error) {
				switch {
				case a.str("name") != "":
					return kb.OntologyThing(ctx, company, a.str("name"))
				case a.str("class") != "":
					return kb.OntologyInstances(ctx, company, a.str("class"), a.str("relation"), a.str("value"), 60)
				}
				return kb.OntologyOverview(ctx, company)
			}},
	}
}
