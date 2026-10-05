// The "physical" side of the twin: a discrete simulation of the plant.
// The 3D view and UI only read this state and send commands through `command()`,
// so swapping it for a live MQTT/OPC UA feed means implementing the same surface.

import {
  AMBIENT, AGVS, BELT_SPEED, FG_ZONE, LINES, PRODUCT_GAP, TRUCKS, TYPE_SPECS, WAREHOUSE,
} from './layout.js';

const HIST_LEN = 240;
const rand = (a, b) => a + Math.random() * (b - a);
const clamp = (v, a, b) => Math.max(a, Math.min(b, v));
const pick = (arr) => arr[Math.floor(Math.random() * arr.length)];
const ema = (dt, tau) => 1 - Math.exp(-dt / tau);

export const STATE_LABEL = {
  running: '运行', idle: '待料', blocked: '阻塞', stopped: '停机', fault: '故障', maint: '维护',
  moving: '行驶', waiting: '等待', handling: '装卸', away: '在途', ok: '正常', empty: '缺料', low: '低位', full: '满载',
};

export class PlantSim {
  constructor() {
    this.clock = 6 * 3600 + 12 * 60; // shift clock, seconds since midnight
    this.elapsed = 0;
    this.assets = new Map();
    this.lines = [];
    this.lineById = {};
    this.agvs = [];
    this.alarms = [];
    this.log = [];
    this.events = [];
    this.alarmSeq = 1;
    this.energyKWh = 0;
    this.powerKW = 0;
    this._histAcc = 0;
    this._build();
  }

  // ---------- construction ----------

  _asset(def) {
    const spec = TYPE_SPECS[def.type];
    const a = {
      group: 'WH', ...def, spec,
      enabled: true, speed: 1, fault: null, maint: 0, state: 'idle',
      temp: AMBIENT, vib: 0, power: 0, health: rand(0.74, 0.97), util: 0.7, avail: 0.97,
      cycles: Math.floor(rand(1200, 5200)), progress: 0, busy: null,
      hist: { temp: [], power: [], vib: [], level: [] },
    };
    if (spec?.runRise !== undefined) a.temp = AMBIENT + spec.idleRise + (spec.runRise - spec.idleRise) * 0.7;
    this.assets.set(a.id, a);
    return a;
  }

  _build() {
    for (const L of LINES) {
      const line = {
        id: L.id, name: L.name, note: L.note, z: L.z, x0: L.x0, x1: L.x1, len: L.x1 - L.x0,
        products: [], spawnT: 0, pid: 0, good: 0, scrap: 0, doneTimes: [],
      };
      line.conveyor = this._asset({ ...L.conveyor, type: 'conveyor', group: L.id, line });
      line.infeed = this._asset({ ...L.infeed, type: 'buffer', group: L.id, line, stock: 26 });
      line.outfeed = this._asset({ ...L.outfeed, type: 'outfeed', group: L.id, line, count: 7 });
      line.stations = L.stations.map((s) => this._asset({ ...s, group: L.id, line, s: s.x - L.x0 }));
      // Warm start: pretend the line has been running for the last hour.
      const ideal = 3600 / Math.max(...line.stations.map((s) => s.cycle));
      const n = Math.round(ideal * rand(0.78, 0.88));
      for (let i = 0; i < n; i++) line.doneTimes.push(-3600 + (i + Math.random()) * (3600 / n));
      line.good = n;
      line.scrap = Math.round(n * rand(0.01, 0.025));
      this._seedProducts(line);
      this.lines.push(line);
      this.lineById[line.id] = line;
    }

    this.wh = this._asset({ id: WAREHOUSE.id, name: WAREHOUSE.name, type: 'warehouse', group: 'WH', stock: 2480, cap: WAREHOUSE.cap });
    this.fg = this._asset({ id: FG_ZONE.id, name: FG_ZONE.name, type: 'fgzone', group: 'WH', count: 132 });

    for (const def of AGVS) {
      const a = this._asset({ id: def.id, name: def.name, type: 'agv', group: 'LOG', battery: rand(62, 96), vel: 0 });
      const wp = def.route[0];
      this.agvs.push({ asset: a, route: def.route, idx: 1, x: wp.x, z: wp.z, heading: 0, cargo: null, mode: 'move', dwell: 0, waitedT: 0, task: '' });
      a.agv = this.agvs[this.agvs.length - 1];
    }

    this.trucks = TRUCKS.map((def, i) => {
      const a = this._asset({ id: def.id, name: def.name, type: 'truck', group: 'LOG', carrier: def.carrier, kind: def.kind, vel: 0 });
      const t = { asset: a, route: def.route, kind: def.kind, idx: 1, x: def.route[0].x, z: def.route[0].z, heading: 0, phase: 'away', wait: 10 + i * 25, dock: 0, load: 0 };
      a.truck = t;
      return t;
    });
  }

  _seedProducts(line) {
    const st = line.stations;
    for (let s = line.len - 2.2; s > 1.5; s -= rand(2.6, 4.2)) {
      if (st.some((a) => Math.abs(a.s - s) < 0.6)) continue;
      let done = -1;
      st.forEach((a, i) => { if (a.s < s) done = i; });
      const kind = done < 0 ? 'raw' : st[done].type === 'pack' ? 'carton' : 'part';
      line.products.push({ id: `${line.id}-${++line.pid}`, s, done, proc: -1, t: 0, kind, stall: 0 });
    }
  }

  // ---------- simulation ----------

  step(dt) {
    this.clock += dt;
    this.elapsed += dt;
    for (const line of this.lines) this._stepLine(line, dt);
    for (const g of this.agvs) this._stepAgv(g, dt);
    for (const t of this.trucks) this._stepTruck(t, dt);
    let p = 0;
    for (const a of this.assets.values()) {
      this._stepTelemetry(a, dt);
      p += a.power;
    }
    this.powerKW = p;
    this.energyKWh += (p * dt) / 3600;

    this._histAcc += dt;
    if (this._histAcc >= 1) {
      this._histAcc -= 1;
      for (const a of this.assets.values()) this._pushHist(a);
    }
  }

  _pushHist(a) {
    const h = a.hist;
    const push = (arr, v) => { arr.push(v); if (arr.length > HIST_LEN) arr.shift(); };
    if (a.spec?.runRise !== undefined) {
      push(h.temp, a.temp); push(h.power, a.power); push(h.vib, a.vib);
    } else if (a.type === 'agv') {
      push(h.level, a.battery); push(h.power, a.power);
    } else if (a.type === 'buffer' || a.type === 'warehouse') {
      push(h.level, a.stock);
    } else {
      push(h.level, a.count);
    }
  }

  _ready(a) {
    return a.enabled && !a.fault && a.maint <= 0;
  }

  _stepLine(line, dt) {
    const cv = line.conveyor;
    const v = this._ready(cv) ? BELT_SPEED * cv.speed : 0;
    const st = line.stations;
    const P = line.products;

    // Release raw parts from the infeed buffer onto the belt.
    line.spawnT -= dt;
    const last = P[P.length - 1];
    if (v > 0 && line.infeed.stock > 0 && line.spawnT <= 0 && (!last || last.s > PRODUCT_GAP + 0.05)) {
      P.push({ id: `${line.id}-${++line.pid}`, s: 0, done: -1, proc: -1, t: 0, kind: 'raw', stall: 0 });
      line.infeed.stock--;
      line.spawnT = 8.4;
    }

    // Front-most part first so each part can see the one ahead of it.
    for (let i = 0; i < P.length; i++) {
      const p = P[i];
      if (p.proc >= 0) {
        const a = st[p.proc];
        if (!this._ready(a)) continue;
        p.t += dt * a.speed;
        a.progress = Math.min(1, p.t / a.cycle);
        if (p.t < a.cycle) continue;
        p.done = p.proc;
        p.proc = -1;
        p.stall = 0;
        a.busy = null;
        a.progress = 0;
        a.cycles++;
        if (a.type === 'cnc' || a.type === 'press') p.kind = 'part';
        if (a.type === 'pack') p.kind = 'carton';
        if (a.type === 'qc') {
          const upstream = st.slice(0, p.done);
          const h = upstream.reduce((sum, u) => sum + u.health, 0) / Math.max(1, upstream.length);
          const ok = Math.random() > 0.012 + (1 - h) * 0.1;
          a.verdict = { ok, at: this.elapsed };
          if (!ok) {
            line.scrap++;
            this.events.push({ type: 'reject', line: line.id, asset: a.id, x: line.x0 + p.s, z: line.z, kind: p.kind });
            P.splice(i, 1);
            i--;
          }
        }
        continue;
      }

      let maxS = line.len;
      const ahead = i > 0 ? P[i - 1] : null;
      if (ahead) maxS = Math.min(maxS, ahead.s - PRODUCT_GAP);
      const n = p.done + 1;
      const next = n < st.length ? st[n] : null;
      if (next) maxS = Math.min(maxS, next.s);

      const before = p.s;
      if (v > 0 && p.s < maxS) p.s = Math.min(p.s + v * dt, maxS);
      p.stall = p.s === before ? p.stall + dt : 0;

      if (next && p.s >= next.s - 1e-4 && !next.busy && this._ready(next)) {
        next.busy = p;
        p.proc = n;
        p.t = 0;
      }
      if (!next && p.s >= line.len - 1e-4 && line.outfeed.count < line.outfeed.cap) {
        line.outfeed.count++;
        line.good++;
        line.doneTimes.push(this.elapsed);
        P.splice(i, 1);
        i--;
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
    if (inf.stock === 0) this._raise(inf, 'empty', 2, '上料缓存已空，产线缺料');
    else this._clear(inf, 'empty');

    const out = line.outfeed;
    out.state = out.count >= out.cap ? 'full' : 'ok';
    if (out.count >= out.cap) this._raise(out, 'full', 2, '成品托盘已满，等待 AGV 取货');
    else this._clear(out, 'full');

    while (line.doneTimes.length && line.doneTimes[0] < this.elapsed - 3600) line.doneTimes.shift();
  }

  _stepAgv(g, dt) {
    const a = g.asset;
    a.vel = 0;
    if (!this._ready(a)) {
      a.state = a.fault ? 'fault' : a.maint > 0 ? 'maint' : 'stopped';
      a.power = a.maint > 0 ? 0 : 0.05;
      return;
    }

    if (g.mode === 'dwell') {
      g.dwell -= dt;
      a.battery = Math.min(100, a.battery + dt * 0.3); // opportunity charging at docks
      a.state = g.waiting ? 'waiting' : 'handling';
      a.power = a.spec.pIdle;
      if (g.dwell <= 0) {
        if (g.waiting) this._agvAct(g);
        else { g.mode = 'move'; g.idx = (g.idx + 1) % g.route.length; }
      }
      return;
    }

    const wp = g.route[g.idx];
    const dx = wp.x - g.x;
    const dz = wp.z - g.z;
    const dist = Math.hypot(dx, dz);
    a.state = 'moving';
    a.power = a.spec.pIdle;
    if (dist < 0.01) {
      g.x = wp.x;
      g.z = wp.z;
      if (wp.act) {
        g.waitedT = 0;
        this._agvAct(g);
      } else g.idx = (g.idx + 1) % g.route.length;
      return;
    }

    // Pivot on the spot, then drive straight: how tape-guided AGVs actually move.
    const want = Math.atan2(dz, dx);
    let diff = want - g.heading;
    diff = Math.atan2(Math.sin(diff), Math.cos(diff));
    const turn = 1.8 * a.speed * dt;
    if (Math.abs(diff) > 0.02) {
      g.heading += clamp(diff, -turn, turn);
      a.power = a.spec.pIdle + 0.6;
      if (Math.abs(diff) > 0.15) return;
    }
    const vmax = 1.3 * a.speed;
    const vel = Math.min(vmax, dist * 1.4 + 0.15);
    const d = Math.min(dist, vel * dt);
    g.x += (dx / dist) * d;
    g.z += (dz / dist) * d;
    a.vel = vel;
    a.power = a.spec.pIdle + a.spec.pRun * (vel / vmax);
    a.battery = Math.max(0, a.battery - dt * 0.045 * a.speed);
    g.task = this._agvTask(g);

    if (Math.random() < dt / 5200) this._trip(a, pick(a.spec.faults));
    if (a.battery < 5) this._trip(a, '电量耗尽');
  }

  _stepTruck(t, dt) {
    const a = t.asset;
    a.vel = 0;
    if (!this._ready(a)) {
      a.state = a.fault ? 'fault' : a.maint > 0 ? 'maint' : 'stopped';
      return;
    }
    if (t.phase === 'away') {
      a.state = 'away';
      t.wait -= dt;
      const needed = t.kind === 'in' ? this.wh.stock < this.wh.cap - 500 : this.fg.count >= 60;
      a.task = t.wait > 0 ? `预计 ${Math.ceil(t.wait)} 秒后到厂` : needed ? '' : t.kind === 'in' ? '待命：原料库位已满' : '待命：成品不足 60 箱';
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
      const rate = 400 / 22;
      if (t.kind === 'in') {
        const n = Math.min(t.load, Math.max(0, this.wh.cap - this.wh.stock), Math.ceil(rate * dt));
        this.wh.stock += n;
        t.load -= n;
        a.task = `卸货入库，剩余 ${t.load} 件`;
        if (t.load <= 0 || t.dock > 30) this._truckDone(t, `卸货完成，原料入库`);
      } else {
        const n = Math.min(this.fg.count, Math.ceil((120 / 22) * dt), 120 - t.load);
        this.fg.count -= n;
        t.load += n;
        a.task = `装车 ${t.load} 箱`;
        if (t.load >= 120 || this.fg.count === 0 || t.dock > 30) this._truckDone(t, `装车 ${t.load} 箱，发运`);
      }
      return;
    }

    const wp = t.route[t.idx];
    const dx = wp.x - t.x;
    const dz = wp.z - t.z;
    const dist = Math.hypot(dx, dz);
    a.state = 'moving';
    a.task = t.phase === 'leave' ? '离厂' : t.kind === 'in' ? '进厂送原料' : '进厂提货';
    if (dist < 0.3) {
      if (wp.act === 'dock' && t.phase === 'drive') {
        t.phase = 'dock';
        t.dock = 0;
        this._note(a, '到达卸货口', 'info');
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
    let diff = Math.atan2(Math.sin(want - t.heading), Math.cos(want - t.heading));
    t.heading += Math.max(-1, Math.min(1, diff)) * Math.min(1, dt * 2.2);
    const v = Math.min(9 * a.speed, dist * 0.9 + 1.5) * (Math.abs(diff) > 0.6 ? 0.4 : 1);
    const d = Math.min(dist, v * dt);
    t.x += Math.cos(t.heading) * d;
    t.z += Math.sin(t.heading) * d;
    // Pull back onto the path so heading lag never lets the truck drift off the road.
    t.x += (dx / dist) * d * 0.15;
    t.z += (dz / dist) * d * 0.15;
    a.vel = v;
  }

  _truckDone(t, text) {
    this._note(t.asset, text, 'info');
    t.phase = 'leave';
    t.idx++;
  }

  _agvTask(g) {
    const wp = g.route[g.idx];
    const line = wp.line ? this.lineById[wp.line] : null;
    if (wp.act === 'load') return '回库取料';
    if (wp.act === 'drop') return `送料至 ${line.infeed.id}`;
    if (wp.act === 'pick') return `前往 ${line.outfeed.id} 取货`;
    if (wp.act === 'deliver') return g.cargo ? `成品送至 ${this.fg.id}` : '返回待命点';
    return g.task;
  }

  _agvAct(g) {
    const wp = g.route[g.idx];
    const line = wp.line ? this.lineById[wp.line] : null;
    const wait = (s, task) => { g.mode = 'dwell'; g.waiting = true; g.dwell = s; g.waitedT += s; g.task = task; };
    const work = (s, task) => { g.mode = 'dwell'; g.waiting = false; g.dwell = s; g.task = task; };

    switch (wp.act) {
      case 'load':
        if (g.cargo) return work(0.1, '');
        if (line.infeed.stock + 20 > line.infeed.cap) return wait(2, `待命：${line.infeed.id} 库存充足`);
        if (this.wh.stock < 20) return wait(2, '待命：原料库存不足');
        this.wh.stock -= 20;
        g.cargo = { kind: 'raw', n: 20 };
        return work(4, '装载原料');
      case 'drop':
        if (g.cargo) line.infeed.stock += g.cargo.n;
        g.cargo = null;
        return work(3, `卸料至 ${line.infeed.id}`);
      case 'pick': {
        const n = line.outfeed.count;
        if (n < 8 && !(g.waitedT > 40 && n > 0)) return wait(2, `等待 ${line.outfeed.id} 成品`);
        line.outfeed.count = 0;
        g.cargo = { kind: 'fg', n };
        return work(4, `取走 ${n} 箱成品`);
      }
      case 'deliver': {
        const had = g.cargo;
        if (had) this.fg.count += had.n;
        g.cargo = null;
        return work(had ? 3 : 0.1, had ? `卸下 ${had.n} 箱成品` : '');
      }
      default:
        return work(0.1, '');
    }
  }

  _stepTelemetry(a, dt) {
    const s = a.spec;
    if (a.maint > 0) {
      a.maint -= dt;
      a.health = Math.min(1, a.health + dt * 0.03);
      if (a.maint <= 0) {
        a.maint = 0;
        a.health = 1;
        this._note(a, '维护完成，健康度恢复至 100%', 'op');
      }
    }

    if (a.type === 'agv') {
      if (a.battery < 18) this._raise(a, 'battery', 3, `电量低 ${a.battery.toFixed(0)}%`);
      else if (a.battery > 25) this._clear(a, 'battery');
      return;
    }
    if (a.type === 'warehouse') {
      a.state = a.stock < 300 ? 'low' : 'ok';
      if (a.stock < 300) this._raise(a, 'low', 3, `原料库存低于 300 件，当前 ${a.stock}`);
      else this._clear(a, 'low');
      return;
    }
    if (s?.runRise === undefined) return;

    const running = a.state === 'running';
    const on = this._ready(a);
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

    if (on && a.temp > s.warn) this._raise(a, 'temp', 2, `温度高 ${a.temp.toFixed(0)}°C，报警值 ${s.warn}°C`);
    else if (a.temp < s.warn - 2) this._clear(a, 'temp');
    if (!a.fault && a.temp > s.trip) this._trip(a, `温度超限 ${a.temp.toFixed(0)}°C，自动停机`);

    if (a.health < 0.35) this._raise(a, 'health', 3, `健康度 ${(a.health * 100).toFixed(0)}%，建议安排维护`);
    else if (a.health > 0.4) this._clear(a, 'health');

    if (running && !a.fault) {
      const rate = (1 / 4200) * (1 + (1 - a.health) * 6) * a.speed * a.speed;
      if (Math.random() < rate * dt) this._trip(a, pick(s.faults));
    }
  }

  // ---------- alarms & log ----------

  _trip(a, text) {
    if (a.fault) return;
    a.fault = { text, at: this.clock };
    this._raise(a, 'fault', 1, text);
    this.events.push({ type: 'fault', asset: a.id });
  }

  _raise(a, code, prio, text) {
    if (this.alarms.some((x) => x.asset === a.id && x.code === code && x.active)) return;
    const al = { id: this.alarmSeq++, asset: a.id, code, prio, text, at: this.clock, active: true, acked: false };
    this.alarms.unshift(al);
    this._note(a, text, 'alarm', prio);
    if (this.alarms.length > 160) {
      const idx = this.alarms.findLastIndex((x) => !x.active && x.acked);
      if (idx >= 0) this.alarms.splice(idx, 1);
    }
  }

  _clear(a, code) {
    for (const al of this.alarms) {
      if (al.asset === a.id && al.code === code && al.active) {
        al.active = false;
        al.clearedAt = this.clock;
      }
    }
  }

  _note(a, text, kind = 'info', prio = 0) {
    this.log.unshift({ at: this.clock, asset: a.id, text, kind, prio });
    if (this.log.length > 300) this.log.pop();
  }

  activeAlarms() {
    return this.alarms.filter((x) => x.active || !x.acked);
  }

  ack(id) {
    const al = this.alarms.find((x) => x.id === id);
    if (al && !al.acked) {
      al.acked = true;
      this._note(this.assets.get(al.asset), `确认报警：${al.text}`, 'op');
    }
  }

  ackAll() {
    for (const al of this.alarms) al.acked = true;
  }

  // ---------- commands (the only write path from the UI) ----------

  command(id, cmd, arg) {
    const a = this.assets.get(id);
    if (!a) return { ok: false, msg: `找不到设备 ${id}` };
    switch (cmd) {
      case 'start':
        if (a.fault) return { ok: false, msg: `${id} 处于故障状态，先复位再启动` };
        a.enabled = true;
        this._note(a, '操作员启动', 'op');
        return { ok: true, msg: `${id} 已启动` };
      case 'stop':
        a.enabled = false;
        this._note(a, '操作员停止', 'op');
        return { ok: true, msg: `${id} 已停止` };
      case 'speed':
        a.speed = clamp(arg, 0.5, 1.5);
        this._note(a, `速度设定为 ${Math.round(a.speed * 100)}%`, 'op');
        return { ok: true, msg: `${id} 速度 ${Math.round(a.speed * 100)}%` };
      case 'reset':
        if (!a.fault) return { ok: false, msg: `${id} 没有需要复位的故障` };
        if (a.spec?.trip && a.temp > a.spec.trip - 4) {
          return { ok: false, msg: `${id} 温度 ${a.temp.toFixed(0)}°C 仍偏高，冷却到 ${a.spec.trip - 4}°C 以下再复位` };
        }
        if (a.type === 'agv' && a.battery < 10) a.battery = 40; // swapped battery
        a.fault = null;
        this._clear(a, 'fault');
        this._note(a, '故障复位', 'op');
        return { ok: true, msg: `${id} 已复位` };
      case 'maint':
        if (a.maint > 0) {
          a.maint = 0;
          this._note(a, '结束维护', 'op');
          return { ok: true, msg: `${id} 结束维护` };
        }
        a.maint = 25;
        if (a.fault) { a.fault = null; this._clear(a, 'fault'); }
        this._note(a, '进入维护', 'op');
        return { ok: true, msg: `${id} 进入维护，约 25 秒` };
      case 'fault':
        if (a.fault) return { ok: false, msg: `${id} 已处于故障状态` };
        this._trip(a, `${pick(a.spec.faults)}（模拟）`);
        return { ok: true, msg: `已向 ${id} 注入故障` };
      case 'restock':
        a.stock = Math.min(a.cap, a.stock + 400);
        this._note(a, '原料入库 400 件', 'op');
        return { ok: true, msg: '原料入库 400 件' };
      case 'ship':
        this._note(a, `成品发运 ${a.count} 箱`, 'op');
        a.count = 0;
        return { ok: true, msg: '成品已发运' };
      default:
        return { ok: false, msg: `未知指令 ${cmd}` };
    }
  }

  // ---------- derived ----------

  lineKpi(line) {
    const tp = line.doneTimes.length; // parts in the last 3600 s
    const ideal = 3600 / Math.max(...line.stations.map((s) => s.cycle));
    const perf = Math.min(1, tp / ideal);
    const avail = line.stations.reduce((s, a) => s + a.avail, 0) / line.stations.length;
    const quality = line.good / Math.max(1, line.good + line.scrap);
    return { tp, perf, avail, quality, oee: avail * perf * quality, good: line.good, scrap: line.scrap };
  }

  kpis() {
    const per = this.lines.map((l) => this.lineKpi(l));
    const avg = (k) => per.reduce((s, x) => s + x[k], 0) / per.length;
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
