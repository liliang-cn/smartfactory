// The "physical" side of the twin: a discrete simulation of the plant.
// The 3D view and UI only read this state and send commands through `command()`,
// so swapping it for a live MQTT/OPC UA feed means implementing the same surface.

import type { MachineCfg } from '@/config/schema';
import type { PlantLayout } from '@/core/layout';
import type {
  AgvState, Alarm, Asset, AssetState, AssetType, Command, CommandResult, Kpis, Line, LineKpi, LogEntry, MachineSpec,
  ProductKind, SimEvent, TruckState, TypeSpec,
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
const rand = (a: number, b: number) => a + Math.random() * (b - a);
const clamp = (v: number, a: number, b: number) => Math.max(a, Math.min(b, v));
const pick = <T,>(arr: T[]) => arr[Math.floor(Math.random() * arr.length)];
const ema = (dt: number, tau: number) => 1 - Math.exp(-dt / tau);

export const STATE_LABEL: Record<AssetState, string> = {
  running: '运行', idle: '待料', blocked: '阻塞', stopped: '停机', fault: '故障', maint: '维护',
  moving: '行驶', waiting: '等待', handling: '装卸', away: '在途', ok: '正常', empty: '缺料', low: '低位', full: '满载',
};

type AssetInit = Partial<Asset> & Pick<Asset, 'id' | 'name' | 'type'>;

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

  constructor(layout: PlantLayout) {
    this.layout = layout;
    this.build();
  }

  // ---------- construction ----------

  private asset(def: AssetInit): Asset {
    const spec = def.machine ? machineSpec(def.machine) : BUILTIN[def.type];
    const a: Asset = {
      group: 'WH', s: 0, cycle: 0, stock: 0, cap: 0, count: 0, battery: 0, vel: 0, task: '', rejects: 0, arch: def.type,
      ...def,
      spec,
      enabled: true, speed: 1, fault: null, maint: 0, state: 'idle',
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
        s: st.x - ll.x0, cycle: st.cycle, makes: st.makes, measure: st.measure,
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

  step(dt: number) {
    this.clock += dt;
    this.elapsed += dt;
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

    this.histAcc += dt;
    if (this.histAcc >= 1) {
      this.histAcc -= 1;
      for (const a of this.assets.values()) this.pushHist(a);
    }
  }

  // One reading per finished unit. Wear and over-speed push the process off target, so
  // a neglected machine shows up first as drift on the chart, then as rejects.
  private measure(a: Asset, quiet = false): boolean {
    const m = a.measure!;
    const wear = 1 - a.health;
    const gauss = Math.sqrt(-2 * Math.log(1 - Math.random())) * Math.cos(2 * Math.PI * Math.random());
    let dev = gauss * m.sigma * (1 + wear * 2.5) + m.drift * wear * m.sigma * 4 + (a.speed - 1) * (m.speedBias ?? 0);
    if (m.abs) dev = Math.abs(dev);
    const v = m.target + dev;
    const ok = (m.lo === undefined || v >= m.lo) && (m.hi === undefined || v <= m.hi);
    const h = a.hist.meas;
    h.push(v);
    if (h.length > 60) h.shift();
    a.reading = { v, ok, at: this.elapsed };
    if (quiet) return ok;
    if (!ok) a.rejects++;

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
    return a.enabled && !a.fault && a.maint <= 0;
  }

  private reject(line: Line, a: Asset, i: number) {
    const p = line.products[i];
    line.scrap++;
    this.events.push({ type: 'reject', line: line.id, asset: a.id, x: line.x0 + p.s, z: line.z, kind: p.kind });
    line.products.splice(i, 1);
  }

  private stepLine(line: Line, dt: number) {
    const cv = line.conveyor;
    const v = this.ready(cv) ? line.speed * cv.speed : 0;
    const st = line.stations;
    const P = line.products;

    // Release raw parts from the infeed buffer onto the belt.
    line.spawnT -= dt;
    const last = P[P.length - 1];
    if (v > 0 && line.infeed.stock > 0 && line.spawnT <= 0 && (!last || last.s > line.gap + 0.05)) {
      P.push({ id: `${line.id}-${++line.pid}`, s: 0, done: -1, proc: -1, t: 0, kind: line.layout.input, stall: 0 });
      line.infeed.stock--;
      line.spawnT = line.spawn;
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
        p.done = p.proc;
        p.proc = -1;
        p.stall = 0;
        a.busy = null;
        a.progress = 0;
        a.cycles++;
        if (a.makes) p.kind = a.makes;
        if (a.measure && !this.measure(a)) {
          this.reject(line, a, i--);
          continue;
        }
        const rejectRate = a.machine?.rejectRate;
        if (rejectRate !== undefined && !a.measure) {
          const upstream = st.slice(0, p.done);
          const h = upstream.reduce((sum, u) => sum + u.health, 0) / Math.max(1, upstream.length);
          const ok = Math.random() > rejectRate + (1 - h) * 0.1;
          a.verdict = { ok, at: this.elapsed };
          if (!ok) this.reject(line, a, i--);
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
        P.splice(i--, 1);
      }
    }

    st.forEach((a, idx) => {
      if (a.fault) a.state = 'fault';
      else if (a.maint > 0) a.state = 'maint';
      else if (!a.enabled) a.state = 'stopped';
      else if (a.busy) a.state = 'running';
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
        if (t.load <= 0 || t.dock > 30) this.truckDone(t, '卸货完成，原料入库');
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
    a.util += ((running ? 1 : 0) - a.util) * ema(dt, 300);
    a.avail += ((on ? 1 : 0) - a.avail) * ema(dt, 600);

    if (running) {
      const over = Math.max(0, a.temp - s.warn) / 10;
      a.health = Math.max(0.05, a.health - dt * 5e-5 * a.speed * a.speed * (1 + over));
    }

    if (on && a.temp > s.warn) this.raise(a, 'temp', 2, `温度高 ${a.temp.toFixed(0)}°C，报警值 ${s.warn}°C`);
    else if (a.temp < s.warn - 2) this.clear(a, 'temp');
    if (!a.fault && a.temp > s.trip) this.trip(a, `温度超限 ${a.temp.toFixed(0)}°C，自动停机`);

    if (a.health < 0.35) this.raise(a, 'health', 3, `健康度 ${(a.health * 100).toFixed(0)}%，建议安排维护`);
    else if (a.health > 0.4) this.clear(a, 'health');

    if (running && !a.fault) {
      const rate = (1 / 4200) * (1 + (1 - a.health) * 6) * a.speed * a.speed;
      if (Math.random() < rate * dt) this.trip(a, pick(s.faults));
    }
  }

  // ---------- alarms & log ----------

  private trip(a: Asset, text: string) {
    if (a.fault) return;
    a.fault = { text, at: this.clock };
    this.raise(a, 'fault', 1, text);
    this.events.push({ type: 'fault', asset: a.id });
  }

  private raise(a: Asset, code: string, prio: Alarm['prio'], text: string) {
    if (this.alarms.some((x) => x.asset === a.id && x.code === code && x.active)) return;
    const al: Alarm = { id: this.alarmSeq++, asset: a.id, code, prio, text, at: this.clock, active: true, acked: false };
    this.alarms.unshift(al);
    this.note(a, text, 'alarm', prio);
    if (this.alarms.length > 160) {
      const idx = this.alarms.findLastIndex((x) => !x.active && x.acked);
      if (idx >= 0) this.alarms.splice(idx, 1);
    }
  }

  private clear(a: Asset, code: string) {
    for (const al of this.alarms) {
      if (al.asset === a.id && al.code === code && al.active) {
        al.active = false;
        al.clearedAt = this.clock;
      }
    }
  }

  private note(a: Asset, text: string, kind: LogEntry['kind'] = 'info', prio = 0) {
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
