// What a plant did, hour by hour, for the history database the advisor (DataIntelligence) reads.
//
// The recorder only observes. Downtime, changeovers and repairs are read off state transitions,
// so they are logged the same way whether the twin simulates a machine or the equipment reports
// its own state. Times are local (Asia/Shanghai) wall-clock strings: the database stores them
// without a zone, and every bucket boundary is a local hour.
import type { Asset, AssetState, Job, Line, Product } from './types';
import type { PlantSim } from './plant';

const TZ_MS = 8 * 3600_000;
const HOUR = 3600_000;

/** "2026-10-06 14:00:00" in local time */
export const localTime = (ms: number) => new Date(ms + TZ_MS).toISOString().slice(0, 19).replace('T', ' ');
const hourOf = (ms: number) => Math.floor(ms / HOUR) * HOUR;

/** a part number's row in the parts table: the family, or family:part number */
export const partId = (line: Line, j?: Job) => (j ? `${line.layout.product.key}:${j.partNo}` : line.layout.product.key);

const DOWN: Partial<Record<AssetState, string>> = { fault: '设备故障', maint: '计划维护', changeover: '换模换型', stopped: '人为停机' };

type StateKey = 'run' | 'idle' | 'blocked' | 'fault' | 'maint' | 'changeover' | 'stopped';
const STATES = new Set<string>(['run', 'idle', 'blocked', 'fault', 'maint', 'changeover', 'stopped']);

interface MachineHour {
  machine: string; hour: string;
  run: number; idle: number; blocked: number; fault: number; maint: number; changeover: number; stopped: number;
  cycles: number; scrap: number; rework: number; kwh: number; fuel: number;
  /** 元: electricity at the hour's tariff, and fuel */
  powerCost: number; fuelCost: number;
}
interface LineHour {
  line: string; hour: string; planned: number; run: number;
  /** units the bottleneck machine finished: what performance counts (setup pieces scrapped upstream never reach it) */
  neckCycles: number;
  started: number; produced: number; good: number; rework: number; scrap: number; goodKg: number; inputKg: number; idealCycle: number;
}
interface QualityHour {
  machine: string; hour: string; feature: string; unit: string; n: number; sum: number; sq: number; min: number; max: number;
  lsl: number | null; usl: number | null; out: number;
}
interface StockHour { item: string; label: string; hour: string; min: number; end: number; cap: number }
interface Downtime { machine: string; line: string; start: string; end: string; category: string; reason: string }
interface ToolChange { machine: string; line: string; tool: string; start: string; end: string; hits: number; life: number; limit: number; trigger: string; cost: number }
interface Defect { machine: string; line: string; at: string; type: string; feature: string; value: number | null; disposition: string; order: string; heat: string; supplier: string }
interface LotRow { heat: string; supplier: string; line: string; part: string; partName: string; first: string; last: string; produced: number; good: number; rework: number; scrap: number }
export interface WorkOrder {
  no: string; line: string; part: string; partName: string; customer: string; heat: string; supplier: string; qty: number; price: number; promise: string;
  issued: string; due: string; completed: string | null; good: number; scrap: number;
}

type Row = Record<string, unknown>;

export interface HistoryBatch {
  machineHours: MachineHour[];
  lineHours: LineHour[];
  quality: (Omit<QualityHour, 'sum' | 'sq'> & { mean: number; sd: number })[];
  stock: StockHour[];
  downtime: Downtime[];
  toolChanges: ToolChange[];
  defects: Defect[];
  orders: WorkOrder[];
  lots: LotRow[];
  purchases: Row[];
  shipments: Row[];
  complaints: Row[];
}

interface Open { state: AssetState; since: number; short: boolean; reason: string; hits: number; life: number; limit: number; trigger: string }

export class History {
  private sim: PlantSim;
  private plant: string;
  private t0: number;
  private hour = 0;
  private mh = new Map<string, MachineHour>();
  private lh = new Map<string, LineHour>();
  private qh = new Map<string, QualityHour>();
  private sh = new Map<string, StockHour>();
  private open = new Map<string, Open>();
  /** lots touched since the last drain */
  private lots = new Map<string, LotRow>();
  private out: HistoryBatch = History.empty();
  /** 元/kWh in the current hour */
  private price = 0;
  /** record only these lines (a what-if run of part of the plant) */
  only?: Set<string>;

  /** `t0` is the wall-clock time (ms) the plant's `elapsed` counts from */
  constructor(sim: PlantSim, plant: string, _plantNo: number, t0: number) {
    this.sim = sim;
    this.plant = plant;
    this.t0 = t0;
    this.hour = hourOf(this.now());
    sim.epoch = t0;
    this.price = this.tariff(this.hour);
  }

  static empty(): HistoryBatch {
    return { machineHours: [], lineHours: [], quality: [], stock: [], downtime: [], toolChanges: [], defects: [], orders: [], lots: [], purchases: [], shipments: [], complaints: [] };
  }

  now() { return this.t0 + this.sim.elapsed * 1000; }
  private id(a: Asset | Line) { return `${this.plant}-${a.id}`; }

  // ---------- per step ----------

  /** Called after every simulation step. */
  step(dt: number) {
    const now = this.now();
    if (hourOf(now) !== this.hour) this.roll(hourOf(now));
    for (const line of this.sim.lines) {
      if (this.only && !this.only.has(line.id)) continue;
      const lh = this.lineHour(line);
      lh.planned += dt;
      const neck = this.neck(line);
      if (neck.state === 'running') lh.run += dt;
      for (const a of line.stations) this.machineStep(a, line, dt, now);
    }
    for (const it of this.sim.inventory) {
      const k = it.key;
      let s = this.sh.get(k);
      if (!s) this.sh.set(k, (s = { item: k, label: it.label, hour: localTime(this.hour), min: it.avail, end: it.avail, cap: it.cap }));
      s.min = Math.min(s.min, it.avail);
      s.end = it.avail;
      s.cap = it.cap;
    }
  }

  private neck(line: Line) {
    return line.stations.reduce((m, a) => (a.cycle > m.cycle ? a : m), line.stations[0]);
  }

  private machineStep(a: Asset, line: Line, dt: number, now: number) {
    const h = this.machineHour(a);
    const st = a.state === 'running' ? 'run' : a.state;
    if (STATES.has(st)) h[st as StateKey] += dt;
    h.kwh += (a.power * dt) / 3600;
    h.powerCost += ((a.power * dt) / 3600) * this.price;
    const fuel = a.machine?.fuel;
    if (fuel) {
      const on = a.state === 'running' ? fuel.run : a.enabled && !a.fault && a.maint <= 0 ? fuel.idle : 0;
      h.fuel += (on * dt) / 3600;
      h.fuelCost += ((on * dt) / 3600) * (this.sim.layout.tariff?.gas ?? 0);
    }
    // downtime and changeovers from state transitions
    const prev = this.open.get(a.id);
    if (prev && prev.state === a.state) return;
    if (prev) this.close(a, line, prev, now);
    if (DOWN[a.state]) {
      const t = a.tool;
      const share = t ? t.used / t.life : 0;
      this.open.set(a.id, {
        state: a.state, since: now, short: a.state === 'fault' && /^短停：/.test(a.fault?.text ?? ''),
        reason: a.state === 'fault' ? (a.fault?.text ?? '设备故障').replace(/^短停：/, '') : a.state === 'changeover' ? `更换${t?.label ?? '工装'}` : a.state === 'maint' ? '保养维护' : '停机',
        hits: t?.used ?? 0, life: t?.life ?? 0, limit: t?.limit ?? 1,
        trigger: !t ? '' : a.changeReason ? a.changeReason : share >= 1 ? '到寿命' : share >= (t.limit ?? 1) - 1e-9 ? '计划更换' : '指令更换',
      });
    } else {
      this.open.delete(a.id);
    }
  }

  private close(a: Asset, line: Line, o: Open, now: number) {
    const ev = { machine: this.id(a), line: this.id(line), start: localTime(o.since), end: localTime(now) };
    this.out.downtime.push({ ...ev, category: o.short ? '短停机' : DOWN[o.state]!, reason: o.reason });
    if (o.state === 'changeover' && a.tool) {
      // a part-number change mounts tools already paid for; a worn one costs a new or reconditioned tool
      this.out.toolChanges.push({ ...ev, machine: this.id(a), tool: a.tool.label, hits: o.hits, life: o.life, limit: o.limit, trigger: o.trigger, cost: o.trigger === '换型' ? 0 : a.tool.cost ?? 0 });
    }
  }

  // ---------- events from the plant ----------

  cycle(a: Asset) {
    this.machineHour(a).cycles++;
    const line = a.line;
    if (line && (!this.only || this.only.has(line.id)) && this.neck(line) === a) this.lineHour(line).neckCycles++;
  }

  /** a unit left the end of the line */
  output(line: Line, p: Product) {
    const lh = this.lineHour(line);
    lh.produced++;
    if (p.rework) lh.rework++;
    else lh.good++;
    const lr = this.lot(line, p);
    if (lr) {
      lr.produced++;
      if (p.rework) lr.rework++;
      else lr.good++;
    }
    lh.goodKg += line.job?.weight ?? line.layout.product.weight ?? 0;
  }

  /** a unit failed a check: scrapped, or reworked and kept on the line */
  defect(line: Line, a: Asset, type: string, feature: string, value: number | null, rework: boolean, p?: Product) {
    const at = localTime(this.now());
    this.out.defects.push({
      machine: this.id(a), line: this.id(line), at, type, feature, value, disposition: rework ? '返修' : '报废', order: line.job?.no ?? '',
      heat: p?.lot ?? '', supplier: p?.sup ?? '',
    });
    const lr = p && !rework ? this.lot(line, p) : null;
    if (lr) {
      lr.produced++;
      lr.scrap++;
    }
    const mh = this.machineHour(a);
    const lh = this.lineHour(line);
    if (rework) {
      mh.rework++;
    } else {
      mh.scrap++;
      lh.scrap++;
      lh.produced++;
    }
  }

  measure(a: Asset, v: number, ok: boolean) {
    const m = a.measure!;
    const key = a.id;
    let q = this.qh.get(key);
    if (!q) {
      q = { machine: this.id(a), hour: localTime(this.hour), feature: m.label, unit: m.unit, n: 0, sum: 0, sq: 0, min: v, max: v, lsl: m.lo ?? null, usl: m.hi ?? null, out: 0 };
      this.qh.set(key, q);
    }
    q.n++;
    q.sum += v;
    q.sq += v * v;
    q.min = Math.min(q.min, v);
    q.max = Math.max(q.max, v);
    if (!ok) q.out++;
  }

  private lot(line: Line, p: Product) {
    if (!p.lot) return null;
    const at = localTime(this.now());
    let r = this.lots.get(p.lot);
    if (!r) {
      const j = line.job;
      r = {
        heat: p.lot, supplier: p.sup ?? '', line: this.id(line), part: partId(line, j), partName: j?.name ?? line.layout.product.name,
        first: at, last: at, produced: 0, good: 0, rework: 0, scrap: 0,
      };
      this.lots.set(p.lot, r);
    }
    r.last = at;
    return r;
  }

  // ---------- buckets ----------

  private machineHour(a: Asset) {
    let h = this.mh.get(a.id);
    if (!h) {
      h = {
        machine: this.id(a), hour: localTime(this.hour), run: 0, idle: 0, blocked: 0, fault: 0, maint: 0, changeover: 0, stopped: 0,
        cycles: 0, scrap: 0, rework: 0, kwh: 0, fuel: 0, powerCost: 0, fuelCost: 0,
      };
      this.mh.set(a.id, h);
    }
    return h;
  }

  private lineHour(line: Line) {
    let h = this.lh.get(line.id);
    if (!h) {
      h = {
        line: this.id(line), hour: localTime(this.hour), planned: 0, run: 0, neckCycles: 0, started: 0, produced: 0, good: 0, rework: 0, scrap: 0,
        goodKg: 0, inputKg: 0, idealCycle: this.neck(line).cycle,
      };
      this.lh.set(line.id, h);
    }
    return h;
  }

  /** material that went into a unit released onto the line */
  released(line: Line) {
    const lh = this.lineHour(line);
    lh.started++;
    lh.inputKg += line.job?.input ?? line.layout.product.input ?? 0;
  }

  private roll(next: number) {
    const r1 = (x: number) => Math.round(x * 10) / 10;
    const r3 = (x: number) => Math.round(x * 1000) / 1000;
    for (const h of this.mh.values()) {
      this.out.machineHours.push({
        ...h, run: r1(h.run), idle: r1(h.idle), blocked: r1(h.blocked), fault: r1(h.fault), maint: r1(h.maint),
        changeover: r1(h.changeover), stopped: r1(h.stopped), kwh: r3(h.kwh), fuel: r3(h.fuel), powerCost: r3(h.powerCost), fuelCost: r3(h.fuelCost),
      });
    }
    for (const h of this.lh.values()) this.out.lineHours.push({ ...h, planned: r1(h.planned), run: r1(h.run), goodKg: r3(h.goodKg), inputKg: r3(h.inputKg) });
    for (const q of this.qh.values()) {
      const mean = q.sum / q.n;
      const sd = q.n > 1 ? Math.sqrt(Math.max(0, (q.sq - q.n * mean * mean) / (q.n - 1))) : 0;
      const { sum: _s, sq: _q, ...rest } = q;
      this.out.quality.push({ ...rest, mean: Math.round(mean * 1e4) / 1e4, sd: Math.round(sd * 1e5) / 1e5 });
    }
    for (const s of this.sh.values()) this.out.stock.push({ ...s });
    this.mh.clear();
    this.lh.clear();
    this.qh.clear();
    this.sh.clear();
    this.hour = next;
    this.price = this.tariff(next);
  }

  // ---------- work orders ----------

  /** A work order as the database keeps it. */
  private order(line: Line, j: Job): WorkOrder {
    const prod = line.layout.product;
    return {
      no: j.no, line: this.id(line), part: partId(line, j), partName: j.name, customer: j.customer || (prod.customer ?? ''), heat: j.heat, supplier: j.supplier,
      price: j.price, promise: localTime(j.promise),
      qty: j.qty, issued: localTime(j.issued), due: localTime(j.due), completed: j.done ? localTime(j.done) : null, good: j.good, scrap: j.scrap,
    };
  }

  /** electricity price (元/kWh) of the local hour starting at `ms` */
  private tariff(ms: number) {
    const t = this.sim.layout.tariff;
    if (!t) return 0;
    const h = Math.floor((ms + TZ_MS) / HOUR) % 24;
    return t.power.find((p) => h >= p.from && h < p.to)?.price ?? 0;
  }

  /** a material lot bought and inspected on arrival */
  purchase(r: Row) {
    this.out.purchases.push({
      ...r, line: `${this.plant}-${r.line}`, ordered: localTime(r.ordered as number), promised: localTime(r.promised as number), received: localTime(r.received as number),
    });
  }

  /** finished goods leaving (delivered null) and arriving (delivered set) */
  shipment(line: Line, r: Row) {
    this.out.shipments.push({
      ...r, line: this.id(line), part: `${line.layout.product.key}:${r.part}`, departed: localTime(r.departed as number), eta: localTime(r.eta as number),
      promise: localTime(r.promise as number), delivered: r.delivered ? localTime(r.delivered as number) : null,
    });
  }

  /** a customer complaint, opened and later closed */
  complaint(line: Line, r: Row) {
    this.out.complaints.push({
      ...r, line: this.id(line), part: `${line.layout.product.key}:${r.part}`, opened: localTime(r.opened as number), closed: r.closed ? localTime(r.closed as number) : null,
    });
  }

  /** a work order the plant finished */
  jobDone(line: Line, j: Job) {
    this.out.orders.push(this.order(line, j));
  }

  /**
   * Closed hours and finished events since the last call; open orders are included as they stand.
   * `flush` also closes the hour in progress (shutting down): the rows are added to, not replaced,
   * so the rest of the hour recorded after a restart completes it.
   */
  drain(flush = false): HistoryBatch {
    if (flush) this.roll(this.hour);
    const b = this.out;
    b.lots.push(...this.lots.values());
    this.lots.clear();
    for (const line of this.sim.lines) if (line.job) b.orders.push(this.order(line, line.job));
    this.out = History.empty();
    return b;
  }
}
