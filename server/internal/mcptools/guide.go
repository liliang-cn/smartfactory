package mcptools

// SceneGuide is how to write industry packs and company configs, for agents that write them.
func SceneGuide() string { return guide }

// guide is what scene_guide returns: enough for an agent to write a working industry pack and
// company config without reading the source.
const guide = `# 用配置搭一个新的数字孪生场景

两份 JSON：
1. **行业包（industry）**：这一行有什么——产品（单元）长什么样、设备（工位）是什么、常用工艺路线。
2. **公司（company）**：在哪里、有多少——公司下的工厂（场地）、每个工厂的线（流程）。

流程：scene_guide → list_industries / get_industry（看现成例子，抄结构最快）→ create_industry → create_company → 在这台服务器上打开 /?company=<公司id>。改了就再 create 一次（同 id 覆盖，用这个行业包的场景会重建）。

## 模型（所有场景都一样）
- 一条 **线（line）** 是一个流程：**单元（product）** 从入口出发，按顺序经过各 **工位（machine）**，每个工位一次处理一个单元，用 cycle 秒；makes 把单元变成下一个**阶段**（换外观）。
- 线可以 repeat（{n} 会替换成序号），不同线可用不同产品和路线。
- 自动生成且可改名的配套：原料仓（plant.warehouse）→ 车辆把物料送到每条线入口；出口满了车辆运走到成品区（plant.finishedGoods）；进出两辆货车（plant.carriers）。
- 每个工位自动有：运行/待料/阻塞/停机/故障/维护状态、温度（thermal）、功率（power）、振动、健康度、故障与维修班组、移动率。
- 质量：工位带 measure（每个单元测一个值，有上下限，SPC/Cpk/漂移报警），超限的单元剔除；或 rejectRate（按概率剔除）。合格率显示为 industry.qualityLabel。

## 非工厂场景（营地、医院、洗车、餐厅……）
- industry.flow: "walk" —— 单元是人或车，沿步道走，不是传送带。
- plant.site: "outdoor" —— 草地、木栅栏、没有车间墙。
- industry.unit：单元量词（组、桌、位、辆）。qualityLabel：比如 "顾客满意度"。
- industry.terms：看板用词 {oee: "营地运营效率", throughput: "每小时接待", power: "实时用电", line: "个营区", vehicle: "送餐车",
  order: {title: "接待跟踪", tab: "预订", prefix: "BK", steps: [下达, 齐套, 进行中, 完成, 检验, 交付 六个阶段名]}}。
- industry.calendar.open: {from: 11, to: 1}：营业时间（可以跨午夜），之外不再进新的单元，已经在线上的走完，设备照常开着。
- 用 measure 表达评分（例如结账时的满意度 1–5 分，lo: 4 以下算投诉被剔除）。

## 库存与容量（帐篷 20 顶在用 18 这种）
- industry.inventory.<key> = { label, unit, kind: "lend"（借出会还：帐篷、桌椅、餐具）| "consume"（用掉要补：木炭、食材、木柴）, capacity, low?（低于这个比例报警，默认 0.1）, refill?（每次进货补多少） }
- 工位（或路线里的步骤）上写 take: {tent: 1}（单元在这里拿走），give: ["tent"]（在这里还回来）；不够时单元在工位等，并报「帐篷不足」。离开时没还的自动归还。
- plant.inventory: {tent: 20, lantern: 25}：这个场地只有这些库存项、用这些容量（不写就是行业包里的全部）。
- 看板「库存」页显示 在用/可用/总数；MCP plant_status 也返回。

## 工装寿命（锻模、切边模、刀具、模具……）
- 设备上写 tooling: { label: "锻模", life: 5000（次）, change: 900（换模秒数）, spare?: "库存key"（换模消耗一副备件）, drift?: 3 }
- 每加工一件计一次；寿命后 40% 测量值往公差边漂（drift 个 sigma），90% 报「准备更换」，到寿命自动停机换模（状态「换模」），没有备件就等并报「缺备用锻模」。
- 指令 retool 可提前换模；数据源可发 toolUsed / toolLife 覆盖计数。

## 接真实数据（任何场景）
数据源按约定 topic sf/{公司}/{厂}/{设备id}/{字段} 发：
- 工位：state、fault、temp、power、vib、speed、health、measure（每单元一次）、toolUsed、toolLife
- 库存：发到仓库（id 一般是 WH）字段 inv.<key>（可用数）、cap.<key>（总数）——预订系统、库存系统
- 车辆定位：车辆 id（AGV-01A…）字段 x、z（米，场地坐标）、heading、battery、task——GPS/UWB/车队系统
Modbus / OPC UA 用 "convention": "sf" 自动生成点位（工位、车辆、库存都在内）。

## 产品（单元）
products.<key> = { name, stages: { <阶段>: { parts: [...] } } }，第一个阶段是入场时的样子；工位 makes 指向后面的阶段。
单元在 walk 场景就是一组人：
` + "```json" + `
{ "shape": "capsule", "size": [0.17, 0.6], "at": [-0.3, 0, 0], "color": "#2f5bea" },
{ "shape": "sphere", "size": [0.13], "at": [-0.3, 0.97, 0], "color": "#f2c9a0" }
` + "```" + `
（身体 capsule 半径 0.17、直线段 0.6 → 总高 0.94；头放在 0.97。多个人沿 x / z 错开。）

## 零件 DSL（parts）
- shape: box [长x, 高y, 深z] · cylinder [半径, 高] · cone [半径, 高] · sphere [半径] · torus [半径, 管径] · capsule [半径, 直线段长]
- at: [x, y, z] 是零件**旋转后包围盒底面中心**（米）；rotate: [x°, y°, z°]
- color: 十六进制或调色板 white shell blue blueDeep blueSoft yellow orange kraft wood grey greyDark ink metal glass red green mint
- metal/rough 0–1，glow 自发光色，opacity，round（box 圆角），anim: {type: spin|stroke|blink|heat, axis, speed, to, window, when}
- **设备里的坐标**：原点在工位中心、地面上；+x 沿流程方向，+z 朝前方过道，y 向上。walk 场景步道顶面约 y=0.05。

## 设备（machines）
machines.<key> = {
  label, code（id 前缀，正则 ^[A-Z][A-Z0-9]{0,4}$，工位 id 自动是 code-线号+序号，如 GR-101）,
  look: { archetype, size?: [沿流程长, 进深, 高], color?, accent?, parts?: [...], operators?(manual 的人数 1–4), style?: "plain"（去掉工厂味：manual 变成服务台，frame 变成本色平台）, glow?, tool?, fence?, stacks?, fluid? },
  cycle（秒）, makes?, thermal: {idle, run, warn, trip}, power: {idle, run}（kW）, vibration, faults: [故障描述...], measure?, rejectRate?
}
- thermal.idle / run 是相对 22°C 环境的**温升**；warn / trip 是**绝对温度**，且必须 warn ≥ 22 + run + 6、trip > warn。
- 冷柜、冷库、速冻隧道这类制冷设备 idle / run 写负数（2°C 的冷藏柜：idle -20）：不会因温度跳停，停机或故障时往环境温度回升。thermal.wear 是完全磨损时多出的温度（默认 18，制冷设备写 2–4）。
- 原型（archetype）与默认尺寸：
  - frame [2,2,2]：**空底座，外观全靠 parts**——天幕、桌椅、烤炉、帐篷、柜台都用它。
  - manual [2.9,3.2,2.6]：工作台 + 操作员（operators 1–4 人）——接待、备菜、服务员岗位。
  - booth [3.4,2,2.4]：小房间（单元从中间穿过）。tunnel [4.5,1.9,2]：隧道/炉。chamber [3,1.8,2.1]：带门的箱。
  - enclosure [2.6,2.3,2.45] 机柜；press 压力机；robot 机械臂（tool: gripper/welder/torch/spray）；gantry 龙门检测；packer 装箱；safety 测试架；filler 灌装；tank 罐；roller 辊。
- routes.<key> = [ "机器key", { "machine": "key", "name": "改名", "cycle": 600, "makes": "阶段" }, ... ]

## 公司（company）
{ id, name, short（≤4 字，徽章）, industry（默认行业包）, plants: [{
  id, name, badge?（≤3 字）, hall（场地名）, sub?（一句说明）, site?: "hall"|"outdoor", industry?,
  lines: [{ name（可含 {n}）, note?, product, route 或 steps, repeat?, input?（入场阶段）, speed?（m/s）, order?: {no, part, qty} }],
  warehouse?（原料仓名）, finishedGoods?（成品区名）, carriers?: [进场车队, 出场车队]
}]}

## 经验
- 每条线 3–8 个工位最好看；cycle 决定节拍，瓶颈工位的 cycle 决定每小时产量。
- 步道上单元之间的间距由产品尺寸自动决定；长时间停留（用餐、过夜）用大 cycle（几百到几千秒）。
- 报错是中文、带路径（例如 machines.grill.thermal），照着改再提交。
`
