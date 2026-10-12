# SmartFactory 协议 SFP/1

浏览器与 SmartFactory 服务器之间的全部接口：实时通道、HTTP 接口、顾问接口、公司自己的助手，以及给智能体用的 MCP。设备和传感器怎样接入，见 [devices.md](devices.md)（SFD/1）。

| 文件 | 作用 |
|---|---|
| `web/src/protocol/sfp.ts` | 标准本身（zod）。前端类型由它推导 |
| `protocol/sfp.schema.json` | 由上面生成的 JSON Schema（draft 2020-12），在 `web/` 里 `npm run schema` 重新生成，不要手改 |
| `server/internal/web/protocol_test.go` | 一致性测试：服务器实际发出的消息逐条按 schema 校验 |

`$defs` 下的名字就是本文里的类型名：`ClientMessage`、`ServerMessage`、`PlantState`、`Health` 等。

## 1. 通用约定

- **传输**：与页面同源。实时通道是 WebSocket `/api/live`，其余接口是 HTTP。所有消息都是 UTF-8 JSON。WebSocket 每帧一条消息，文本帧。
- **身份**：令牌写在 `Authorization: Bearer <token>`，WebSocket 和 HTTP 也可以用 `?token=`。不带令牌、令牌不对或已过期，都按访客处理，不报错。每个令牌对应一个人或一个客户端，带一个角色和它管的公司；在它不管的公司里，持有人也是访客。角色从低到高（`Role`）：

  | 角色 | 能做的事 |
  |---|---|
  | `visitor` 访客 | 看；试操作仿真设备（限流，90 s 后自动撤销）；向助手提问，助手只给草稿不保存 |
  | `member` 成员 | 加上：通过助手记笔记、建工单、改工单；改工单状态 |
  | `operator` 操作员 | 加上：会下发到真实设备的命令、全公司仿真倍速、顾问的决定和例行诊断、看审计日志 |
  | `admin` 管理员 | 加上：增删助手、上传删除资料、删除笔记；经 MCP 建删场景 |

  另有 `erp` ERP 接口：不在上面的阶梯里，给 ERP 之类的系统用，只能发、查、撤它所管公司的生产工单（`/api/erp/{company}/orders…`），别处一律按访客，也进不了 MCP。

  令牌由 `smartfactory token -name 张工 -role operator -company huaduan` 生成，服务器配置里只存它的 SHA-256。最早的单一 `token` 仍然有效，等同于所有公司的管理员。
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
  ── WebSocket 连接 ?company=&token= ──▶
  ◀──── hello {protocol, operator, role, name, sim, version, sources}
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
| `time` | `scale` 0–20 | 设置全公司的仿真倍速 | 该公司操作员以上 |
| `hist` | `plant`, `asset` | 取一台设备的趋势曲线 | 所有人 |

### 2.3 服务器 → 浏览器：`ServerMessage`

| type | 字段 | 何时发 |
|---|---|---|
| `hello` | `protocol`, `operator`, `role`, `name`, `sim`, `version`, `sources: Source[]` | 连接后第一条。`role` 和 `operator` 说的是连接时 `?company=` 那家公司；`name` 是令牌持有人，没有令牌是“访客”。`sim: true` 表示由服务器运行仿真，浏览器跟随 |
| `frame` | `data: {company, timeScale, plants: {厂id: PlantState}}` | 每 0.5 s 一帧。内容见下 |
| `result` | `ok`, `msg` | 回应 `cmd`；`time` 被拒绝时也会发。`msg` 是给人看的中文 |
| `hist` | `plant`, `asset`, `data: {temp, power, vib, level, meas: number[]} \| null` | 回应 `hist`。设备不存在时 `data` 为 `null` |
| `cmd` | `cmd: {c, p, a, cmd, arg?, by: "operator"\|"mcp", n?, o?}` | 操作员或经 MCP 的客户端操作了当前跟随的公司，`n` 是令牌持有人 |
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
- 只有该公司操作员以上的命令会下发到真实设备，并记入审计日志。其他人（包括成员）的命令按访客处理，只作用于仿真设备。
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

一个页面只是一家公司的工厂：实时通道的 `hello` 和 `sources` 只带这家公司的采集源，下面这些公开接口也不会列出别的公司。

| 方法 路径 | 响应 | 说明 |
|---|---|---|
| `GET /api/health` | `Health` | 版本、单步耗时 `stepMs`、上次存盘时间 `savedAt`；公司列表和采集源只列令牌管的公司（访客为空） |
| `GET /api/config/directory` | `Directory` | 运行时创建的公司中令牌管的那些：`[{id, name, short}]`（访客为空） |
| `GET /api/config/company/{id}` | `RuntimeCompany` | 运行时创建的公司配置及其行业包，格式见 `web/src/config/schema/*.json`；不存在时 404 |
| `GET /api/tags?company=&plant=&asset=` | `Tags` | 设备最新实测值：`[{c, p, a, f, v, src, t, q?}]`；不带 `company` 时只有令牌管的公司。打了 1、2 标记的读数和超过 1 分钟前的读数不会成为最新值 |
| `GET /api/series/{company}/{plant}/{asset}?field=&from=&to=&step=` | `SeriesAnswer` | 设备发来的读数，见下面「设备数据」；该公司成员以上 |
| `GET /api/advisors?company=` | `Advisors` | 这家公司的顾问控制台地址；不带 `company` 时是令牌管的公司的 |
| `GET /api/devices/{company}/{plant}` | `DeviceMap` | 这个厂在设备约定下的地址表（MQTT topic、Modbus 寄存器、OPC UA 节点），见 devices.md 第 6 节 |
| `GET /api/audit/{company}?before=&limit=` | `AuditLog` | 这家公司的改动，新的在前：谁（`who`、`role`、`key`）、从哪来（`via`：`page` 页面、`live` 实时通道、`assistant` 助手、`mcp`）、做了什么（`action`、`target`、`detail`）、成没成（`ok`、`error`）。`limit` 默认 100、最多 500，`before` 是上一页最后一条的 `id`。需要该公司操作员以上。访客的试操作不记 |

### 设备数据 `/api/series/{company}/{plant}/{asset}`

服务器有数据目录时，设备发来的每个读数都存进 `{data}/series.db`，不需要另外的数据库。读数带设备的采集时间（devices.md 的「采集时间」），网关补发的读数也按原来的时间入库。

- **存多久**：原始读数 `series.raw`（默认 14d），按分钟聚合 `series.minute`（默认 90d），按小时聚合 `series.hour`（默认 5y）。每过完一小时，这一小时的读数压成一块（平均每个读数 2 字节左右），同时写出分钟和小时的最小值、最大值、平均值、读数个数、最后一个值和被标记的个数。整个库的大小受 `series.maxMB`（默认 4096）限制，超出时先删最旧的原始读数，再删最旧的分钟。
- **标记**：1 超出合理范围，2 变化比设备可能的还快，4 采集时间不可用、改用收到时间。标记 1、2 的读数照样入库，但不算进聚合的平均、最小、最大，不进孪生，并计入采集源的 `flagged` 和 `lastFlag`（`GET /api/health`）。
- **查询**：`field` 逗号分隔，不带时返回这台设备存过的全部字段。`from`、`to` 是 Unix 毫秒或 RFC 3339，默认最近一小时。`step` 可以是 `raw`、`minute`、`hour`；不带时按范围自动选：6 小时以内、并且仍在原始读数保留期内的用 `raw`，8 天以内用 `minute`，更长用 `hour`。`raw` 每个字段最多返回 20000 个读数，超了会自动改用 `minute`；手动指定 `raw` 的只返回最新的那些，并带上 `truncated`。
- **点的格式**：`raw` 是 `[t, 值, 标记]`；`minute`、`hour` 是 `[t, 平均, 最小, 最大, 读数个数, 被标记个数]`，一个区间里全是被标记的读数时，平均、最小、最大为 `null`。状态词、故障文字这类字符串只在变化时存，放在 `events` 里。

## 4. 顾问接口 `/api/advisor/{company}/…`

由服务器代理给该公司的 DataIntelligence。浏览器不需要 DI 的账号：只读请求以分析员身份转发，写操作以审批人身份转发，而写操作要求请求方是该公司操作员以上，并记入审计日志。没有配置顾问的公司返回 404。

| 方法 路径 | 请求 | 响应 | 权限 |
|---|---|---|---|
| `GET …/metrics` | — | `Metrics` | 所有人 |
| `GET …/dimensions` | — | `Dimensions` | 所有人 |
| `POST …/query` | `Query` | `Answer {columns, rows, sql}` | 所有人 |
| `GET …/consult` | — | `Board`（咨询档案：目标、发现、计划、决定、验收） | 所有人 |
| `POST …/plans/{plan}/{adopt\|reject\|measure\|accept}` | `DecideRequest {why?}` | 对象 | 操作员以上 |
| `POST …/ask` | `AskRequest {question}` | SSE，`AskEvent` 流 | 所有人；同一 IP 同一时间只能有一个问题，否则返回 429 |
| `GET …/reviews` | — | `Reviews {reviews, runs}` | 所有人 |
| `GET …/reviews/runs/{id}` | — | `ReviewRun` | 所有人 |
| `POST …/reviews/{name}/run` | `{}` | SSE，`AskEvent` 流 | 操作员以上 |

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

谁在问：带令牌的按令牌区分，每个令牌有自己的对话；访客用请求头 `X-SF-Visitor`（浏览器自己生成并保存的 id）区分，没有这个头时按 IP 区分。对话只有发起人自己能看到。所有人都可以提问。助手的写入工具（保存笔记、建工单、改工单）每次调用前经 agent-go 的权限审批：该公司成员以上放行，以提问人的名字写入并记入审计日志；其他人被拒绝，助手把整理好的内容直接回给提问人，说明没有保存。

权限列里的“成员”“管理员”都指该公司的这个角色或以上。

| 方法 路径 | 请求 | 响应 | 权限 |
|---|---|---|---|
| `GET /api/agents/{company}` | — | `Assistants {ready, operator, role, you, templates, agents, severities, statuses}`，`role` 是提问人在这家公司的角色 | 所有人 |
| `POST /api/agents/{company}` | `AssistantRequest {kind, name, instructions?}` | `Assistant` | 管理员 |
| `PUT /api/agents/{company}/{id}` | `AssistantRequest {name, instructions?}` | `Assistant` | 管理员 |
| `DELETE /api/agents/{company}/{id}` | — | `Done` | 管理员 |
| `POST /api/agents/{company}/{id}/ask` | `AssistantAsk {conversation?, text}` | SSE，`AssistantEvent` 流 | 所有人；同一个人同一时间只能问一个问题，否则 429；没有配置大模型时 503 |
| `GET /api/agents/{company}/{id}/conversations` | — | `Conversations`（最近 50 段） | 本人 |
| `GET /api/agents/{company}/{id}/conversations/{conv}` | — | `ConversationView {conversation, messages}` | 本人 |
| `DELETE /api/agents/{company}/{id}/conversations/{conv}` | — | `Done` | 本人 |
| `GET /api/library/{company}/docs` | — | `Docs` | 所有人 |
| `GET /api/library/{company}/docs/{doc}` | — | `DocView {doc, body}` | 所有人 |
| `POST /api/library/{company}/docs` | `DocUpload {title, body}`，正文是 Markdown 或纯文本 | `Doc` | 管理员 |
| `POST /api/library/{company}/files` | `multipart/form-data`：一个或多个 `file`；只传一个文件时可以在它前面放 `title` | `UploadResults` | 管理员 |
| `GET /api/library/{company}/docs/{doc}/versions` | — | `DocVersions`，新的在前 | 所有人 |
| `GET /api/library/{company}/docs/{doc}/file?version=` | — | 原文件（不带 `version` 是当前版本；手工输入的文档下载为 `.md`） | 成员 |
| `DELETE /api/library/{company}/docs/{doc}` | — | `Done`，连同所有版本和原文件 | 管理员 |
| `GET /api/library/{company}/notes` | — | `Notes` | 所有人 |
| `DELETE /api/library/{company}/notes/{note}` | — | `Done` | 管理员 |
| `GET /api/library/{company}/tickets` | — | `Tickets` | 所有人 |
| `PATCH /api/library/{company}/tickets/{ticket}` | `TicketPatch {status}` | `Ticket` | 成员 |

资料可以按文件上传：PDF（按页切成「第 N 页」小节，段落能指明页码）、Word `.docx`（标题按样式或手工排版的大号粗体识别，表格每行写成「表头=值」）、Excel `.xlsx`（每个工作表一节，每行「表头=值」）、CSV、Markdown 和文本（UTF-8、带 BOM 的 UTF-16、GBK / GB18030 自动识别）。每个文件不超过 50 MB，一次请求不超过 200 MB。没有文字层的扫描件照样保存，`problems` 里说明没有文字（暂不识别图片）；旧版 `.doc`、`.xls` 单独拒收，提示另存为新格式。同一家公司里标题或文件名相同的再传一次是这份资料的下一版：知识库换成新版的段落，旧版的原文件留着可以下载。入知识库时，段落按工位编号（`IH-101`、`ih101`、`IH 101` 都算）、设备代号后跟编号或「系列/型」（`HP-1xx`、`HP 系列`）、料号（含变型）、产线名（空格不计）、设备名、产品、供应商和客户连到工艺图；编号只按整词匹配，一个字的名字不连。

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

## 5a. 后台接口 `/api/admin/…`

后台（`/admin`，独立的页面 `admin.html`）搭建孪生里的东西：公司（工厂）、行业包、令牌、ERP 对接。只有管理员令牌能用：不带令牌 401，角色不够 403。管理某几家公司的管理员只看得到、只管得了那几家；新建公司、导入行业包、看所有公司的记录要管理所有公司的管理员。

| 方法 路径 | 请求 | 响应 | 说明 |
|---|---|---|---|
| `GET /api/admin` | — | `AdminOverview {me, companies, industries, issue, assistants, version}` | 登录的人、令牌管的公司（`builtin` 随应用内置的不能替换或删除）、行业包；`issue` 是能不能在这里发令牌（服务器有数据目录） |
| `POST /api/admin/companies` | 公司配置（`web/src/config/schema/company.json`） | `CompanyCreated` | 新建，或同 `id` 替换（仿真从头开始）。没通过校验 422 `ConfigProblem {error, errors, where}`。`id` 只能用小写字母、数字、`-` |
| `DELETE /api/admin/companies/{id}` | — | `Done` | 删除运行时建的公司；内置的 422 |
| `GET /api/admin/industries/{id}` | — | 行业包配置（默认值已填） | |
| `POST /api/admin/industries` | 行业包配置 | 行业包摘要 | 新建或替换，用它的公司按新的重建；要管理所有公司 |
| `POST /api/admin/compose` | `ComposeRequest {kind?, brief, base?}` | SSE，`ComposeEvent` 流 | 搭建员：按一段描述（`base` 是现在的配置，在它上面改）写公司配置（`kind: company`，默认）或单独一个行业包（`kind: industry`）。现有行业包里没有要的产品或工序时它写一个新行业包。每版都经引擎检查（不创建、不装）：通过的行业包作为 `industry` 事件、公司配置作为 `draft` 事件发出；用新行业包的公司配置和它一起检查，建的时候先装行业包。没有配置大模型时 503，同一个令牌同一时间只能跑一个 |
| `GET /api/admin/tokens` | — | `Accounts` | 令牌：`from` 是 `legacy`（最初的共用令牌）、`config`（配置文件里的，只能在那里改）或 `issued`（后台发的）；撤销过的也列出 |
| `POST /api/admin/tokens` | `IssueToken {name, role, companies, expires?}` | `IssuedToken {token, account}` | 发令牌，立即生效；`companies` 为 `null` 是所有公司（要管理所有公司）。令牌只在这个响应里出现一次，服务器只存 SHA-256 |
| `DELETE /api/admin/tokens/{key}` | — | `Done` | 撤销后台发的令牌，立即生效（页面、实时通道里的下一次操作、MCP 都按访客对待）；配置文件里的 409，自己正在用的 409 |
| `GET /api/admin/audit?company=&before=&limit=` | — | `AuditLog` | 同 `/api/audit/{company}`；`company` 不填是所有公司，`*` 是共用的改动（行业包、所有公司的令牌） |

| `GET /api/admin/companies/{id}` | — | 公司配置 | 内置的和运行时建的都有 |
| `POST /api/admin/companies?check=1` | 公司配置 | `CompanyCreated` | 只检查（校验、排布），不创建 |
| `POST /api/admin/agent` | `{conversation?, text}` | SSE，`ConsoleEvent` 流 | 工作台助手：用后台自己的操作回答；它提出的改动作为 `action` 事件发出，**不会自己执行**。第一个事件 `conversation` 给出对话 id，带上它继续说。同一个令牌同一时间只能跑一个 |
| `POST /api/admin/actions` | `ConsoleAction {id?, kind, args}` | 视 `kind` 而定 | 执行一个改动（通常是助手提出、人确认的那个），以确认的人的身份，检查和审计与对应的接口相同。`kind`：`create_company {config}`、`delete_company {id}`、`create_industry {config}`、`issue_token {name, role, companies, expires?}`、`revoke_token {key}`、`rebuild_knowledge {company}`、`cancel_order {company, no}`、`apply_calibration {company, machines?}`、`approve_command {company, id}`、`deny_command {company, id, reason?}`。带助手给的 `id` 的另记一条 `agent.confirm` |
| `POST /api/admin/actions/dismiss` | `ConsoleAction {id, kind, args}` | `Done` | 不执行助手提出的改动，记一条 `agent.dismiss`；管理员 |

`ComposeEvent` 的 `kind` 与 `AssistantEvent` 相同（没有 `conversation`），另加 `draft`（一份通过检查的公司配置）和 `industry`（一个通过检查的行业包），都在 `result` 里。`ConsoleEvent` 另加 `action`：一个待确认的改动，在 `result` 里，带 `id`（`AP-…`）、`title`、`detail`；提出时记一条 `agent.propose`。审计日志新增 `token.issue`、`token.revoke`、`knowledge.rebuild`；请求头带 `X-SF-Via: cli` 的（命令行）记为 `via: cli`，其余后台请求记为 `page`。

**操作只有一份**：后台页面、命令行（`smartfactory company|industries|tokens|kb|orders|activity|agent`）和工作台助手调用的是服务器上同一组操作（`server/internal/ops`），权限检查和审计记录完全相同。

### 部署方式和模板

SmartFactory 由工厂或公司自己部署在自己的服务器上，一套服务器就是这一家公司的智能工厂：它的数据只在它自己这里，后台管的就是它自己的工厂。配置决定这台服务器跑什么：

- 默认（有配置文件、没写 `companies`/`demo`）：**一家公司**。第一次启动是空的，在 `data/initial-admin-token`（只有本用户可读）写一个管理员令牌；用它登录 `/admin`，从模板起步、或者描述给 AI、或者贴配置，建好这家公司的工厂，之后就只跑它。`GET /api/site` 说它跑的是哪家（`Site {companies}`），页面不带 `?company` 时打开的就是它；还没建时首页转到 `/admin`。
- `"demo": true`：每个内置模板各跑成一家公司（开发和演示；不带配置文件启动也是这样）。
- `"companies": [...]`：只跑这几家（演示部署里每个演示公司一个服务器就是这么配的）。
- `smartfactory instance init -from DIR -company ID -dir DIR -http ADDR -mqtt ADDR`：把一家公司从一个跑多家的服务器里拆出来，单独成一套：只带它的设备接入、历史库、令牌和数据（仿真状态、助手、资料、笔记、工单、知识库、它那部分操作记录），数据库用 `VACUUM INTO` 拷，源服务器不用停。

**模板**是建工厂的起点：公司配置（id 是模板自己的）加上它用到的、不是内置的行业包；只有结构，不带任何公司的数据。来源两种：内置的，和这台服务器上存的（从自己的工厂存，或者导入别的工厂导出的文件）。

| 方法 路径 | 请求 | 响应 | 说明 |
|---|---|---|---|
| `GET /api/admin/templates` | — | `Templates` | 内置的和这里的，各带 `source`（`builtin`、`local`） |
| `GET /api/admin/templates/{source}/{id}` | — | `Template` | 一个模板的全部；导出的文件就是它 |
| `POST /api/admin/templates` | `SaveTemplate {id, name, description?, from? \| company?}` | `TemplateSummary` | 把一家公司（`from`）或一份配置存成模板；公司名换成模板名 |
| `POST /api/admin/templates/import` | `Template` | `TemplateSummary` | 导入别的工厂导出的模板（同一个标识就替换） |
| `DELETE /api/admin/templates/{id}` | — | `Done` | 删这里的模板 |

### 知识库 `/api/knowledge/{company}`

每家公司一个知识库（CortexDB，不用向量）：
- **工艺**：工厂 → 产线 → 按顺序的工位 → 设备类型，产品、供应商、客户，由引擎排布出的场景生成，场景一改自动重写。每个工位还有一段，写明它在哪条线第几道、上游和下游是谁。
- **工单**：ERP 的生产工单，每张是一个 `order` 节点，`for_customer` 连客户、`of_product` 连产品、`runs_on` 连产线，另有一段写状态、进度、交期。工单变化几秒内写进去，在做的进度每分钟更新；排队和在做的全留，完成的留最近 300 张、90 天以内，撤回的删掉。
- **设备记录**：设备最近 30 天发生的事，各是一个节点连到它的工位，另有一段写清经过：`check`（点检、首件检验、化验里有超标或异常的那次，`checked_on`；每个工位还有一个「最近点检」，写每张表最近一次谁填的、有没有异常、这一轮是否超期）、`anomaly`（读数异常提示：哪个读数、什么状态下、偏了几个σ、平常多少、恢复没有，`seen_on`）、`calibration`（在用的校准和 30 天内的拟合：参数模板值 → 校准值、回放误差，`calibrates`）、`command`（AI 发给设备的指令：等待批准、谁批准或拒绝、被哪条规则拒绝、过期，当时设备的状态，`targets`）。每个工位每类最多留 8 条（还没恢复的异常、等待批准的指令都留），每类一共最多 300 条，30 天前的删掉。填表、异常开始或恢复、拟合或启用撤回校准、指令发出或处理后几秒内写进去，另外每 5 分钟看一遍（超期、过期这类随时间变的），没变的不重写；不按读数写。这些段落只连到自己的节点，不直接"提到"工位，免得一个工位被几十段提到，成了搜索不从它出发的热门节点。
- **资料**：资料库的文档按标题切段，每段和它提到的工位、设备、产线、产品、供应商、客户、工单连起来。

搜索先从问题里认出公司自己的名字：工位、设备、产线、产品（"法兰"认作风电法兰）、客户、工单号，从它们出发沿图走（personalized PageRank，只走写进去的关系，不走统计出来的共现）；讲到这些东西本身的段落排在前面，其次是提到它们的工单。问题里同时有点检、异常、校准、指令这类词时，沿图走更多地走向这类设备记录，这台设备的这类记录排在最前、最新的在前，所以问"RF-101 最近点检有没有超标"先给 RF-101 的点检记录；没问到时设备记录排在工位本身的段落后面。所以问"远岳动力的工单到哪了"先给这个客户的工单，问"HP-101 的上游"先给 HP-101 那一段，问"IH-101 的始锻温度"能找到只写了"中频感应加热炉"的工艺卡。没认出名字时按词找。CortexDB 自己从文本里抽出的名字（编号前缀、段落标题）写入后删掉，免得把不相干的工位连在一起。该公司成员以上可读，管理员可重建。

| 方法 路径 | 响应 | 说明 |
|---|---|---|
| `GET /api/knowledge/{company}` | `KnowledgeOverview` | 各类节点、关系的数量，工艺段落、工单、设备记录、文档段落数，资料库有几篇、入库了几篇 |
| `GET /api/knowledge/{company}/search?q=&limit=` | `KnowledgeAnswer` | 最相关的段落：`from` 是 `doc`（带文档 id 和段号）、`process`、`order`，或设备记录 `check`、`anomaly`、`calibration`、`command`；`mode` 是 `lexical` 或 `ppr` |
| `GET /api/knowledge/{company}/entity?name=` | `EntityAnswer` | 一样东西和它的全部关系、提到它的段落；设备记录在关系最后、每类最新的在前，带一句话概要（`title`）；设备记录本身带 `detail`（各项值和填写人、σ和平常范围、参数、处理人）；没有同名的时给出候选 |
| `GET /api/knowledge/{company}/graph` | `KnowledgeGraph` | 整张图（工艺、工单和设备记录，节点带被提到的次数），供绘制 |
| `POST /api/knowledge/{company}/rebuild` | `{docs}` | 重写工艺图、工单和设备记录，重新入库全部文档；管理员 |

助手的 `search_docs` 在有知识库时搜的就是它（文档、工艺、工单、设备记录一起），另有 `process_graph` 查图里一样东西的关系。

### 生产工单（ERP）`/api/erp/{company}/orders`、`/api/admin/erp/{company}`

公司的 ERP（或 MES、中间件）下达生产工单，孪生把每张排到一条产线上做，开工、完工回传给 ERP。没接 ERP 时产线自己排工单（仿真）。

- **进来**：ERP 带这家公司的 ERP 接口令牌（角色 `erp`，也可以用操作员令牌）推送（`POST`），或者孪生按设置去 ERP 的接口拉（GET/POST，返回 JSON 里列表的路径和每个字段的路径可配，下标取数组，适合按行返回数组的 ERP）。拉取用的请求头值存在服务器上，读设置时只给名字。
- **排线**：工单写了 `line`（产线 id 或名字，可加 `plant`）就在那条线；否则在做这个料号（`partNo`，产品的料号或它的变型）或产品（`product`）的产线里，挑手上活最少的。哪条线都做不了的单独拒收。
- **排序**：每条线按交期先后做，没写交期的排在后面、按收到的先后。线上在做的是 ERP 的工单时，孪生里的工单号、料号、客户、交期就是 ERP 的。
- **没工单的产线**：`idle` 停线等单（和真实工厂一样），`simulate` 自己排一张（演示）。
- **改和撤**：同一个工单号再发一次：排队的全部更新、重新排线；在做的只改数量、交期、客户、单价；完成的不变；撤回的重新排队。`DELETE` 撤回，在做的产线停下它、转做下一张。
- **回传**：设了回调地址，产线开工、完工时 `POST` `OrderCallback`，请求头 `X-SF-Event`，`X-SF-Signature: sha256=<hex(HMAC-SHA256(密钥, 请求体))>`。密钥第一次设回调时生成，只显示这一次；失败重试三次。ERP 也可以随时 `GET` 工单查进度。
- 服务器重启后，孪生的产线接着做停机前在做的工单；状态丢了的，工单回到队列。

| 方法 路径 | 请求 → 响应 | 说明 |
|---|---|---|
| `GET /api/erp/{company}/orders?status=&q=&limit=` | `Orders` | 在做的、排队的（按要做的顺序）、其余新的在前；ERP 接口或成员以上 |
| `GET /api/erp/{company}/orders/{no}` | `Order` | 一张工单 |
| `POST /api/erp/{company}/orders` | `OrderInput`、它的数组或 `{orders: [...]}` → `OrderResults` | 收下工单，每张单独回答；ERP 接口或操作员以上 |
| `DELETE /api/erp/{company}/orders/{no}` | → `Order` | 撤回；完成的不能撤（409）；ERP 接口或操作员以上 |
| `GET /api/admin/erp/{company}` | `ERPOverview` | 对接设置（不含密钥）、上次拉取和回调怎么样、各状态工单数；管理员 |
| `PUT /api/admin/erp/{company}` | `ERPConfig` → `{ok, secret?}` | 设置；请求头值留空表示不改 |
| `POST /api/admin/erp/{company}/pull?dry=1` | → `PullResult` | 现在拉一次；`dry` 只看读到什么、会排到哪条线，不存 |
| `POST /api/admin/erp/{company}/secret` | → `{secret}` | 换回调签名密钥 |

命令行：`smartfactory orders <company>`（`show`、`send -f`、`cancel`、`pull [-dry]`、`erp`）。工作台助手有 `list_orders`、`erp_status`，撤回工单要管理员确认。孪生里的售后服务助手用 `production_orders` 回答客户问的订单进度。

### 仿真校准 `/api/admin/calib/{company}`、`/api/calib/{company}/residuals`

接了实测数据的设备，孪生用实测值代替仿真值，同时让模型自己接着算一份，记下"模型 − 实测"的偏差（残差）：温度按模型自己的升温过程算，功率和振动按当前状态和速度算。残差按分钟存在数据目录的 `calib.db`。

拟合从存下的读数（时序库）出发，按仿真用的同一套公式估计每台设备的参数：

- 待机、运行温升和升温时间常数：按记录的状态序列把温度模型重放一遍，时间常数网格搜索，两个温升最小二乘；状态切换少于 3 次时时间常数沿用模板。
- 待机功率、运行加功率、运行振动：各状态下的中位数（按速度、健康度折算到满速满健康）。
- 节拍：要设备上报 `cycles`（累计件数），用运行时长除以件数。
- 故障间隔和修复时间：从 `state` 的故障事件统计，至少 3 次。

每个参数带依据的读数（或事件）个数、可信度（`good` / `fair` / `poor`）；读数太少、覆盖时间太短、设备没上报状态时不拟合，`note` 说明缺什么。拟合结果附回放误差：同一段时间用模板参数和拟合参数各算一遍，和实测比平均绝对误差。

拟合不会改变仿真。管理员应用后，这台设备在仿真里改用拟合的参数（只替换拟合出来的那些），存档里带着它，推演（`simulate_plan`）也按它算，并在结果的 `校准` 里列出哪些设备哪些参数是拟合的。随时可以撤销，恢复模板。公司的配置文件不变。

| 方法 路径 | 请求 → 响应 | 说明 |
|---|---|---|
| `GET /api/admin/calib/{company}` | `Calibration` | 每台设备：模板、拟合、在用的参数，回放误差，近 24 小时的残差；管理员 |
| `POST /api/admin/calib/{company}/fit` | `CalibRequest {machines?, hours?}` → `Calibration` | 按最近 `hours`（默认 24）小时的读数拟合；不给 `machines` 就是所有存了读数的设备 |
| `POST /api/admin/calib/{company}/apply` | `CalibRequest {machines?}` → `CalibOutcomes` | 应用拟合的参数；不给就是所有拟合过的设备 |
| `POST /api/admin/calib/{company}/revert` | `CalibRequest {machines?}` → `CalibOutcomes` | 撤销，恢复模板参数；不给就是全部 |
| `GET /api/calib/{company}/residuals?machine=&field=&from=&to=&step=` | `Residuals` | 一台设备一个字段（`temp`、`power`、`vib`）的残差，按 `step` 秒（至少一分钟）汇总；成员以上 |

设备用编号（`HP-101`）或 `工厂/编号`（`p1/HP-101`）指定。审计记录 `calib.fit`、`calib.apply`、`calib.revert`。命令行：`smartfactory calib <company>`（`show <设备>`、`fit [-hours 24]`、`apply`、`revert`）。工作台助手有 `calibration` 看校准，`propose_apply_calibration` 提出应用（`ConsoleAction` 的 `apply_calibration {company, machines?}`），要管理员确认。
### 指令把关 `/api/guard/{company}`

发给真实设备的指令先过把关。"真实设备"指采集源不是演示设备（`public`）的机器：它的读数来自这样的采集源，或者这样的采集源为它配了指令。只到孪生的机器（纯仿真、演示设备）不经把关，和以前一样。

- **规则**：人和 AI 的指令都要过。每条规则 `{cmd, when, say}`：`cmd` 是一种指令或 `*`；`when` 是用 `and` 连接的条件，每个条件是 字段 比较 值，字段有 `state`（running、idle、blocked、fault、stopped…）、`temp`、`warn`、`trip`（°C）、`speed`、`health`、`alarms`（在报警的条数）、`arg`（指令的参数）、`maint`、`changing`，比较是 `= != > >= < <=`，值是数、词或另一个字段；条件成立就不执行，`say` 是给出的理由（可以写 `{temp}`、`{warn}`、`{trip}`、`{speed}`、`{arg}`、`{state}`、`{alarms}`）。比较报警线、停机线的规则对没有这两条线的机器不成立。默认：故障或超过报警线不能启动，没降到停机线以下不能复位，速度在 50%～120% 之间，加工中不能换模。
- **等级**（只管 AI：经 MCP 的客户端、助手）：`advisory` 只提议，AI 的每条指令都等人批准（默认）；`guarded` 有护栏，`approve` 里列出的指令等人批准，其余过了规则就执行；`autonomous` 自主，过了规则就执行。
- **等人批准**：指令记成一条请求（`CQ-…`），带发出时设备的状态，`expire` 分钟（默认 15）内没人处理就作废。这家公司的操作员或管理员在孪生顶栏的「待批准」、后台的「指令」页或命令行批准或拒绝；批准时按设备当时的状态再过一遍规则，过了才下发，以发起方的名义加上批准人。MCP 的 `send_command` 这时回答 `status: waiting` 和请求编号，用 `command_request`（可以 `wait` 最多 120 秒）问结果。
- **审计**：每条发给真实设备的指令，执行了的（`command`）、被规则拒绝的（`command.refused`）、提出的（`command.pending`）、批准的（`command.approved`）、拒绝的（`command.denied`）、作废的（`command.expired`），都记下当时设备的状态、温度、报警，拒绝它的规则，谁发起、谁批准（`AuditEntry.context`）。

| 方法 路径 | 请求 → 响应 | 说明 |
|---|---|---|
| `GET /api/guard/{company}` | `GuardOverview` | 设置、默认设置、接了真实设备的机器、等待批准的条数；操作员以上 |
| `PUT /api/guard/{company}` | `GuardSettings` → `GuardOverview` | 改设置；规则写不对 422，带每条问题；管理员 |
| `GET /api/guard/{company}/requests?status=` | `CommandRequests` | 等批准的在前，其余新的在前；操作员以上 |
| `POST /api/guard/{company}/requests/{id}/approve` | → `CommandRequest` | 批准并下发；已经处理过或过期的 409 |
| `POST /api/guard/{company}/requests/{id}/deny` | `{reason?}` → `CommandRequest` | 拒绝 |

命令行：`smartfactory guard <company>`（`set -level guarded -approve start,speed -expire 15`、`set -f settings.json`、`pending`、`requests`、`approve <编号>`、`deny <编号> -reason …`）。工作台助手有 `guard_status`，批准、拒绝要管理员确认。

### 设备身份 `/api/admin/devices/{company}`

内置 broker 上的每台设备（或网关）可以有自己的身份，而不是共用一个密码：

- **登记**：设备名（字母、数字、`.-_`，整台服务器上不重复）就是它连 MQTT 的 client id 和用户名，也是证书的 CN。登记时选一个厂和它负责的设备：编号（`HP-101`）、前缀（`RF-*`）或 `*`（整个厂）。
- **凭据**：`token` 是令牌（`sfd_…`），连 MQTT 时当密码，服务器只存它的 SHA-256；`cert` 是服务器自己的 CA 签发的客户端证书（CN 是设备名，另有 URI `urn:smartfactory:device:{名字}`），设备用它连 TLS 端口（`mqtt.tls`），不用密码，私钥服务器不留。CA 第一次用到时生成在数据目录的 `ca/`（目录 0700，文件 0600）。凭据只在登记、换新时给一次。
- **能用的主题**：只能发 `{prefix}/{company}/{plant}/{设备}` 和 `…/{设备}/{字段}`（字段不能是 `cmd`），只能订阅 `…/{设备}/cmd`（负责整个厂的可以用 `+` 代替设备）。别的发布静默丢弃（QoS 0；MQTT 3.1.1 的 QoS 1、2 按协议断开），订阅回 0x80；client id 不是设备名、令牌不对、证书已换新或撤销的连接被拒。每次被拒都计数、记下最近一次，服务器日志每台设备每分钟至多一条。
- **撤销、换新**：立即断开它的连接，旧凭据不再能用。
- **共用账号**（配置的 `mqtt.users`）照旧，拿到密码的客户端什么主题都能发、能订阅。没有共用账号时，本机的客户端不用密码也能连（和以前一样），但不能冒用登记过的设备名。没有共用账号也没登记设备时，broker 只听本机；登记第一台设备后对网络开放，不用重启。

| 方法 路径 | 请求 → 响应 | 说明 |
|---|---|---|
| `GET /api/admin/devices/{company}` | `BrokerDevices` | 登记的设备（在线、最近、被拒次数），已撤销的在后；共用账号；broker 的端口；管理员 |
| `POST /api/admin/devices/{company}` | `RegisterDevice` → `DeviceCredential` | 登记；令牌或证书、私钥、CA（`bundle` 是三者的 zip，base64）只给这一次 |
| `POST /api/admin/devices/{company}/{id}/renew` | → `DeviceCredential` | 换新凭据，旧的立即失效 |
| `DELETE /api/admin/devices/{company}/{id}` | → `BrokerDevice` | 撤销 |

审计记录 `device.add`、`device.renew`、`device.revoke`。命令行：`smartfactory device <company>`（`list [-all]`、`add <名字> -plant p1 -assets 'RF-*,HP-101' [-cert] [-out 目录]`、`cert <名字>`、`token <名字>`、`revoke <名字>`）。边缘网关用证书时在 `mqtt` 里写 `"url": "tls://主机:端口"` 和 `cert`、`key`、`ca` 三个文件。
### 人工记录 `/api/checks/{company}`

点检、首件检验、化验这些手写的数据，由管理员在后台「点检」页设成表单，操作员在手机上（`/check`）对着设备填。

- **表单**（`CheckForm`）：用于哪个厂、哪些产线或设备（都不选就是全厂的工位）；每项是数字（单位、上下限、目标值）、选择（如 正常/异常，`bad` 是算异常的答案，默认含「异常」「不合格」的）、是否、文字或照片。周期：每班（`at` 是各班开始时间，默认 08:00、20:00）、每天、每几小时（从当天第一次起），`within` 是开始后多少分钟内要做完，不填就是下一轮开始前。删除的表单停用，填过的记录保留。
- **轮次**（`CheckRound`）：每台设备每张表一轮一轮算。本轮填过是 `done`；没填、还在时间内是 `due`；过了 `within` 的时间，或者上一轮整轮没人填（表单那时已经有了），是 `overdue`，`since` 是从什么时候起逾期的。
- **读数**：数字项按填写时间（`taken`，手机离线时记下的时间）作为读数入库，来源 `manual`，和设备读数走同一条路：先校验（不合理的标记、照样入库、不进孪生），再进 `/api/series`。设了 `twin`（`measure`、`temp`、`power`、`vib`）的存在设备的这个字段下，其余存在项的 `key` 下。`measure` 还会进孪生的质量图，没写上下限时按设备的公差判。人填的读数不让设备算作「在上报」，孪生照样仿真它。超过一分钟才到的（离线补发）只入库，不进孪生。
- **异常**：数字超出上下限、选了异常的答案，在孪生里给这台设备报一条质量报警（和孪生自己判出的漂移报警同一级），同一项下一次填正常就消除。只按这张表在这台设备上最新的一次填写来报和消；补发的旧记录、标记为不合理的读数不报。
- **离线**：手机没网时把填好的存在本机，带着填写时间，联网后自动补发；`id` 是手机自己起的编号，重复发只收一次（`duplicate: true`）。
- **审计**：设表单（`check.form`）、停用（`check.form.delete`）、每次填写（`check.submit`，`context` 里是每一项的值、超差原因，离线补发的写明什么时候记的、什么时候收到），都记在填写人名下。

| 方法 路径 | 请求 → 响应 | 说明 |
|---|---|---|
| `GET /api/checks/{company}` | `ChecksOverview` | 表单、每台有表的设备和它的轮次、调用者能不能填、能不能设；成员以上 |
| `GET /api/checks/{company}/templates` | `CheckForms` | 模板：设备点检、首件检验、化验 |
| `POST /api/checks/{company}/forms` | `CheckForm` → `CheckForm` | 新建（不带 `id`）或替换；写得不对 422，带每条问题；管理员 |
| `DELETE /api/checks/{company}/forms/{id}` | → `{ok}` | 停用 |
| `GET /api/checks/{company}/rounds?status=overdue,due` | `CheckRounds` | 逾期的在前（逾期最久的在前），再按截止时间 |
| `GET /api/checks/{company}/machines/{plant}/{asset}` | `MachineChecks` | 一台设备的轮次、用于它的表单、每张表最近一次填的；没有表用于它 404 |
| `POST /api/checks/{company}/submissions` | `CheckInput` → `CheckSubmission` | 填一次；照片是 data URL，最大 2 MB；最多补发 30 天前的；操作员以上 |
| `GET /api/checks/{company}/submissions?form=&plant=&asset=&from=&to=&bad=1&limit=` | `CheckSubmissions` | 按填写时间新的在前；`format=csv` 导出表格，一项一行，带 BOM |
| `GET /api/checks/{company}/photos/{id}` | 图片 | 成员以上 |
| `GET /api/qr?text=` | SVG | 把一个链接画成二维码，后台打印设备码用 |

手机页面：`/check?company=…&plant=…&asset=…` 直接打开一台设备（设备上贴的二维码就是这个链接），令牌可以带一次 `&token=`，之后存在手机上。

命令行：`smartfactory checks <company>`（表单和逾期数）、`rounds [-status overdue,due]`、`results [-asset HP-101] [-bad] [-hours 24]`、`export -from 2026-10-01 -to 2026-10-12 -o 记录.csv`、`template 点检`、`save -f form.json`、`rm <编号>`、`submit -f values.json`。工作台助手有 `check_forms`、`check_rounds`、`check_results`；孪生里的研发笔记助手和售后服务助手用 `manual_records` 查人工记录。
### 成效 `/api/value/{company}`

一段时间里孪生和它的 AI 给这家公司做了什么，每个数（`ValueFigure`）带 `n`（从多少条记录算的：计数就是它自己，比例是分母，工厂的数是产线小时）和 `basis`（依据）；没有可算的记录时 `value` 是 `null`，页面写「没有数据」，不估。

- **建议**（`suggestions`）：顾问服务的咨询档案（`GET /v1/consult`，用 `advisorToken`）。提出的按提出时间、采纳和否决按决定时间落在这段时间里算；采纳率是这段时间的决定里采纳的比例；决定用时取中位数。`plans` 是这段时间采纳或量过的计划，各自和自己的基线比（`change`、`better`），指标不同不加总。
- **AI**（`ai`）：工作台助手提出的改动卡片（`agent.propose`）有多少确认（`agent.confirm`）、点了不要（`agent.dismiss`）、没回应；AI（经 MCP、助手）的设备指令直接执行的、等人批准的（指令把关的请求）、批准、拒绝、被规则拒绝、过期、还在等。
- **校准**（`calibration`）：在用校准的设备、这段时间应用和拟合的，`fields` 是在用校准的设备拟合期的回放误差（模板 → 拟合），没有在用的就是这段时间拟合过的，并在 `note` 里说明。
- **工单**（`orders`）：ERP 下达的工单，收到、完工、按期、延期、按期完工率（只算有交期的）、现在已过交期的。
- **工厂**（`plant`）：历史库（`history` 里配的 Postgres）逐小时的记录。有设备实测的小时就只算实测的（`source: measured`），没有就用仿真（或部分实测）的并标 `simulated`，说明不代表真实工厂。`weeks` 从周一起，截到这段时间；`compare` 是第一条采纳的计划前后各 N 整天（N 不超过这段时间的天数和采纳以来的天数，采纳当天不算），只说明同时发生，不证明是计划带来的。

| 方法 路径 | 请求 → 响应 | 说明 |
|---|---|---|
| `GET /api/value/{company}?days=30` | `ValueReport` | 往前 `days` 天（默认 30，1～366）；或 `from=YYYY-MM-DD&to=YYYY-MM-DD`（两天都含）；成员以上 |
| `GET /api/value/{company}?…&format=csv` | CSV | 同一份报告的表格（UTF-8 带 BOM，表格软件直接打开） |

命令行：`smartfactory value <company>`（`-days 7`、`-from 2026-09-01 -to 2026-09-30`、`-csv 成效.csv`）。工作台助手有 `value_report`，MCP 也有 `value_report`（成员以上）。

## 6. MCP `POST /mcp`

- 协议：Model Context Protocol，Streamable HTTP，无状态，用 JSON 响应。
- 必须带 `Authorization: Bearer <令牌>`，否则返回 401。服务器一个令牌都没有配置时返回 503。
- 每个客户端应该有自己的令牌（`smartfactory token -name 某某客户端 …`），能单独吊销，审计日志里也分得开。
- 令牌只看得到、只能操作它管的公司；角色不够的调用返回工具错误，说明缺什么。

| 工具 | 作用 | 权限 |
|---|---|---|
| `list_sites` | 公司、工厂，以及各自的实测点数（只列令牌管的公司） | 成员 |
| `plant_status` | 一个厂的 KPI、各线数据、库存、报警数、故障设备 | 成员 |
| `list_alarms` | 报警，可以按厂筛选，或只看未消除的 | 成员 |
| `get_asset` | 一台设备的状态、遥测、报警、实测值 | 成员 |
| `read_tags` | 设备实测值及其来源 | 成员 |
| `list_sources` | 采集源的连接状态（只列喂给令牌所管公司的） | 成员 |
| `send_command` | 操作设备，取值同 2.5；真实设备的指令先过指令把关：`status` 是 `sent`、`waiting`（等人批准，带 `request`）或 `refused`（带 `reason`）；记入审计日志 | 操作员 |
| `command_request` | 一条等人批准的指令现在怎样了，`wait` 秒内等结果（最多 120） | 操作员 |
| `check_forms` | 手填的表（点检、首件检验、化验）：每项问什么、标准、用于哪些设备、周期 | 成员 |
| `check_rounds` | 各设备的轮次：逾期（默认）、待做、已做，上次谁什么时候填的 | 成员 |
| `check_results` | 最近填的记录，每项的值、是否超差或异常、谁记的、什么时候记的 | 成员 |
| `simulate_plan` | 假设分析：复制当前状态，分别按原样和按改动后往前推演，对比结果 | 成员 |
| `value_report` | 一段时间里这套系统带来了什么（`days`，或 `from`、`to`），同 `GET /api/value/{company}` | 成员 |
| `scene_guide` | 怎样用两份 JSON 搭建一个新场景 | 成员 |
| `list_industries` / `get_industry` | 列出行业包 / 读取一个完整行业包 | 成员 |
| `create_industry` | 新建或替换行业包，带校验；行业包是所有公司共用的 | 管理所有公司的管理员 |
| `create_company` / `delete_company` | 新建或替换公司，立即开始仿真 / 删除 | 这家公司的管理员 |

## 7. 修改协议

1. 先改 `web/src/protocol/sfp.ts`。
2. 在 `web/` 里 `npm run schema` 重新生成 `protocol/sfp.schema.json`，`npm run typecheck` 检查前端。
3. 改服务器，在 `server/` 里跑 `go test ./internal/web/ -run TestProtocol`（需要先在 `web/` 里 `npm run build:engine`）。服务器发出不合 schema 的消息，或者 `clientMsg` 的字段和标准对不上，测试都会失败。
4. 按第 1 节的兼容规则决定是否要把 `PROTOCOL` 和 `web.Protocol` 一起加 1。
5. 更新本文。
