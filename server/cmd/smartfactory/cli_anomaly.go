package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"smartfactory/internal/anomaly"
	"smartfactory/internal/client"
	"smartfactory/internal/ops"
)

var hintKind = map[string]string{anomaly.Level: "持续偏离", anomaly.Drift: "缓慢漂移"}

// cliAnomalies: readings of measured machines that are unusual for them, each machine's normals,
// and learning a machine again.
func cliAnomalies(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("anomalies", flag.ExitOnError)
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New("smartfactory anomalies <company> [show <设备> | reset <设备…>]")
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
	machines := []string{}
	if len(rest) > 2 {
		machines = rest[2:]
	}
	var raw json.RawMessage
	var rep ops.AnomalyReport
	load := func() error {
		if err := c.Do(ctx, "GET", "/api/anomalies/"+company, nil, &raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &rep)
	}
	switch verb {
	case "":
		if err := load(); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		s := rep.Settings
		fmt.Println(dim(fmt.Sprintf("和每台设备自己在同一状态下的平常读数比：偏离 %g 个标准差持续 %s，或近期平均慢慢偏离，就提示一次；每个状态先学 %s。只是提示，设备照常运行。",
			s.K, time.Duration(s.Sustain*float64(time.Second)), time.Duration(s.Warmup*float64(time.Second)))))
		if len(rep.Current) == 0 {
			fmt.Println(green("现在没有读数异常"))
		} else {
			rows := make([][]string, len(rep.Current))
			for i, a := range rep.Current {
				rows[i] = []string{a.Plant + "/" + a.Asset, anomaly.FieldLabel(a.Field), hintKind[a.Kind], red(fmt.Sprintf("%+.1fσ", a.Z)),
					fmt.Sprintf("%s±%s", short(a.Mean), short(a.Sigma)), anomaly.StateLabel(a.State), a.Since.Local().Format("01-02 15:04")}
			}
			table([]string{"设备", "读数", "怎么了", "现在", "平常", "状态", "开始"}, rows)
		}
		var ended [][]string
		for _, a := range rep.Recent {
			if a.Ended != nil && len(ended) < 20 {
				ended = append(ended, []string{a.Since.Local().Format("01-02 15:04"), a.Ended.Sub(a.Since).Round(time.Second).String(), dim(a.Text)})
			}
		}
		if len(ended) > 0 {
			fmt.Println("\n" + bold("最近结束的"))
			table([]string{"开始", "持续", "提示"}, ended)
		}
		learning := 0
		for _, m := range rep.Machines {
			for _, f := range m.Fields {
				if f.Now != nil && !f.Now.Warm {
					learning++
				}
			}
		}
		fmt.Println(dim(fmt.Sprintf("\n%d 台设备有实测读数，%d 个读数还在学平常值", len(rep.Machines), learning)))
	case "show":
		if len(machines) == 0 {
			return errors.New("smartfactory anomalies <company> show <设备>")
		}
		if err := load(); err != nil {
			return err
		}
		for _, m := range rep.Machines {
			if m.Asset != machines[0] && m.Plant+"/"+m.Asset != machines[0] {
				continue
			}
			if asJSON {
				return printJSON(m)
			}
			fmt.Printf("%s %s %s\n", bold(m.Plant+"/"+m.Asset), anomaly.StateLabel(m.State), dim("来自 "+m.Source))
			var rows [][]string
			for _, f := range m.Fields {
				z := dim("学习中")
				if f.Now != nil && f.Now.Warm {
					z = fmt.Sprintf("%+.1fσ", f.Z)
					if f.Anomaly != nil {
						z = red(z + " " + hintKind[f.Anomaly.Kind])
					}
				}
				for i, b := range f.States {
					name, now, cur := "", "", ""
					if i == 0 {
						name, now, cur = anomaly.FieldLabel(f.Field), short(f.Value), z
					}
					normal := fmt.Sprintf("%s±%s", short(b.Mean), short(b.Sigma))
					if !b.Warm {
						normal += dim(fmt.Sprintf("（学了 %.0f%%）", b.Progress*100))
					}
					rows = append(rows, []string{name, now, cur, anomaly.StateLabel(b.State), normal, fmt.Sprint(b.N)})
				}
			}
			table([]string{"读数", "现在", "偏离", "状态", "平常", "读数个数"}, rows)
			return nil
		}
		return fmt.Errorf("%s 还没有收到过设备读数", machines[0])
	case "reset":
		if len(machines) == 0 {
			return errors.New("smartfactory anomalies <company> reset <设备…>")
		}
		if !*yes && !confirm("忘掉 "+strings.Join(machines, "、")+" 学过的平常读数，从现在起重新学习？学满之前不提示") {
			return nil
		}
		var out []ops.ResetOutcome
		if err := c.Do(ctx, "POST", "/api/admin/anomalies/"+company+"/reset", map[string]any{"machines": machines}, &out); err != nil {
			return err
		}
		if asJSON {
			return printJSON(out)
		}
		for _, o := range out {
			if o.OK {
				fmt.Printf("%s %s 重新学习\n", green("✓"), o.Machine)
			} else {
				fmt.Printf("%s %s %s\n", red("✗"), o.Machine, o.Error)
			}
		}
	default:
		return fmt.Errorf("anomalies %s：用 show、reset", verb)
	}
	return nil
}

// short is a number without trailing zeros, three decimals at most.
func short(x float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", x), "0"), ".")
}
