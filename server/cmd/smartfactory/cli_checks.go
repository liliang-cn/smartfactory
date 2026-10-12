package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"smartfactory/internal/checks"
	"smartfactory/internal/client"
	"smartfactory/internal/ops"
)

var roundLabel = map[string]string{"overdue": "逾期", "due": "待做", "done": "已做", "open": "不定期"}
var everyLabel = map[string]string{"": "不定期", "shift": "每班", "day": "每天", "hours": "每几小时"}

// cliChecks: the forms people fill in by hand at the machines, the rounds that are overdue, and
// what was written.
func cliChecks(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("checks", flag.ExitOnError)
	file := fs.String("f", "", "save, submit: the form or the filled-in values as JSON; - for stdin")
	status := fs.String("status", "overdue,due", "rounds: overdue, due, done, open, comma-separated; all for every round")
	form := fs.String("form", "", "results, export: only this form (F-…)")
	asset := fs.String("asset", "", "results, export: only this machine")
	hours := fs.Int("hours", 24, "results: how far back")
	bad := fs.Bool("bad", false, "results, export: only those with a value out of spec or abnormal")
	n := fs.Int("n", 30, "results: at most this many")
	from := fs.String("from", "", "export: from this day, YYYY-MM-DD")
	to := fs.String("to", "", "export: up to and including this day, YYYY-MM-DD")
	out := fs.String("o", "", "export: the CSV file (default: stdout)")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New("smartfactory checks <company> [forms | form <编号> | template 点检|首件检验|化验 | save -f form.json | rm <编号> | rounds [-status overdue,due] | results [-form F-…] [-asset HP-101] [-hours 24] [-bad] | export [-from 2026-10-01] [-to 2026-10-12] [-o file.csv] | submit -f values.json]")
	}
	c, err := connect()
	if err != nil {
		return err
	}
	company := client.Q(rest[0])
	base := "/api/checks/" + company
	verb := "show"
	if len(rest) > 1 {
		verb = rest[1]
	}
	arg := ""
	if len(rest) > 2 {
		arg = rest[2]
	}
	get := func(p string, v any) error {
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", p, nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		return json.Unmarshal(raw, v)
	}
	switch verb {
	case "show", "forms":
		var ov ops.ChecksOverview
		if err := get(base, &ov); err != nil || asJSON {
			return err
		}
		if len(ov.Forms) == 0 {
			fmt.Println(dim("还没有表单：smartfactory checks " + rest[0] + " template 点检 > form.json，改好后 save -f form.json"))
			return nil
		}
		count := map[string][2]int{}
		for _, m := range ov.Machines {
			for _, r := range m.Rounds {
				x := count[r.Form]
				x[0]++
				if r.Status == "overdue" {
					x[1]++
				}
				count[r.Form] = x
			}
		}
		rows := make([][]string, len(ov.Forms))
		for i, f := range ov.Forms {
			late := dim("0")
			if count[f.ID][1] > 0 {
				late = red(strconv.Itoa(count[f.ID][1]))
			}
			rows[i] = []string{f.ID, f.Name, ov.Kinds[f.Kind], every(f.Schedule), fmt.Sprintf("%d 项", len(f.Fields)), fmt.Sprintf("%d 台", count[f.ID][0]), late}
		}
		table([]string{"编号", "名称", "类型", "周期", "内容", "设备", "逾期"}, rows)
	case "form":
		if arg == "" {
			return errors.New("smartfactory checks <company> form <编号>")
		}
		var ov ops.ChecksOverview
		if err := get(base, &ov); err != nil || asJSON {
			return err
		}
		for _, f := range ov.Forms {
			if f.ID == arg {
				return printJSON(f)
			}
		}
		return fmt.Errorf("没有表单 %s", arg)
	case "template":
		var list []checks.Form
		if err := c.Do(ctx, "GET", base+"/templates", nil, &list); err != nil {
			return err
		}
		var names []string
		for _, f := range list {
			if arg == "" || f.Name == arg || strings.Contains(f.Name, arg) {
				return printJSON(f)
			}
			names = append(names, f.Name)
		}
		return fmt.Errorf("没有模板 %s；有 %s", arg, strings.Join(names, "、"))
	case "save":
		if *file == "" {
			return errors.New("smartfactory checks <company> save -f form.json")
		}
		b, err := readInput(*file)
		if err != nil {
			return err
		}
		var f checks.Form
		if err := json.Unmarshal(b, &f); err != nil {
			return fmt.Errorf("%s 不是 JSON：%w", *file, err)
		}
		var saved checks.Form
		if err := c.Do(ctx, "POST", base+"/forms", f, &saved); err != nil {
			return err
		}
		fmt.Printf("%s %s %s，%d 项，%s\n", green("✓"), saved.ID, saved.Name, len(saved.Fields), every(saved.Schedule))
	case "rm":
		if arg == "" {
			return errors.New("smartfactory checks <company> rm <编号>")
		}
		if !*yes && !confirm("停用表单 "+arg+"？填过的记录会保留") {
			return nil
		}
		if err := c.Do(ctx, "DELETE", base+"/forms/"+client.Q(arg), nil, nil); err != nil {
			return err
		}
		fmt.Println(green("✓") + " 已停用 " + arg)
	case "rounds", "overdue":
		st := *status
		if verb == "overdue" {
			st = "overdue"
		}
		if st == "all" {
			st = ""
		}
		var list []checks.Round
		if err := get(base+"/rounds?status="+url.QueryEscape(st), &list); err != nil || asJSON {
			return err
		}
		if len(list) == 0 {
			fmt.Println(dim("没有这样的轮次"))
			return nil
		}
		rows := make([][]string, len(list))
		for i, r := range list {
			s := roundLabel[r.Status]
			when := ""
			switch r.Status {
			case "overdue":
				s = red(s)
				if r.Since != nil {
					when = "从 " + clock(*r.Since) + " 起"
				}
			case "due":
				if r.Due != nil {
					when = clock(*r.Due) + " 前"
				}
			case "done":
				s = green(s)
			}
			last := dim("没填过")
			if r.Last != nil {
				last = clock(*r.Last) + " " + r.LastBy
			}
			rows[i] = []string{s, r.Asset, r.AssetName, r.FormName, when, last}
		}
		table([]string{"状态", "设备", "名称", "表单", "时间", "上次"}, rows)
	case "results":
		q := url.Values{"limit": {strconv.Itoa(*n)}, "from": {strconv.FormatInt(time.Now().Add(-time.Duration(*hours)*time.Hour).UnixMilli(), 10)}}
		filter(q, *form, *asset, *bad)
		var list []checks.Submission
		if err := get(base+"/submissions?"+q.Encode(), &list); err != nil || asJSON {
			return err
		}
		if len(list) == 0 {
			fmt.Println(dim("这段时间没有记录"))
			return nil
		}
		for _, s := range list {
			mark := green("✓")
			if s.Bad > 0 {
				mark = red(fmt.Sprintf("✗ %d 项异常", s.Bad))
			}
			late := ""
			if s.Late {
				late = dim("（" + clock(s.Received) + " 补发）")
			}
			fmt.Printf("%s  %s %s  %s  %s%s  %s\n", clock(s.Taken), s.Asset, s.AssetName, bold(s.FormName), s.By, late, mark)
			for _, v := range s.Values {
				fmt.Println("    " + valueText(v))
			}
			if s.Note != "" {
				fmt.Println("    " + dim("备注 "+s.Note))
			}
		}
	case "export":
		q := url.Values{"format": {"csv"}}
		filter(q, *form, *asset, *bad)
		for _, d := range []struct {
			key, day string
			next     bool
		}{{"from", *from, false}, {"to", *to, true}} {
			if d.day == "" {
				continue
			}
			t, err := time.ParseInLocation("2006-01-02", d.day, time.Local)
			if err != nil {
				return fmt.Errorf("-%s 要写成 2026-10-01", d.key)
			}
			if d.next {
				t = t.AddDate(0, 0, 1)
			}
			q.Set(d.key, strconv.FormatInt(t.UnixMilli(), 10))
		}
		var w io.Writer = os.Stdout
		if *out != "" {
			f, err := os.Create(*out)
			if err != nil {
				return err
			}
			defer f.Close()
			w = f
		}
		if err := c.Fetch(ctx, base+"/submissions?"+q.Encode(), w); err != nil {
			return err
		}
		if *out != "" {
			fmt.Fprintln(os.Stderr, green("✓")+" "+*out)
		}
	case "submit":
		if *file == "" {
			return errors.New("smartfactory checks <company> submit -f values.json（{form, plant, asset, values: {项: 值}, taken?}）")
		}
		b, err := readInput(*file)
		if err != nil {
			return err
		}
		var in checks.Input
		if err := json.Unmarshal(b, &in); err != nil {
			return fmt.Errorf("%s 不是 JSON：%w", *file, err)
		}
		var s checks.Submission
		if err := c.Do(ctx, "POST", base+"/submissions", in, &s); err != nil {
			return err
		}
		mark := green("✓")
		if s.Bad > 0 {
			mark = red(fmt.Sprintf("✗ %d 项异常", s.Bad))
		}
		fmt.Printf("%s %s %s %s\n", mark, s.ID, s.Asset, s.FormName)
		for _, v := range s.Values {
			fmt.Println("    " + valueText(v))
		}
	default:
		return fmt.Errorf("checks %s：用 forms、form、template、save、rm、rounds、overdue、results、export、submit", verb)
	}
	return nil
}

func filter(q url.Values, form, asset string, bad bool) {
	if form != "" {
		q.Set("form", form)
	}
	if asset != "" {
		q.Set("asset", asset)
	}
	if bad {
		q.Set("bad", "1")
	}
}

func every(s checks.Schedule) string {
	if s.Every == "hours" {
		return fmt.Sprintf("每 %d 小时", s.Hours)
	}
	return everyLabel[s.Every]
}

func clock(t time.Time) string { return t.Local().Format("01-02 15:04") }

func valueText(v checks.Value) string {
	x := v.Text
	switch {
	case v.Kind == checks.Photo:
		x = dim("照片 " + v.Text)
	case v.Num != nil:
		x = strings.TrimSpace(strconv.FormatFloat(*v.Num, 'f', -1, 64) + " " + v.Unit)
	}
	line := v.Label + " " + x
	switch {
	case v.Bad && v.Num != nil:
		line = red(line + "（" + v.Why + "）")
	case v.Bad:
		line = red(line + "（异常）")
	}
	if v.Flag != "" {
		line += dim("  读数不可信：" + v.Flag)
	}
	return line
}
