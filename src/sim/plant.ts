// The "physical" side of the twin: a discrete simulation of the plant.
// The 3D view and UI only read this state and send commands through `command()`,
// so swapping it for a live MQTT/OPC UA feed means implementing the same surface.

import type { History } from './history';
import type { MachineCfg } from '@/config/schema';
import type { PlantLayout } from '@/core/layout';
import type {
  AgvState, Alarm, Asset, AssetState, AssetType, Command, CommandResult, InventoryState, Job, Kpis, Line, LineKpi, LiveValues, LogEntry, MachineSpec,
  Product, ProductKind, SimEvent, TruckState, TypeSpec,
} from './types';

export const AMBIENT = 22;

// Behaviour of the equipment every plant has, by convention.
const BUILTIN: Partial<Record<AssetType, TypeSpec>> = {
  conveyor: { label: '输送线', idleRise: 4, runRise: 14, warn: 50, trip: 60, pIdle: 0.3, pRun: 3.2, vib: 0.9, faults: ['皮带打滑', '驱动电机过流'] },
  agv: { label: 'AGV 小车', pIdle: 0.15, pRun: 1.8, faults: ['激光雷达检测到障碍', '导航磁条丢失', '驱动轮过流'] },
  truck: { label: '货车', faults: ['车门传感器异常', '司机未签到'] },
  buffer: { label: '上料缓存' },
  outfeed: { label: '成品托盘' },
  warehouse: { label: '立体仓库' },
  fgzone: { label: '成品库' },
};

const machineSpec = (m: MachineCfg): MachineSpec => ({
  label: m.label, idleRise: m.thermal.idle, runRise: m.thermal.run, warn: m.thermal.warn, trip: m.thermal.trip, tau: m.thermal.tau,
  pIdle: m.power.idle, pRun: m.power.run, vib: m.vibration, faults: m.faults,
});

export const isMachineSpec = (s: TypeSpec | undefined): s is MachineSpec => s?.runRise !== undefined;

const HIST_LEN = 240;
/** How long a value from equipment stays authoritative after it arrives. */
const LIVE_TTL_MS = 20_000;

/** Live field values of an asset if its equipment reported recently. */
export const liveFields = (a: Asset): LiveValues['f'] | null =>
  a.live && Date.now() - a.live.at < LIVE_TTL_MS ? a.live.f : null;

const truthy = (v: unknown) => v === true || v === 1 || (typeof v === 'string' && /^(1|true|on|yes|run|running)$/i.test(v));
const falsy = (v: unknown) => v === false || v === 0 || (typeof v === 'string' && /^(0|false|off|no|stop|stopped|)$/i.test(v));
const hhmm = (sec: number) => {
  const m = Math.floor(sec / 60) % 1440;
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
};
const r2 = (v: number) => Math.round(v * 100) / 100;
const rand = (a: number, b: number) => a + Math.random() * (b - a);
const clamp = (v: number, a: number, b: number) => Math.max(a, Math.min(b, v));
const pick = <T,>(arr: T[]) => arr[Math.floor(Math.random() * arr.length)];
const ema = (dt: number, tau: number) => 1 - Math.exp(-dt / tau);

export const STATE_LABEL: Record<AssetState, string> = {
  running: '运行', idle: '待料', blocked: '阻塞', stopped: '停机', fault: '故障', maint: '维护', changeover: '换模',
  moving: '行驶', waiting: '等待', handling: '装卸', away: '在途', ok: '正常', empty: '缺料', low: '低位', full: '满载',
};

type AssetInit = Partial<Asset> & Pick<Asset, 'id' | 'name' | 'type'>;

type ProductTuple = [id: string, s: number, done: number, proc: number, t: number, kind: string, stall: number, held?: Record<string, number> | null];

/** A plant as written to disk by the server. */
export interface SavedPlant {
  fp: string;
  state: PlantState;
  /** completion times of the last hour, per line in layout order */
  done: number[][];
  meas: [string, number[]][];
  repair: [string, number][];
  /** tools out for reconditioning: [inventory key, elapsed when back] */
  recond?: [string, number, number?][];
}

/** A plant's replicated state, as sent from the server to browsers. */
export interface PlantState {
  clock: number;
  elapsed: number;
  energy: number;
  power: number;
  alarmSeq: number;
  assets: (Pick<Asset, 'id' | 'enabled' | 'speed' | 'fault' | 'maint' | 'state' | 'temp' | 'vib' | 'power' | 'health' | 'util' | 'avail' | 'cycles' | 'progress' | 'rejects' | 'stock' | 'count' | 'battery' | 'vel' | 'task'> & {
    toolUsed?: number;
    toolLimit?: number;
    changing?: number;
    busy: string | null;
    reading: Asset['reading'] | null;
    verdict: Asset['verdict'] | null;
    live: LiveValues | null;
  })[];
  lines: { id: string; spawnT: number; pid: number; good: number; scrap: number; tp: number; products: ProductTuple[]; lot?: [string, string]; job?: [string, string, number, number, number] }[];
  agvs: Pick<AgvState, 'idx' | 'x' | 'z' | 'heading' | 'cargo' | 'mode' | 'dwell' | 'waiting' | 'waitedT' | 'task'>[] & { id: string }[];
  trucks: (Pick<TruckState, 'idx' | 'x' | 'z' | 'heading' | 'phase' | 'wait' | 'dock' | 'load'> & { id: string })[];
  alarms: Alarm[];
  log: LogEntry[];
  /** [key, available, capacity] */
  inventory?: [string, number, number][];
  events: SimEvent[];
}

export class PlantSim {
  readonly layout: PlantLayout;
  clock = 6 * 3600 + 12 * 60; // shift clock, seconds since midnight
  elapsed = 0;
  assets = new Map<string, Asset>();
  lines: Line[] = [];
  lineById: Record<string, Line> = {};
  agvs: AgvState[] = [];
  trucks: TruckState[] = [];
  alarms: Alarm[] = [];
  log: LogEntry[] = [];
  events: SimEvent[] = [];
  energyKWh = 0;
  powerKW = 0;
  wh!: Asset;
  fg!: Asset;
  private alarmSeq = 1;
  private histAcc = 0;
  /**
   * Follows a server that runs the authoritative simulation. The local copy keeps moving parts
   * and vehicles between server frames, but faults, alarms, the log, readings and rejects only
   * arrive from the server.
   */
  follower = false;
  /** what the site keeps a count of (tents, charcoal …) */
  inventory: InventoryState[] = [];
  /** false until a follower has received its first state from the server */
  synced = true;
  /** hour-by-hour record for the history database (server only) */
  history?: History;
  /** nobody will look at sparklines (history backfill, what-if runs): skip keeping them */
  headless = false;
  /** `${asset} ${code}` of every active alarm, so clearing a condition that is not raised is cheap */
  private raised = new Set<string>();
  /** reconditioned tools coming back to stock: [inventory key, elapsed when back] */
  private repairs: [string, number, number?][] = [];

  constructor(layout: PlantLayout) {
    this.layout = layout;
    this.build();
  }

  // ---------- construction ----------

  private asset(def: AssetInit): Asset {
    let spec = def.machine ? machineSpec(def.machine) : BUILTIN[def.type];
    if (def.type === 'agv' && this.layout.terms.vehicle) spec = { ...spec!, label: this.layout.terms.vehicle };
    const a: Asset = {
      group: 'WH', s: 0, cycle: 0, stock: 0, cap: 0, count: 0, battery: 0, vel: 0, task: '', rejects: 0, arch: def.type,
      ...def,
      spec,
      enabled: true, speed: 1, fault: null, maint: 0, changing: 0, state: 'idle',
      temp: AMBIENT, vib: 0, power: 0, health: rand(0.74, 0.97), util: 0.7, avail: 0.97,
      cycles: Math.floor(rand(1200, 5200)), progress: 0, busy: null,
      hist: { temp: [], power: [], vib: [], level: [], meas: [] },
    };
    // Test stations start capable (Cpk around 1.3+) and only drift as they wear.
    // Pretend the last shift's worth of units went through, so the SPC chart starts full.
    if (a.measure) {
      a.health = rand(0.9, 0.98);
      for (let i = 0; i < 40; i++) this.measure(a, true);
    }
    if (isMachineSpec(spec)) a.temp = AMBIENT + spec.idleRise + (spec.runRise - spec.idleRise) * 0.7;
    this.assets.set(a.id, a);
    return a;
  }

  private build() {
    const L = this.layout;
    // lent items start all on the shelf; consumables start mostly stocked
    this.inventory = L.inventory.map((it) => ({ ...it, avail: it.kind === 'lend' ? it.cap : Math.round(it.cap * 0.8) }));
    for (const ll of L.lines) {
      const line = {
        id: ll.id, name: ll.name, note: ll.note, layout: ll, z: ll.z, x0: ll.x0, x1: ll.x1, len: ll.x1 - ll.x0,
        speed: ll.speed, gap: ll.gap, spawn: ll.spawn,
        products: [], spawnT: 0, pid: 0, good: 0, scrap: 0, doneTimes: [],
      } as unknown as Line;
      line.conveyor = this.asset({ ...ll.conveyor, type: 'conveyor', group: ll.id, line });
      line.infeed = this.asset({ id: ll.infeed.id, name: ll.infeed.name, cap: ll.infeed.cap, type: 'buffer', group: ll.id, line, stock: 26 });
      line.outfeed = this.asset({ id: ll.outfeed.id, name: ll.outfeed.name, cap: ll.outfeed.cap, type: 'outfeed', group: ll.id, line, count: Math.min(7, ll.outfeed.cap - 1) });
      line.stations = ll.stations.map((st) => this.asset({
        id: st.id, name: st.name, type: 'station', arch: st.machine.look.archetype, machine: st.machine, group: ll.id, line,
        s: st.x - ll.x0, cycle: st.cycle, makes: st.makes, measure: st.measure, take: st.take, give: st.give,
        // tools are part-way through their life when the shift starts
        tool: st.machine.tooling ? { ...st.machine.tooling, used: Math.floor(st.machine.tooling.life * rand(0.05, 0.97)) } : undefined,
      }));
      // Warm start: pretend the line has been running for the last hour.
      const ideal = 3600 / Math.max(...line.stations.map((s) => s.cycle));
      const n = Math.round(ideal * rand(0.78, 0.88));
      for (let i = 0; i < n; i++) line.doneTimes.push(-3600 + (i + Math.random()) * (3600 / n));
      line.good = n;
      line.scrap = Math.round(n * rand(0.01, 0.025));
      this.seedProducts(line);
      this.lines.push(line);
      this.lineById[line.id] = line;
    }

    this.wh = this.asset({ id: L.warehouse.id, name: L.warehouse.name, type: 'warehouse', group: 'WH', stock: Math.round(L.warehouse.cap * 0.78), cap: L.warehouse.cap });
    this.fg = this.asset({ id: L.fg.id, name: L.fg.name, type: 'fgzone', group: 'WH', count: 40 + 20 * L.lines.length });

    for (const def of L.agvs) {
      const a = this.asset({ id: def.id, name: def.name, type: 'agv', group: 'LOG', battery: rand(62, 96) });
      const wp = def.route[0];
      const g: AgvState = { asset: a, route: def.route, idx: 1, x: wp.x, z: wp.z, heading: 0, cargo: null, mode: 'move', dwell: 0, waiting: false, waitedT: rand(0, 20), task: '' };
      a.agv = g;
      this.agvs.push(g);
    }

    this.trucks = L.trucks.map((def, i) => {
      const a = this.asset({ id: def.id, name: def.name, type: 'truck', group: 'LOG', carrier: def.carrier, kind: def.kind });
      const t: TruckState = { asset: a, route: def.route, kind: def.kind, idx: 1, x: def.route[0].x, z: def.route[0].z, heading: 0, phase: 'away', wait: 10 + i * 25, dock: 0, load: 0 };
      a.truck = t;
      return t;
    });
  }

  private seedProducts(line: Line) {
    const st = line.stations;
    for (let s = line.len - 2.2; s > 1.5; s -= rand(2.6, 4.2)) {
      if (st.some((a) => Math.abs(a.s - s) < 0.6)) continue;
      let done = -1;
      st.forEach((a, i) => { if (a.s < s) done = i; });
      let kind: ProductKind = line.layout.input;
      for (let i = 0; i <= done; i++) kind = st[i].makes ?? kind;
      line.products.push({ id: `${line.id}-${++line.pid}`, s, done, proc: -1, t: 0, kind, stall: 0 });
    }
  }

  // ---------- simulation ----------

  /**
   * Plant time is China wall-clock time: `epoch` is the wall-clock moment of elapsed 0 (now, for a
   * plant built now; set back for a history backfill). A follower takes the server's clock instead.
   */
  epoch = Date.now();
  /** night shift now, and the hour of the week the calendar was last checked */
  private night = false;
  private calHour = -1;

  wall() { return this.epoch + this.elapsed * 1000; }

  private advanceClock(dt: number) {
    this.elapsed += dt;
    if (this.follower) {
      this.clock += dt;
      return;
    }
    const local = this.wall() + 8 * 3600_000;
    this.clock = (local % 86_400_000) / 1000;
    const hour = Math.floor(local / 3_600_000);
    if (hour !== this.calHour) {
      this.calHour = hour;
      this.calendar(local);
    }
  }

  /** Once an hour: night shift or not, and whether a line's weekly planned maintenance starts now. */
  private calendar(local: number) {
    const cal = this.layout.calendar;
    const h = Math.floor(local / 3_600_000) % 24;
    const n = cal?.night;
    this.night = !!n && (n.from < n.to ? h >= n.from && h < n.to : h >= n.from || h < n.to);
    const pm = cal?.pm;
    if (!pm || h !== pm.start) return;
    const day = new Date(local).getUTCDay();
    const week = Math.floor(local / (7 * 86_400_000));
    for (const line of this.lines) {
      // lines take turns through the week
      if ((line.layout.no - 1) % 7 !== day || line.pmWeek === week) continue;
      line.pmWeek = week;
      for (const a of line.stations) {
        if (liveFields(a)) continue;
        a.maint = Math.max(a.maint, pm.hours * 3600);
      }
      this.note(line.stations[0], `${line.name}每周计划保养 ${pm.hours} 小时`, 'op');
    }
  }

  // ---------- work orders and part numbers ----------

  /** The part numbers a line can run (one, when the product names none). */
  variants(line: Line) {
    const p = line.layout.product;
    return p.variants?.length ? p.variants : [{ partNo: p.partNo ?? p.key, name: p.name, weight: p.weight, input: p.input, share: 1 }];
  }

  /**
   * The next work order: about a day's output of one part number. Orders often repeat the part
   * that is running; a different one means changing over to that part's own tools first.
   */
  private newJob(line: Line) {
    const vs = this.variants(line);
    const prev = line.job;
    let v = prev ? prev.variant : 0;
    if (!prev || (vs.length > 1 && Math.random() > 0.35)) {
      let r = Math.random() * vs.reduce((s, x) => s + (x.share ?? 1), 0);
      v = Math.max(0, vs.findIndex((x) => (r -= x.share ?? 1) <= 0));
    }
    const L = line.layout;
    const neck = Math.max(...line.stations.map((a) => a.cycle));
    const base = L.order.qty;
    const qty = base >= 100 ? Math.max(50, Math.round((base * rand(0.6, 1.4)) / 10) * 10) : Math.max(1, Math.round(base * rand(0.6, 1.4)));
    const now = this.wall();
    const d = new Date(now + 8 * 3600_000).toISOString();
    const ymd = d.slice(2, 4) + d.slice(5, 7) + d.slice(8, 10);
    const seq = (prev?.seq ?? 0) + 1;
    const parts = L.order.no.split('-');
    line.job = {
      no: `${parts[0] || 'MO'}-${ymd}-${parts[parts.length - 1]}${String(seq % 100).padStart(2, '0')}`, seq, variant: v,
      partNo: vs[v].partNo, name: vs[v].name, weight: vs[v].weight ?? L.product.weight ?? 0, input: vs[v].input ?? L.product.input ?? 0, qty, good: 0, scrap: 0, issued: now,
      // the planner promises at the efficiency the line's recent orders ran at, plus three hours:
      // a bad day (a breakdown, no spare die, a bad heat, a changeover) makes the order late
      due: now + ((qty * neck) / (line.planEff ?? 0.75)) * 1000 + 3 * 3600_000,
      heat: line.lot?.no ?? '', supplier: line.lot?.supplier ?? '',
    };
    if (prev && prev.variant !== v) this.changeover(line, prev, line.job);
  }

  /** Changes a line over to another part number: every tooled machine mounts that part's tools. */
  private changeover(line: Line, from: Job, to: Job) {
    for (const a of line.stations) {
      const t = a.tool;
      if (!t || liveFields(a)) continue;
      a.dies ??= {};
      a.dies[from.variant] = t.used;
      // the other part's tools come out of storage as worn as they were put away
      t.used = a.dies[to.variant] ?? Math.floor(t.life * rand(0.05, 0.7));
      a.changeReason = '换型';
      a.changing = Math.max(a.changing, t.change);
      a.setupLeft = t.setup ?? 0;
      a.sinceChange = 0;
    }
    this.note(line.stations[0], `${line.name}换型：${from.name} → ${to.name}（工单 ${to.no}）`, 'op');
  }

  private jobOutput(line: Line) {
    const j = line.job;
    if (!j) return;
    j.good++;
    if (j.good >= j.qty) {
      j.done = this.wall();
      const neck = Math.max(...line.stations.map((a) => a.cycle));
      const ran = Math.min(1, Math.max(0.2, (j.qty * neck) / Math.max(1, (j.done - j.issued) / 1000)));
      line.planEff = 0.7 * (line.planEff ?? 0.75) + 0.3 * ran;
      this.history?.jobDone(line, j);
      this.newJob(line);
    }
  }

  /** A new material lot (one steel heat) for a line, drawn from the product's suppliers. */
  private newLot(line: Line) {
    const prod = line.layout.product;
    const sups = prod.suppliers?.length ? prod.suppliers : [{ name: '', share: 1, bad: 0, factor: 1 }];
    let r = Math.random() * sups.reduce((s, x) => s + x.share, 0);
    const s = sups.find((x) => (r -= x.share) <= 0) ?? sups[0];
    const factor = (Math.random() < (s.bad ?? 0) ? (s.factor ?? 1) : 1) * rand(0.8, 1.25);
    const d = new Date(this.wall() + 8 * 3600_000);
    const no = `${String(d.getUTCFullYear()).slice(2)}${'ABCDEFGHJK'[Math.floor(Math.random() * 10)]}${String(Math.floor(Math.random() * 1e5)).padStart(5, '0')}`;
    // a heat is around 40 t of steel
    const left = Math.max(1, Math.round(40_000 / (prod.input ?? 40)));
    line.lot = { no, supplier: s.name, factor, left };
  }

  step(dt: number) {
    this.advanceClock(dt);
    for (const line of this.lines) this.stepLine(line, dt);
    for (const g of this.agvs) this.stepAgv(g, dt);
    for (const t of this.trucks) this.stepTruck(t, dt);
    let p = 0;
    for (const a of this.assets.values()) {
      this.stepTelemetry(a, dt);
      p += a.power;
    }
    this.powerKW = p;
    this.energyKWh += (p * dt) / 3600;

    if (this.repairs.length) this.stepRepairs();
    this.history?.step(dt);

    this.histAcc += dt;
    if (this.histAcc >= 1) {
      this.histAcc -= 1;
      if (!this.headless) for (const a of this.assets.values()) this.pushHist(a);
      this.checkInventory();
    }
  }

  /**
   * Steps only some lines, with logistics assumed to keep up (infeed full, outfeed collected) and
   * nothing else of the plant moving: a what-if run of a line costs a fraction of the plant.
   */
  stepLines(ids: Set<string>, dt: number) {
    this.advanceClock(dt);
    for (const line of this.lines) {
      if (!ids.has(line.id)) continue;
      line.infeed.stock = line.infeed.cap;
      line.outfeed.count = 0;
      this.stepLine(line, dt);
      for (const a of line.stations) this.stepTelemetry(a, dt);
      this.stepTelemetry(line.conveyor, dt);
    }
    if (this.repairs.length) this.stepRepairs();
    this.history?.step(dt);
    this.histAcc += dt;
    if (this.histAcc >= 1) {
      this.histAcc -= 1;
      this.checkInventory();
    }
  }

  /**
   * Leaves at most one flaw in a unit: likelier with a bad material lot, a worn tool, a tool not yet
   * at temperature, and on the night shift. An inspection downstream finds it, or the customer does.
   */
  private flaw(a: Asset, p: Product) {
    const t = a.tool;
    const worn = t ? Math.max(0, (t.used / t.life - 0.6) / 0.4) : 0;
    const cold = !!t && (a.sinceChange ?? 1e9) < (t.warm ?? 0);
    const night = this.night ? this.layout.calendar?.night?.flaws ?? 1 : 1;
    for (const f of a.machine!.flaws!) {
      const r = f.rate * (f.material ? p.lf ?? 1 : 1) * (1 + (f.wear - 1) * worn * worn) * (cold ? f.cold : 1) * night;
      if (Math.random() < r) {
        p.flaw = f.type;
        return;
      }
    }
  }

  // One reading per finished unit. Wear and over-speed push the process off target, so
  // a neglected machine shows up first as drift on the chart, then as rejects.
  private measure(a: Asset, quiet = false): boolean {
    if (this.follower) return true;
    // A machine that reports its own measurements decides pass/fail itself.
    const lv = liveFields(a);
    if (lv && ('measure' in lv || 'mcount' in lv)) return a.reading?.ok ?? true;
    const m = a.measure!;
    const wear = 1 - a.health;
    const gauss = Math.sqrt(-2 * Math.log(1 - Math.random())) * Math.cos(2 * Math.PI * Math.random());
    // a worn tool pushes readings towards the limit over the last 40 % of its life
    const toolWear = a.tool ? Math.max(0, (a.tool.used / a.tool.life - 0.6) / 0.4) : 0;
    const mw = m.wear ?? { shift: 4, spread: 2.5 };
    let dev = gauss * m.sigma * (1 + wear * mw.spread) + m.drift * wear * m.sigma * mw.shift + m.drift * toolWear * toolWear * m.sigma * (a.tool?.drift ?? 0) + (a.speed - 1) * (m.speedBias ?? 0);
    if (m.abs) dev = Math.abs(dev);
    return this.record(a, m.target + dev, quiet);
  }

  /** Records one measured unit (simulated, or reported by the equipment) and checks for drift. */
  private record(a: Asset, v: number, quiet = false): boolean {
    const m = a.measure!;
    if (!quiet) {
      this.mcount.set(a.id, ((this.mcount.get(a.id) ?? 0) + 1) % 65536);
      if (this.readings && this.readings.length < 500) this.readings.push([a.id, Math.round(v * 1e4) / 1e4]);
    }
    const ok = (m.lo === undefined || v >= m.lo) && (m.hi === undefined || v <= m.hi);
    const h = a.hist.meas;
    h.push(v);
    if (h.length > 60) h.shift();
    a.reading = { v, ok, at: this.elapsed };
    if (quiet) return ok;
    if (!ok) a.rejects++;
    this.history?.measure(a, v, ok);

    if (h.length >= 5) {
      const mean = h.slice(-5).reduce((x, y) => x + y, 0) / 5;
      const limit = mean >= m.target ? m.hi : m.lo;
      if (limit !== undefined) {
        const used = (mean - m.target) / (limit - m.target); // 0 on target, 1 at the limit
        const txt = `${m.label}最近 5 件均值 ${mean.toFixed(m.digits)} ${m.unit}，正向${limit === m.hi ? '上' : '下'}限漂移`;
        if (used > 0.6) this.raise(a, 'drift', 3, txt);
        else if (used < 0.4) this.clear(a, 'drift');
      }
    }
    return ok;
  }

  private pushHist(a: Asset) {
    const h = a.hist;
    const push = (arr: number[], v: number) => { arr.push(v); if (arr.length > HIST_LEN) arr.shift(); };
    if (isMachineSpec(a.spec)) {
      push(h.temp, a.temp); push(h.power, a.power); push(h.vib, a.vib);
    } else if (a.type === 'agv') {
      push(h.level, a.battery); push(h.power, a.power);
    } else if (a.type === 'buffer' || a.type === 'warehouse') {
      push(h.level, a.stock);
    } else {
      push(h.level, a.count);
    }
  }

  private ready(a: Asset) {
    return a.enabled && !a.fault && a.maint <= 0 && a.changing <= 0 && !(a.tool && a.tool.used >= a.tool.life);
  }

  private reject(line: Line, a: Asset, i: number) {
    const p = line.products[i];
    this.giveInv(p);
    line.scrap++;
    if (line.job) line.job.scrap++;
    this.events.push({ type: 'reject', line: line.id, asset: a.id, x: line.x0 + p.s, z: line.z, kind: p.kind });
    line.products.splice(i, 1);
  }

  private stepLine(line: Line, dt: number) {
    if (!line.job && !this.follower) this.newJob(line);
    const cv = line.conveyor;
    const v = this.ready(cv) ? line.speed * cv.speed : 0;
    const st = line.stations;
    const P = line.products;

    // Release raw parts from the infeed buffer onto the belt.
    line.spawnT -= dt;
    const last = P[P.length - 1];
    if (v > 0 && line.infeed.stock > 0 && line.spawnT <= 0 && (!last || last.s > line.gap + 0.05)) {
      if (!this.follower && (!line.lot || line.lot.left <= 0)) this.newLot(line);
      const lot = line.lot;
      if (lot) lot.left--;
      P.push({ id: `${line.id}-${++line.pid}`, s: 0, done: -1, proc: -1, t: 0, kind: line.layout.input, stall: 0, lot: lot?.no, sup: lot?.supplier, lf: lot?.factor });
      line.infeed.stock--;
      line.spawnT = line.spawn;
      this.history?.released(line);
    }

    // Front-most part first so each part can see the one ahead of it.
    for (let i = 0; i < P.length; i++) {
      const p = P[i];
      if (p.proc >= 0) {
        const a = st[p.proc];
        if (!this.ready(a)) continue;
        p.t += dt * a.speed;
        a.progress = Math.min(1, p.t / a.cycle);
        if (p.t < a.cycle) continue;
        // inventory this step lends out: the unit waits here until there is enough
        if (a.take && !this.follower) {
          const short = this.shortOf(a.take);
          if (short) {
            p.t = a.cycle;
            if (a.waitFor !== short.label) {
              a.waitFor = short.label;
              this.raise(a, 'inv', 3, `${short.label}不足，等待中`);
            }
            continue;
          }
          this.takeInv(p, a.take);
        }
        if (a.waitFor) {
          a.waitFor = undefined;
          this.clear(a, 'inv');
        }
        if (a.give) this.giveInv(p, a.give);
        if (a.tool) a.tool.used++;
        p.done = p.proc;
        p.proc = -1;
        p.stall = 0;
        a.busy = null;
        a.progress = 0;
        a.cycles++;
        this.history?.cycle(a);
        if (a.makes) p.kind = a.makes;
        if (a.tool && !this.follower) {
          a.sinceChange = (a.sinceChange ?? 1e9) + 1;
          // the first pieces after a change go into setting the new tool up
          if ((a.setupLeft ?? 0) > 0) {
            a.setupLeft!--;
            this.history?.defect(line, a, '换模调试件', '首件调试', null, false, p);
            this.reject(line, a, i--);
            continue;
          }
        }
        if (a.machine?.flaws && !p.flaw && !this.follower) this.flaw(a, p);
        if (a.machine?.detects && !this.follower) {
          const d = a.machine.detects;
          const caught = !!p.flaw && d.types.includes(p.flaw) && Math.random() < d.catch;
          const m = a.measure;
          let v: number | null = null;
          if (m && !liveFields(a)?.measure) {
            const lim = m.hi ?? m.target * 4;
            v = caught ? lim * rand(1.15, 4) : m.target + Math.abs(Math.sqrt(-2 * Math.log(1 - Math.random())) * Math.cos(2 * Math.PI * Math.random())) * m.sigma;
            this.record(a, Math.min(v, caught ? v : lim * 0.95));
          }
          if (caught) {
            this.history?.defect(line, a, p.flaw!, m?.label ?? a.name, v, false, p);
            this.reject(line, a, i--);
            continue;
          }
        } else if (a.measure && !this.measure(a)) {
          const m = a.measure;
          const v = a.reading?.v ?? null;
          const low = v !== null && m.lo !== undefined && v < m.lo;
          const type = (low ? m.defects?.lo : m.defects?.hi) ?? `${m.label}超差`;
          this.history?.defect(line, a, type, m.label, v, m.rework, p);
          if (m.rework) {
            p.rework = true;
            continue;
          }
          this.reject(line, a, i--);
          continue;
        }
        const rejectRate = a.machine?.rejectRate;
        if (rejectRate !== undefined && !a.measure && !this.follower) {
          const upstream = st.slice(0, p.done);
          const h = upstream.reduce((sum, u) => sum + u.health, 0) / Math.max(1, upstream.length);
          // worn upstream machines make more of what this check rejects, in proportion to its base rate
          const ok = Math.random() > rejectRate * (1 + (1 - h) * 4);
          a.verdict = { ok, at: this.elapsed };
          if (!ok) {
            this.history?.defect(line, a, a.machine?.defect ?? '外观不良', '外观', null, false, p);
            this.reject(line, a, i--);
          }
        }
        continue;
      }

      let maxS = line.len;
      const ahead = i > 0 ? P[i - 1] : null;
      if (ahead) maxS = Math.min(maxS, ahead.s - line.gap);
      const n = p.done + 1;
      const next = n < st.length ? st[n] : null;
      if (next) maxS = Math.min(maxS, next.s);

      const before = p.s;
      if (v > 0 && p.s < maxS) p.s = Math.min(p.s + v * dt, maxS);
      p.stall = p.s === before ? p.stall + dt : 0;

      if (next && p.s >= next.s - 1e-4 && !next.busy && this.ready(next)) {
        next.busy = p;
        p.proc = n;
        p.t = 0;
      }
      if (!next && p.s >= line.len - 1e-4 && line.outfeed.count < line.outfeed.cap) {
        line.outfeed.count++;
        line.good++;
        line.doneTimes.push(this.elapsed);
        this.history?.output(line, p);
        this.jobOutput(line);
        this.giveInv(p);
        P.splice(i--, 1);
      }
    }

    st.forEach((a, idx) => {
      if (a.fault) a.state = 'fault';
      else if (a.changing > 0) a.state = 'changeover';
      else if (a.tool && a.tool.used >= a.tool.life && a.waitFor) a.state = 'blocked';
      else if (a.maint > 0) a.state = 'maint';
      else if (!a.enabled) a.state = 'stopped';
      else if (a.busy) a.state = a.waitFor ? 'blocked' : 'running';
      else {
        const stuck = P.some((p) => p.done === idx && Math.abs(p.s - a.s) < 0.02 && p.stall > 0.6);
        a.state = stuck ? 'blocked' : 'idle';
      }
    });
    cv.state = cv.fault ? 'fault' : cv.maint > 0 ? 'maint' : !cv.enabled ? 'stopped' : 'running';

    const inf = line.infeed;
    inf.state = inf.stock === 0 ? 'empty' : inf.stock < 8 ? 'low' : 'ok';
    if (inf.stock === 0) this.raise(inf, 'empty', 2, '上料缓存已空，产线缺料');
    else this.clear(inf, 'empty');

    const out = line.outfeed;
    out.state = out.count >= out.cap ? 'full' : 'ok';
    if (out.count >= out.cap) this.raise(out, 'full', 2, '成品托盘已满，等待 AGV 取货');
    else this.clear(out, 'full');

    while (line.doneTimes.length && line.doneTimes[0] < this.elapsed - 3600) line.doneTimes.shift();
  }

  private stepAgv(g: AgvState, dt: number) {
    const a = g.asset;
    const spec = a.spec!;
    a.vel = 0;
    // A located vehicle (GPS / UWB / the fleet system) drives to where it was reported.
    const lv = liveFields(a);
    if (lv && typeof lv.x === 'number' && typeof lv.z === 'number') {
      const dx = lv.x - g.x;
      const dz = lv.z - g.z;
      const dist = Math.hypot(dx, dz);
      const k = 1 - Math.exp(-dt * 2.5);
      g.x += dx * k;
      g.z += dz * k;
      if (dist > 0.05) g.heading = typeof lv.heading === 'number' ? lv.heading : Math.atan2(dz, dx);
      a.vel = (dist * k) / Math.max(dt, 1e-3);
      a.state = a.vel > 0.1 ? 'moving' : 'waiting';
      a.power = a.vel > 0.1 ? spec.pRun! : spec.pIdle!;
      return;
    }
    if (!this.ready(a)) {
      a.state = a.fault ? 'fault' : a.maint > 0 ? 'maint' : 'stopped';
      a.power = a.maint > 0 ? 0 : 0.05;
      return;
    }

    if (g.mode === 'dwell') {
      g.dwell -= dt;
      a.battery = Math.min(100, a.battery + dt * 0.3); // opportunity charging at docks
      a.state = g.waiting ? 'waiting' : 'handling';
      a.power = spec.pIdle!;
      if (g.dwell <= 0) {
        if (g.waiting) this.agvAct(g);
        else { g.mode = 'move'; g.idx = (g.idx + 1) % g.route.length; }
      }
      return;
    }

    const wp = g.route[g.idx];
    const dx = wp.x - g.x;
    const dz = wp.z - g.z;
    const dist = Math.hypot(dx, dz);
    a.state = 'moving';
    a.power = spec.pIdle!;
    if (dist < 0.01) {
      g.x = wp.x;
      g.z = wp.z;
      if (wp.act) {
        g.waitedT = 0;
        this.agvAct(g);
      } else g.idx = (g.idx + 1) % g.route.length;
      return;
    }

    // Pivot on the spot, then drive straight: how tape-guided AGVs actually move.
    const want = Math.atan2(dz, dx);
    const diff = Math.atan2(Math.sin(want - g.heading), Math.cos(want - g.heading));
    const turn = 1.8 * a.speed * dt;
    if (Math.abs(diff) > 0.02) {
      g.heading += clamp(diff, -turn, turn);
      a.power = spec.pIdle! + 0.6;
      if (Math.abs(diff) > 0.15) return;
    }
    const vmax = 1.3 * a.speed;
    const vel = Math.min(vmax, dist * 1.4 + 0.15);
    const d = Math.min(dist, vel * dt);
    g.x += (dx / dist) * d;
    g.z += (dz / dist) * d;
    a.vel = vel;
    a.power = spec.pIdle! + spec.pRun! * (vel / vmax);
    a.battery = Math.max(0, a.battery - dt * 0.045 * a.speed);
    g.task = this.agvTask(g);
    a.task = g.task;

    if (Math.random() < dt / 5200) this.trip(a, pick(spec.faults!));
    if (a.battery < 5) this.trip(a, '电量耗尽');
  }

  private stepTruck(t: TruckState, dt: number) {
    const a = t.asset;
    a.vel = 0;
    if (!this.ready(a)) {
      a.state = a.fault ? 'fault' : a.maint > 0 ? 'maint' : 'stopped';
      return;
    }
    if (t.phase === 'away') {
      a.state = 'away';
      t.wait -= dt;
      const needed = t.kind === 'in' ? this.wh.stock < this.wh.cap - 500 : this.fg.count >= 30;
      a.task = t.wait > 0 ? `预计 ${Math.ceil(t.wait)} 秒后到厂` : needed ? '' : t.kind === 'in' ? '待命：原料库位已满' : `待命：成品不足 30 ${this.layout.unit}`;
      if (t.wait <= 0 && needed) {
        t.phase = 'drive';
        t.idx = 1;
        t.x = t.route[0].x;
        t.z = t.route[0].z;
        t.heading = Math.atan2(t.route[1].z - t.z, t.route[1].x - t.x);
        t.load = t.kind === 'in' ? 400 : 0;
      }
      return;
    }
    if (t.phase === 'dock') {
      a.state = 'handling';
      t.dock += dt;
      if (t.kind === 'in') {
        const n = Math.min(t.load, Math.max(0, this.wh.cap - this.wh.stock), Math.ceil((400 / 22) * dt));
        this.wh.stock += n;
        t.load -= n;
        a.task = `卸货入库，剩余 ${t.load} 件`;
        if (t.load <= 0 || t.dock > 30) {
          this.truckDone(t, '卸货完成，原料入库');
          this.refill(t.asset);
        }
      } else {
        const n = Math.min(this.fg.count, Math.ceil((120 / 22) * dt), 120 - t.load);
        this.fg.count -= n;
        t.load += n;
        a.task = `装车 ${t.load} ${this.layout.unit}`;
        if (t.load >= 120 || this.fg.count === 0 || t.dock > 30) this.truckDone(t, `装车 ${t.load} ${this.layout.unit}，发运`);
      }
      return;
    }

    const wp = t.route[t.idx];
    const dx = wp.x - t.x;
    const dz = wp.z - t.z;
    const dist = Math.hypot(dx, dz);
    a.state = 'moving';
    a.task = t.phase === 'leave' ? '离厂' : t.kind === 'in' ? '进厂送料' : '进厂提货';
    if (dist < 0.3) {
      if (wp.act === 'dock' && t.phase === 'drive') {
        t.phase = 'dock';
        t.dock = 0;
        this.note(a, '到达卸货口', 'info');
        return;
      }
      t.idx++;
      if (t.idx >= t.route.length) {
        t.phase = 'away';
        t.wait = t.kind === 'in' ? 140 : 110;
      }
      return;
    }
    const want = Math.atan2(dz, dx);
    const diff = Math.atan2(Math.sin(want - t.heading), Math.cos(want - t.heading));
    t.heading += clamp(diff, -1, 1) * Math.min(1, dt * 2.2);
    const v = Math.min(9 * a.speed, dist * 0.9 + 1.5) * (Math.abs(diff) > 0.6 ? 0.4 : 1);
    // Within one step of the waypoint: arrive. With coarse steps (an unwatched plant, a backfill)
    // the heading lag would otherwise keep the truck circling a waypoint it never quite reaches.
    if (dist <= v * dt) {
      t.x = wp.x;
      t.z = wp.z;
      t.heading = want;
      a.vel = v;
      return;
    }
    const d = Math.min(dist, v * dt);
    t.x += Math.cos(t.heading) * d;
    t.z += Math.sin(t.heading) * d;
    // Pull back onto the path so heading lag never lets the truck drift off the road.
    t.x += (dx / dist) * d * 0.15;
    t.z += (dz / dist) * d * 0.15;
    a.vel = v;
  }

  private truckDone(t: TruckState, text: string) {
    this.note(t.asset, text, 'info');
    t.phase = 'leave';
    t.idx++;
  }

  private agvTask(g: AgvState): string {
    const wp = g.route[g.idx];
    const line = wp.line ? this.lineById[wp.line] : null;
    if (wp.act === 'load') return '回库取料';
    if (wp.act === 'drop') return `送料至 ${line!.infeed.id}`;
    if (wp.act === 'pick') return `前往 ${line!.outfeed.id} 取货`;
    if (wp.act === 'deliver') return g.cargo ? `成品送至 ${this.fg.id}` : '返回待命点';
    return g.task;
  }

  private agvAct(g: AgvState) {
    const wp = g.route[g.idx];
    const line = wp.line ? this.lineById[wp.line] : null;
    const set = (task: string) => { g.task = task; g.asset.task = task; };
    const wait = (s: number, task: string) => { g.mode = 'dwell'; g.waiting = true; g.dwell = s; g.waitedT += s; set(task); };
    const work = (s: number, task: string) => { g.mode = 'dwell'; g.waiting = false; g.dwell = s; set(task); };

    switch (wp.act) {
      case 'load':
        if (g.cargo) return work(0.1, '');
        if (line!.infeed.stock + 20 > line!.infeed.cap) return wait(2, `待命：${line!.infeed.id} 库存充足`);
        if (this.wh.stock < 20) return wait(2, '待命：原料库存不足');
        this.wh.stock -= 20;
        g.cargo = { kind: 'raw', n: 20 };
        return work(4, '装载原料');
      case 'drop':
        if (g.cargo) line!.infeed.stock += g.cargo.n;
        g.cargo = null;
        return work(3, `卸料至 ${line!.infeed.id}`);
      case 'pick': {
        const n = line!.outfeed.count;
        if (n < Math.min(8, line!.outfeed.cap) && !(g.waitedT > 40 && n > 0)) return wait(2, `等待 ${line!.outfeed.id} 成品`);
        line!.outfeed.count = 0;
        g.cargo = { kind: 'fg', n };
        return work(4, `取走 ${n} ${this.layout.unit}成品`);
      }
      case 'deliver': {
        const had = g.cargo;
        if (had) this.fg.count += had.n;
        g.cargo = null;
        return work(had ? 3 : 0.1, had ? `卸下 ${had.n} ${this.layout.unit}成品` : '');
      }
      default:
        return work(0.1, '');
    }
  }

  private stepTelemetry(a: Asset, dt: number) {
    this.crew(a);
    if (a.tool) this.stepTool(a, dt);
    const s = a.spec;
    if (a.maint > 0) {
      a.maint -= dt;
      a.health = Math.min(1, a.health + dt * 0.03);
      if (a.maint <= 0) {
        a.maint = 0;
        a.health = 1;
        this.note(a, '维护完成，健康度恢复至 100%', 'op');
      }
    }

    if (a.type === 'agv') {
      if (a.battery < 18) this.raise(a, 'battery', 3, `电量低 ${a.battery.toFixed(0)}%`);
      else if (a.battery > 25) this.clear(a, 'battery');
      return;
    }
    if (a.type === 'warehouse') {
      a.state = a.stock < 300 ? 'low' : 'ok';
      if (a.stock < 300) this.raise(a, 'low', 3, `原料库存低于 300 件，当前 ${a.stock}`);
      else this.clear(a, 'low');
      return;
    }
    if (!isMachineSpec(s)) return;

    const running = a.state === 'running';
    const on = this.ready(a);
    let target = AMBIENT;
    if (on) target += (running ? s.runRise : s.idleRise) * (0.6 + 0.4 * a.speed) + (1 - a.health) * 18;
    a.temp += (target - a.temp) * ema(dt, s.tau ?? 40);
    a.vib = running
      ? s.vib * (0.7 + 0.3 * a.speed) * (1 + (1 - a.health) * 2.5) * rand(0.9, 1.1)
      : on ? s.vib * 0.06 * rand(0.8, 1.2) : 0;
    a.power = on ? (s.pIdle + (running ? s.pRun * a.speed : 0)) * rand(0.96, 1.04) : a.maint > 0 ? 0 : 0.1;
    // Measured values win over the model; alarms and wear below then run on the real numbers.
    const lv = liveFields(a);
    if (lv) {
      if (typeof lv.temp === 'number') a.temp = lv.temp;
      if (typeof lv.power === 'number') a.power = lv.power;
      if (typeof lv.vib === 'number') a.vib = lv.vib;
    }
    a.util += ((running ? 1 : 0) - a.util) * ema(dt, 300);
    a.avail += ((on ? 1 : 0) - a.avail) * ema(dt, 600);

    const up = a.machine?.upkeep;
    if (running) {
      const over = Math.max(0, a.temp - s.warn) / 10;
      // health falls from 100 % to the 45 % that calls for maintenance over `pm` running hours
      const wear = up ? 0.55 / (up.pm * 3600) : 5e-5;
      a.health = Math.max(0.05, a.health - dt * wear * a.speed * a.speed * (1 + over));
    }

    if (on && a.temp > s.warn) this.raise(a, 'temp', 2, `温度高 ${a.temp.toFixed(0)}°C，报警值 ${s.warn}°C`);
    else if (a.temp < s.warn - 2) this.clear(a, 'temp');
    if (!a.fault && a.temp > s.trip) this.trip(a, `温度超限 ${a.temp.toFixed(0)}°C，自动停机`);

    if (a.health < 0.35) this.raise(a, 'health', 3, `健康度 ${(a.health * 100).toFixed(0)}%，建议安排维护`);
    else if (a.health > 0.4) this.clear(a, 'health');

    if (running && !a.fault && !lv) {
      const rate = (1 / ((up?.mtbf ?? 3.33) * 3600)) * (1 + (1 - a.health) * 6) * a.speed * a.speed;
      if (Math.random() < rate * dt) this.trip(a, pick(s.faults));
      else if (up?.stops && Math.random() < (up.stops.perHour / 3600) * dt * (this.night ? this.layout.calendar?.night?.stops ?? 1 : 1)) {
        // a short stop: the operator clears it, no crew needed
        this.trip(a, `短停：${pick(up.stops.reasons)}`);
        a.repairAt = this.elapsed + rand(up.stops.secs[0], up.stops.secs[1]);
      }
    }
  }

  // ---------- inventory ----------

  private inv(key: string) {
    return this.inventory.find((i) => i.key === key);
  }

  /** The first item there is not enough of, if any. */
  private shortOf(take: Record<string, number>) {
    for (const [k, n] of Object.entries(take)) {
      const it = this.inv(k);
      if (it && it.avail < n) return it;
    }
    return null;
  }

  private takeInv(p: Product, take: Record<string, number>) {
    for (const [k, n] of Object.entries(take)) {
      const it = this.inv(k);
      if (!it) continue;
      it.avail -= n;
      if (it.kind === 'lend') {
        p.held ??= {};
        p.held[k] = (p.held[k] ?? 0) + n;
      }
    }
  }

  /** Returns what a unit was lent: the listed keys, or everything it still holds. */
  private giveInv(p: Product, keys?: string[]) {
    if (!p.held) return;
    for (const k of keys ?? Object.keys(p.held)) {
      const n = p.held[k];
      const it = this.inv(k);
      if (!n || !it) continue;
      it.avail = Math.min(it.cap, it.avail + n);
      delete p.held[k];
    }
  }

  private refill(truck: Asset) {
    const got: string[] = [];
    for (const it of this.inventory) {
      if (it.kind !== 'consume' || it.avail >= it.cap || it.lead) continue;
      const n = Math.min(it.refill, it.cap - it.avail);
      it.avail += n;
      got.push(`${it.label} +${n} ${it.unit}`);
    }
    if (got.length) this.note(truck, `补货：${got.join('，')}`, 'info');
  }

  private checkInventory() {
    for (const it of this.inventory) {
      // bought to order: at the reorder point, `refill` more arrive after the lead time
      if (it.lead && it.avail <= Math.floor(it.cap * it.low) && !this.repairs.some(([k, , n]) => k === it.key && n)) {
        this.repairs.push([it.key, this.elapsed + it.lead * 3600, it.refill]);
        this.note(this.wh, `${it.label}低于订货点，采购 ${it.refill} ${it.unit}，约 ${Math.round(it.lead / 24)} 天到货`, 'info');
      }
      const code = `inv-${it.key}`;
      if (it.avail <= Math.floor(it.cap * it.low)) this.raise(this.wh, code, 3, `${it.label}可用只剩 ${it.avail} ${it.unit}（共 ${it.cap}）`);
      else this.clear(this.wh, code);
    }
  }

  // ---------- tooling (dies, cutters …) ----------

  private stepTool(a: Asset, dt: number) {
    const t = a.tool!;
    if (a.changing > 0) {
      a.changing -= dt;
      if (a.changing <= 0) {
        a.changing = 0;
        // the removed tool goes for reconditioning (a die is re-sunk) and comes back as a spare
        if (a.changeReason) {
          // a part-number change mounted that part's own tools: they keep their wear
          a.changeReason = undefined;
        } else {
          if (t.spare && t.repair && Math.random() < t.repair.rate) this.repairs.push([t.spare, this.elapsed + t.repair.hours * 3600]);
          t.used = 0;
        }
        a.setupLeft = t.setup ?? 0;
        a.sinceChange = 0;
        a.health = Math.min(1, a.health + 0.05);
        this.clear(a, 'tool');
        this.note(a, `${t.label}更换完成，寿命计数清零`, 'op');
      }
      return;
    }
    // equipment that reports its tool counter decides when to change it
    if (this.follower || liveFields(a)?.toolUsed !== undefined) return;
    const share = t.used / t.life;
    const limit = t.limit ?? 1;
    if (share >= limit * 0.9) this.raise(a, 'tool', 3, `${t.label}寿命 ${Math.round(share * 100)}%，准备更换（${t.used}/${t.life} 次）`);
    // at the planned change point the tool is changed as soon as the machine is free, with a spare
    // from stock; without a spare it keeps going until the end of its life
    if (share >= limit && !a.busy && !a.fault && (t.used >= t.life || !this.lacksSpare(t))) this.startChange(a);
  }

  private lacksSpare(t: NonNullable<Asset['tool']>) {
    const it = t.spare ? this.inv(t.spare) : undefined;
    return !!it && it.avail < 1;
  }

  private stepRepairs() {
    this.repairs = this.repairs.filter(([key, at, bought]) => {
      if (this.elapsed < at) return true;
      const it = this.inv(key);
      if (it) {
        it.avail = Math.min(it.cap, it.avail + (bought ?? 1));
        if (bought) this.note(this.wh, `${it.label}采购到货 ${bought} ${it.unit}`, 'info');
      }
      return false;
    });
  }

  private startChange(a: Asset): boolean {
    const t = a.tool!;
    if (t.spare) {
      const it = this.inv(t.spare);
      if (it && it.avail < 1) {
        if (a.waitFor !== it.label) {
          a.waitFor = it.label;
          this.raise(a, 'tool-spare', 2, `缺${it.label}，${t.label}到寿命无法更换`);
        }
        return false;
      }
      if (it) it.avail -= 1;
    }
    if (a.waitFor) {
      a.waitFor = undefined;
      this.clear(a, 'tool-spare');
    }
    a.changing = t.change;
    this.note(a, `开始更换${t.label}（已用 ${t.used} 次），约 ${Math.round(t.change / 60)} 分钟`, 'op');
    return true;
  }

  // ---------- shift crew ----------

  /**
   * What the people on shift do without being asked: the maintenance crew clears a fault a few
   * minutes after it happens (once the machine has cooled), and worn machines go in for planned
   * maintenance. Without this a plant that runs for hours ends up with every machine faulted.
   */
  private crew(a: Asset) {
    // equipment that reports its own state has its own crew
    if (this.follower || !a.spec?.faults || liveFields(a)) return;
    if (a.fault) {
      const rt = a.machine?.upkeep.repair ?? [1, 3];
      a.repairAt ??= this.elapsed + rand(rt[0] * 60, rt[1] * 60);
      const hot = a.spec.trip !== undefined && a.temp > a.spec.trip - 4;
      if (this.elapsed >= a.repairAt && !hot) {
        const short = a.fault.text.startsWith('短停：');
        a.fault = null;
        a.repairAt = undefined;
        if (!short) a.health = Math.min(1, a.health + 0.15);
        this.clear(a, 'fault');
        this.note(a, short ? '操作工处理完毕，恢复生产' : '维修班组到场处理，故障已排除', 'op');
      }
      return;
    }
    a.repairAt = undefined;
    if (a.health < 0.45 && a.maint <= 0 && a.type === 'station') {
      const pt = a.machine?.upkeep.pmTime ?? [0.67, 1.5];
      a.maint = rand(pt[0] * 60, pt[1] * 60);
      this.note(a, `健康度 ${(a.health * 100).toFixed(0)}%，班组安排计划维护`, 'op');
    }
  }

  // ---------- alarms & log ----------

  private trip(a: Asset, text: string) {
    if (a.fault || this.follower) return;
    a.fault = { text, at: this.clock };
    this.raise(a, 'fault', 1, text);
    this.events.push({ type: 'fault', asset: a.id });
  }

  private raise(a: Asset, code: string, prio: Alarm['prio'], text: string) {
    if (this.follower) return;
    const key = `${a.id} ${code}`;
    if (this.raised.has(key)) return;
    this.raised.add(key);
    const al: Alarm = { id: this.alarmSeq++, asset: a.id, code, prio, text, at: this.clock, active: true, acked: false };
    this.alarms.unshift(al);
    this.note(a, text, 'alarm', prio);
    if (this.alarms.length > 160) {
      let idx = this.alarms.length - 1;
      while (idx >= 0 && !(!this.alarms[idx].active && this.alarms[idx].acked)) idx--;
      if (idx >= 0) this.alarms.splice(idx, 1);
    }
  }

  private clear(a: Asset, code: string) {
    if (this.follower) return;
    if (!this.raised.delete(`${a.id} ${code}`)) return;
    for (const al of this.alarms) {
      if (al.asset === a.id && al.code === code && al.active) {
        al.active = false;
        al.clearedAt = this.clock;
      }
    }
  }

  private note(a: Asset, text: string, kind: LogEntry['kind'] = 'info', prio = 0) {
    if (this.follower) return;
    this.log.unshift({ at: this.clock, asset: a.id, text, kind, prio });
    if (this.log.length > 300) this.log.pop();
  }

  activeAlarms() {
    return this.alarms.filter((x) => x.active || !x.acked);
  }

  ack(id: number) {
    const al = this.alarms.find((x) => x.id === id);
    if (al && !al.acked) {
      al.acked = true;
      this.note(this.assets.get(al.asset)!, `确认报警：${al.text}`, 'op');
    }
  }

  ackAll() {
    for (const al of this.alarms) al.acked = true;
  }

  // ---------- commands (the only write path from the UI) ----------

  command(id: string, cmd: Command, arg?: number): CommandResult {
    const a = this.assets.get(id);
    if (!a) return { ok: false, msg: `找不到设备 ${id}` };
    switch (cmd) {
      case 'start':
        if (a.fault) return { ok: false, msg: `${id} 处于故障状态，先复位再启动` };
        a.enabled = true;
        this.note(a, '操作员启动', 'op');
        return { ok: true, msg: `${id} 已启动` };
      case 'stop':
        a.enabled = false;
        this.note(a, '操作员停止', 'op');
        return { ok: true, msg: `${id} 已停止` };
      case 'speed':
        a.speed = clamp(arg ?? 1, 0.5, 1.5);
        this.note(a, `速度设定为 ${Math.round(a.speed * 100)}%`, 'op');
        return { ok: true, msg: `${id} 速度 ${Math.round(a.speed * 100)}%` };
      case 'reset':
        if (!a.fault) return { ok: false, msg: `${id} 没有需要复位的故障` };
        if (a.spec?.trip && a.temp > a.spec.trip - 4) {
          return { ok: false, msg: `${id} 温度 ${a.temp.toFixed(0)}°C 仍偏高，冷却到 ${a.spec.trip - 4}°C 以下再复位` };
        }
        if (a.type === 'agv' && a.battery < 10) a.battery = 40; // swapped battery
        a.fault = null;
        this.clear(a, 'fault');
        this.note(a, '故障复位', 'op');
        return { ok: true, msg: `${id} 已复位` };
      case 'maint':
        if (a.maint > 0) {
          a.maint = 0;
          this.note(a, '结束维护', 'op');
          return { ok: true, msg: `${id} 结束维护` };
        }
        a.maint = 25;
        if (a.fault) { a.fault = null; this.clear(a, 'fault'); }
        this.note(a, '进入维护', 'op');
        return { ok: true, msg: `${id} 进入维护，约 25 秒` };
      case 'fault':
        if (a.fault) return { ok: false, msg: `${id} 已处于故障状态` };
        if (!a.spec?.faults) return { ok: false, msg: `${id} 不支持故障演练` };
        this.trip(a, `${pick(a.spec.faults)}（模拟）`);
        return { ok: true, msg: `已向 ${id} 注入故障` };
      case 'retool':
        if (!a.tool) return { ok: false, msg: `${id} 没有可更换的工装` };
        if (a.changing > 0) return { ok: false, msg: `${id} 正在更换${a.tool.label}` };
        if (a.busy) return { ok: false, msg: `${id} 正在加工，等这一件完成再换` };
        return this.startChange(a) ? { ok: true, msg: `${id} 开始更换${a.tool.label}` } : { ok: false, msg: `${id} 缺备件，无法更换${a.tool.label}` };
      case 'toollimit': {
        if (!a.tool) return { ok: false, msg: `${id} 没有工装` };
        const v = clamp(arg ?? 1, 0.3, 1);
        a.tool.limit = v;
        this.note(a, `${a.tool.label}计划更换点设为寿命的 ${Math.round(v * 100)}%（${Math.round(a.tool.life * v)} 次）`, 'op');
        return { ok: true, msg: `${id} ${a.tool.label}在 ${Math.round(v * 100)}% 寿命时更换` };
      }
      case 'restock':
        a.stock = Math.min(a.cap, a.stock + 400);
        this.note(a, '原料入库 400 件', 'op');
        return { ok: true, msg: '原料入库 400 件' };
      case 'ship':
        this.note(a, `成品发运 ${a.count} ${this.layout.unit}`, 'op');
        a.count = 0;
        return { ok: true, msg: '成品已发运' };
    }
  }

  // ---------- values from equipment (MQTT / Modbus TCP / OPC UA via the server) ----------

  /**
   * Applies one field reported by real equipment. Continuous values (temp, power, vib) replace
   * the simulated ones while fresh; state changes go through the same paths operators use.
   */
  ingest(id: string, field: string, value: number | string | boolean, src: string): boolean {
    const a = this.assets.get(id);
    if (!a) return false;
    a.live ??= { src, at: 0, f: {} };
    const prev = a.live.f[field];
    a.live.src = src;
    a.live.at = Date.now();
    a.live.f[field] = value;
    const num = typeof value === 'number' ? value : Number(value);
    // inventory counts from a stock / booking system: inv.<key> available, cap.<key> capacity
    if (field.startsWith('inv.') || field.startsWith('cap.')) {
      const it = this.inv(field.slice(4));
      if (it && Number.isFinite(num)) {
        if (field.startsWith('inv.')) it.avail = Math.max(0, Math.round(num));
        else it.cap = Math.max(0, Math.round(num));
      }
      return true;
    }
    // vehicles report position and task; their state words are not machine states
    if (a.type === 'agv' || a.type === 'truck') {
      if (field === 'battery' && Number.isFinite(num)) a.battery = num;
      if (field === 'task' && typeof value === 'string') a.task = value;
      return true;
    }
    switch (field) {
      case 'running':
        if (truthy(value)) a.enabled = true;
        else if (falsy(value)) a.enabled = false;
        break;
      case 'state':
        this.deviceState(a, String(value), src);
        break;
      case 'fault':
        if (falsy(value)) this.deviceRecovered(a, src);
        else this.deviceFault(a, typeof value === 'string' ? value : '');
        break;
      case 'faultCode':
        if (num === 0) this.deviceRecovered(a, src);
        else if (Number.isFinite(num)) this.deviceFault(a, a.spec?.faults?.[num - 1] ?? `故障代码 ${num}`);
        break;
      case 'health':
        if (Number.isFinite(num)) a.health = clamp(num, 0, 1);
        break;
      case 'toolUsed':
        if (a.tool && Number.isFinite(num)) a.tool.used = Math.max(0, Math.round(num));
        break;
      case 'toolLife':
        if (a.tool && num > 0) a.tool.life = Math.round(num);
        break;
      case 'speed':
        if (Number.isFinite(num)) a.speed = clamp(num > 5 ? num / 100 : num, 0.5, 1.5);
        break;
      case 'measure':
        // one message per measured unit (MQTT)
        if (a.measure && Number.isFinite(num)) this.record(a, num);
        break;
      case 'mcount': {
        // polled protocols: a new unit was measured when the counter moved
        const last = a.live.f.lastMeasure;
        if (a.measure && prev !== undefined && Number(prev) !== num && typeof last === 'number') this.record(a, last);
        break;
      }
      case 'stock':
        if (Number.isFinite(num)) a.stock = num;
        break;
      case 'count':
        if (Number.isFinite(num)) a.count = num;
        break;
      case 'battery':
        if (Number.isFinite(num)) a.battery = num;
        break;
    }
    return true;
  }

  /** running / idle / blocked / stopped / fault / maint as the machine reports it. */
  private deviceState(a: Asset, v: string, src: string) {
    if (v === 'fault') {
      const f = a.live?.f;
      const code = Number(f?.faultCode);
      this.deviceFault(a, typeof f?.fault === 'string' && f.fault ? f.fault : code > 0 ? a.spec?.faults?.[code - 1] ?? '' : '');
      return;
    }
    this.deviceRecovered(a, src);
    if (v === 'changeover') {
      // the machine is changing its tool; keep the twin in changeover while it says so
      a.changing = Math.max(a.changing, 5);
      return;
    }
    if (v === 'maint') a.maint = Math.max(a.maint, 5);
    else a.maint = 0;
    if (v === 'stopped') a.enabled = false;
    else if (v !== 'maint') a.enabled = true;
  }

  private deviceFault(a: Asset, text: string) {
    const t = text || `设备上报故障（${a.live?.src ?? '设备'}）`;
    if (!a.fault) this.trip(a, t);
    else if (text && a.fault.text !== text) a.fault.text = text;
  }

  private deviceRecovered(a: Asset, src: string) {
    if (!a.fault) return;
    a.fault = null;
    a.repairAt = undefined;
    this.clear(a, 'fault');
    this.note(a, `设备已恢复运行（${src}）`, 'op');
  }

  // ---------- running as the equipment (device emulator) ----------

  /** Measured units waiting to be published, when this plant plays the equipment. */
  private readings: [string, number][] | null = null;
  private mcount = new Map<string, number>();

  /** Makes this plant keep a queue of measured units for publishing. */
  playEquipment() {
    this.readings = [];
  }

  /** Machines with thermal/power behaviour, in a fixed order both sides of a protocol agree on. */
  machines(): Asset[] {
    return [...this.assets.values()].filter((a) => isMachineSpec(a.spec));
  }

  /** What the equipment reports right now, plus units measured since the last call. */
  telemetry() {
    const r = (v: number, d = 100) => Math.round(v * d) / d;
    const machines = this.machines().map((a) => {
      const code = a.fault ? (a.spec?.faults?.indexOf(a.fault.text.replace('（模拟）', '')) ?? -1) + 1 || 99 : 0;
      return {
        id: a.id, state: a.fault ? 'fault' : a.state, fault: a.fault?.text ?? '', faultCode: code,
        temp: r(a.temp, 10), power: r(a.power, 10), vib: r(a.vib), speed: r(a.speed), health: r(a.health, 1000),
        mcount: this.mcount.get(a.id) ?? 0, lastMeasure: a.reading?.v ?? 0, measured: !!a.measure,
        toolUsed: a.tool?.used ?? 0, toolLife: a.tool?.life ?? 0,
      };
    });
    const readings = this.readings ? this.readings.splice(0) : [];
    const vehicles = this.agvs.map((g) => ({
      id: g.asset.id, x: r(g.x), z: r(g.z), heading: r(g.heading, 1000), battery: r(g.asset.battery, 10), task: g.asset.task, state: g.asset.state,
    }));
    const inventory = this.inventory.map((i) => ({ key: i.key, avail: i.avail, cap: i.cap }));
    return { machines, readings, vehicles, inventory };
  }

  /** Vehicles in protocol order. */
  vehicles(): Asset[] {
    return this.agvs.map((g) => g.asset);
  }

  /** A compact picture of the plant for the server (MCP tools read it). */
  snapshot() {
    const k = this.kpis();
    const L = this.layout;
    return {
      id: L.id, name: L.name, hall: L.hall, unit: L.unit,
      kpis: {
        oee: r2(k.oee), avail: r2(k.avail), perf: r2(k.perf), quality: r2(k.quality),
        throughputPerHour: k.tp, good: k.good, scrap: k.scrap, powerKW: r2(k.power), energyKWh: r2(k.energy), clock: hhmm(this.clock),
      },
      lines: this.lines.map((l, i) => ({
        id: l.id, name: l.name, product: l.layout.product.name, oee: r2(k.lines[i].oee), throughputPerHour: k.lines[i].tp,
        good: l.good, scrap: l.scrap,
      })),
      alarms: this.alarms.slice(0, 60).map((x) => ({ asset: x.asset, text: x.text, prio: x.prio, at: hhmm(x.at), active: x.active, acked: x.acked })),
      inventory: this.inventory.map((i) => ({
        key: i.key, label: i.label, unit: i.unit, kind: i.kind === 'lend' ? '借用（会归还）' : '消耗（需补货）',
        capacity: i.cap, available: i.avail, inUse: i.kind === 'lend' ? i.cap - i.avail : undefined,
      })),
      assets: [...this.assets.values()].map((a) => ({
        id: a.id, name: a.name, type: a.type, line: a.line?.name, state: a.fault ? 'fault' : a.state, fault: a.fault?.text,
        temp: r2(a.temp), power: r2(a.power), vib: r2(a.vib), health: r2(a.health), util: r2(a.util), speed: r2(a.speed),
        live: liveFields(a) ? a.live!.src : undefined,
        tool: a.tool ? `${a.tool.label} ${a.tool.used}/${a.tool.life} 次（${Math.round((a.tool.used / a.tool.life) * 100)}%）${a.changing > 0 ? '，更换中' : ''}` : undefined,
      })),
    };
  }

  // ---------- replication (server → browsers) ----------

  /** Everything a follower needs to match this plant; drains the event queue unless told not to. */
  exportState(drain = true): PlantState {
    const p3 = (v: number) => Math.round(v * 1000) / 1000;
    return {
      clock: p3(this.clock), elapsed: p3(this.elapsed), energy: p3(this.energyKWh), power: p3(this.powerKW), alarmSeq: this.alarmSeq,
      assets: [...this.assets.values()].map((a) => ({
        id: a.id, enabled: a.enabled, speed: a.speed, fault: a.fault, maint: p3(a.maint), state: a.state,
        temp: p3(a.temp), vib: p3(a.vib), power: p3(a.power), health: p3(a.health), util: p3(a.util), avail: p3(a.avail),
        cycles: a.cycles, progress: p3(a.progress), busy: a.busy?.id ?? null, reading: a.reading ?? null, rejects: a.rejects,
        verdict: a.verdict ?? null, stock: a.stock, count: a.count, battery: p3(a.battery), vel: p3(a.vel), task: a.task, live: a.live ?? null,
        toolUsed: a.tool?.used, toolLimit: a.tool?.limit, changing: a.changing ? p3(a.changing) : undefined,
      })),
      lines: this.lines.map((l) => ({
        id: l.id, spawnT: p3(l.spawnT), pid: l.pid, good: l.good, scrap: l.scrap, tp: l.doneTimes.length,
        lot: l.lot ? [l.lot.no, l.lot.supplier] as [string, string] : undefined,
        job: l.job ? [l.job.no, l.job.name, l.job.qty, l.job.good, l.job.issued] as [string, string, number, number, number] : undefined,
        products: l.products.map((x) => [x.id, p3(x.s), x.done, x.proc, p3(x.t), x.kind, p3(x.stall), x.held ?? null] as ProductTuple),
      })),
      agvs: this.agvs.map((g) => ({
        id: g.asset.id, idx: g.idx, x: p3(g.x), z: p3(g.z), heading: p3(g.heading), cargo: g.cargo, mode: g.mode,
        dwell: p3(g.dwell), waiting: g.waiting, waitedT: p3(g.waitedT), task: g.task,
      })),
      trucks: this.trucks.map((t) => ({
        id: t.asset.id, idx: t.idx, x: p3(t.x), z: p3(t.z), heading: p3(t.heading), phase: t.phase, wait: p3(t.wait), dock: p3(t.dock), load: t.load,
      })),
      alarms: this.alarms.slice(0, 80),
      log: this.log.slice(0, 60),
      inventory: this.inventory.map((i) => [i.key, i.avail, i.cap] as [string, number, number]),
      events: drain ? this.events.splice(0) : [],
    };
  }

  // ---------- persistence (server restarts) ----------

  /** Identifies the plant's structure; saved state only applies to the same structure. */
  fingerprint(): string {
    const lines = this.lines.map((l) => `${l.id}:${l.stations.map((a) => a.id).join(',')}`).join(';');
    return `${lines}|${this.agvs.map((g) => g.asset.id).join(',')}|${this.trucks.map((t) => t.asset.id).join(',')}`;
  }

  /**
   * Everything needed to carry on after a restart. The short trend buffers are left out (they
   * refill within minutes); SPC readings, which take much longer to accumulate, are kept.
   */
  saveState(): SavedPlant {
    const r = (v: number) => Math.round(v * 1000) / 1000;
    const assets = [...this.assets.values()];
    return {
      fp: this.fingerprint(),
      state: this.exportState(false),
      done: this.lines.map((l) => l.doneTimes.map(r)),
      meas: assets.filter((a) => a.hist.meas.length).map((a) => [a.id, a.hist.meas.map(r)]),
      repair: assets.filter((a) => a.repairAt !== undefined).map((a) => [a.id, a.repairAt!]),
      recond: this.repairs,
    };
  }

  /** Carries on from a saved state; false (and nothing changed) if the plant's structure differs. */
  restoreState(saved: SavedPlant): boolean {
    if (saved.fp !== this.fingerprint()) return false;
    this.importState(saved.state);
    this.lines.forEach((l, i) => { if (saved.done[i]) l.doneTimes = saved.done[i]; });
    for (const [id, m] of saved.meas) {
      const a = this.assets.get(id);
      if (a) a.hist.meas = m;
    }
    for (const [id, t] of saved.repair) {
      const a = this.assets.get(id);
      if (a) a.repairAt = t;
    }
    this.repairs = saved.recond ?? [];
    // carry on in the present: the plant's wall clock is now
    this.epoch = Date.now() - this.elapsed * 1000;
    return true;
  }

  /** Adopts the server's state; local motion continues from here until the next frame. */
  importState(s: PlantState) {
    this.clock = s.clock;
    this.elapsed = s.elapsed;
    this.energyKWh = s.energy;
    this.powerKW = s.power;
    this.alarmSeq = s.alarmSeq;
    const busy: [Asset, string][] = [];
    for (const x of s.assets) {
      const a = this.assets.get(x.id);
      if (!a) continue;
      if (x.reading && x.reading.at !== a.reading?.at) {
        a.hist.meas.push(x.reading.v);
        if (a.hist.meas.length > 60) a.hist.meas.shift();
      }
      Object.assign(a, {
        enabled: x.enabled, speed: x.speed, fault: x.fault, maint: x.maint, state: x.state, temp: x.temp, vib: x.vib, power: x.power,
        health: x.health, util: x.util, avail: x.avail, cycles: x.cycles, progress: x.progress, reading: x.reading ?? undefined,
        rejects: x.rejects, verdict: x.verdict ?? undefined, stock: x.stock, count: x.count, battery: x.battery, vel: x.vel, task: x.task,
        live: x.live ?? undefined, busy: null, changing: x.changing ?? 0,
      });
      if (a.tool && x.toolUsed !== undefined) a.tool.used = x.toolUsed;
      if (a.tool && x.toolLimit !== undefined) a.tool.limit = x.toolLimit;
      if (x.busy) busy.push([a, x.busy]);
    }
    const byId = new Map<string, Product>();
    for (const x of s.lines) {
      const l = this.lineById[x.id];
      if (!l) continue;
      l.spawnT = x.spawnT;
      l.pid = x.pid;
      // a follower shows the order the server is running; the server keeps its own
      if (x.job && this.follower) l.job = { ...(l.job ?? ({} as Job)), no: x.job[0], name: x.job[1], qty: x.job[2], good: x.job[3], issued: x.job[4] };
      if (x.lot) l.lot = { no: x.lot[0], supplier: x.lot[1], factor: l.lot?.factor ?? 1, left: l.lot?.no === x.lot[0] ? l.lot.left : 0 };
      l.good = x.good;
      l.scrap = x.scrap;
      // Only the count of the last hour's completions matters locally (throughput KPI).
      if (l.doneTimes.length !== x.tp) l.doneTimes = new Array(x.tp).fill(this.elapsed);
      l.products = x.products.map(([id, ps, done, proc, t, kind, stall, held]) => {
        const p: Product = { id, s: ps, done, proc, t, kind, stall, held: held ?? undefined };
        byId.set(id, p);
        return p;
      });
    }
    for (const [a, id] of busy) a.busy = byId.get(id) ?? null;
    for (const x of s.agvs) {
      const g = this.agvs.find((v) => v.asset.id === x.id);
      if (g) Object.assign(g, { idx: x.idx, x: x.x, z: x.z, heading: x.heading, cargo: x.cargo, mode: x.mode, dwell: x.dwell, waiting: x.waiting, waitedT: x.waitedT, task: x.task });
    }
    for (const x of s.trucks) {
      const t = this.trucks.find((v) => v.asset.id === x.id);
      if (t) Object.assign(t, { idx: x.idx, x: x.x, z: x.z, heading: x.heading, phase: x.phase, wait: x.wait, dock: x.dock, load: x.load });
    }
    this.alarms = s.alarms;
    this.raised = new Set(s.alarms.filter((x) => x.active).map((x) => `${x.asset} ${x.code}`));
    this.log = s.log;
    for (const [k, avail, cap] of s.inventory ?? []) {
      const it = this.inv(k);
      if (it) Object.assign(it, { avail, cap });
    }
    this.events.push(...s.events);
    this.synced = true;
  }

  // ---------- derived ----------

  lineKpi(line: Line): LineKpi {
    const tp = line.doneTimes.length; // parts in the last 3600 s
    const ideal = 3600 / Math.max(...line.stations.map((s) => s.cycle));
    const perf = Math.min(1, tp / ideal);
    const avail = line.stations.reduce((s, a) => s + a.avail, 0) / line.stations.length;
    const quality = line.good / Math.max(1, line.good + line.scrap);
    return { tp, perf, avail, quality, oee: avail * perf * quality, good: line.good, scrap: line.scrap };
  }

  kpis(): Kpis {
    const per = this.lines.map((l) => this.lineKpi(l));
    const avg = (k: keyof LineKpi) => per.reduce((s, x) => s + x[k], 0) / per.length;
    const good = per.reduce((s, x) => s + x.good, 0);
    const scrap = per.reduce((s, x) => s + x.scrap, 0);
    return {
      lines: per,
      oee: avg('oee'), avail: avg('avail'), perf: avg('perf'), quality: good / Math.max(1, good + scrap),
      tp: per.reduce((s, x) => s + x.tp, 0), good, scrap,
      power: this.powerKW, energy: this.energyKWh,
    };
  }
}
