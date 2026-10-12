package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"smartfactory/internal/client"
	"smartfactory/internal/mqttauth"
	"smartfactory/internal/ops"
)

// cliDevice: devices with identities of their own on the embedded broker — registered for a
// plant's assets, given a token or a certificate once, renewed, revoked.
func cliDevice(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("device", flag.ExitOnError)
	plant := fs.String("plant", "", "add: the plant it belongs to")
	assets := fs.String("assets", "", "add: its assets, comma-separated; RF-* for a prefix, * for the whole plant")
	cert := fs.Bool("cert", false, "add: a client certificate instead of a token")
	out := fs.String("out", "", "add, cert, token: where to write the credential (a directory for a certificate, a file for a token); a token is printed without it")
	all := fs.Bool("all", false, "list: revoked devices too")
	yes := fs.Bool("y", false, "do not ask")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New("smartfactory device <company> [list | add <名字> -plant p1 -assets 'RF-*,HP-101' [-cert] [-out 目录] | cert <名字> [-out 目录] | token <名字> | revoke <名字>]")
	}
	c, err := connect()
	if err != nil {
		return err
	}
	company := client.Q(rest[0])
	base := "/api/admin/devices/" + company
	verb := "list"
	if len(rest) > 1 {
		verb = rest[1]
	}
	name := ""
	if len(rest) > 2 {
		name = rest[2]
	}
	var ov ops.DevicesOverview
	load := func() error {
		var raw json.RawMessage
		if err := c.Do(ctx, "GET", base, nil, &raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &ov)
	}
	find := func() (mqttauth.Device, error) {
		if name == "" {
			return mqttauth.Device{}, fmt.Errorf("smartfactory device <company> %s <名字>", verb)
		}
		if err := load(); err != nil {
			return mqttauth.Device{}, err
		}
		for _, d := range ov.Devices {
			if d.Revoked == nil && (d.Name == name || d.ID == name) {
				return d, nil
			}
		}
		return mqttauth.Device{}, fmt.Errorf("%s 没有登记叫 %s 的设备（或它已经撤销了）", rest[0], name)
	}
	switch verb {
	case "list", "ls":
		if asJSON {
			var raw json.RawMessage
			if err := c.Do(ctx, "GET", base, nil, &raw); err != nil {
				return err
			}
			return printJSON(raw)
		}
		if err := load(); err != nil {
			return err
		}
		var rows [][]string
		for _, d := range ov.Devices {
			if d.Revoked != nil && !*all {
				continue
			}
			state := dim("离线")
			switch {
			case d.Revoked != nil:
				state = dim("已撤销 " + d.Revoked.Local().Format("01-02 15:04"))
			case d.Connected:
				state = green("在线")
			}
			seen := dim("没连过")
			if d.LastSeen != nil {
				seen = d.LastSeen.Local().Format("01-02 15:04")
			}
			refused := ""
			if d.Refused > 0 {
				refused = red(fmt.Sprintf("%d 次", d.Refused)) + " " + dim(d.LastRefusal)
			}
			rows = append(rows, []string{bold(d.Name), d.Plant, strings.Join(d.Assets, "、"), credLabel[d.Kind], state, seen, refused})
		}
		if len(rows) == 0 {
			fmt.Println(dim("还没有登记设备"))
		} else {
			table([]string{"设备", "厂", "管哪些设备", "凭据", "状态", "最近", "被拒"}, rows)
		}
		for _, u := range ov.Shared {
			fmt.Printf("%s %s %s\n", dim("共用账号"), u, red("能发、能订阅任何主题"))
		}
		brokerLine(ov.Broker)
	case "add":
		if name == "" || *plant == "" || *assets == "" {
			return errors.New("smartfactory device <company> add <名字> -plant p1 -assets 'RF-*,HP-101' [-cert] [-out 目录]")
		}
		kind := mqttauth.Token
		if *cert {
			kind = mqttauth.Cert
		}
		in := ops.DeviceInput{Name: name, Plant: *plant, Assets: strings.Split(*assets, ","), Kind: kind}
		var res ops.DeviceIssued
		if err := c.Do(ctx, "POST", base, in, &res); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s 登记了 %s：%s 的 %s\n", green("✓"), res.Device.Name, res.Device.Plant, strings.Join(res.Device.Assets, "、"))
		if err := keepCredential(res, *out); err != nil {
			return err
		}
		if err := load(); err == nil {
			brokerLine(ov.Broker)
		}
	case "cert", "token":
		d, err := find()
		if err != nil {
			return err
		}
		if want := map[string]string{"cert": mqttauth.Cert, "token": mqttauth.Token}[verb]; d.Kind != want {
			return fmt.Errorf("%s 用的是%s，用 smartfactory device %s %s %s 换一个新的", d.Name, credLabel[d.Kind], rest[0], map[string]string{mqttauth.Cert: "cert", mqttauth.Token: "token"}[d.Kind], d.Name)
		}
		if !*yes && !confirm("给 "+d.Name+" 换新的"+credLabel[d.Kind]+"？旧的立即失效，设备会断开") {
			return nil
		}
		var res ops.DeviceIssued
		if err := c.Do(ctx, "POST", base+"/"+client.Q(d.ID)+"/renew", nil, &res); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s %s 换了新的%s，旧的已经失效\n", green("✓"), d.Name, credLabel[d.Kind])
		return keepCredential(res, *out)
	case "revoke":
		d, err := find()
		if err != nil {
			return err
		}
		if !*yes && !confirm("撤销 "+d.Name+"？它立即断开，凭据再也不能用") {
			return nil
		}
		if err := c.Do(ctx, "DELETE", base+"/"+client.Q(d.ID), nil, nil); err != nil {
			return err
		}
		fmt.Println(green("✓"), "已撤销", d.Name)
	default:
		return fmt.Errorf("device %s：用 list、add、cert、token、revoke", verb)
	}
	return nil
}

var credLabel = map[string]string{mqttauth.Token: "令牌", mqttauth.Cert: "证书"}

// keepCredential hands over what the server gives once: a certificate, its key and the CA go
// to files only this user can read; a token is printed, or written to a file.
func keepCredential(res ops.DeviceIssued, out string) error {
	if asJSON {
		return printJSON(res)
	}
	name := res.Device.Name
	if res.Cert != "" {
		dir := out
		if dir == "" {
			dir = name
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		for f, body := range map[string]string{name + ".crt": res.Cert, name + ".key": res.Key, "ca.crt": res.CA} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o600); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "  证书、私钥和 CA 在 %s（只显示这一次，服务器不留私钥）：%s.crt、%s.key、ca.crt\n", dir, name, name)
		fmt.Fprintf(os.Stderr, "  设备用这张证书连 broker 的 TLS 端口，client id 填 %s，不用密码\n", name)
		return nil
	}
	if out != "" {
		if err := os.WriteFile(out, []byte(res.Token+"\n"), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  令牌在 %s（只显示这一次）\n", out)
	} else {
		fmt.Fprintln(os.Stderr, "  令牌（只显示这一次）：")
		fmt.Println(res.Token)
	}
	fmt.Fprintf(os.Stderr, "  设备连 MQTT 时 client id 和用户名都填 %s，密码填令牌\n", name)
	return nil
}

func brokerLine(b ops.BrokerView) {
	if b.Listen == "" {
		fmt.Println(red("这台服务器没有开内置 broker（配置 mqtt.listen）"))
		return
	}
	_, port, _ := net.SplitHostPort(b.Listen)
	line := "broker 端口 " + port
	if b.TLS != "" {
		_, tp, _ := net.SplitHostPort(b.TLS)
		line += "，TLS 端口 " + tp
	}
	if b.Local {
		line += red("（现在只接受本机连接）")
	}
	fmt.Println(dim(line + "，主题前缀 " + b.Prefix))
}
