// Live objects the simulation mutates. Static plant structure comes from core/layout.
import type { MachineCfg, MeasureCfg, ToolingCfg } from '@/config/schema';
import type { LineLayout, Waypoint } from '@/core/layout';

export type AssetType = 'station' | 'conveyor' | 'buffer' | 'outfeed' | 'warehouse' | 'fgzone' | 'agv' | 'truck' | 'table';

export type AssetState =
  | 'running' | 'idle' | 'blocked' | 'stopped' | 'fault' | 'maint' | 'changeover'
  | 'moving' | 'waiting' | 'handling' | 'away'
  | 'ok' | 'empty' | 'low' | 'full'
  | 'free' | 'occupied' | 'reserved' | 'cleaning';

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
  /** °C a fully worn machine runs above a healthy one (default 18) */
  wear?: number;
  faults?: string[];
}

/** Spec of a machine that has thermal/power telemetry. */
export type MachineSpec = Required<Omit<TypeSpec, 'tau' | 'wear'>> & { tau?: number; wear?: number };
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
  /** a group of people (a walk-flow site): how many, elapsed when they arrived, seconds they have waited so far */
  n?: number;
  arr?: number;
  w?: number;
  /** the place it holds at a station with slots (table, pitch) */
  slot?: number;
  /** 元 the group has spent on what it took */
  spend?: number;
  /** how far the last measured step's reading was off target, in sigmas (a billet's heating) */
  dev?: number;
  /** what the group will take at the station it is at, once worked out */
  want?: Record<string, number>;
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
  /** 元 a guest pays per unit */
  price?: number;
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
  vehicle: 'truck' | 'van' | 'rider';
  /** sent by a schedule (a delivery round, a pick-up), not by the store's level */
  go?: boolean;
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
  /** units held at once (tables, pitches), people per slot, seconds by group size, hour units leave, takes per person */
  slots?: number;
  seats?: number;
  bySize?: number[];
  until?: number;
  perPerson?: boolean;
  /** why a crewed machine is paused (a meal break) */
  pause?: string;
  /** a place of a station with slots (a table, a pitch): where it is and how many it seats */
  place?: PlaceInfo;
  /** elapsed the place's state began */
  since?: number;
  /** seconds a place takes to clear after a group left */
  clear?: number;
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
  /** what the model alone says while measured values replace it (its own temperature, run on) */
  shadow?: { temp: number };
  /** model − measured since the last collection, per field: seconds, sum of errors, of absolute errors, seconds within tolerance */
  resid?: Record<string, Residual>;
  /** parameters fitted from this machine's measurements, in place of its template's */
  calib?: Calibration;
  /** the template's own values, kept while a calibration replaces them */
  template?: Calibration;
}

export interface Residual { s: number; sum: number; abs: number; ok: number }

/** A machine's parameters, as a calibration overrides its template's (any subset). */
export interface Calibration {
  idleRise?: number;
  runRise?: number;
  tau?: number;
  pIdle?: number;
  pRun?: number;
  vib?: number;
  /** seconds per unit at 100 % speed */
  cycle?: number;
  /** running hours between breakdowns at full health */
  mtbf?: number;
  /** minutes a repair takes, [min, max] */
  repair?: [number, number];
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
  /** index of the first station with slots: units come in only while it has room */
  gate?: number;
  /** planned stops (meal breaks, closed hours) within the counting window, [from, to] elapsed */
  pauses: [number, number][];
  /** elapsed before which the planner does not look for a new order again (made to stock, nothing needed) */
  noJobUntil?: number;
  /** good and scrap when the shift began, for the handover note */
  shiftMark?: { good: number; scrap: number };
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

export interface LineKpi {
  tp: number; perf: number; avail: number; quality: number; oee: number; good: number; scrap: number;
  /** share of the line's stations whose readings come from the equipment right now */
  measured: number;
}
export interface Kpis extends Omit<LineKpi, 'oee'> {
  oee: number; lines: LineKpi[]; power: number; energy: number;
  /** the longest window output is counted over, in seconds */
  span: number;
  /** 元 of electricity today at the company's tariff, and the price now */
  cost: number; price: number;
}

export interface PlaceInfo {
  line: string; station: string; slot: number; area: string; seats: number;
  /** what a place is called (桌, 营位) */
  label: string;
  /** kept overnight (a pitch): stays count as check-in and check-out */
  overnight: boolean;
  x: number; z: number;
}

/** A business day of a walk-flow site, from its opening hour. */
export interface VenueDay {
  day: number;
  /** groups and people that left, what they spent */
  groups: number; persons: number; revenue: number;
  /** groups seated, the seconds they waited in all; groups that left without coming in (on seeing the queue / after waiting) */
  seated: number; waitSum: number; balked: number; reneged: number;
  /** scores given, those in spec */
  rated: number; liked: number;
  /** delivery orders picked up and what they brought */
  takeaway: number; takeawayRevenue: number;
  /** stays that began / ended at a place kept overnight (pitches) */
  checkins: number; checkouts: number;
  /** sessions at places begun today, seconds of the closed ones and how many */
  sessions: number; diningSum: number; diningN: number;
}

/** A group waiting at the door: size, elapsed it came, elapsed it gives up. */
export interface Waiting { n: number; at: number; until: number }

/** A delivery order being made: elapsed it came in, elapsed it is ready (0 until stock is there), 元. */
export interface TakeawayOrder { at: number; ready: number; spend: number }

/** A finished batch in a depot's store, oldest first. */
export interface Batch { no: string; product: string; qty: number; made: number; expires: number }

/** A delivery round on its way to a site: wall-clock departure and arrival, what it carries. */
export interface Delivery { no: string; from: string; to: string; items: Record<string, number>; departed: number; eta: number; planned: number; full: boolean }
