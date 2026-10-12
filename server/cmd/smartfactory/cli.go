package main

// The command line for administrators: everything the console does, against a running server.
//
//	smartfactory login -server http://localhost:4720
//	smartfactory company | company show <id> | company new -f cfg.json | company new -ai "描述" | company rm <id>
//	smartfactory industries | industries show <id> | industries new -ai "描述" | industries new -f pack.json
//	smartfactory tokens | tokens issue -name 张工 -role operator -company huaduan | tokens revoke <key>
//	smartfactory kb <company> | kb <company> search "问题" | entity <名字> | docs | upload <文件>… | rebuild
//	smartfactory orders <company> | orders <company> show <工单号> | send -f orders.json | cancel <工单号> | pull [-dry] | erp
//	smartfactory calib <company> | calib <company> show <设备> | fit [-hours 24] [设备…] | apply [设备…] | revert [设备…]
//	smartfactory guard <company> | guard <company> set -level guarded -approve start,speed | pending | approve <编号> | deny <编号>
//	smartfactory anomalies <company> | anomalies <company> show <设备> | reset <设备…>
//	smartfactory activity [-company id] [-n 30]
//	smartfactory agent ["一句话"]
//
// -json on any of them prints what the server answered.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/term"

	"smartfactory/internal/client"
)

var cliCommands = map[string]func(ctx context.Context, args []string) error{
	"login": cliLogin, "logout": cliLogout, "whoami": cliWhoami,
	"company": cliCustomers, "industries": cliIndustries, "tokens": cliTokens,
	"kb": cliKB, "orders": cliOrders, "calib": cliCalib, "guard": cliGuard, "anomalies": cliAnomalies, "activity": cliActivity, "agent": cliAgent,
}

// ---- output ----

var asJSON bool

// flags parses fs, letting -json come anywhere.
func flags(fs *flag.FlagSet, args []string) []string {
	fs.BoolVar(&asJSON, "json", false, "print what the server answered")
	var rest []string
	for len(args) > 0 {
		fs.Parse(args)
		args = fs.Args()
		if len(args) > 0 {
			rest = append(rest, args[0])
			args = args[1:]
		}
	}
	return rest
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

var color = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""

func paint(code, s string) string {
	if !color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
func bold(s string) string  { return paint("1", s) }
func dim(s string) string   { return paint("2", s) }
func green(s string) string { return paint("32", s) }
func red(s string) string   { return paint("31", s) }
func blue(s string) string  { return paint("34", s) }

// width is how many columns s takes in a terminal: CJK and full-width characters take two.
func width(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		switch {
		case r == '\033':
			inEsc = true
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case unicode.Is(unicode.Han, r) || (r >= 0xFF00 && r <= 0xFFEF) || (r >= 0x3000 && r <= 0x303F):
			n += 2
		default:
			n++
		}
	}
	return n
}

// table prints rows in columns.
func table(head []string, rows [][]string) {
	w := make([]int, len(head))
	for i, h := range head {
		w[i] = width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			w[i] = max(w[i], width(c))
		}
	}
	line := func(cells []string, style func(string) string) {
		var b strings.Builder
		for i, c := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(style(c))
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", w[i]-width(c)))
			}
		}
		fmt.Println(b.String())
	}
	line(head, dim)
	for _, r := range rows {
		line(r, func(s string) string { return s })
	}
}

func confirm(question string) bool {
	fmt.Print(question + " [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
}

func connect() (*client.Client, error) {
	l, err := client.Saved()
	if err != nil {
		return nil, err
	}
	return client.New(l), nil
}

// ---- login ----

func cliLogin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "http://localhost:4720", "server address")
	flags(fs, args)
	fmt.Print("管理员令牌：")
	var token string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		token = string(b)
	} else {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		token = line
	}
	l := client.Login{Server: strings.TrimRight(*server, "/"), Token: strings.TrimSpace(token)}
	var ov overview
	if err := client.New(l).Do(ctx, "GET", "/api/admin", nil, &ov); err != nil {
		return err
	}
	p, err := client.Save(l)
	if err != nil {
		return err
	}
	fmt.Printf("已登录 %s：%s，%s，能管 %d 家公司。令牌存在 %s\n", l.Server, bold(ov.Me.Name), scope(ov.Me), len(ov.Companies), p)
	return nil
}

func cliLogout(context.Context, []string) error {
	if err := client.Forget(); err != nil {
		return err
	}
	fmt.Println("已退出，令牌从这台电脑上删掉了")
	return nil
}

type me struct {
	Name       string   `json:"name"`
	Role       string   `json:"role"`
	Companies  []string `json:"companies"`
	Everywhere bool     `json:"everywhere"`
	Key        string   `json:"key"`
}

func scope(m me) string {
	if m.Everywhere {
		return "所有公司的管理员"
	}
	return strings.Join(m.Companies, "、") + " 的管理员"
}

type overview struct {
	Me        me `json:"me"`
	Companies []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Short    string `json:"short"`
		Industry string `json:"industry"`
		Builtin  bool   `json:"builtin"`
		Plants   []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Hall     string `json:"hall"`
			Lines    int    `json:"lines"`
			Machines int    `json:"machines"`
		} `json:"plants"`
		LiveTags int  `json:"liveTags"`
		Advisor  bool `json:"advisor"`
	} `json:"companies"`
	Industries []struct {
		ID       string            `json:"id"`
		Name     string            `json:"name"`
		Flow     string            `json:"flow"`
		Builtin  bool              `json:"builtin"`
		Products []string          `json:"products"`
		Machines map[string]string `json:"machines"`
	} `json:"industries"`
	Version string `json:"version"`
}

func cliWhoami(ctx context.Context, args []string) error {
	flags(flag.NewFlagSet("whoami", flag.ExitOnError), args)
	c, err := connect()
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.Do(ctx, "GET", "/api/admin", nil, &raw); err != nil {
		return err
	}
	var ov overview
	json.Unmarshal(raw, &ov)
	if asJSON {
		return printJSON(ov.Me)
	}
	fmt.Printf("%s，%s，令牌 %s，服务器 %s（%s）\n", bold(ov.Me.Name), scope(ov.Me), ov.Me.Key, c.Server, ov.Version)
	return nil
}

// ---- the company ----

func cliCustomers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("company", flag.ExitOnError)
	file := fs.String("f", "", "company config (JSON) to create, or - for stdin")
	brief := fs.String("ai", "", "describe the customer; the scene architect writes the config")
	check := fs.Bool("check", false, "only check the config")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	c, err := connect()
	if err != nil {
		return err
	}
	verb := "ls"
	if len(rest) > 0 {
		verb = rest[0]
	}
	switch verb {
	case "ls", "list":
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", "/api/admin", nil, &raw); err != nil {
			return err
		}
		var ov overview
		json.Unmarshal(raw, &ov)
		if asJSON {
			return printJSON(ov.Companies)
		}
		inds := map[string]string{}
		for _, i := range ov.Industries {
			inds[i.ID] = i.Name
		}
		var rows [][]string
		for _, co := range ov.Companies {
			lines, machines := 0, 0
			for _, p := range co.Plants {
				lines += p.Lines
				machines += p.Machines
			}
			kind := "自建"
			if co.Builtin {
				kind = dim("内置")
			}
			live := ""
			if co.LiveTags > 0 {
				live = green(fmt.Sprintf("%d 个实时点", co.LiveTags))
			}
			rows = append(rows, []string{co.ID, bold(co.Name), inds[co.Industry], fmt.Sprint(len(co.Plants)), fmt.Sprint(lines), fmt.Sprint(machines), kind, live})
		}
		table([]string{"标识", "名称", "行业", "工厂", "产线", "设备", "", ""}, rows)
	case "show":
		if len(rest) < 2 {
			return errors.New("smartfactory company show <id>")
		}
		var cfg json.RawMessage
		if err := c.Do(ctx, "GET", "/api/admin/companies/"+client.Q(rest[1]), nil, &cfg); err != nil {
			return err
		}
		if asJSON {
			return printJSON(cfg)
		}
		var co struct {
			ID, Name, Short, Industry string
			Plants                    []struct {
				ID, Name, Hall string
				Lines          []struct {
					Name, Product, Route string
					Repeat               int
				}
			}
		}
		json.Unmarshal(cfg, &co)
		fmt.Printf("%s（%s，%s），行业 %s\n", bold(co.Name), co.Short, co.ID, co.Industry)
		for _, p := range co.Plants {
			fmt.Printf("  %s %s\n", bold(p.Name), dim(p.Hall))
			for _, l := range p.Lines {
				n := max(l.Repeat, 1)
				fmt.Printf("    %s × %d  %s\n", strings.ReplaceAll(l.Name, "{n}", "N"), n, dim(l.Product+" / "+l.Route))
			}
		}
		var kb struct {
			Kinds     map[string]int `json:"kinds"`
			Docs      int            `json:"docs"`
			Library   int            `json:"library"`
			Relations map[string]int `json:"relations"`
		}
		if c.Do(ctx, "GET", "/api/knowledge/"+client.Q(co.ID), nil, &kb) == nil {
			fmt.Printf("  知识库：%d 个工位、%d 种设备、%d 个产品，%d 段文档（资料库 %d 篇）\n", kb.Kinds["station"], kb.Kinds["machine"], kb.Kinds["product"], kb.Docs, kb.Library)
		}
		fmt.Printf("  孪生：%s/?company=%s\n", c.Server, co.ID)
	case "new":
		var cfg map[string]any
		// industry packs the architect wrote for it, installed before it is built
		var packs map[string]map[string]any
		switch {
		case *brief != "":
			if cfg, packs, err = compose(ctx, c, "company", *brief); err != nil {
				return err
			}
		case *file != "":
			var b []byte
			if *file == "-" {
				b, err = io.ReadAll(os.Stdin)
			} else {
				b, err = os.ReadFile(*file)
			}
			if err != nil {
				return err
			}
			if err := json.Unmarshal(b, &cfg); err != nil {
				return fmt.Errorf("%s 不是合法的 JSON：%w", *file, err)
			}
		default:
			return errors.New("smartfactory company new -f cfg.json，或 -ai \"描述工厂\"")
		}
		var res json.RawMessage
		if len(packs) == 0 {
			// with new packs the architect has already checked them together
			if err := c.Do(ctx, "POST", "/api/admin/companies?check=1", cfg, &res); err != nil {
				return err
			}
			summary(res)
		}
		for _, p := range packs {
			fmt.Print(dim("新行业包 "))
			packSummary(p)
		}
		if *check {
			fmt.Println(green("检查通过"), dim("（没有创建）"))
			return nil
		}
		if !*yes && !confirm(fmt.Sprintf("建立 %v？", cfg["name"])) {
			return nil
		}
		for id, p := range packs {
			if err := c.Do(ctx, "POST", "/api/admin/industries", p, nil); err != nil {
				return fmt.Errorf("行业包 %s：%w", id, err)
			}
		}
		if err := c.Do(ctx, "POST", "/api/admin/companies", cfg, &res); err != nil {
			return err
		}
		if asJSON {
			return printJSON(res)
		}
		fmt.Printf("%s 已建好，正在仿真：%s/?company=%v\n", green("✓"), c.Server, cfg["id"])
	case "rm", "delete":
		if len(rest) < 2 {
			return errors.New("smartfactory company rm <id>")
		}
		if !*yes && !confirm("删除公司 "+rest[1]+"？它的仿真停止、配置删掉（助手、资料、记录保留）") {
			return nil
		}
		if err := c.Do(ctx, "DELETE", "/api/admin/companies/"+client.Q(rest[1]), nil, nil); err != nil {
			return err
		}
		fmt.Println(green("✓"), "已删除", rest[1])
	default:
		return fmt.Errorf("company %s：用 ls、show、new、rm", verb)
	}
	return nil
}

// summary prints what the engine said about a config it accepted.
func summary(raw json.RawMessage) {
	var r struct {
		ID, Name string
		Plants   []struct {
			Name     string
			Lines    []string
			Machines int
		}
	}
	json.Unmarshal(raw, &r)
	fmt.Printf("%s（%s）\n", bold(r.Name), r.ID)
	for _, p := range r.Plants {
		fmt.Printf("  %s：%d 条线，%d 台设备  %s\n", p.Name, len(p.Lines), p.Machines, dim(strings.Join(p.Lines, "、")))
	}
}

// compose has the scene architect write a company config (kind company) or an industry pack
// (kind industry) from brief, showing its steps; it returns the config and the packs it wrote.
func compose(ctx context.Context, c *client.Client, kind, brief string) (map[string]any, map[string]map[string]any, error) {
	var draft map[string]any
	packs := map[string]map[string]any{}
	var failed string
	err := c.Stream(ctx, "/api/admin/compose", map[string]any{"kind": kind, "brief": brief}, func(e client.Event) {
		switch e.Kind {
		case "tool_call":
			fmt.Println(dim("· " + toolLabel(e.Tool, e.Args)))
		case "tool_result":
			if s, ok := e.Result.(string); ok && strings.Contains(s, "没通过") {
				fmt.Println(red("  " + firstLine(s)))
			}
		case "draft":
			draft, _ = e.Result.(map[string]any)
		case "industry":
			if p, ok := e.Result.(map[string]any); ok {
				packs[fmt.Sprint(p["id"])] = p
			}
		case "complete":
			fmt.Println(e.Text)
		case "error":
			failed = e.Text
		}
	})
	if err != nil {
		return nil, nil, err
	}
	if kind == "industry" {
		if len(packs) == 0 {
			return nil, nil, errors.New(cmpOr(failed, "没写出能通过检查的行业包"))
		}
		return nil, packs, nil
	}
	if draft == nil {
		return nil, nil, errors.New(cmpOr(failed, "没写出能通过检查的配置"))
	}
	// only the packs the config uses
	used := map[string]bool{fmt.Sprint(draft["industry"]): true}
	if ps, ok := draft["plants"].([]any); ok {
		for _, p := range ps {
			if m, ok := p.(map[string]any); ok && m["industry"] != nil {
				used[fmt.Sprint(m["industry"])] = true
			}
		}
	}
	for id := range packs {
		if !used[id] {
			delete(packs, id)
		}
	}
	return draft, packs, nil
}

// packSummary prints an industry pack's products, machines and routes in a line each.
func packSummary(p map[string]any) {
	names := func(k, field string) string {
		m, _ := p[k].(map[string]any)
		var out []string
		for _, v := range m {
			if x, ok := v.(map[string]any); ok {
				out = append(out, fmt.Sprint(x[field]))
			}
		}
		sort.Strings(out)
		return strings.Join(out, "、")
	}
	fmt.Printf("%s（%v）\n", bold(fmt.Sprint(p["name"])), p["id"])
	fmt.Println("  产品：" + names("products", "name"))
	fmt.Println("  设备：" + names("machines", "label"))
}

func readInput(file string) ([]byte, error) {
	if file == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(file)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

var toolNames = map[string]string{
	"list_industries": "看行业包", "get_industry": "读行业包", "list_companies": "看现有的公司", "get_company": "参考公司",
	"propose_company": "检查配置", "list_customers": "看公司", "get_customer": "读公司", "check_company": "检查配置",
	"list_tokens": "看令牌", "activity": "看操作记录", "knowledge_search": "搜知识库", "knowledge_entity": "查工艺图",
	"propose_create_company": "提出建公司", "propose_delete_company": "提出删公司", "propose_issue_token": "提出发令牌",
	"propose_revoke_token": "提出撤销令牌", "propose_rebuild_knowledge": "提出重建知识库", "propose_industry": "检查新写的行业包",
	"list_orders": "看工单", "erp_status": "看 ERP 对接", "propose_cancel_order": "提出撤回工单",
	"check_industry": "检查行业包", "propose_create_industry": "提出保存行业包",
	"guard_status": "看指令把关", "propose_approve_command": "提出批准指令", "propose_deny_command": "提出拒绝指令",
	"calibration": "看校准", "propose_apply_calibration": "提出应用校准",
	"anomalies": "看读数异常", "propose_reset_baseline": "提出重新学习读数",
}

func toolLabel(tool string, args map[string]any) string {
	label := cmpOr(toolNames[tool], tool)
	for _, k := range []string{"id", "company", "name", "query"} {
		if v, ok := args[k].(string); ok && v != "" {
			label += " " + v
		}
	}
	return label
}

// ---- industry packs ----

func cliIndustries(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("industries", flag.ExitOnError)
	brief := fs.String("ai", "", "new: describe the kind of site; the architect writes the pack")
	file := fs.String("f", "", "new: the pack as JSON (- for stdin)")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	c, err := connect()
	if err != nil {
		return err
	}
	if len(rest) >= 1 && rest[0] == "new" {
		var pack map[string]any
		switch {
		case *brief != "":
			var packs map[string]map[string]any
			if _, packs, err = compose(ctx, c, "industry", *brief); err != nil {
				return err
			}
			for _, p := range packs {
				pack = p
			}
		case *file != "":
			b, err := readInput(*file)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(b, &pack); err != nil {
				return fmt.Errorf("%s 不是合法的 JSON：%w", *file, err)
			}
		default:
			return errors.New(`smartfactory industries new -ai "描述" 或 -f pack.json`)
		}
		if pack == nil {
			return errors.New("没写出能通过检查的行业包")
		}
		packSummary(pack)
		if !*yes && !confirm(fmt.Sprintf("保存行业包 %v？", pack["name"])) {
			return nil
		}
		if err := c.Do(ctx, "POST", "/api/admin/industries", pack, nil); err != nil {
			return err
		}
		fmt.Printf("%s 行业包 %v 已保存，建工厂时可以选它\n", green("✓"), pack["id"])
		return nil
	}
	if len(rest) >= 2 && rest[0] == "show" {
		var cfg json.RawMessage
		if err := c.Do(ctx, "GET", "/api/admin/industries/"+client.Q(rest[1]), nil, &cfg); err != nil {
			return err
		}
		if asJSON {
			return printJSON(cfg)
		}
		var ind struct {
			ID, Name string
			Products map[string]struct{ Name string }
			Machines map[string]struct {
				Label string
				Code  string
				Cycle float64
			}
			Routes map[string][]any
		}
		json.Unmarshal(cfg, &ind)
		fmt.Printf("%s（%s）\n", bold(ind.Name), ind.ID)
		var ps []string
		for _, p := range ind.Products {
			ps = append(ps, p.Name)
		}
		sort.Strings(ps)
		fmt.Println("  产品：" + strings.Join(ps, "、"))
		var rows [][]string
		for k, m := range ind.Machines {
			rows = append(rows, []string{"  " + m.Code, m.Label, fmt.Sprintf("%g 秒", m.Cycle), dim(k)})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
		table([]string{"  编号", "设备", "节拍", "键"}, rows)
		for k, steps := range ind.Routes {
			var names []string
			for _, s := range steps {
				key, _ := s.(string)
				if m, ok := s.(map[string]any); ok {
					key, _ = m["machine"].(string)
				}
				names = append(names, ind.Machines[key].Label)
			}
			fmt.Printf("  %s  %s\n", dim(k), strings.Join(names, " → "))
		}
		return nil
	}
	var raw json.RawMessage
	if err := c.Do(ctx, "GET", "/api/admin", nil, &raw); err != nil {
		return err
	}
	var ov overview
	json.Unmarshal(raw, &ov)
	if asJSON {
		return printJSON(ov.Industries)
	}
	used := map[string][]string{}
	for _, co := range ov.Companies {
		used[co.Industry] = append(used[co.Industry], co.Name)
	}
	var rows [][]string
	for _, i := range ov.Industries {
		flow := "传送带"
		if i.Flow == "walk" {
			flow = "步行"
		}
		rows = append(rows, []string{i.ID, bold(i.Name), flow, fmt.Sprintf("%d 种产品，%d 种设备", len(i.Products), len(i.Machines)), strings.Join(used[i.ID], "、")})
	}
	table([]string{"标识", "名称", "流转", "", "在用"}, rows)
	return nil
}

// ---- tokens ----

func cliTokens(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tokens", flag.ExitOnError)
	name := fs.String("name", "", "who holds it")
	role := fs.String("role", "member", "member | operator | admin | erp (the order API only)")
	company := fs.String("company", "", "companies it covers, comma-separated; * for every company")
	expires := fs.String("expires", "", "last day it works, YYYY-MM-DD")
	all := fs.Bool("all", false, "list revoked tokens too")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	c, err := connect()
	if err != nil {
		return err
	}
	verb := "ls"
	if len(rest) > 0 {
		verb = rest[0]
	}
	switch verb {
	case "ls", "list":
		var list []struct {
			Key, Name, Role, From, Expires, CreatedBy string
			Companies                                 []string
			Used, Revoked                             *time.Time
			Expired                                   bool
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", "/api/admin/tokens", nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		json.Unmarshal(raw, &list)
		roles := map[string]string{"member": "成员", "operator": "操作员", "admin": "管理员"}
		from := map[string]string{"legacy": "最初的共用令牌", "config": "配置文件", "issued": "后台签发"}
		var rows [][]string
		for _, a := range list {
			if a.Revoked != nil && !*all {
				continue
			}
			cos := strings.Join(a.Companies, "、")
			if cos == "" {
				cos = "所有公司"
			}
			used := dim("没用过")
			if a.Used != nil {
				used = a.Used.Local().Format("01-02 15:04")
			}
			state := green("有效")
			switch {
			case a.Revoked != nil:
				state = dim("已撤销")
			case a.Expired:
				state = red("过期")
			case a.Expires != "":
				state = green("到 " + a.Expires)
			}
			src := from[a.From]
			if a.From == "issued" {
				src += dim(" " + a.CreatedBy)
			}
			rows = append(rows, []string{a.Key, bold(a.Name), roles[a.Role], cos, src, used, state})
		}
		table([]string{"key", "持有人", "角色", "公司", "来源", "最近使用", "状态"}, rows)
	case "issue":
		if *name == "" {
			return errors.New("smartfactory tokens issue -name 张工 -role operator -company huaduan")
		}
		req := map[string]any{"name": *name, "role": *role, "companies": nil}
		if *company != "" && *company != "*" {
			var cs []string
			for _, s := range strings.Split(*company, ",") {
				if s = strings.TrimSpace(s); s != "" {
					cs = append(cs, s)
				}
			}
			req["companies"] = cs
		}
		if *expires != "" {
			req["expires"] = *expires
		}
		var res struct {
			Token   string `json:"token"`
			Account struct {
				Key, Name, Role string
			} `json:"account"`
		}
		if err := c.Do(ctx, "POST", "/api/admin/tokens", req, &res); err != nil {
			return err
		}
		if asJSON {
			return printJSON(res)
		}
		fmt.Fprintf(os.Stderr, "%s 给%s的令牌（key %s），只显示这一次：\n", green("✓"), res.Account.Name, res.Account.Key)
		fmt.Println(res.Token)
	case "revoke":
		if len(rest) < 2 {
			return errors.New("smartfactory tokens revoke <key>")
		}
		if !*yes && !confirm("撤销令牌 "+rest[1]+"？立即失效") {
			return nil
		}
		if err := c.Do(ctx, "DELETE", "/api/admin/tokens/"+client.Q(rest[1]), nil, nil); err != nil {
			return err
		}
		fmt.Println(green("✓"), "已撤销", rest[1])
	default:
		return fmt.Errorf("tokens %s：用 ls、issue、revoke", verb)
	}
	return nil
}

// ---- knowledge ----

func cliKB(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("kb", flag.ExitOnError)
	n := fs.Int("n", 6, "passages")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New("smartfactory kb <company> [search \"问题\" | entity <名字> | docs | upload <文件>… | rebuild]")
	}
	c, err := connect()
	if err != nil {
		return err
	}
	company := client.Q(rest[0])
	verb := ""
	if len(rest) > 1 {
		verb = rest[1]
	}
	arg := strings.Join(rest[min(2, len(rest)):], " ")
	switch verb {
	case "":
		var st struct {
			Kinds     map[string]int `json:"kinds"`
			Relations map[string]int `json:"relations"`
			Docs      int            `json:"docs"`
			Process   int            `json:"process"`
			Orders    int            `json:"orders"`
			Library   int            `json:"library"`
			Indexed   int            `json:"indexed"`
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", "/api/knowledge/"+company, nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		json.Unmarshal(raw, &st)
		fmt.Printf("工艺图：%d 个工厂、%d 条线、%d 个工位、%d 种设备、%d 个产品、%d 家供应商、%d 家客户\n",
			st.Kinds["plant"], st.Kinds["line"], st.Kinds["station"], st.Kinds["machine"], st.Kinds["product"], st.Kinds["supplier"], st.Kinds["customer"])
		fmt.Printf("关系：上下游 %d，工位属于产线 %d，工位是什么设备 %d，段落提到 %d\n", st.Relations["feeds"], st.Relations["has_station"], st.Relations["is_a"], st.Relations["mentions"])
		if st.Orders > 0 {
			fmt.Printf("工单：%d 张，连到客户 %d、产品 %d、产线 %d\n", st.Orders, st.Relations["for_customer"], st.Relations["of_product"], st.Relations["runs_on"])
		}
		fmt.Printf("段落：工艺 %d，工单 %d，文档 %d（资料库 %d 篇，已入库 %d 篇）\n", st.Process, st.Orders, st.Docs, st.Library, st.Indexed)
	case "search":
		if arg == "" {
			return errors.New(`smartfactory kb <company> search "问题"`)
		}
		var a struct {
			Mode     string `json:"mode"`
			Entities []string
			Passages []struct {
				From, Title, Doc, Text string
				Part                   int
				Entities               []string
			}
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", fmt.Sprintf("/api/knowledge/%s/search?q=%s&limit=%d", company, urlQ(arg), *n), nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		json.Unmarshal(raw, &a)
		mode := map[string]string{"ppr": "沿工艺图找", "lexical": "按词找", "hybrid": "混合"}[a.Mode]
		fmt.Println(dim(fmt.Sprintf("%s，%d 段", cmpOr(mode, a.Mode), len(a.Passages))))
		for i, p := range a.Passages {
			tag := blue("工艺")
			switch p.From {
			case "doc":
				tag = green("文档")
			case "order":
				tag = red("工单")
			}
			fmt.Printf("\n%d. %s %s\n", i+1, tag, bold(p.Title))
			fmt.Println(indent(strings.TrimSpace(p.Text), "   "))
		}
	case "entity":
		if arg == "" {
			return errors.New("smartfactory kb <company> entity <名字>")
		}
		var a struct {
			Entity *struct {
				Name, Kind string
				Links      []struct {
					Type, Name, Kind string
					Out              bool
				}
				Mentions []string
			}
			Candidates []struct{ Name, Kind string }
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", fmt.Sprintf("/api/knowledge/%s/entity?name=%s", company, urlQ(arg)), nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		json.Unmarshal(raw, &a)
		if a.Entity == nil {
			if len(a.Candidates) == 0 {
				return fmt.Errorf("工艺图里没有「%s」", arg)
			}
			fmt.Println("没有完全同名的，可能是：")
			for _, x := range a.Candidates {
				fmt.Printf("  %s %s\n", x.Name, dim(kindLabel[x.Kind]))
			}
			return nil
		}
		e := a.Entity
		fmt.Printf("%s %s\n", bold(e.Name), dim(kindLabel[e.Kind]))
		for _, l := range e.Links {
			fmt.Printf("  %s %s %s\n", relLabel(l.Type, l.Out), l.Name, dim(kindLabel[l.Kind]))
		}
		if len(e.Mentions) > 0 {
			fmt.Println(dim("  提到它的段落："))
			for _, m := range e.Mentions {
				fmt.Println("    " + m)
			}
		}
	case "docs":
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", "/api/library/"+company+"/docs", nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var docs []struct {
			ID, Title, Kind, File, By string
			Parts, Pages, Version     int
			Problems                  []string
		}
		json.Unmarshal(raw, &docs)
		rows := make([][]string, len(docs))
		for i, d := range docs {
			pages := ""
			if d.Pages > 0 {
				pages = fmt.Sprint(d.Pages)
			}
			rows[i] = []string{d.Title, d.Kind, fmt.Sprint(d.Parts), pages, fmt.Sprintf("v%d", d.Version), d.By, red(strings.Join(d.Problems, "；"))}
		}
		table([]string{"资料", "类型", "段落", "页", "版本", "上传", ""}, rows)
	case "upload":
		files := rest[min(2, len(rest)):]
		if len(files) == 0 {
			return errors.New("smartfactory kb <company> upload 文件… （PDF、Word、Excel、CSV、Markdown、文本）")
		}
		var res []struct {
			File, Outcome, Error string
			OK                   bool
			Doc                  *struct {
				Title        string
				Parts, Pages int
				Version      int
				Problems     []string
			}
		}
		if err := c.Upload(ctx, "/api/library/"+company+"/files", files, &res); err != nil {
			return err
		}
		if asJSON {
			return printJSON(res)
		}
		failed := 0
		for _, r := range res {
			if !r.OK {
				failed++
				fmt.Printf("%s %s %s\n", red("✗"), r.File, r.Error)
				continue
			}
			what := fmt.Sprintf("%d 段", r.Doc.Parts)
			if r.Doc.Pages > 0 {
				what = fmt.Sprintf("%d 页，%s", r.Doc.Pages, what)
			}
			verb := "收下"
			if r.Outcome == "updated" {
				verb = fmt.Sprintf("更新为第 %d 版", r.Doc.Version)
			}
			fmt.Printf("%s %s %s：%s %s\n", green("✓"), r.File, verb, what, dim(strings.Join(r.Doc.Problems, "；")))
		}
		if failed > 0 {
			return fmt.Errorf("%d 个文件没有收下", failed)
		}
	case "rebuild":
		if !*yes && !confirm("重建 "+rest[0]+" 的知识库？") {
			return nil
		}
		var res struct{ Docs int }
		if err := c.Do(ctx, "POST", "/api/knowledge/"+company+"/rebuild", nil, &res); err != nil {
			return err
		}
		fmt.Printf("%s 工艺图已重写，%d 篇文档重新入库\n", green("✓"), res.Docs)
	default:
		return fmt.Errorf("kb %s：用 search、entity、docs、upload、rebuild", verb)
	}
	return nil
}

var kindLabel = map[string]string{
	"company": "公司", "plant": "工厂", "line": "产线", "station": "工位", "machine": "设备", "product": "产品", "supplier": "供应商", "customer": "客户", "order": "工单",
}

// relLabel reads a relation from the thing's side: "IH-101 feeds HP-101" is 下游 from IH-101.
func relLabel(typ string, out bool) string {
	labels := map[string][2]string{
		"has_plant": {"有工厂", "属于公司"}, "has_line": {"有产线", "在工厂"}, "makes": {"生产", "由产线生产"},
		"has_station": {"有工位", "在产线"}, "is_a": {"是", "这种设备的工位"}, "feeds": {"下游", "上游"},
		"supplies": {"供应", "供应商"}, "sold_to": {"卖给", "买"},
		"for_customer": {"客户", "工单"}, "of_product": {"产品", "工单"}, "runs_on": {"产线", "工单"},
	}
	l, ok := labels[typ]
	if !ok {
		return typ
	}
	if out {
		return l[0]
	}
	return l[1]
}

func indent(s, pre string) string {
	return pre + strings.ReplaceAll(s, "\n", "\n"+pre)
}

func urlQ(s string) string {
	r := strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", " ", "%20", "#", "%23", "?", "%3F")
	return r.Replace(s)
}

// ---- activity ----

func cliActivity(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("activity", flag.ExitOnError)
	company := fs.String("company", "", "one company (default every company)")
	n := fs.Int("n", 30, "entries")
	flags(fs, args)
	c, err := connect()
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.Do(ctx, "GET", fmt.Sprintf("/api/admin/audit?company=%s&limit=%d", client.Q(*company), *n), nil, &raw); err != nil {
		return err
	}
	if asJSON {
		return printJSON(raw)
	}
	var list []struct {
		At                                        time.Time
		Who, Via, Company, Action, Target, Detail string
		OK                                        bool
		Error                                     string
	}
	json.Unmarshal(raw, &list)
	var rows [][]string
	for _, e := range list {
		what := strings.TrimSpace(e.Action + " " + e.Target + " " + e.Detail)
		if !e.OK {
			what = red(what + "（没成功：" + e.Error + "）")
		}
		rows = append(rows, []string{e.At.Local().Format("01-02 15:04"), bold(e.Who), e.Via, e.Company, what})
	}
	table([]string{"时间", "谁", "从哪", "公司", "做了什么"}, rows)
	return nil
}

// ---- the console agent ----

func cliAgent(ctx context.Context, args []string) error {
	rest := flags(flag.NewFlagSet("agent", flag.ExitOnError), args)
	c, err := connect()
	if err != nil {
		return err
	}
	conv := ""
	say := func(text string) error {
		var actions []map[string]any
		err := c.Stream(ctx, "/api/admin/agent", map[string]any{"conversation": conv, "text": text}, func(e client.Event) {
			switch e.Kind {
			case "conversation":
				conv = e.ID
			case "tool_call":
				fmt.Println(dim("· " + toolLabel(e.Tool, e.Args)))
			case "action":
				if a, ok := e.Result.(map[string]any); ok {
					actions = append(actions, a)
				}
			case "complete":
				fmt.Println("\n" + e.Text)
			case "error":
				fmt.Println(red(e.Text))
			}
		})
		if err != nil {
			return err
		}
		for _, a := range actions {
			fmt.Printf("\n%s %v\n", bold("待确认："), a["title"])
			if d, _ := a["detail"].(string); d != "" {
				fmt.Println(dim("  " + d))
			}
			if !confirm("执行？") {
				fmt.Println(dim("  没执行"))
				continue
			}
			var res json.RawMessage
			if err := c.Do(ctx, "POST", "/api/admin/actions", a, &res); err != nil {
				fmt.Println(red("  " + err.Error()))
				continue
			}
			var issued struct {
				Token string `json:"token"`
			}
			if json.Unmarshal(res, &issued) == nil && issued.Token != "" {
				fmt.Println(green("  ✓ 令牌（只显示这一次）："))
				fmt.Println("  " + issued.Token)
			} else {
				fmt.Println(green("  ✓ 已执行"))
			}
		}
		return nil
	}
	if len(rest) > 0 {
		return say(strings.Join(rest, " "))
	}
	fmt.Println(dim("工作台助手。说要做什么，空行或 Ctrl-D 退出。"))
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(bold("› "))
		if !in.Scan() {
			fmt.Println()
			return nil
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			return nil
		}
		if err := say(line); err != nil {
			fmt.Println(red(err.Error()))
		}
	}
}
