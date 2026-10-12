package main

// `smartfactory install` sets a company's own server up as a systemd service: the binary in
// /usr/local/bin, a system user, its data under /var/lib/smartfactory, its config and
// environment under /etc/smartfactory, a hardened unit, started. Run again with a newer binary
// it upgrades (the config, the environment, the unit and the data are kept). `uninstall`
// removes the service and keeps the data unless told otherwise.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	installBin  = "/usr/local/bin/smartfactory"
	installEtc  = "/etc/smartfactory"
	installData = "/var/lib/smartfactory"
	installUnit = "/etc/systemd/system/smartfactory.service"
	installUser = "smartfactory"
)

const unitFile = `[Unit]
Description=SmartFactory (digital twin, console and assistants)
Documentation=https://github.com/liliang-cn/smartfactory
After=network-online.target
Wants=network-online.target

[Service]
User=smartfactory
Group=smartfactory
Environment=HOME=/var/lib/smartfactory
# LLM_BASE_URL, LLM_API_KEY, LLM_MODEL for the assistants; ${VAR}s the config refers to
EnvironmentFile=-/etc/smartfactory/env
ExecStart=/usr/local/bin/smartfactory -config /etc/smartfactory/smartfactory.json
WorkingDirectory=/var/lib/smartfactory
Restart=on-failure
RestartSec=3
TimeoutStopSec=30
LimitNOFILE=65536

NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/smartfactory
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
LockPersonality=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictRealtime=yes
CapabilityBoundingSet=
ProtectClock=yes
ProtectHostname=yes
ProtectKernelLogs=yes
ProtectProc=invisible
ProcSubset=pid
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
UMask=0027

[Install]
WantedBy=multi-user.target
`

const envFile = `# SmartFactory 的环境变量（systemd 启动时读）。改完：sudo systemctl restart smartfactory
#
# 助手和 AI 搭建要一个兼容 OpenAI 接口的大模型：
# LLM_BASE_URL=https://api.example.com/v1
# LLM_API_KEY=
# LLM_MODEL=
#
# 要经代理才能出网的（大模型、ERP 接口）：
# HTTPS_PROXY=http://代理地址:端口
# NO_PROXY=localhost,127.0.0.1,内网网段
`

func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	httpAddr := fs.String("http", ":4720", "where the console and the twin listen (first install only)")
	mqtt := fs.String("mqtt", ":41883", "the MQTT broker devices report to; empty for none (first install only)")
	mqttTLS := fs.String("mqtt-tls", ":48883", "the broker's TLS listener, for devices with a certificate; empty for none (first install only)")
	unit := fs.Bool("unit", false, "only print the systemd unit (to install by hand)")
	fs.Parse(args)
	if *unit {
		fmt.Print(unitFile)
		return nil
	}
	if err := systemdHost(); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	upgrade := fileExists(installUnit)

	// the binary
	if self != installBin {
		if err := copyFile(self, installBin+".new"); err != nil {
			return err
		}
		os.Chmod(installBin+".new", 0o755)
		if err := os.Rename(installBin+".new", installBin); err != nil {
			return err
		}
	}
	// the user and the directories
	if _, err := user.Lookup(installUser); err != nil {
		if out, err := exec.Command("useradd", "--system", "--home-dir", installData, "--shell", "/usr/sbin/nologin", "--user-group", installUser).CombinedOutput(); err != nil {
			return fmt.Errorf("useradd: %s", strings.TrimSpace(string(out)))
		}
	}
	u, _ := user.Lookup(installUser)
	var uid, gid int
	fmt.Sscan(u.Uid, &uid)
	fmt.Sscan(u.Gid, &gid)
	for _, d := range []struct {
		path string
		mode os.FileMode
	}{{installEtc, 0o750}, {installData, 0o750}} {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return err
		}
		os.Chmod(d.path, d.mode)
	}
	os.Chown(installEtc, 0, gid)
	if err := os.Chown(installData, uid, gid); err != nil {
		return err
	}
	// the config: written once, kept on upgrades
	cfgPath := filepath.Join(installEtc, "smartfactory.json")
	if !fileExists(cfgPath) {
		cfg := map[string]any{"http": *httpAddr, "data": installData}
		if *mqtt != "" {
			// no shared password: each device is registered in the console and gets a token or a
			// certificate of its own (the CA is made under the data directory when first needed);
			// until one is, the broker takes connections from this machine only
			m := map[string]any{"listen": *mqtt, "prefix": "sf"}
			if *mqttTLS != "" {
				m["tls"] = map[string]any{"listen": *mqttTLS}
			}
			cfg["mqtt"] = m
		} else {
			cfg["mqtt"] = map[string]any{"listen": ""}
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := writeOwned(cfgPath, append(b, '\n'), 0o640, gid); err != nil {
			return err
		}
	}
	if envPath := filepath.Join(installEtc, "env"); !fileExists(envPath) {
		if err := writeOwned(envPath, []byte(envFile), 0o640, gid); err != nil {
			return err
		}
	}
	// the unit: ours to write the first time; a unit changed on this machine is kept
	if !upgrade {
		if err := os.WriteFile(installUnit, []byte(unitFile), 0o644); err != nil {
			return err
		}
	}
	for _, c := range [][]string{{"daemon-reload"}, {"enable", "smartfactory"}, {"restart", "smartfactory"}} {
		if out, err := exec.Command("systemctl", c...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %s", strings.Join(c, " "), strings.TrimSpace(string(out)))
		}
	}
	listen := *httpAddr
	if b, err := os.ReadFile(cfgPath); err == nil {
		var c struct{ HTTP string }
		json.Unmarshal(b, &c)
		if c.HTTP != "" {
			listen = c.HTTP
		}
	}
	if err := waitServing(listen, 60*time.Second); err != nil {
		return err
	}
	addr := consoleURL(listen)
	if upgrade {
		fmt.Printf("%s 已升级到 %s 并重启：%s\n", green("✓"), version, addr)
		return nil
	}
	tokenFile := filepath.Join(installData, "initial-admin-token")
	for i := 0; i < 50 && !fileExists(tokenFile); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Printf("%s SmartFactory 已装好并启动（systemd 服务 smartfactory，开机自启）\n\n", green("✓"))
	fmt.Printf("  打开后台   %s/admin\n", addr)
	fmt.Printf("  管理员令牌 sudo cat %s   （登录后建议删掉这个文件）\n", tokenFile)
	fmt.Printf("  配置       %s\n", cfgPath)
	fmt.Printf("  大模型     在 %s 里填 LLM_BASE_URL、LLM_API_KEY、LLM_MODEL，再 sudo systemctl restart smartfactory\n", filepath.Join(installEtc, "env"))
	if *mqtt != "" {
		port := *mqtt
		if *mqttTLS != "" {
			port += "（TLS " + *mqttTLS + "）"
		}
		fmt.Printf("  设备接入   MQTT %s：在后台公司的「接入」里给每台设备登记，各发一个令牌或证书（smartfactory device add 也行）\n", port)
	}
	fmt.Printf("  数据       %s（备份它就是备份整个工厂）\n", installData)
	fmt.Printf("  日志       journalctl -u smartfactory -f\n")
	return nil
}

func runUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete the data, the config and the user")
	fs.Parse(args)
	if err := systemdHost(); err != nil {
		return err
	}
	exec.Command("systemctl", "disable", "--now", "smartfactory").Run()
	os.Remove(installUnit)
	exec.Command("systemctl", "daemon-reload").Run()
	os.Remove(installBin)
	if !*purge {
		fmt.Printf("%s 服务已停止并卸载；数据和配置留着：%s、%s（-purge 一起删）\n", green("✓"), installData, installEtc)
		return nil
	}
	for _, p := range []string{installData, installEtc} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	exec.Command("userdel", installUser).Run()
	fmt.Printf("%s 服务、数据、配置和用户都已删除\n", green("✓"))
	return nil
}

func systemdHost() error {
	if runtime.GOOS != "linux" {
		return errors.New("install 只在 Linux（systemd）上用；其他系统直接运行 smartfactory -config 配置文件")
	}
	if !fileExists("/run/systemd/system") {
		return errors.New("这台机器没有用 systemd 启动；直接运行 smartfactory -config 配置文件，或者用 install -unit 拿到 unit 自己装")
	}
	if os.Geteuid() != 0 {
		return errors.New("要 root：sudo ./smartfactory install")
	}
	return nil
}

// waitServing waits until the service answers on listen (it restores its plants' state first),
// and says why when it does not.
func waitServing(listen string, limit time.Duration) error {
	_, port, _ := net.SplitHostPort(listen)
	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/api/site"
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(limit)
	for {
		if res, err := client.Get(url); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		out, _ := exec.Command("systemctl", "is-active", "smartfactory").Output()
		state := strings.TrimSpace(string(out))
		if time.Now().After(deadline) || state == "failed" || state == "inactive" {
			logs, _ := exec.Command("journalctl", "-u", "smartfactory", "-n", "20", "--no-pager").CombinedOutput()
			return fmt.Errorf("服务没有起来（%s）：\n%s", state, logs)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// consoleURL is how a browser on the network reaches listen.
func consoleURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
					host = n.IP.String()
					break
				}
			}
		}
	}
	return "http://" + net.JoinHostPort(host, port)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func writeOwned(path string, b []byte, mode os.FileMode, gid int) error {
	if err := os.WriteFile(path, b, mode); err != nil {
		return err
	}
	return os.Chown(path, 0, gid)
}
