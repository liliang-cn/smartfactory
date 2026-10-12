package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"smartfactory/internal/client"
	"smartfactory/internal/ops"
)

var sourceText = map[string]string{"measured": "实测", "simulated": "仿真"}

// cliValue: what the twin and its AI did for a company over a period, and the export.
func cliValue(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("value", flag.ExitOnError)
	days := fs.Int("days", 30, "days back from now")
	from := fs.String("from", "", "first day, YYYY-MM-DD (with -to)")
	to := fs.String("to", "", "last day, YYYY-MM-DD, included")
	out := fs.String("csv", "", "write the report as CSV to this file (- for stdout)")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New("smartfactory value <company> [-days 30 | -from 2026-09-01 -to 2026-09-30] [-csv 成效.csv]")
	}
	c, err := connect()
	if err != nil {
		return err
	}
	q := url.Values{}
	if *from != "" || *to != "" {
		q.Set("from", *from)
		q.Set("to", *to)
	} else {
		q.Set("days", strconv.Itoa(*days))
	}
	path := "/api/value/" + client.Q(rest[0]) + "?" + q.Encode()
	if *out != "" {
		q.Set("format", "csv")
		b, err := c.Get(ctx, "/api/value/"+client.Q(rest[0])+"?"+q.Encode())
		if err != nil {
			return err
		}
		if *out == "-" {
			_, err = os.Stdout.Write(b)
			return err
		}
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			return err
		}
		fmt.Println(green("✓ 写到了 " + *out))
		return nil
	}
	var raw json.RawMessage
	if err := c.Do(ctx, "GET", path, nil, &raw); err != nil {
		return err
	}
	if asJSON {
		return printJSON(raw)
	}
	var v ops.Value
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	day := func(t time.Time) string { return t.Local().Format("2006-01-02") }
	fmt.Printf("%s %s %s\n", bold(v.Name+" 的成效"), v.Period.Label, dim(day(v.Period.From)+" 至 "+day(v.Period.To.Add(-time.Second))))
	section := func(title string, s ops.Section) {
		fmt.Println("\n" + bold(title))
		if s.Missing != "" {
			fmt.Println(dim("  " + s.Missing))
		}
		var rows [][]string
		for _, f := range s.Figures {
			val := f.Text()
			if f.Value == nil {
				val = dim(val)
			}
			if f.Source != "" {
				val += dim("（" + sourceText[f.Source] + "）")
			}
			rows = append(rows, []string{f.Label, val, strconv.Itoa(f.N), dim(f.Basis)})
		}
		table([]string{"指标", "数值", "n", "依据"}, rows)
		if s.Note != "" {
			fmt.Println(dim("  " + s.Note))
		}
	}
	section("顾问的建议", v.Suggestions.Section)
	if len(v.Suggestions.Plans) > 0 {
		var rows [][]string
		for _, p := range v.Suggestions.Plans {
			rows = append(rows, []string{p.ID, p.Does, p.Label, valueText(p.Baseline, p.Unit) + " → " + valueText(p.Measured, p.Unit), changeText(p.Change), outcomeText(p)})
		}
		table([]string{"计划", "做什么", "指标", "基线 → 量出", "变化", "验收"}, rows)
	}
	section("AI 的提议和指令", v.AI)
	section("仿真校准", v.Calibration.Section)
	for _, f := range v.Calibration.Fields {
		fmt.Printf("  %s回放误差 %s → %s %s（%d 台，%d 个读数）\n", f.Label, valueText(&f.Before, ""), valueText(&f.After, ""), f.Unit, f.Machines, f.N)
	}
	section("工单", v.Orders)
	section("工厂", v.Plant.Section)
	if len(v.Plant.Weeks) > 0 && v.Plant.Source != "" {
		var rows [][]string
		for _, w := range v.Plant.Weeks {
			rows = append(rows, []string{w.Start.Format("01-02"), strconv.Itoa(w.Days), valueText(w.OEE, "%"), valueText(w.OutputPerDay, ""), valueText(w.DowntimePerDay, ""), valueText(w.Measured, "%")})
		}
		fmt.Println(dim("  每周（" + sourceText[v.Plant.Source] + "，产量和停机按有数据的天平均）"))
		table([]string{"周一", "天数", "OEE", "日均产量", "日均停机 h", "实测占比"}, rows)
	}
	if cp := v.Plant.Compare; cp != nil {
		fmt.Printf("\n%s %s %s，各 %d 天%s\n", bold("第一条采纳的计划前后"), cp.Plan, cp.Does, cp.Days, dim("（"+sourceText[cp.Source]+"）"))
		if cp.Days > 0 && cp.Source != "" {
			table([]string{"", "OEE", "日均产量", "日均停机 h", "有数据的天数"}, [][]string{
				{"采纳前", valueText(cp.Before.OEE, "%"), valueText(cp.Before.OutputPerDay, ""), valueText(cp.Before.DowntimePerDay, ""), strconv.Itoa(cp.Before.Days)},
				{"采纳后", valueText(cp.After.OEE, "%"), valueText(cp.After.OutputPerDay, ""), valueText(cp.After.DowntimePerDay, ""), strconv.Itoa(cp.After.Days)},
			})
		}
		fmt.Println(dim("  " + cp.Note))
	}
	return nil
}

func valueText(v *float64, unit string) string {
	if v == nil {
		return dim("没有数据")
	}
	f := ops.Figure{Value: v, Unit: unit}
	return f.Text()
}

func changeText(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%+.1f%%", *v*100)
}

func outcomeText(p ops.PlanResult) string {
	if !p.Accepted {
		return dim("未验收")
	}
	return map[string]string{"achieved": green("达成"), "missed": red("未达成"), "guard_broken": red("护栏破了"), "no_data": dim("无数据")}[p.Outcome]
}
