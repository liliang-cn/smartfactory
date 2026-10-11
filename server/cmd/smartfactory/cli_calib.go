package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"smartfactory/internal/calib"
	"smartfactory/internal/client"
)

var qualityLabel = map[string]string{"good": "可信", "fair": "参考", "poor": "粗略"}
var fieldLabel = map[string]string{"temp": "温度", "power": "功率", "vib": "振动"}

// cliCalib: each machine's simulation parameters against its measurements — fitted, applied,
// taken back.
func cliCalib(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("calib", flag.ExitOnError)
	hours := fs.Float64("hours", 24, "fit: hours of readings to fit from")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New("smartfactory calib <company> [show <设备> | fit [-hours 24] [设备…] | apply [设备…] | revert [设备…]]")
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
	base := "/api/admin/calib/" + company
	switch verb {
	case "":
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", base, nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var ms []calib.Machine
		json.Unmarshal(raw, &ms)
		var rows [][]string
		for _, m := range ms {
			if !m.Stored && m.AppliedAt == nil && len(m.Residual) == 0 {
				continue
			}
			fitted := 0
			for _, p := range m.Params {
				if p.Fitted != nil {
					fitted++
				}
			}
			state := dim("没拟合")
			if m.FittedAt != nil {
				state = fmt.Sprintf("拟合 %d 项", fitted)
			}
			if m.AppliedAt != nil {
				state = green("在用") + "，" + state
			}
			var res []string
			for _, x := range m.Residual {
				res = append(res, fmt.Sprintf("%s %.2f", fieldLabel[x.Field], x.MAE))
			}
			live := dim("—")
			if m.Live {
				live = green("实测")
			}
			rows = append(rows, []string{m.Asset, m.Label, m.Line, live, state, strings.Join(res, "  ")})
		}
		if len(rows) == 0 {
			fmt.Println(dim("没有设备上报过实测数据，没有可校准的"))
			return nil
		}
		table([]string{"设备", "类型", "产线", "数据", "校准", "近 24 小时模型误差"}, rows)
	case "show":
		if len(machines) == 0 {
			return errors.New("smartfactory calib <company> show <设备>")
		}
		var ms []calib.Machine
		if err := c.Do(ctx, "GET", base, nil, &ms); err != nil {
			return err
		}
		for _, m := range ms {
			if m.Asset != machines[0] && m.Plant+"/"+m.Asset != machines[0] {
				continue
			}
			if asJSON {
				return printJSON(m)
			}
			fmt.Printf("%s %s %s\n", bold(m.Asset), m.Label, dim(m.PlantName+" "+m.Line))
			rows := [][]string{}
			for _, p := range m.Params {
				f, a := dim("—"), ""
				if p.Fitted != nil {
					f = fmt.Sprintf("%g", *p.Fitted)
					if q := qualityLabel[p.Quality]; q != "" {
						f += dim(fmt.Sprintf(" %s，%d", q, p.N))
					}
				} else if p.Note != "" {
					f = dim(p.Note)
				}
				if p.Applied != nil {
					a = green(fmt.Sprintf("%g", *p.Applied))
				}
				rows = append(rows, []string{p.Label, fmt.Sprintf("%g %s", p.Template, p.Unit), f, a})
			}
			table([]string{"参数", "模板", "拟合", "在用"}, rows)
			for _, r := range m.Replay {
				fmt.Printf("  %s 回放误差：模板 %.3f → 拟合 %.3f（%d 个读数）\n", fieldLabel[r.Field], r.Before, r.After, r.N)
			}
			return nil
		}
		return fmt.Errorf("没有设备 %s", machines[0])
	case "fit":
		var raw json.RawMessage
		if err := c.Do(ctx, "POST", base+"/fit", map[string]any{"machines": machines, "hours": *hours}, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var ms []calib.Machine
		json.Unmarshal(raw, &ms)
		if len(ms) == 0 {
			fmt.Println(dim("没有拟合任何设备：这段时间没有存下的实测读数"))
		}
		for _, m := range ms {
			n := 0
			for _, p := range m.Params {
				if p.Fitted != nil {
					n++
				}
			}
			var rp []string
			for _, r := range m.Replay {
				rp = append(rp, fmt.Sprintf("%s %.2f→%.2f", fieldLabel[r.Field], r.Before, r.After))
			}
			fmt.Printf("%s %s 拟合 %d 项  %s\n", green("✓"), m.Asset, n, dim(strings.Join(rp, "  ")))
		}
	case "apply", "revert":
		which := "所有拟合过的设备"
		if len(machines) > 0 {
			which = strings.Join(machines, "、")
		}
		q := "应用 " + which + " 的拟合参数？仿真和推演改用它们"
		if verb == "revert" {
			q = "撤销 " + which + " 的校准，恢复模板参数？"
		}
		if !*yes && !confirm(q) {
			return nil
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "POST", base+"/"+verb, map[string]any{"machines": machines}, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var out []calib.Outcome
		json.Unmarshal(raw, &out)
		if len(out) == 0 {
			fmt.Println(dim("没有要处理的设备"))
		}
		for _, o := range out {
			if o.OK {
				fmt.Printf("%s %s\n", green("✓"), o.Machine)
			} else {
				fmt.Printf("%s %s %s\n", red("✗"), o.Machine, o.Error)
			}
		}
	default:
		return fmt.Errorf("calib %s：用 show、fit、apply、revert", verb)
	}
	return nil
}
