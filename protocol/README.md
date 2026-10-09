# SmartFactory 协议 SFP/1

浏览器与 SmartFactory 服务器之间的全部接口：实时通道、HTTP 接口、顾问接口、公司自己的助手，以及给智能体用的 MCP。设备和传感器怎样接入，见 [devices.md](devices.md)（SFD/1）。

| 文件 | 作用 |
|---|---|
| `src/protocol/sfp.ts` | 标准本身（zod）。前端类型由它推导 |
| `protocol/sfp.schema.json` | 由上面生成的 JSON Schema（draft 2020-12），`npm run schema` 重新生成，不要手改 |
| `server/internal/web/protocol_test.go` | 一致性测试：服务器实际发出的消息逐条按 schema 校验 |

`$defs` 下的名字就是本文里的类型名：`ClientMessage`、`ServerMessage`、`PlantState`、`Health` 等。

## 1. 通用约定

- **传输**：与页面同源。实时通道是 WebSocket `/api/live`，其余接口是 HTTP。所有消息都是 UTF-8 JSON。WebSocket 每帧一条消息，文本帧。
- **身份**：分访客和操作员两种。操作员带运营令牌：WebSocket 写在 `?token=`，HTTP 写在 `Authorization: Bearer <token>`，也可以用 `?token=`。不带令牌、或令牌不对，都按访客处理，不会报错。
- **版本**：服务器在 `hello.protocol` 里声明版本号。浏览器看到版本号和自己不一致时，必须停止跟随，并提示刷新页面。
- **兼容**：新增消息类型、新增可选字段，版本号不变。改名、删字段、改语义，版本号加 1。收到不认识的消息类型或字段时，接收方必须忽略，不能报错。
- **发送方必须严格**：服务器只发 schema 里列出的字段，一致性测试按严格模式校验。顾问接口（第 4 节）例外：DI 返回的字段可能比这里列的多。
- **时间**：
  - `clock`：班次时钟，当天 0 点起的秒数。
  - `elapsed`、`at`（报警、日志、读数）：仿真开始以来的秒数。
  - `live.at`、`Tag.t`：墙钟毫秒。
  - HTTP 里的日期：RFC 3339。
- **错误**：HTTP 出错时返回非 2xx 状态码，响应体是 `ErrorBody` `{"error": "中文说明", "rule"?: "规则名"}`，`Content-Type: application/json`。实时通道的出错走 `result` 消息。

## 2. 实时通道 `GET /api/live`

### 2.1 过程

```
浏览器                                  服务器
  ── WebSocket 连接 [?token=] ─────────▶
  ◀──────────── hello {protocol, operator, sim, version, sources}
  ── watch {company, plant} ───────────▶
  ◀──────────── frame（第一帧含该公司所有厂）
  ◀──────────── frame …（每 0.5 s）
  ── cmd / time / hist / plant ────────▶
  ◀──────────── result / hist / cmd / sources
```

连接断开时，浏览器保留最后的状态继续本地仿真，并退避重连（1.5 s 起，每次翻倍）。连续 3 次都连不上（大约 10 s），就当作没有服务器（静态托管），不再重连。

### 2.2 浏览器 → 服务器：`ClientMessage`

| type | 字段 | 含义 | 权限 |
|---|---|---|---|
| `watch` | `company`, `plant` | 跟随一家公司。`plant` 填当前看的厂，填 `"all"` 表示所有厂都按全速推送。换公司也发这条 | 所有人 |
| `plant` | `plant` | 切换当前看的厂。下一帧带上所有厂 | 所有人 |
| `cmd` | `plant`, `asset`, `cmd`, `arg?`, `o?` | 操作设备，`cmd` 的取值见 2.5。`o` 是发送页签的标识，广播时原样带回，发送方据此忽略自己的操作 | 所有人，访客有限制（2.4） |
| `time` | `scale` 0–20 | 设置全公司的仿真倍速 | 仅操作员 |
| `hist` | `plant`, `asset` | 取一台设备的趋势曲线 | 所有人 |

### 2.3 服务器 → 浏览器：`ServerMessage`

| type | 字段 | 何时发 |
|---|---|---|
| `hello` | `protocol`, `operator`, `sim`, `version`, `sources: Source[]` | 连接后第一条。`sim: true` 表示由服务器运行仿真，浏览器跟随 |
| `frame` | `data: {company, timeScale, plants: {厂id: PlantState}}` | 每 0.5 s 一帧。内容见下 |
| `result` | `ok`, `msg` | 回应 `cmd`；`time` 被拒绝时也会发。`msg` 是给人看的中文 |
| `hist` | `plant`, `asset`, `data: {temp, power, vib, level, meas: number[]} \| null` | 回应 `hist`。设备不存在时 `data` 为 `null` |
| `cmd` | `cmd: {c, p, a, cmd, arg?, by: "operator"\|"mcp", o?}` | 操作员或 AI 助手操作了当前跟随的公司 |
| `sources` | `sources: Source[]` | 每 5 s 一次，报告采集源状态 |

每帧 `frame` 包含哪些厂：

| 情况 | 包含的厂 |
|---|---|
| 新 `watch` 或 `plant` 之后的第一帧 | 所有厂 |
| 之后，跟随单个厂 | 当前看的厂，每 0.5 s 一次 |
| 每隔 4 s | 所有厂一次 |
| 跟随 `all` | 每帧都含所有厂 |

`PlantState` 的 `events` 只记录上一帧之后发生的事，不会重复发送。

`Source`：

| 字段 | 含义 |
|---|---|
| `name` | 采集源名称 |
| `type` | `mqtt` / `modbus` / `opcua` |
| `url` | 地址 |
| `connected` | 是否已连接 |
| `error?` | 出错信息 |
| `points` | 配置的点数 |
| `received` | 已收到的值的个数 |
| `lastAt` | RFC 3339 时间；从没收到过值时为零值时间 |
| `targets` | 这个源喂给哪些设备，格式 `公司/厂/设备` |
| `labels?` | 上面这些设备给人看的名字 |

### 2.4 限制

- 每条浏览器消息最大 64 KB。
- 访客的 `cmd`：令牌桶，最多连发 5 次，之后每 3 s 恢复 1 次；超出时回复 `result {ok:false}`。
- 访客执行 `stop`、`fault`、`speed` 后 90 s 自动撤销，避免一个访客把产线停住影响所有人。
- 只有操作员的命令会下发到真实设备。访客的命令只作用于仿真设备。
- 浏览器处理不过来时，服务器丢弃旧帧；下一帧总是完整的当前状态。

### 2.5 `cmd` 的取值

| cmd | 含义 | arg |
|---|---|---|
| `start` / `stop` | 启动 / 停止 | — |
| `reset` | 复位故障 | — |
| `maint` | 切换维护状态 | — |
| `speed` | 调速 | 0.5–1.5 |
| `fault` | 故障演练 | — |
| `restock` | 原料入库 | — |
| `ship` | 成品发运 | — |
| `retool` | 立即换模 | — |
| `toollimit` | 设定计划换模点 | 0.3–1，占模具寿命的比例 |

### 2.6 `PlantState`

数值保留 3 位小数。

| 字段 | 含义 |
|---|---|
| `clock` | 班次时钟（秒） |
| `elapsed` | 仿真累计秒数 |
| `energy` | 本班能耗（kWh） |
| `power` | 当前功率（kW） |
| `alarmSeq` | 下一个报警编号 |
| `assets[]` | 每台设备一项，见下 |
| `lines[]` | `id`, `spawnT`, `pid`, `good`, `scrap`, `tp`（统计窗口内完成的件数）, `products: ProductTuple[]`, `lot?: [炉号, 供应商]`, `job?: [工单号, 零件名, 数量, 合格数, 下达时间]` |
| `agvs[]` | `id`, `idx`, `x`, `z`, `heading`, `cargo: {kind: raw\|fg, n} \| null`, `mode: move\|dwell`, `dwell`, `waiting`, `waitedT`, `task` |
| `trucks[]` | `id`, `idx`, `x`, `z`, `heading`, `phase: away\|drive\|dock\|leave`, `wait`, `dock`, `load` |
| `alarms[]` | 最新在前，最多 80 条：`id`, `asset`, `code`, `prio: 1\|2\|3`, `text`, `at`, `active`, `acked`, `clearedAt?` |
| `log[]` | 最新在前，最多 60 条：`at`, `asset`, `text`, `kind: info\|op\|alarm`, `prio` |
| `inventory?` | `[品项, 可用, 容量][]` |
| `events[]` | `{type:"reject", line, asset, x, z, kind}` 或 `{type:"fault", asset}` |

`assets[]` 的每一项：

| 字段 | 含义 |
|---|---|
| `id` | 设备号 |
| `enabled` | 是否启用 |
| `speed` | 速度倍率 |
| `state` | 设备状态，取值见 schema 里的 `AssetState` |
| `fault` | `{text, at}` 或 `null` |
| `maint` | 维护状态 |
| `temp`、`vib`、`power` | 温度、振动、功率 |
| `health`、`util`、`avail` | 健康度、稼动率、可用率，均为 0–1 |
| `cycles` | 累计节拍数 |
| `progress` | 当前节拍进度 |
| `busy` | 正在加工的件号，或 `null` |
| `reading` | 最近一次检测 `{v, ok, at}`，或 `null` |
| `rejects` | 判废件数 |
| `verdict` | 最近一次判定 `{ok, at}`，或 `null` |
| `stock`、`count` | 库存、计数 |
| `battery`、`vel`、`task` | 电量、速度、当前任务（车辆用） |
| `live` | 设备实测值 `{src, at, f: {字段: 值}}`，或 `null` |
| `toolUsed?`、`toolLimit?` | 当前模具已做件数、计划换模点 |
| `changing?` | 换模剩余秒数 |

`ProductTuple` 按顺序是：`[件号, 线上位置, 已完成工步, 在做工步, 本工步已用秒数, 形态, 停滞秒数, 占用库存 | null]`。

## 3. HTTP 接口

| 方法 路径 | 响应 | 说明 |
|---|---|---|
| `GET /api/health` | `Health` | 版本、公司列表、单步耗时 `stepMs`、上次存盘时间 `savedAt`、采集源 |
| `GET /api/config/directory` | `Directory` | 运行时（经 MCP）创建的公司：`[{id, name, short}]` |
| `GET /api/config/company/{id}` | `RuntimeCompany` | 运行时创建的公司配置及其行业包，格式见 `src/config/schema/*.json`；不存在时 404 |
| `GET /api/tags?company=&plant=&asset=` | `Tags` | 设备最新实测值：`[{c, p, a, f, v, src, t}]` |
| `GET /api/advisors` | `Advisors` | 每家公司的顾问控制台地址 |
| `GET /api/devices/{company}/{plant}` | `DeviceMap` | 这个厂在设备约定下的地址表（MQTT topic、Modbus 寄存器、OPC UA 节点），见 devices.md 第 6 节 |

## 4. 顾问接口 `/api/advisor/{company}/…`

由服务器代理给该公司的 DataIntelligence。浏览器不需要 DI 的账号：只读请求以分析员身份转发，写操作以审批人身份转发，而写操作要求请求方带运营令牌。没有配置顾问的公司返回 404。

| 方法 路径 | 请求 | 响应 | 权限 |
|---|---|---|---|
| `GET …/metrics` | — | `Metrics` | 所有人 |
| `GET …/dimensions` | — | `Dimensions` | 所有人 |
| `POST …/query` | `Query` | `Answer {columns, rows, sql}` | 所有人 |
| `GET …/consult` | — | `Board`（咨询档案：目标、发现、计划、决定、验收） | 所有人 |
| `POST …/plans/{plan}/{adopt\|reject\|measure\|accept}` | `DecideRequest {why?}` | 对象 | 操作员 |
| `POST …/ask` | `AskRequest {question}` | SSE，`AskEvent` 流 | 所有人；同一 IP 同一时间只能有一个问题，否则返回 429 |
| `GET …/reviews` | — | `Reviews {reviews, runs}` | 所有人 |
| `GET …/reviews/runs/{id}` | — | `ReviewRun` | 所有人 |
| `POST …/reviews/{name}/run` | `{}` | SSE，`AskEvent` 流 | 操作员 |

**SSE 的格式**：`Content-Type: text/event-stream`。每个事件是一行 `data: <AskEvent JSON>`，后跟一个空行。`kind` 有以下几种：

| kind | 含义 |
|---|---|
| `thinking` | 推理过程 |
| `tool_call` | 调用工具，带 `tool` 和 `args` |
| `tool_result` | 工具结果，带 `tool` 和 `result` |
| `verify` | 开始核对答案 |
| `verified` | 核对通过 |
| `verify_skipped` | 跳过核对 |
| `complete` | 最终回答，在 `text` 里 |
| `error` | 出错 |

集团诊断会在服务端一直跑完，浏览器中途断开也不影响，结果照样存档。

## 5. 助手接口 `/api/agents/{company}/…`、`/api/library/{company}/…`

公司自己的助手，跑在 agent-go 上。每个助手由一种模板（`docs` 文档助手、`notes` 研发笔记、`service` 售后服务）加上公司起的名字和要求组成，工作对象是公司的资料库、研发笔记和售后工单，以及孪生里的设备。一家公司第一次被访问时自动建好三个助手，华锻另带示例资料。服务器没有配置数据目录时，这些接口都返回 503。

谁在问：带运营令牌的是运营人员；其他人是访客，用请求头 `X-SF-Visitor`（浏览器自己生成并保存的 id）区分，没有这个头时按 IP 区分。对话只有发起人自己能看到。访客可以提问，但助手的写入工具（保存笔记、建工单、改工单）对访客返回“只有运营人员可以保存”，助手会把整理好的内容直接回给访客。

| 方法 路径 | 请求 | 响应 | 权限 |
|---|---|---|---|
| `GET /api/agents/{company}` | — | `Assistants {ready, operator, templates, agents, severities, statuses}` | 所有人 |
| `POST /api/agents/{company}` | `AssistantRequest {kind, name, instructions?}` | `Assistant` | 运营人员 |
| `PUT /api/agents/{company}/{id}` | `AssistantRequest {name, instructions?}` | `Assistant` | 运营人员 |
| `DELETE /api/agents/{company}/{id}` | — | `Done` | 运营人员 |
| `POST /api/agents/{company}/{id}/ask` | `AssistantAsk {conversation?, text}` | SSE，`AssistantEvent` 流 | 所有人；同一个人同一时间只能问一个问题，否则 429；没有配置大模型时 503 |
| `GET /api/agents/{company}/{id}/conversations` | — | `Conversations`（最近 50 段） | 本人 |
| `GET /api/agents/{company}/{id}/conversations/{conv}` | — | `ConversationView {conversation, messages}` | 本人 |
| `DELETE /api/agents/{company}/{id}/conversations/{conv}` | — | `Done` | 本人 |
| `GET /api/library/{company}/docs` | — | `Docs` | 所有人 |
| `GET /api/library/{company}/docs/{doc}` | — | `DocView {doc, body}` | 所有人 |
| `POST /api/library/{company}/docs` | `DocUpload {title, body}`，正文是 Markdown 或纯文本 | `Doc` | 运营人员 |
| `DELETE /api/library/{company}/docs/{doc}` | — | `Done` | 运营人员 |
| `GET /api/library/{company}/notes` | — | `Notes` | 所有人 |
| `DELETE /api/library/{company}/notes/{note}` | — | `Done` | 运营人员 |
| `GET /api/library/{company}/tickets` | — | `Tickets` | 所有人 |
| `PATCH /api/library/{company}/tickets/{ticket}` | `TicketPatch {status}` | `Ticket` | 运营人员 |

不带 `conversation` 的提问开始一段新对话，第一个事件 `conversation` 给出它的 id；之后带上这个 id 继续问，助手记得前面说过的话。`AssistantEvent` 的 `kind`：

| kind | 含义 |
|---|---|
| `conversation` | 这次提问所在的对话，带 `id` 和 `title` |
| `thinking` | 模型在想 |
| `tool_call` / `tool_result` | 调用工具和它的结果，带 `tool`，以及 `args` 或 `result` |
| `delta` | 回答的一段，按顺序拼起来 |
| `reset` | 丢掉已经收到的 `delta`，回答会重写 |
| `complete` | 最终回答，在 `text` 里 |
| `error` | 出错，原因在 `text` 里 |

浏览器断开时回答停止，已经写入的笔记和工单保留，这条回答记为 `stopped`。

## 6. MCP `POST /mcp`

- 协议：Model Context Protocol，Streamable HTTP，无状态，用 JSON 响应。
- 必须带 `Authorization: Bearer <运营令牌>`，否则返回 401。服务器没有配置令牌时返回 503。

| 工具 | 作用 |
|---|---|
| `list_sites` | 公司、工厂，以及各自的实测点数 |
| `plant_status` | 一个厂的 KPI、各线数据、库存、报警数、故障设备 |
| `list_alarms` | 报警，可以按厂筛选，或只看未消除的 |
| `get_asset` | 一台设备的状态、遥测、报警、实测值 |
| `read_tags` | 设备实测值及其来源 |
| `list_sources` | 采集源的连接状态 |
| `send_command` | 操作设备，取值同 2.5；会经采集源下发到真实设备 |
| `simulate_plan` | 假设分析：复制当前状态，分别按原样和按改动后往前推演，对比结果 |
| `scene_guide` | 怎样用两份 JSON 搭建一个新场景 |
| `list_industries` / `get_industry` | 列出行业包 / 读取一个完整行业包 |
| `create_industry` | 新建或替换行业包，带校验 |
| `create_company` / `delete_company` | 新建或替换公司，立即开始仿真 / 删除 |

## 7. 修改协议

1. 先改 `src/protocol/sfp.ts`。
2. `npm run schema` 重新生成 `protocol/sfp.schema.json`，`npm run typecheck` 检查前端。
3. 改服务器，跑 `go test ./internal/web/ -run TestProtocol`（需要先 `npm run build:engine`）。服务器发出不合 schema 的消息，或者 `clientMsg` 的字段和标准对不上，测试都会失败。
4. 按第 1 节的兼容规则决定是否要把 `PROTOCOL` 和 `web.Protocol` 一起加 1。
5. 更新本文。
