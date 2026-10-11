# SmartFactory

一家公司自己部署的智能工厂：后台（搭建和管理工厂）、工厂的实时 3D 孪生、AI 助手。数据全在你自己的服务器上。

一个程序，不依赖数据库或别的服务。前端、仿真、MQTT broker、知识库都在里面。

## 安装（Linux，systemd）

下载对应架构的包（`linux-amd64` 或 `linux-arm64`），解开后：

```sh
sudo ./smartfactory install
```

它会：
- 把自己装到 `/usr/local/bin`；
- 建系统用户 `smartfactory`；
- 数据放在 `/var/lib/smartfactory`，配置放在 `/etc/smartfactory`；
- 给设备生成 MQTT 密码；
- 写好 systemd 服务（`smartfactory.service`，开机自启、加固），并启动。

最后它会打印后台地址和管理员令牌的位置：

```
  打开后台   http://<服务器地址>:4720/admin
  管理员令牌 sudo cat /var/lib/smartfactory/initial-admin-token
```

用这个令牌登录后台。你可以挑一个内置模板，或者用几句话描述你的工厂让 AI 搭，也可以粘贴配置。建好之后这台服务器就只运行你这一家公司。登录后在「账号」里给自己和同事发令牌，然后删掉 `initial-admin-token`。

| | |
|---|---|
| 升级 | 用新版本再执行一次 `sudo ./smartfactory install`。配置、数据和服务文件都保留，只换程序并重启 |
| 卸载 | `sudo smartfactory uninstall`。数据和配置保留；加 `-purge` 一起删除 |
| 备份 | 备份 `/var/lib/smartfactory` 就是备份整个工厂：场景、仿真状态、账号、知识库、工单、操作记录 |
| 日志 | `journalctl -u smartfactory -f` |
| 状态 | `systemctl status smartfactory` |

首次安装可以指定端口：`sudo ./smartfactory install -http 127.0.0.1:4720 -mqtt :41883`。`-mqtt ""` 表示不开 MQTT。不想让程序自己装的，用 `smartfactory install -unit` 拿到服务文件，自己放置。

不用 systemd 的机器（macOS、容器）：直接运行 `smartfactory -config smartfactory.json`。Windows 上在 WSL 里用 Linux 版。不带配置运行时，数据放在当前目录的 `data/` 下。

## 配置

`/etc/smartfactory/smartfactory.json`，改完执行 `sudo systemctl restart smartfactory`。

```json
{
  "http": ":4720",
  "data": "/var/lib/smartfactory",
  "mqtt": { "listen": ":41883", "prefix": "sf", "users": { "device": "…" } }
}
```

- `http`：后台和孪生的地址。前面放了 HTTPS 反向代理的，改成 `127.0.0.1:4720`。
- `mqtt.users`：设备连 broker 用的用户名和密码，可以加多个。一个用户都没有时，broker 只接受本机连接。
- `sources`：从现有的 PLC、网关、MQTT broker 读数据（Modbus TCP、OPC UA、MQTT），见 [设备协议](protocol/devices.md)。
- `history`：把生产历史写进 Postgres，给经营顾问用。

`/etc/smartfactory/env` 是环境变量文件：

- AI 助手和"让 AI 搭"需要一个兼容 OpenAI 接口的大模型，在这里填 `LLM_BASE_URL`、`LLM_API_KEY`、`LLM_MODEL`。
- 服务器要经代理才能出网的，在这里填 `HTTPS_PROXY`。

不配大模型，除了 AI 以外的功能都能正常使用。

## HTTPS

在前面放一个反向代理。用 Caddy 的话，下面几行就够了，证书会自动申请：

```
factory.example.com {
    reverse_proxy 127.0.0.1:4720
}
```

## 接设备和业务系统

- **设备**：按约定的 MQTT 主题 `sf/{公司}/{工厂}/{设备}/{字段}` 发数据就能接上，不用配置。每个工厂的点位表在后台公司页的「接入」里。Modbus、OPC UA 和已有的 broker 用配置里的 `sources` 接入。详见 [protocol/devices.md](protocol/devices.md)。
- **ERP**：在后台「接入」里配置。ERP 可以把工单推给孪生，也可以由孪生去 ERP 拉取。工单排到对应产线上生产，开工和完工都带签名回传给 ERP。详见 [protocol/README.md](protocol/README.md) 的「生产工单」一节。
- **MCP**：`POST /mcp`，带令牌，供 AI 客户端读取和操作孪生。

## 命令行

后台能做的，命令行也都能做（对着运行中的服务器）：

```sh
smartfactory login -server https://factory.example.com    # 粘贴管理员令牌
smartfactory company                                       # 工厂概况
smartfactory orders <公司>                                 # 生产工单
smartfactory industries new -ai "注塑件工厂：……"            # 让 AI 写一个行业包
smartfactory agent "现在在做哪些工单，哪张会延期？"            # 问工作台助手
```

## 从源码构建

需要 Node.js 22+ 和 Go 1.27+。

```sh
npm ci
scripts/build.sh              # 本机：server/bin/smartfactory
scripts/release.sh            # 各平台的发布包和 SHA256SUMS：server/bin/release/<版本>/
```

开发时运行 `npm run dev`（页面，端口 4719），另开一个终端运行 `go run ./server/cmd/smartfactory -demo`（服务端，端口 4720；`-demo` 会把每个内置模板各跑成一家公司）。
