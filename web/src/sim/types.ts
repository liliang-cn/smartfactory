// Live objects the simulation mutates. Static plant structure comes from core/layout.
import type { MachineCfg, MeasureCfg, ToolingCfg } from '@/config/schema';
import type { LineLayout, Waypoint } from '@/core/layout';

export type AssetType = 'station' | 'conveyor' | 'buffer' | 'outfeed' | 'warehouse' | 'fgzone' | 'agv' | 'truck';

export type AssetState =
  | 'running' | 'idle' | 'blocked' | 'stopped' | 'fault' | 'maint' | 'changeover'
  | 'moving' | 'waiting' | 'handling' | 'away'
  | 'ok' | 'empty' | 'low' | 'full';

/** A product stage key from the industry pack, e.g. billet, forged, packed. */
export type ProductKind = string;
export type GroupId = string;
export type { Waypoint };

export interface TypeSpec {
  label: string;
  idleRise?: number;
  runRise?: number;
  warn?: number;
  trip?: number;
  pIdle?: number;
  pRun?: number;
  vib?: number;
  tau?: number;
  faults?: string[];
}

/** Spec of a machine that has thermal/power telemetry. */
export type MachineSpec = Required<Omit<TypeSpec, 'tau'>> & { tau?: number };
export type Measure = MeasureCfg;

// ---------- live state ----------

export interface Product {
  id: string;
  s: number;
  done: number;
  proc: number;
  t: number;
  kind: ProductKind;
  stall: number;
  /** inventory this unit has been lent (tents, tableware …), by key */
  held?: Record<string, number>;
  /** failed a check and was reworked: not first-pass good */
  rework?: boolean;
  /** seconds this unit takes at the machine it is in (the nominal cycle plus this unit's scatter) */
  need?: number;
  /** a flaw left by a step, waiting for the inspection that finds it */
  flaw?: string;
  /** the material lot (steel heat) it was made from, its supplier and flaw factor */
  lot?: string;
  sup?: string;
  lf?: number;
}

/** Live count of one inventory item of the plant. */
export interface InventoryState {
  key: string;
  label: string;
  unit: string;
  kind: 'lend' | 'consume';
  cap: number;
  avail: number;
  low: number;
  refill: number;
  /** hours from order to delivery for items bought to order */
  lead?: number;
}

export interface Cargo { kind: 'raw' | 'fg'; n: number }

export interface AgvState {
  asset: Asset;
  route: Waypoint[];
  idx: number;
  x: number;
  z: number;
  heading: number;
  cargo: Cargo | null;
  mode: 'move' | 'dwell';
  dwell: number;
  waiting: boolean;
  waitedT: number;
  task: string;
  /** route index of the dock a located vehicle is standing at */
  dock?: number;
}

export interface TruckState {
  asset: Asset;
  route: Waypoint[];
  kind: 'in' | 'out';
  idx: number;
  x: number;
  z: number;
  heading: number;
  phase: 'away' | 'drive' | 'dock' | 'leave';
  wait: number;
  dock: number;
  load: number;
}

export interface Asset {
  id: string;
  name: string;
  type: AssetType;
  group: GroupId;
  spec?: TypeSpec;
  enabled: boolean;
  speed: number;
  fault: { text: string; at: number } | null;
  maint: number;
  state: AssetState;
  temp: number;
  vib: number;
  power: number;
  health: number;
  util: number;
  avail: number;
  cycles: number;
  progress: number;
  busy: Product | null;
  hist: { temp: number[]; power: number[]; vib: number[]; level: number[]; meas: number[] };
  // line stations
  line?: Line;
  machine?: MachineCfg;
  /** archetype of the machine's look, or the asset type for everything else */
  arch: string;
  s: number;
  cycle: number;
  makes?: ProductKind;
  measure?: Measure;
  /** inventory lent / used when a unit finishes here, and returned here */
  take?: Record<string, number>;
  give?: string[];
  /** label of the inventory item this station is waiting for */
  waitFor?: string;
  /** wearing tool (die, cutter …): units made with the current one */
  tool?: ToolingCfg & { used: number };
  /** seconds left of a tool change */
  changing: number;
  /** units still to scrap while setting up a new tool, and units made since the change */
  setupLeft?: number;
  sinceChange?: number;
  /** the tools of the part numbers not running now: units made with each */
  dies?: Record<number, number>;
  /** why the current change happened, when it is not wear (a part-number change) */
  changeReason?: string;
  reading?: { v: number; ok: boolean; at: number };
  rejects: number;
  verdict?: { ok: boolean; at: number };
  // stores
  stock: number;
  cap: number;
  count: number;
  // vehicles
  battery: number;
  vel: number;
  task: string;
  carrier?: string;
  kind?: 'in' | 'out';
  agv?: AgvState;
  truck?: TruckState;
  /** sim time when the maintenance crew will have cleared the current fault */
  repairAt?: number;
  /** values read from equipment (MQTT / Modbus TCP / OPC UA); while fresh they replace the simulated ones */
  live?: LiveValues;
}

export interface LiveValues {
  /** adapter name, e.g. broker, plc-1 */
  src: string;
  /** wall-clock ms of the last value */
  at: number;
  f: Record<string, number | string | boolean>;
}

/** A work order a line is running: one part number, a quantity, a due date. */
export interface Job {
  no: string; seq: number; variant: number; partNo: string; name: string; weight: number; input: number;
  qty: number; good: number; scrap: number; issued: number; due: number; heat: string; supplier: string; done?: number;
  /** who it is for, hours on the road to them, price per unit, the delivery date promised to them */
  customer: string; transit: number; price: number; promise: number;
  /** flawed units that got past final inspection, by flaw */
  escapes?: Record<string, number>;
  /** a production order from the company's ERP, not one the simulation made up */
  ext?: boolean;
}

/** A production order from the company's ERP, queued for a line. */
export interface ExtOrder { no: string; partNo?: string; name?: string; qty: number; due?: number; customer?: string; price?: number }

/** A line started or finished an ERP order. */
export interface OrderEvent { kind: 'start' | 'done'; no: string; plant: string; line: string; good: number; scrap: number; plantName: string; lineName: string }

/**
 * Where a plant's lines get ERP orders: `take` gives a line its next order, null when it should
 * wait for one, undefined when it makes up its own; `event` hears what became of them.
 */
export interface OrderFeed { take(line: Line): ExtOrder | null | undefined; event(e: OrderEvent): void }

/** Something that happens later on its own: goods leave, arrive, a customer complains. */
export interface Later { at: number; kind: 'ship' | 'deliver' | 'complain' | 'close'; data: Record<string, unknown> }

/** A material lot (one steel heat) feeding a line. */
export interface Lot { no: string; supplier: string; factor: number; left: number }

export interface Line {
  id: string;
  name: string;
  note: string;
  /** the material lot being used */
  lot?: Lot;
  /** the week its planned maintenance last ran */
  pmWeek?: number;
  /** the work order being run */
  job?: Job;
  /** the order it finished last, while it waits for the ERP's next one */
  last?: Job;
  /** the efficiency the planner promises due dates by: what the line's recent orders actually ran at */
  planEff?: number;
  layout: LineLayout;
  z: number;
  x0: number;
  x1: number;
  len: number;
  speed: number;
  gap: number;
  spawn: number;
  products: Product[];
  spawnT: number;
  pid: number;
  good: number;
  scrap: number;
  doneTimes: number[];
  conveyor: Asset;
  infeed: Asset;
  outfeed: Asset;
  stations: Asset[];
}

export interface Alarm {
  id: number;
  asset: string;
  code: string;
  prio: 1 | 2 | 3;
  text: string;
  at: number;
  active: boolean;
  acked: boolean;
  clearedAt?: number;
}

export interface LogEntry { at: number; asset: string; text: string; kind: 'info' | 'op' | 'alarm'; prio: number }

export type SimEvent =
  | { type: 'reject'; line: string; asset: string; x: number; z: number; kind: ProductKind }
  | { type: 'fault'; asset: string };

export type Command = 'start' | 'stop' | 'speed' | 'reset' | 'maint' | 'fault' | 'restock' | 'ship' | 'retool' | 'toollimit';
export interface CommandResult { ok: boolean; msg: string }

export interface LineKpi { tp: number; perf: number; avail: number; quality: number; oee: number; good: number; scrap: number }
export interface Kpis extends Omit<LineKpi, 'oee'> {
  oee: number; lines: LineKpi[]; power: number; energy: number;
  /** the longest window output is counted over, in seconds */
  span: number;
}
