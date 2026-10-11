package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"smartfactory/internal/client"
	"smartfactory/internal/guard"
	"smartfactory/internal/ops"
)

var levelLabel = map[guard.Level]string{
	guard.Advisory: "只提议：AI 发给真实设备的指令都要人批准", guard.Guarded: "有护栏：AI 发的列出的指令要人批准，其余过规则就执行",
	guard.Autonomous: "自主：AI 的指令过了规则就执行",
}

// cliGuard: what a company lets through to its machines, and the commands that wait for a person.
func cliGuard(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("guard", flag.ExitOnError)
	level := fs.String("level", "", "set: advisory | guarded | autonomous")
	approve := fs.String("approve", "", "set: commands an AI sends that wait for a person at guarded, comma-separated")
	expire := fs.Int("expire", 0, "set: minutes a command waits before it lapses")
	file := fs.String("f", "", "set: the whole settings as JSON (level, approve, rules, expire); - for stdin")
	reason := fs.String("reason", "", "deny: why")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New("smartfactory guard <company> [show | set -level guarded -approve start,speed | pending | approve <编号> | deny <编号> -reason …]")
	}
	c, err := connect()
	if err != nil {
		return err
	}
	company := client.Q(rest[0])
	verb := "show"
	if len(rest) > 1 {
		verb = rest[1]
	}
	arg := ""
	if len(rest) > 2 {
		arg = rest[2]
	}
	var ov ops.GuardOverview
	load := func() error {
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", "/api/guard/"+company, nil, &raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &ov)
	}
	switch verb {
	case "show":
		if asJSON {
			var raw json.RawMessage
			if err := c.Do(ctx, "GET", "/api/guard/"+company, nil, &raw); err != nil {
				return err
			}
			return printJSON(raw)
		}
		if err := load(); err != nil {
			return err
		}
		s := ov.Settings
		fmt.Printf("%s %s\n", bold(string(s.Level)), dim(levelLabel[s.Level]))
		if s.Level == guard.Guarded {
			fmt.Println("  要批准：" + strings.Join(s.Approve, "、"))
		}
		fmt.Printf("  等待批准的指令 %d 分钟后作废，现在有 %d 条在等\n", s.Expire, ov.Waiting)
		fmt.Printf("  接了真实设备的机器：%s\n", cmpOr(strings.Join(ov.Machines, "、"), "没有（指令只到孪生）"))
		fmt.Println("  规则：")
		for _, r := range s.Rules {
			fmt.Printf("    %-9s %s  %s\n", r.Cmd, r.When, dim(r.Say))
		}
	case "set":
		if err := load(); err != nil {
			return err
		}
		s := ov.Settings
		if *file != "" {
			b, err := readInput(*file)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(b, &s); err != nil {
				return fmt.Errorf("%s 不是 JSON：%w", *file, err)
			}
		}
		if *level != "" {
			s.Level = guard.Level(*level)
		}
		if *approve != "" {
			s.Approve = strings.Split(strings.ReplaceAll(*approve, " ", ""), ",")
		}
		if *expire > 0 {
			s.Expire = *expire
		}
		if err := c.Do(ctx, "PUT", "/api/guard/"+company, s, nil); err != nil {
			return err
		}
		fmt.Printf("%s %s：%s\n", green("✓"), s.Level, levelLabel[s.Level])
	case "pending", "requests":
		status := "waiting"
		if verb == "requests" {
			status = ""
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", "/api/guard/"+company+"/requests?status="+status, nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var list []guard.Request
		json.Unmarshal(raw, &list)
		if len(list) == 0 {
			fmt.Println(dim("没有等待批准的指令"))
			return nil
		}
		rows := make([][]string, len(list))
		for i, r := range list {
			rows[i] = []string{r.ID, ops.StatusLabel[r.Status], r.Plant + "/" + r.Asset, guard.Text(r.Cmd, r.Arg), r.Who, r.Expires.Local().Format("15:04"), dim(r.Context)}
		}
		table([]string{"编号", "状态", "设备", "指令", "谁发的", "有效到", "当时"}, rows)
	case "approve", "deny":
		if arg == "" {
			return fmt.Errorf("smartfactory guard <company> %s <编号>", verb)
		}
		if verb == "approve" && !*yes && !confirm("批准 "+arg+"？指令会下到真实设备") {
			return nil
		}
		var r guard.Request
		body := map[string]string{"reason": *reason}
		if err := c.Do(ctx, "POST", "/api/guard/"+company+"/requests/"+client.Q(arg)+"/"+verb, body, &r); err != nil {
			return err
		}
		mark := green("✓")
		if r.Status != "sent" && verb == "approve" {
			mark = red("✗")
		}
		fmt.Printf("%s %s %s：%s\n", mark, r.ID, ops.StatusLabel[r.Status], r.Result)
	default:
		return fmt.Errorf("guard %s：用 show、set、pending、requests、approve、deny", verb)
	}
	return nil
}
