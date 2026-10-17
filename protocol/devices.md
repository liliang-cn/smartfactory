# SmartFactory 设备协议 SFD/1

设备（PLC、传感器、网关、车队系统、库存系统）怎样把数据送进孪生，孪生又怎样把命令下发给设备。支持 MQTT、Modbus TCP、OPC UA 三种协议。每种协议有两种接法：

- **约定接入（`sf`）**：设备按本文的约定发布数据，孪生不需要任何点位配置。新设备优先用这种。
- **映射接入**：设备已有自己的 topic、寄存器或节点，就在服务器配置里写映射规则（第 5 节），设备一侧不用改。

| 文件 | 作用 |
|---|---|
| `web/src/protocol/sfp.ts` | 标准本身（zod），定义 `DeviceFields`、`DeviceCommand`、`DeviceMap` |
| `protocol/sfp.schema.json` | 由上面生成的 JSON Schema，与浏览器协议共用一个文件 |
| `server/internal/e2e/standard_test.go` | 一致性测试：抓取设备模拟器在 MQTT 上的全部流量和服务器下发的命令，逐条按 schema 校验 |
| `GET /api/devices/{company}/{plant}` | 某个厂的完整地址表（第 6 节），由代码生成 |

浏览器和服务器之间的协议见 [README.md](README.md)。

## 1. 通用约定

### 1.1 地址

每个值都落在 **公司 / 厂 / 设备 / 字段** 这四级上。前三级用配置里的 id，例如 `huaduan / p1 / HP-101`。

### 1.2 字段

所有协议共用同一套字段，schema 名为 `DeviceFields`。

| 字段 | 类型 | 单位 / 取值 | 作用 |
|---|---|---|---|
| `state` | 字符串 | `idle` `running` `blocked` `stopped` `fault` `maint` `changeover` | 设备状态。`fault` 触发故障；`stopped` 停机；`maint` 维护；`changeover` 换模；其余几种都按运行处理 |
| `fault` | 字符串 / 布尔 / 数字 | 故障文字 | 非空即故障，文字显示在报警里。`""`、`false`、`0` 表示清除故障 |
| `faultCode` | 整数 | 0 = 无 | 第 n 个故障，对应设备类型 `faults` 列表里的第 n 项 |
| `running` | 布尔 / 数字 / 字符串 | `true` `1` `on` `run` / `false` `0` `off` `stop` | 开机或停机 |
| `temp` | 数字 | °C | 替换模型算出的温度，超限报警按实测值判断 |
| `power` | 数字 | kW | 替换模型算出的功率，能耗按实测值累计 |
| `vib` | 数字 | mm/s | 替换模型算出的振动 |
| `speed` | 数字 | 倍率 0.5–1.5；大于 5 时按百分数理解 | 速度 |
| `health` | 数字 | 0–1 | 健康度 |
| `measure` | 数字 | 检测项的单位 | 一个件测一次，每件发一条。孪生按检测项的公差判合格与否 |
| `lastMeasure` + `mcount` | 数字 + 计数 | — | 轮询类协议用的测量方式：`lastMeasure` 是最近一次测量值，`mcount` 每测一件加 1。计数一变化就记一件；孪生第一次读到的计数只当作起点，不算一件。设备要先写 `lastMeasure`，再加计数 |
| `toolUsed` / `toolLife` | 整数 | 件 | 当前模具已做件数、模具寿命 |
| `stock` / `count` | 数字 | 件 | 缓存库存、计数 |
| `cycles` | 整数 | 件 | 机器自己的累计加工件数（只增，可以归零重计）。孪生不拿它计产量，只用来按实测校准节拍 |
| `battery` | 数字 | % | 车辆电量 |
| `x` / `z` / `heading` | 数字 | 厂内坐标（米）、弧度 | 车辆定位。孪生里的车会开到上报的位置 |
| `task` | 字符串 | — | 车辆当前任务 |
| `inv.<品项>` / `cap.<品项>` | 数字 | 品项的单位 | 库存可用量 / 容量，发在仓库这台设备上 |

不在表里的字段照样保存，并在设备面板里显示，但孪生不解释它们的含义。

### 1.3 实测值什么时候生效

- 设备上报的值从到达起 **20 秒** 内优先于仿真。超过 20 秒没有新值，这台设备就回到仿真。
- 状态和故障走的是和操作员操作相同的路径，报警、日志、停机统计都会照常记录。
- 上报了 `measure` 或 `mcount` 的设备，合格判定以实测值为准。
- 一条消息里有多个字段时，按字段名的字母顺序处理。这样 `fault`（故障文字）会先于 `state` 生效，`lastMeasure` 也会先于 `mcount` 生效。

### 1.4 命令

孪生下发的命令（schema 名为 `DeviceCommand`）：

| cmd | 含义 | arg |
|---|---|---|
| `start` / `stop` | 启动 / 停止 | — |
| `reset` | 复位故障 | — |
| `maint` | 切换维护状态 | — |
| `speed` | 调速 | 0.5–1.5 |
| `fault` | 故障演练 | — |
| `retool` | 立即换模 | — |
| `toollimit` | 设定计划换模点 | 0.3–1 |
| `restock` / `ship` | 原料入库 / 成品发运 | — |

命令发给谁：
- 操作员和 AI 助手（MCP）发出的命令，会下发到所有数据源。
- 访客的命令和访客操作的自动撤销，只会下发到配置里标了 `public` 的数据源，也就是模拟设备。

设备执行命令后，应该在下一次上报里体现新的状态。孪生以设备上报的状态为准。

## 2. MQTT

| 项 | 约定 |
|---|---|
| Broker | 服务器内置 broker，默认 `:41883`（配置项 `mqtt.listen`），可另开 TLS 端口（`mqtt.tls.listen`）。也可以接工厂自己的 broker，在数据源里写 `"convention": "sf"` |
| 身份 | 每台设备在后台「接入」登记（或 `smartfactory device add`），拿到自己的令牌（当密码）或客户端证书（连 TLS 端口，不用密码）；client id 和用户名都是设备名。登记的设备只能发它负责的设备的读数、只能订阅它们的 `cmd`，见 README.md 的「设备身份」。配置里的共用账号（`mqtt.users`）不受限 |
| 前缀 | `sf`（配置项 `mqtt.prefix`） |
| 数据源名 | 设备的 client id（登记的设备就是设备名）。断开连接时，这个数据源在界面上显示为离线 |
| QoS | 数据用 0 或 1 都可以；命令以 QoS 1 下发 |

| Topic | 方向 | 载荷 |
|---|---|---|
| `sf/{company}/{plant}/{asset}` | 设备 → 孪生 | JSON 对象，键是字段（1.2 节）；可以带 `"t"`，是这些读数的采集时间 |
| `sf/{company}/{plant}/{asset}/{field}` | 设备 → 孪生 | 单个字段：纯文本的数字、`true`/`false` 或字符串；也可以是 `{"v": …}` 或 `{"value": …}`，再加 `"t"` 是采集时间 |
| `sf/{company}/{plant}/{asset}/measure` | 设备 → 孪生 | 一件的测量值，纯文本数字 |
| `sf/{company}/{plant}/{asset}/cmd` | 孪生 → 设备 | `{"cmd": "speed", "arg": 1.2, "by": "operator", "t": 1791431180107}` |

例子：

```
sf/huaduan/p1/HP-101          {"state":"running","temp":67.2,"power":182.5,"vib":2.1,"speed":1,"health":0.93,"toolUsed":8120,"toolLife":12000}
sf/huaduan/p1/HP-101/temp     67.2
sf/huaduan/p1/HP-101/measure  64.03
sf/huaduan/p1/AGV-01A         {"x":12.4,"z":-3.1,"heading":1.571,"battery":76,"task":"送料 L2"}
sf/huaduan/p1/WH              {"inv.crankDie":6,"cap.crankDie":8}
sf/huaduan/p1/HP-101/temp     {"v":67.2,"t":1791431180107}
sf/huaduan/p1/HP-101          {"temp":67.2,"power":182.5,"t":"2026-10-11T16:26:20+08:00"}
```

**采集时间**：`t` 是 Unix 毫秒或秒（数字或数字字符串），或 RFC 3339。不带 `t` 的读数按服务器收到的时间算。比服务器时钟快 5 分钟以上的 `t` 不用，改按收到时间，并标记 4。超过 1 分钟前的读数（网关断线后补发的）照样入库，但不再驱动孪生。

**入库和校验**：服务器有数据目录时，每个读数都会入库（见 README.md 的「设备数据」）。入库前先校验：超出这个字段的合理范围标记 1，变化比设备可能的还快标记 2。打了这两种标记的读数照样入库，但不进孪生，并计入采集源的 `flagged`。合理范围默认按设备推出：温度最高到跳闸温度加 200 °C，每秒最多变 50 °C；功率最高到运行功率的 5 倍加 10 kW；振动、健康度、速度、电量有固定上下限；测量值以目标值为中心，宽度取 100σ 和 10 倍公差中较大的。配置里的 `series.limits` 可以按公司、厂、设备（可写 `HP-*` 这样的前缀）、字段覆盖 `min`、`max`、`rate`（每秒最多变多少）。同一字段连续 3 次跳变后，新的水平按真实变化接受。


### 2.1 餐桌

饭店的桌也按这个约定上报，资产是桌号（如 `A03`），字段固定两个：

| Topic | 载荷 |
|---|---|
| `sf/{company}/{plant}/{table}/table` | 桌态：`{"state":"occupied","people":3,"since":"2026-10-17T12:05:00+08:00","source":"cam-1"}`，`state` 是 `free`、`occupied`、`reserved`、`cleaning`；只发一个词（`occupied`）也行；`since` 不写按收到的时间 |
| `sf/{company}/{plant}/{table}/session` | 收银的用餐事件：`{"event":"seated","people":4,"at":1792216800000,"source":"pos"}`，`event` 是 `seated`、`ordered`、`paid`、`left` |

`source` 不写时用设备名。它们和 HTTP 上报（README.md 的「餐桌」）进同一个桌态库：同一条报两次只记一次，补发的旧状态按时间放进用餐记录，不回退孪生。这两个字段不当读数送进孪生的设备。摄像头盒子在后台「接入」登记成设备，资产填它看得到的桌号（或 `*` 整个门店）。

```
mosquitto_pub -h 192.168.1.20 -p 41883 -i cam-front -u cam-front -P "$SF_DEVICE_TOKEN" \
  -t sf/qianyu/chunxi/A03/table -m '{"state":"occupied","people":4}'
mosquitto_pub -h 192.168.1.20 -p 41883 -i pos-1 -u pos-1 -P "$SF_DEVICE_TOKEN" \
  -t sf/qianyu/chunxi/A03/session -m '{"event":"paid"}'
```

## 3. Modbus TCP

- 设备是从站，孪生作为主站轮询。轮询间隔由数据源的 `poll` 设置，默认 1 s。站号由 `unit` 设置，默认 1。
- 全部数据都在保持寄存器里，字节序为大端。`float32` 占两个寄存器，高字在前。
- 设备按 **地址表里的顺序** 排列（第 6 节）。这个顺序来自厂的配置，不要自己编号。

**机器**：第 i 台机器占用 14 个寄存器，起始地址为 `i × 14`。

| 偏移 | 字段 | 类型 | 换算 |
|---|---|---|---|
| +0 | `state` | uint16 | 状态码，见下表 |
| +1 | `temp` | int16 | ÷10 |
| +2 | `power` | uint16 | ÷10 |
| +3 | `vib` | int16 | ÷100 |
| +4 | `speed` | uint16 | ÷100 |
| +5 | `health` | uint16 | ÷1000 |
| +6..+7 | `lastMeasure` | float32 | 只有带检测项的机器才有 |
| +8 | `mcount` | uint16 | 每测一件加 1 |
| +9 | `faultCode` | uint16 | 0 = 无故障 |
| +10 | 命令码 | uint16 | 由孪生写入，见 3.1 |
| +11 | 命令参数 | int16 | ×100 |
| +12 | `toolUsed` | uint16 | 只有带模具的机器才有 |
| +13 | `toolLife` | uint16 | 只有带模具的机器才有 |

状态码：`0 idle` `1 running` `2 blocked` `3 stopped` `4 fault` `5 maint` `6 changeover`

**车辆**：排在所有机器之后，每辆车占 6 个寄存器：

| 偏移 | 字段 | 类型 | 换算 |
|---|---|---|---|
| +0 | `x` | int16 | ÷10 |
| +1 | `z` | int16 | ÷10 |
| +2 | `heading` | int16 | ÷1000 |
| +3 | `battery` | uint16 | ÷10 |
| +4..+5 | 保留 | — | — |

**库存**：排在所有车辆之后，每个品项占 2 个寄存器：`inv.<品项>`、`cap.<品项>`，都是 uint16。

### 3.1 命令握手

1. 孪生先把参数 ×100 写入 +11。
2. 再把命令码写入 +10。
3. 设备执行后，把 +10 清零，表示已收到。

命令码：`1 start` `2 stop` `3 reset` `4 maint` `5 speed` `6 fault` `7 retool` `8 toollimit`

## 4. OPC UA

- 设备是服务器，孪生作为客户端轮询。轮询间隔默认 1 s。
- 安全策略为 None。鉴权方式：数据源配置了用户名就用用户名和密码，否则匿名登录。
- 节点放在命名空间 1，使用字符串 id：`ns=1;s={asset}.{field}`。

| 节点 | 类型 | 说明 |
|---|---|---|
| `{machine}.state` | String | 状态，取值同 1.2 节 |
| `{machine}.fault` | String | 故障文字；空串表示无故障 |
| `{machine}.temp` `.power` `.vib` `.speed` `.health` | Double | 单位同 1.2 节，不需要换算 |
| `{machine}.lastMeasure` `.mcount` | Double | 只有带检测项的机器才有 |
| `{machine}.toolUsed` `.toolLife` | Double | 只有带模具的机器才有 |
| `{machine}.cmd` | String，可写 | 命令名，见 1.4 节 |
| `{machine}.arg` | Double，可写 | 命令参数 |
| `{vehicle}.x` `.z` `.heading` `.battery` | Double | 车辆 |
| `{vehicle}.task` | String | 车辆任务 |
| `{warehouse}.inv.<品项>` `.cap.<品项>` | Double | 库存 |

**命令握手**：
1. 孪生先写 `{machine}.arg`。
2. 再写 `{machine}.cmd`。
3. 设备执行后，把 `cmd` 置为空串。

## 5. 映射接入（现有设备）

在服务器配置的 `sources` 里，为每个数据源列出 `points`（读）和 `commands`（写）。完整例子见 `server/examples/smartfactory.json`。

| 协议 | 读：一个 point | 写：一个 command |
|---|---|---|
| MQTT | `topic` 是 topic 过滤器，`+` 匹配到的段依次记为 `{1}` `{2}` …，可以用在 `asset`、`field` 里；`json` 是载荷里的点路径，例如 `data.temperature` | `topic` 和 `payload`，`payload` 里可以用 `{cmd}` `{arg}` `{asset}` 占位 |
| Modbus | `register`（`holding` `input` `coil` `discrete`）、`address`、`kind`（`int16` `uint16` `int32` `uint32` `float32` `bool`） | `register`、`address`、`value`；参数可以另写到 `argAddress`，按 `argScale` 放大 |
| OPC UA | `node`，例如 `ns=2;s=Press1.Temperature` | `node`、`text` 或 `value`；参数写到 `argNode` |

所有 point 都能做换算：`value = raw × scale + offset`；`map` 把枚举值翻成字段值，例如 `{"0": "stopped", "1": "running", "2": "fault"}`。

## 6. 地址表 `GET /api/devices/{company}/{plant}`

返回 `DeviceMap`：这个厂在约定接入下每个值的位置。华锻一厂有 60 台机器，Modbus 共 554 个点，车辆从地址 840 开始。返回内容包括：
- MQTT 的三类 topic；
- Modbus 每个点的寄存器地址、类型和换算，每台机器的命令寄存器，以及状态码表和命令码表；
- OPC UA 每个节点，以及每台机器的命令节点。

厂不存在时返回 404。接线时以这个接口的结果为准，第 3、4 节是它的生成规则。

```
curl https://factory.superleo.cn/api/devices/huaduan/p1
```

## 7. 修改协议

1. 先改 `web/src/protocol/sfp.ts`，再在 `web/` 里运行 `npm run schema`。
2. 改 `server/internal/sfconv`（Modbus 和 OPC UA 的布局）、`server/internal/mapping`（MQTT 的约定），或 `web/src/sim/plant.ts` 的 `ingest`（字段含义）。
3. 运行 `go test ./internal/e2e/ ./internal/web/`。设备的实际流量、服务器下发的命令和地址表都会按 schema 校验。
4. 更新本文。Modbus 寄存器布局或命令码一旦变化，现场设备就得重新编程，所以应当把 SFD 的版本号加 1。
