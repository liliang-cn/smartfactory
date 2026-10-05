// Static plant definition: where everything stands and how each machine type behaves.
// Units are metres and seconds. Material flows along +x on both lines.

export const AMBIENT = 22;
export const LINE_Y = 0.9; // belt top height
export const PRODUCT_GAP = 1.0; // minimum spacing between parts on a belt
export const BELT_SPEED = 0.55; // m/s at 100%

export const FLOOR = { w: 76, d: 48 };

export const TYPE_SPECS = {
  cnc: {
    label: '数控加工中心', idleRise: 6, runRise: 40, warn: 72, trip: 85, pIdle: 3.2, pRun: 18, vib: 2.4,
    faults: ['主轴过载', '刀具寿命到限', '冷却液压力低'],
  },
  robot: {
    label: '装配机器人', idleRise: 4, runRise: 30, warn: 64, trip: 76, pIdle: 0.8, pRun: 6.5, vib: 1.2,
    faults: ['J3 轴扭矩超限', '夹爪真空丢失', '安全光栅触发'],
  },
  welder: {
    label: '焊接机器人', idleRise: 5, runRise: 34, warn: 66, trip: 78, pIdle: 1.2, pRun: 11, vib: 1.4,
    faults: ['送丝异常', '保护气流量低', 'J5 轴扭矩超限'],
  },
  press: {
    label: '液压冲床', idleRise: 8, runRise: 36, warn: 70, trip: 82, pIdle: 4, pRun: 28, vib: 4.8,
    faults: ['液压压力低', '模具位置偏差', '双手按钮失效'],
  },
  oven: {
    label: '固化炉', idleRise: 150, runRise: 165, warn: 200, trip: 214, pIdle: 22, pRun: 34, vib: 0.6, tau: 90,
    faults: ['二区加热器故障', '循环风机停转', '排风温度高'],
  },
  qc: {
    label: '视觉检测站', idleRise: 6, runRise: 16, warn: 50, trip: 60, pIdle: 0.4, pRun: 1.6, vib: 0.2,
    faults: ['相机帧同步丢失', '光源亮度衰减', '标定偏差超限'],
  },
  pack: {
    label: '装箱机', idleRise: 5, runRise: 20, warn: 55, trip: 66, pIdle: 1, pRun: 5.5, vib: 1.6,
    faults: ['纸箱仓空', '封箱胶带断', '推杆卡滞'],
  },
  conveyor: {
    label: '输送线', idleRise: 4, runRise: 14, warn: 50, trip: 60, pIdle: 0.3, pRun: 3.2, vib: 0.9,
    faults: ['皮带打滑', '驱动电机过流'],
  },
  agv: {
    label: 'AGV 小车', pIdle: 0.15, pRun: 1.8,
    faults: ['激光雷达检测到障碍', '导航磁条丢失', '驱动轮过流'],
  },
  truck: { label: '货车', faults: ['车门传感器异常', '司机未签到'] },
  buffer: { label: '上料缓存' },
  outfeed: { label: '成品托盘' },
  warehouse: { label: '立体仓库' },
  fgzone: { label: '成品暂存区' },
};

export const LINES = [
  {
    id: 'L1', name: '一号线', note: '机加工', z: -9, x0: -17, x1: 18,
    conveyor: { id: 'CV-101', name: '一号线输送带' },
    infeed: { id: 'BF-101', name: '一号线上料缓存', cap: 40 },
    outfeed: { id: 'OF-101', name: '一号线成品托盘', cap: 24 },
    stations: [
      { id: 'CNC-101', type: 'cnc', name: '加工中心 1', x: -11, cycle: 9 },
      { id: 'CNC-102', type: 'cnc', name: '加工中心 2', x: -4, cycle: 8 },
      { id: 'RB-101', type: 'robot', name: '装配机器人', x: 3, cycle: 7 },
      { id: 'QC-101', type: 'qc', name: '视觉检测', x: 9, cycle: 4 },
      { id: 'PK-101', type: 'pack', name: '装箱机', x: 14, cycle: 5 },
    ],
  },
  {
    id: 'L2', name: '二号线', note: '冲压焊接', z: 9, x0: -17, x1: 18,
    conveyor: { id: 'CV-201', name: '二号线输送带' },
    infeed: { id: 'BF-201', name: '二号线上料缓存', cap: 40 },
    outfeed: { id: 'OF-201', name: '二号线成品托盘', cap: 24 },
    stations: [
      { id: 'PR-201', type: 'press', name: '冲床', x: -11, cycle: 6 },
      { id: 'RB-201', type: 'welder', name: '焊接机器人', x: -4, cycle: 8.5 },
      { id: 'OV-201', type: 'oven', name: '固化炉', x: 3, cycle: 8, len: 5 },
      { id: 'QC-201', type: 'qc', name: '视觉检测', x: 9, cycle: 4 },
      { id: 'PK-201', type: 'pack', name: '装箱机', x: 14, cycle: 5 },
    ],
  },
];

// Docks sit on the aisle side of each line. Lanes are kept apart so AGVs never share track.
const WH_X = -23.5;
const IN_X = -16.4;
const OUT_X = 19.6;
const FG_X = 22;

export const AGVS = [
  {
    id: 'AGV-01', name: '一号线送料车',
    route: [
      { x: WH_X, z: -1.6, act: 'load', line: 'L1' },
      { x: IN_X, z: -1.6 },
      { x: IN_X, z: -5.4, act: 'drop', line: 'L1' },
      { x: IN_X, z: -1.6 },
    ],
  },
  {
    id: 'AGV-02', name: '二号线送料车',
    route: [
      { x: WH_X, z: 1.6, act: 'load', line: 'L2' },
      { x: IN_X, z: 1.6 },
      { x: IN_X, z: 5.4, act: 'drop', line: 'L2' },
      { x: IN_X, z: 1.6 },
    ],
  },
  {
    id: 'AGV-03', name: '一号线成品车',
    route: [
      { x: FG_X, z: -1.4, act: 'deliver' },
      { x: OUT_X, z: -1.4 },
      { x: OUT_X, z: -6.4, act: 'pick', line: 'L1' },
      { x: OUT_X, z: -1.4 },
    ],
  },
  {
    id: 'AGV-04', name: '二号线成品车',
    route: [
      { x: FG_X, z: 1.4, act: 'deliver' },
      { x: OUT_X, z: 1.4 },
      { x: OUT_X, z: 6.4, act: 'pick', line: 'L2' },
      { x: OUT_X, z: 1.4 },
    ],
  },
];

export const WAREHOUSE = { id: 'WH-01', name: '原料立体库', x0: -36, x1: -25, z0: -16, z1: 16, cap: 3200 };
export const FG_ZONE = { id: 'FG-01', name: '成品暂存区', x0: 23.5, x1: 32, z0: -6, z1: 6 };

export const GROUPS = [
  { id: 'L1', name: '一号线', note: '机加工' },
  { id: 'L2', name: '二号线', note: '冲压焊接' },
  { id: 'LOG', name: '物流', note: 'AGV' },
  { id: 'WH', name: '仓储', note: '' },
];

// Road traffic: trucks come in off the public road, dock outside the hall, and leave the other way.
export const TRUCKS = [
  {
    id: 'TRK-11', name: '原料进厂车', kind: 'in', carrier: '顺达物流',
    route: [{ x: -130, z: 27 }, { x: -50, z: 27 }, { x: -50, z: 1.5, act: 'dock' }, { x: -50, z: -80 }],
  },
  {
    id: 'TRK-27', name: '成品发运车', kind: 'out', carrier: '远航货运',
    route: [{ x: 140, z: 27 }, { x: 42, z: 27 }, { x: 42, z: 1.5, act: 'dock' }, { x: 42, z: -80 }],
  },
];
