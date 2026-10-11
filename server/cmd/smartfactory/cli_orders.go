package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"smartfactory/internal/client"
	"smartfactory/internal/erp"
)

var statusLabel = map[string]string{erp.Queued: "排队", erp.Running: "生产中", erp.Done: "完成", erp.Cancelled: "已撤回"}

// cliOrders: a company's production orders from its ERP, and the connection that brings them.
func cliOrders(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("orders", flag.ExitOnError)
	status := fs.String("status", "", "queued,running,done,cancelled")
	q := fs.String("q", "", "find in number, part, customer, line")
	file := fs.String("f", "", "orders to send (JSON: one, a list, or {orders: [...]}); - for stdin")
	dry := fs.Bool("dry", false, "pull: show what it reads, store nothing")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New("smartfactory orders <company> [show <工单号> | send -f orders.json | cancel <工单号> | pull [-dry] | erp]")
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
	arg := ""
	if len(rest) > 2 {
		arg = rest[2]
	}
	switch verb {
	case "":
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", fmt.Sprintf("/api/erp/%s/orders?status=%s&q=%s", company, urlQ(*status), urlQ(*q)), nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var list []erp.Order
		json.Unmarshal(raw, &list)
		if len(list) == 0 {
			fmt.Println(dim("没有工单"))
			return nil
		}
		rows := make([][]string, len(list))
		for i, o := range list {
			rows[i] = []string{o.No, statusText(o), cmpOr(o.PartNo, o.Product), fmt.Sprintf("%d/%d", o.Good, o.Qty), day(o.Due), o.Customer, cmpOr(o.LineName, red(o.Problem))}
		}
		table([]string{"工单", "状态", "料号", "完成", "交期", "客户", "产线"}, rows)
	case "show":
		if arg == "" {
			return errors.New("smartfactory orders <company> show <工单号>")
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", "/api/erp/"+company+"/orders/"+client.Q(arg), nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var o erp.Order
		json.Unmarshal(raw, &o)
		fmt.Printf("%s %s\n", bold(o.No), statusText(o))
		fmt.Printf("  做：%s × %d，合格 %d，报废 %d\n", strings.TrimSpace(cmpOr(o.PartNo, o.Product)+" "+o.Name), o.Qty, o.Good, o.Scrap)
		fmt.Printf("  在：%s %s\n", o.PlantName, cmpOr(o.LineName, red(o.Problem)))
		if o.Customer != "" || o.Due != nil {
			fmt.Printf("  给：%s，交期 %s\n", cmpOr(o.Customer, "—"), day(o.Due))
		}
		fmt.Println(dim(fmt.Sprintf("  来自 %s，%s 收到", map[string]string{"push": "ERP 推送", "pull": "ERP 拉取", "console": "后台录入"}[o.Source], o.Created.Local().Format("01-02 15:04"))))
	case "send":
		var b []byte
		switch *file {
		case "":
			return errors.New("smartfactory orders <company> send -f orders.json（- 读标准输入）")
		case "-":
			b, err = io.ReadAll(os.Stdin)
		default:
			b, err = os.ReadFile(*file)
		}
		if err != nil {
			return err
		}
		var body any
		if err := json.Unmarshal(b, &body); err != nil {
			return fmt.Errorf("%s 不是 JSON：%w", *file, err)
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "POST", "/api/erp/"+company+"/orders", body, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var res []erp.Result
		json.Unmarshal(raw, &res)
		for _, r := range res {
			if r.OK {
				fmt.Printf("%s %s %s %s\n", green("✓"), r.No, map[erp.Outcome]string{erp.Created: "收下", erp.Updated: "已更新", erp.Unchanged: "没有变化", erp.Requeued: "重新排队"}[r.Outcome], dim(r.LineName))
			} else {
				fmt.Printf("%s %s %s\n", red("✗"), cmpOr(r.No, "（没有工单号）"), r.Error)
			}
		}
	case "cancel":
		if arg == "" {
			return errors.New("smartfactory orders <company> cancel <工单号>")
		}
		if !*yes && !confirm("撤回工单 "+arg+"？在做的产线会停下它，转做下一张") {
			return nil
		}
		if err := c.Do(ctx, "DELETE", "/api/erp/"+company+"/orders/"+client.Q(arg), nil, nil); err != nil {
			return err
		}
		fmt.Printf("%s 已撤回 %s\n", green("✓"), arg)
	case "pull":
		p := "/api/admin/erp/" + company + "/pull"
		if *dry {
			p += "?dry=1"
		}
		var raw json.RawMessage
		if err := c.Do(ctx, "POST", p, nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var res erp.PullResult
		json.Unmarshal(raw, &res)
		for _, it := range res.Items {
			if it.Error != "" {
				fmt.Printf("%s %s %s\n", red("✗"), cmpOr(it.Order.No, "（没有工单号）"), it.Error)
			} else {
				fmt.Printf("%s %s %s × %d %s\n", green("✓"), it.Order.No, cmpOr(it.Order.PartNo, it.Order.Product), it.Order.Qty, dim(it.LineName))
			}
		}
		if *dry {
			fmt.Println(dim(fmt.Sprintf("读到 %d 张，试拉，没有存", res.Fetched)))
		} else {
			fmt.Println(dim(fmt.Sprintf("读到 %d 张：新增 %d，更新 %d，失败 %d", res.Fetched, res.Created, res.Updated, res.Failed)))
		}
	case "erp":
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", "/api/admin/erp/"+company, nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var ov struct {
			Config struct {
				erp.Config
				SecretSet bool
			}
			Status erp.Status
			Counts map[string]int
		}
		json.Unmarshal(raw, &ov)
		cfg := ov.Config
		if !cfg.Enabled {
			fmt.Println("没有接 ERP：产线自己排工单（仿真）")
		} else {
			fmt.Printf("%s 已接 ERP，没有工单时%s\n", green("●"), map[string]string{"idle": "停线等单", "simulate": "照常仿真"}[cfg.WhenEmpty])
		}
		fmt.Println("  推送：POST /api/erp/" + rest[0] + "/orders，用操作员令牌")
		if p := cfg.Pull; p != nil {
			every := "手动"
			if p.Every > 0 {
				every = fmt.Sprintf("每 %d 分钟", p.Every)
			}
			fmt.Printf("  拉取：%s %s（%s）\n", p.Method, p.URL, every)
			if st := ov.Status; st.LastPull != nil {
				fmt.Printf("    上次 %s，读到 %d 张%s\n", st.LastPull.Local().Format("01-02 15:04"), st.PullFetched, errText(st.PullError))
			}
		}
		if n := cfg.Notify; n != nil {
			fmt.Printf("  回调：%s（成功 %d，失败 %d）%s\n", n.URL, ov.Status.Delivered, ov.Status.Failed, errText(ov.Status.NotifyError))
		}
		fmt.Printf("  工单：排队 %d，生产中 %d，完成 %d，撤回 %d\n", ov.Counts["queued"], ov.Counts["running"], ov.Counts["done"], ov.Counts["cancelled"])
	default:
		return fmt.Errorf("orders %s：用 show、send、cancel、pull、erp", verb)
	}
	return nil
}

func statusText(o erp.Order) string {
	s := statusLabel[o.Status]
	switch o.Status {
	case erp.Running:
		return blue(s)
	case erp.Done:
		return green(s)
	case erp.Queued:
		if o.Problem != "" {
			return red("排不上")
		}
	}
	return s
}

func day(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("01-02 15:04")
}

func errText(e string) string {
	if e == "" {
		return ""
	}
	return "，" + red(e)
}
