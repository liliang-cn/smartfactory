// A walk-flow site's guests: who arrives when (the day's curve, the weekday, the weather), the
// queue at the door and who gives up on it, which table or pitch each group gets, delivery orders
// made for riders, and the business day's figures. The places themselves (tables, pitches) are
// assets of the plant; a place that an outside system reports on (a camera, the POS, a waiter's
// phone) follows the reports, and the simulation seats nobody there.
import type { DemandCfg } from '@/config/schema';
import type { Asset, Line, Product, TakeawayOrder, VenueDay, Waiting } from './types';
import type { PlantSim } from './plant';

const rand = (a: number, b: number) => a + Math.random() * (b - a);
/** How long a reported place stays governed by its reports after the last one. */
export const REPORT_TTL_MS = 3 * 3600_000;

/** The reported state of a place, while its reports are recent. */
export const reported = (a: Asset) => !!a.live && Date.now() - a.live.at < REPORT_TTL_MS;

export const emptyDay = (day: number): VenueDay => ({
  day, groups: 0, persons: 0, revenue: 0, seated: 0, waitSum: 0, balked: 0, reneged: 0, rated: 0, liked: 0,
  takeaway: 0, takeawayRevenue: 0, checkins: 0, checkouts: 0, sessions: 0, diningSum: 0, diningN: 0,
});

/** A session of a place as the server keeps it: opened (end null) or closed. */
export interface PlaceSession { plant: string; table: string; start: number; end: number | null; people: number; source: string }

/** Intensity of the curve at hour h (0..24), joining the points with straight lines and wrapping round midnight. */
export function intensity(curve: [number, number][], h: number) {
  const pts = curve;
  for (let i = 0; i < pts.length; i++) {
    const [h1, v1] = pts[i];
    const [h2, v2] = pts[(i + 1) % pts.length];
    const end = i + 1 < pts.length ? h2 : h2 + 24;
    const hh = h < h1 ? h + 24 : h;
    if (hh >= h1 && hh <= end) return end === h1 ? v1 : v1 + ((v2 - v1) * (hh - h1)) / (end - h1);
  }
  return pts[0][1];
}

export class Venue {
  readonly sim: PlantSim;
  readonly demand: DemandCfg;
  /** every place (table, pitch) of the plant */
  places: Asset[] = [];
  queue: Waiting[] = [];
  orders: TakeawayOrder[] = [];
  /** 元 of each bag waiting at the counter for a rider */
  counter: number[] = [];
  day: VenueDay;
  /** rain today (drawn each morning when the pack has weather) */
  rain = false;
  /** sessions opened and closed since the server last collected them */
  sessions: PlaceSession[] = [];
  private tick = 0;

  constructor(sim: PlantSim, demand: DemandCfg) {
    this.sim = sim;
    this.demand = demand;
    this.day = emptyDay(-1);
  }

  /** The hour the business day starts: opening time, or 05:00 for a site that never closes. */
  dayStart() {
    const o = this.sim.layout.calendar?.open;
    return o && o.from !== o.to % 24 ? o.from : 5;
  }

  /** The business day a wall-clock moment falls in. */
  dayOf(ms: number) {
    return Math.floor((ms + 8 * 3600_000 - this.dayStart() * 3600_000) / 86_400_000);
  }

  private weekday() {
    return new Date(this.sim.wall() + 8 * 3600_000).getUTCDay();
  }

  /** Arrivals per hour now: the curve, the weekday, the weather, for the places the simulation runs. */
  rateNow() {
    const d = this.demand;
    const h = this.sim.clock / 3600;
    const free = this.places.filter((a) => !reported(a)).length;
    return d.rate * free * intensity(d.curve, h) * (d.week?.[this.weekday()] ?? 1) * (this.rain ? d.weather?.factor ?? 1 : 1);
  }

  step(dt: number, closed: boolean) {
    const s = this.sim;
    const today = this.dayOf(s.wall());
    if (today !== this.day.day) {
      // a new business day: yesterday's figures are done, the weather is drawn
      this.day = emptyDay(today);
      const w = this.demand.weather;
      this.rain = !!w && Math.random() < w.rain;
      if (w) s.note(s.wh, `今日天气：${this.rain ? '有雨' : '晴'}`, 'info');
    }
    if (closed) {
      if (this.queue.length) {
        this.day.reneged += this.queue.length;
        s.note(s.wh, `打烊，门口还在等的 ${this.queue.length} 组客人请回`, 'info');
        this.queue.length = 0;
      }
    } else {
      // Poisson arrivals: at most one group a step at the step sizes the server runs
      if (Math.random() < (this.rateNow() / 3600) * dt) this.arrive();
      const t = this.demand.takeaway;
      if (t && Math.random() < ((t.rate * intensity(this.demand.curve, s.clock / 3600) * (this.demand.week?.[this.weekday()] ?? 1)) / 3600) * dt) {
        this.orders.push({ at: s.elapsed, ready: 0, spend: Math.round(rand(t.spend[0], t.spend[1])) });
      }
    }
    this.tick += dt;
    if (this.tick < 1) return;
    this.tick -= 1;
    // once a second: who gives up waiting, delivery orders, places being cleared
    for (let i = this.queue.length - 1; i >= 0; i--) {
      if (this.queue[i].until <= s.elapsed) {
        this.queue.splice(i, 1);
        this.day.reneged++;
      }
    }
    this.stepOrders();
    for (const a of this.places) {
      if (reported(a)) continue;
      if (a.state === 'cleaning' && (a.since ?? 0) + (a.clear ?? 180) <= s.elapsed) {
        a.state = 'free';
        a.since = s.elapsed;
      }
    }
  }

  private arrive() {
    const d = this.demand;
    const s = this.sim;
    let r = Math.random() * d.sizes.reduce((a, b) => a + b, 0);
    let n = 1;
    for (let i = 0; i < d.sizes.length; i++) if ((r -= d.sizes[i]) <= 0) { n = i + 1; break; }
    const q = this.queue.length;
    // a long queue sends newcomers away: some at `balk` groups waiting, all at twice that
    if (q >= d.balk && Math.random() < Math.min(1, (q - d.balk + 1) / (d.balk + 1))) {
      this.day.balked++;
      return;
    }
    this.queue.push({ n, at: s.elapsed, until: s.elapsed + rand(d.patience[0], d.patience[1]) * 60 });
  }

  private stepOrders() {
    const t = this.demand.takeaway;
    if (!t) return;
    const s = this.sim;
    for (let i = 0; i < this.orders.length; i++) {
      const o = this.orders[i];
      if (o.ready === 0) {
        // made as soon as the kitchen has what it needs
        if (s.shortOf(t.take)) continue;
        s.useInv(t.take);
        o.ready = s.elapsed + t.prep * 60 * rand(0.75, 1.35);
      } else if (o.ready <= s.elapsed) {
        this.counter.push(o.spend);
        s.fg.count = this.counter.length;
        this.orders.splice(i--, 1);
      }
    }
  }

  /** A rider takes up to `max` bags from the counter. */
  pickUp(max: number) {
    const bags = this.counter.splice(0, max);
    this.sim.fg.count = this.counter.length;
    this.day.takeaway += bags.length;
    for (const b of bags) this.day.takeawayRevenue += b;
    return bags.length;
  }

  /** Places of a station, in slot order. */
  placesOf(station: Asset) {
    return this.places.filter((p) => p.place!.station === station.id);
  }

  /**
   * The next group to let in on this line and the place it gets: the first in the queue a free
   * place fits (the smallest that seats them; a big group may take the biggest left), or none.
   */
  admit(line: Line): { g: Waiting; place: Asset } | null {
    if (!this.queue.length || line.gate === undefined) return null;
    const free = this.places.filter((p) => p.place!.line === line.id && p.state === 'free' && !reported(p));
    if (!free.length) return null;
    const biggest = Math.max(...free.map((p) => p.place!.seats));
    for (let i = 0; i < this.queue.length; i++) {
      const g = this.queue[i];
      let best: Asset | null = null;
      for (const p of free) if (p.place!.seats >= g.n && (!best || p.place!.seats < best.place!.seats)) best = p;
      // a group bigger than every place joins tables at the biggest one, once nobody smaller is ahead
      if (!best && g.n > biggest && i === 0) best = free.find((p) => p.place!.seats === biggest)!;
      if (!best) continue;
      this.queue.splice(i, 1);
      return { g, place: best };
    }
    return null;
  }

  /** A group got its place (seated at a table, checked in at a pitch). */
  seat(p: Product, place: Asset) {
    const s = this.sim;
    place.state = 'occupied';
    place.count = p.n ?? 1;
    place.since = s.elapsed;
    place.busy = p;
    this.day.seated++;
    this.day.sessions++;
    this.day.waitSum += p.w ?? 0;
    if (place.place!.overnight) this.day.checkins++;
    this.sessions.push({ plant: s.layout.id, table: place.id, start: Math.round(s.wall()), end: null, people: place.count, source: 'sim' });
  }

  /** A group left its place: the place is cleared before the next one gets it. */
  vacate(place: Asset) {
    const s = this.sim;
    const start = place.since ?? s.elapsed;
    this.day.diningSum += s.elapsed - start;
    this.day.diningN++;
    if (place.place!.overnight) this.day.checkouts++;
    this.sessions.push({ plant: s.layout.id, table: place.id, start: Math.round(s.epoch + start * 1000), end: Math.round(s.wall()), people: place.count, source: 'sim' });
    place.busy = null;
    place.count = 0;
    if (reported(place)) return;
    place.state = 'cleaning';
    place.since = s.elapsed;
    // seconds the place takes to clear: a table a few minutes, a pitch longer
    place.clear = place.place!.overnight ? rand(1200, 2400) : rand(120, 300);
  }

  /** A group left the site through the end of the line. */
  left(p: Product) {
    this.day.groups++;
    this.day.persons += p.n ?? 1;
    this.day.revenue += (p.spend ?? 0) + this.demand.fee * (p.n ?? 1);
  }

  /** A score given at the checkout. */
  rated(ok: boolean) {
    this.day.rated++;
    if (ok) this.day.liked++;
  }

  /**
   * A state an outside system reported for a place. Opening and closing sessions follow its
   * changes, so today's figures count reported and simulated places alike.
   */
  report(a: Asset, state: string, people: number, sinceS: number) {
    const was = a.state;
    const occupied = state === 'occupied';
    if (was === 'occupied' && !occupied) {
      const start = a.since ?? sinceS;
      this.day.diningSum += Math.max(0, sinceS - start);
      this.day.diningN++;
    }
    if (occupied && was !== 'occupied') this.day.sessions++;
    a.state = state as Asset['state'];
    a.count = occupied ? people : 0;
    a.since = sinceS;
  }

  /** Figures of the business day and of now, for the KPI cards. */
  figures() {
    const places = this.places;
    const occupied = places.filter((p) => p.state === 'occupied');
    const seats = places.reduce((n, p) => n + p.place!.seats, 0);
    const people = occupied.reduce((n, p) => n + p.count, 0);
    const d = this.day;
    const s = this.sim;
    const waiting = this.queue.reduce((n, g) => n + g.n, 0);
    const oldest = this.queue.length ? (s.elapsed - Math.min(...this.queue.map((g) => g.at))) / 60 : 0;
    const nReported = places.filter(reported).length;
    return {
      places: places.length, occupied: occupied.length, seats, people,
      occupancy: places.length ? occupied.length / places.length : 0,
      seatUse: seats ? people / seats : 0,
      turnover: places.length ? d.sessions / places.length : 0,
      dining: d.diningN ? d.diningSum / d.diningN / 60 : 0,
      queue: this.queue.length, waiting, oldest,
      wait: d.seated ? d.waitSum / d.seated / 60 : 0,
      ticket: d.persons ? d.revenue / d.persons : 0,
      rating: d.rated ? d.liked / d.rated : 1,
      reported: places.length ? nReported / places.length : 0,
      rain: this.rain,
      day: d,
    };
  }
}
