// A plant that keeps other plants of the company stocked: a central kitchen and its shops. Finished
// work orders are batches in its store (with a shelf life and a retained sample each); at the shops'
// delivery hours its van loads what each shop is short of, oldest batches first, and the shop gets
// it when the van arrives. Its lines make to stock: a new order only when stock and work in
// progress fall below what the shops hold.
import type { Batch, Delivery, Job, Line } from './types';
import type { PlantSim } from './plant';

const rand = (a: number, b: number) => a + Math.random() * (b - a);
const HOUR = 3600_000;

export class Depot {
  readonly sim: PlantSim;
  outlets: PlantSim[] = [];
  batches: Batch[] = [];
  /** retained samples: batch, product, wall ms they may be thrown away */
  samples: [string, string, number][] = [];
  /** the last deliveries: on time and in full or not */
  otif: boolean[] = [];
  /** units thrown away past their shelf life, today and the business day that is */
  wasted = 0;
  wastedDay = -1;
  /** wall ms of the next departure to each outlet */
  next: Record<string, number> = {};
  private tick = 0;

  constructor(sim: PlantSim) {
    this.sim = sim;
  }

  /** Names of the sites it delivers to (work orders for them go to stock). */
  outletNames() {
    return this.outlets.map((o) => o.layout.name);
  }

  stock(product: string) {
    let n = 0;
    for (const b of this.batches) if (b.product === product) n += b.qty;
    return n;
  }

  /**
   * The stock the depot aims to keep of a product: half of what the outlets' shelves hold of it,
   * about a delivery round's worth (more would only age towards its shelf life).
   */
  par(product: string) {
    let n = 0;
    for (const o of this.outlets) {
      for (const [key, prods] of Object.entries(o.layout.supply!.items)) {
        if (!prods.includes(product)) continue;
        const it = o.inventory.find((i) => i.key === key);
        if (it) n += (it.cap / prods.length) * 0.5;
      }
    }
    return Math.round(n);
  }

  /** The store as a running depot has it: most of the target stock, made over the last shift. */
  seed() {
    const now = this.sim.wall();
    for (const line of this.sim.lines) {
      const pr = line.layout.product;
      if (this.batches.some((b) => b.product === pr.key)) continue;
      const qty = Math.round(this.par(pr.key) * 0.7);
      if (!qty) continue;
      const made = now - rand(4, 14) * HOUR;
      if (pr.sample) this.samples.push([`${line.layout.order.no.split('-')[0]}-${this.sim.ymd(made)}-${line.layout.no}00`, pr.key, made + pr.sample * HOUR]);
      this.batches.push({ no: `${line.layout.order.no.split('-')[0]}-${this.sim.ymd(made)}-${line.layout.no}00`, product: pr.key, qty, made, expires: pr.shelf ? made + pr.shelf * HOUR : made + 3650 * 24 * HOUR });
    }
    // oldest first: the order they go out in
    this.batches.sort((a, b) => a.made - b.made);
    this.count();
  }

  /** Whether the line should start an order for the outlets now, and how big. 0: nothing needed. */
  plan(line: Line, base: number) {
    const key = line.layout.product.key;
    if (!this.par(key)) return base;
    let wip = 0;
    for (const l of this.sim.lines) if (l.layout.product.key === key && l.job && l !== line) wip += Math.max(0, l.job.qty - l.job.good);
    const short = this.par(key) - this.stock(key) - wip;
    if (short < base * 0.25) return 0;
    return Math.max(10, Math.round(Math.min(short, base * 1.5) / 10) * 10);
  }

  /** A finished order for the outlets: a batch in the store, one unit kept back as its sample. */
  add(line: Line, j: Job) {
    const pr = line.layout.product;
    const now = this.sim.wall();
    let qty = j.good;
    if (pr.sample && qty > 1) {
      qty--;
      this.samples.push([j.no, pr.key, now + pr.sample * HOUR]);
    }
    this.batches.push({ no: j.no, product: pr.key, qty, made: now, expires: pr.shelf ? now + pr.shelf * HOUR : now + 3650 * 24 * HOUR });
    this.sim.note(this.sim.fg, `${pr.name}入库 ${qty} ${this.sim.layout.unit}（批号 ${j.no}${pr.shelf ? `，保质期 ${pr.shelf} 小时` : ''}${pr.sample ? '，已留样' : ''}）`, 'info');
    this.count();
  }

  private count() {
    this.sim.fg.count = this.batches.reduce((n, b) => n + b.qty, 0);
  }

  /** wall ms of the next time of day `h` after `after` */
  private nextAt(h: number, after: number) {
    const local = after + 8 * HOUR;
    const midnight = Math.floor(local / (24 * HOUR)) * 24 * HOUR;
    let t = midnight + h * HOUR - 8 * HOUR;
    while (t <= after) t += 24 * HOUR;
    return t;
  }

  step(dt: number) {
    this.tick += dt;
    if (this.tick < 1) return;
    this.tick -= 1;
    const s = this.sim;
    const now = s.wall();
    const day = Math.floor((now + 8 * HOUR - 5 * HOUR) / (24 * HOUR));
    if (day !== this.wastedDay) {
      this.wastedDay = day;
      this.wasted = 0;
    }
    // past their shelf life: thrown away, never sent
    for (let i = this.batches.length - 1; i >= 0; i--) {
      const b = this.batches[i];
      if (b.expires > now) continue;
      this.batches.splice(i, 1);
      this.wasted += b.qty;
      s.raise(s.fg, `expired-${b.no}`, 3, `批号 ${b.no} 过了保质期，报废 ${b.qty} ${s.layout.unit}`);
      this.count();
    }
    if (this.samples.length && this.samples[0][2] <= now) this.samples = this.samples.filter((x) => x[2] > now);
    for (const o of this.outlets) {
      const sup = o.layout.supply!;
      // planned from now when there is no plan, or the clock was moved far from it (a backfill, a test)
      const planned = this.next[o.layout.id];
      if (planned === undefined || planned - now > 25 * HOUR || now - planned > 25 * HOUR) this.next[o.layout.id] = Math.min(...sup.depart.map((h) => this.nextAt(h, now)));
      if (now < this.next[o.layout.id]) continue;
      this.next[o.layout.id] = Math.min(...sup.depart.map((h) => this.nextAt(h, now + 60_000)));
      this.dispatch(o);
    }
  }

  /** Loads what the outlet is short of (oldest batches first) and sends the van. */
  dispatch(o: PlantSim) {
    const s = this.sim;
    const sup = o.layout.supply!;
    const items: Record<string, number> = {};
    const by: Record<string, number> = {};
    let full = true;
    const lots = new Set<string>();
    for (const it of o.inventory) {
      if (it.kind !== 'consume' || it.lead) continue;
      let coming = 0;
      for (const d of o.incoming) coming += d.items[it.key] ?? 0;
      const need = Math.max(0, it.cap - it.avail - coming);
      if (need < it.cap * 0.05) continue;
      const prods = sup.items[it.key];
      // what the depot does not make comes on the same van from what the company buys in
      if (!prods) {
        items[it.key] = need;
        continue;
      }
      let got = 0;
      for (const b of this.batches) {
        if (got >= need) break;
        if (!prods.includes(b.product) || b.qty <= 0) continue;
        const k = Math.min(b.qty, need - got);
        b.qty -= k;
        got += k;
        by[b.product] = (by[b.product] ?? 0) + k;
        lots.add(b.no);
      }
      if (got) items[it.key] = got;
      if (got < need * 0.95) full = false;
    }
    this.batches = this.batches.filter((b) => b.qty > 0);
    this.count();
    if (!Object.keys(items).length) return;
    const now = s.wall();
    const f = s.layout.freight[0] ?? { carrier: '', late: 0.05 };
    const late = Math.random() < f.late;
    const planned = now + sup.transit * HOUR;
    const eta = planned + (late ? rand(15, 70) : rand(-5, 8)) * 60_000;
    const no = `PS-${s.ymd(now)}-${String(s.nextSeq()).padStart(4, '0')}`;
    const d: Delivery = { no, from: s.layout.id, to: o.layout.id, items, departed: now, eta, planned, full };
    this.otif.push(!late && full);
    if (this.otif.length > 30) this.otif.shift();
    o.receive(d);
    const label = (k: string) => o.inventory.find((i) => i.key === k);
    const what = Object.entries(items).map(([k, n]) => `${label(k)?.label ?? k} ${n} ${label(k)?.unit ?? ''}`).join('、');
    s.note(s.fg, `${s.layout.words.outTruck}发往${o.layout.name}：${what}${lots.size ? `（批号 ${[...lots].join('、')}）` : ''}${full ? '' : '，库存不够没配齐'}`, 'info');
    const truck = s.trucks.find((t) => t.kind === 'out');
    if (truck) truck.go = true;
    // the shipments as the history keeps them, one per product, on the line that makes it
    for (const [key, n] of Object.entries(by)) {
      const line = s.lines.find((l) => l.layout.product.key === key);
      if (!line) continue;
      const pr = line.layout.product;
      s.history?.shipment(line, {
        no: `${no}-${key}`, order: [...lots].join(','), line: line.id, customer: o.layout.name, part: pr.partNo ?? key, partName: pr.name,
        units: n, tonnes: Math.round(((n * (pr.weight ?? 0)) / 1000) * 1000) / 1000, trucks: 1, carrier: f.carrier,
        departed: now, eta: planned, delivered: null, promise: planned, revenue: Math.round(n * (pr.price ?? 0)), materialCost: 0,
      });
    }
  }

  figures() {
    const ok = this.otif.filter(Boolean).length;
    return { otif: this.otif.length ? ok / this.otif.length : 1, deliveries: this.otif.length, samples: this.samples.length, wasted: this.wasted, stock: this.batches.reduce((n, b) => n + b.qty, 0) };
  }
}
