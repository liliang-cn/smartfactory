// The simulation as the Go server runs it (embedded JS engine): every plant of every company,
// in real time, with no browser involved. Browsers follow it through frames (exportState),
// MCP tools read snapshots of it, and protocol adapters feed it measured values.
// Built by `npm run build:engine` into server/internal/engine/js/engine.js.
import { z } from 'zod';
import { ConfigError, layoutCompany, loadAll, validateCompany, validateIndustry, type CompanyBundle } from '@/config/loader';
import { Company as CompanySchema, Industry as IndustrySchema, type CompanyCfg, type IndustryCfg } from '@/config/schema';
import { History } from '@/sim/history';
import { isMachineSpec, liveFields, PlantSim, type SavedPlant } from '@/sim/plant';
import type { Calibration, Command, ExtOrder, OrderEvent } from '@/sim/types';
import type { PlantLayout } from '@/core/layout';

declare const host: { log(msg: string): void };

const companyRaw = import.meta.glob('../config/companies/*.json', { eager: true, import: 'default' });
const industryRaw = import.meta.glob('../config/industries/*.json', { eager: true, import: 'default' });

interface Company { bundle: CompanyBundle; plants: PlantSim[]; byId: Record<string, PlantSim>; timeScale: number }
const companies = new Map<string, Company>();

/** Events nobody collected (no browser watching) are dropped beyond this. */
const MAX_EVENTS = 200;

/** Every industry pack known: bundled ones and those created at run time. */
const industries = new Map<string, IndustryCfg>();
const builtinIndustries = new Set<string>();
const builtinCompanies = new Set<string>();

function register(bundle: CompanyBundle) {
  const plants = bundle.plants.map((l) => new PlantSim(l));
  attachFeed(bundle.company.id, plants);
  companies.set(bundle.company.id, { bundle, plants, byId: Object.fromEntries(plants.map((p) => [p.layout.id, p])), timeScale: 1 });
  applyCalibration(bundle.company.id);
  pending.delete(bundle.company.id);
  if (recording.has(bundle.company.id)) attach(bundle.company.id, Date.now());
}

function init(): string {
  for (const [path, raw] of Object.entries(industryRaw)) {
    try {
      const ind = validateIndustry(raw, path);
      industries.set(ind.id, ind);
      builtinIndustries.add(ind.id);
    } catch (e) {
      host.log((e as Error).message);
    }
  }
  for (const bundle of loadAll(companyRaw, industryRaw, (m) => host.log(m))) {
    register(bundle);
    builtinCompanies.add(bundle.company.id);
    builtinBundles.set(bundle.company.id, bundle);
  }
  return JSON.stringify([...companies.keys()]);
}

// ---------- templates ----------
//
// A template is a factory to start from: a company config and the industry packs it uses that are
// not built in. The built-in companies are the built-in templates (an instance that runs none of
// them still offers them).

const builtinBundles = new Map<string, CompanyBundle>();

const summarize = (plants: PlantLayout[]) => ({
  plants: plants.map((p) => ({ id: p.id, name: p.name, site: p.site, lines: p.lines.map((l) => l.name), machines: p.lines.reduce((n, l) => n + l.stations.length, 0) })),
});

/** The built-in templates: each built-in company's config, its industry and what it lays out to. */
function builtinTemplates(): string {
  return JSON.stringify([...builtinBundles.values()].map((b) => ({
    id: b.company.id, name: b.company.name, industry: b.company.industry,
    industryName: industries.get(b.company.industry)?.name ?? b.company.industry, ...summarize(b.plants),
  })));
}

function builtinTemplate(id: string): string {
  const b = builtinBundles.get(id);
  return b ? JSON.stringify(b.company) : 'null';
}

/**
 * Checks a template without installing anything: its industry packs validate, and its company
 * validates and lays out with them (and the packs already here).
 */
function checkTemplate(json: string): string {
  return result(() => {
    const t = JSON.parse(json) as { company?: unknown; industries?: Record<string, unknown> };
    const packs = new Map(industries);
    for (const [id, raw] of Object.entries(t.industries ?? {})) {
      const ind = validateIndustry(raw, `行业包 ${id}`);
      if (builtinIndustries.has(ind.id)) continue;
      packs.set(ind.id, ind);
    }
    const cfg = validateCompany(t.company, '模板的公司配置');
    const plants = layoutCompany(cfg, packs, '模板的公司配置');
    return { id: cfg.id, name: cfg.name, industry: cfg.industry, industryName: packs.get(cfg.industry)?.name ?? cfg.industry, ...summarize(plants) };
  });
}

/** Runs only the listed companies (a customer's own instance); the rest stop. */
function keep(json: string): string {
  const ids = new Set(JSON.parse(json) as string[]);
  for (const id of [...companies.keys()]) if (!ids.has(id)) companies.delete(id);
  return JSON.stringify([...companies.keys()]);
}

// ---------- configs created at run time (MCP) ----------

/** Runs fn and reports {ok: true, ...} or {ok: false, errors} with readable (Chinese) issues. */
function result(fn: () => object): string {
  try {
    return JSON.stringify({ ok: true, ...fn() });
  } catch (e) {
    if (e instanceof ConfigError) return JSON.stringify({ ok: false, errors: e.issues, where: e.file });
    return JSON.stringify({ ok: false, errors: [(e as Error).message ?? String(e)] });
  }
}

const runtimeCompanies = new Map<string, CompanyCfg>();

function addIndustry(json: string): string {
  return result(() => {
    const raw = JSON.parse(json) as { id?: string };
    const ind = validateIndustry(raw, `行业包 ${raw?.id ?? ''}`);
    if (builtinIndustries.has(ind.id)) throw new ConfigError(`行业包 ${ind.id}`, [`${ind.id} 是内置行业包，换一个 id`]);
    industries.set(ind.id, ind);
    // companies built on the previous version of this pack are rebuilt (their state starts over)
    const rebuilt: string[] = [];
    for (const [id, cfg] of runtimeCompanies) {
      if (cfg.plants.some((p) => (p.industry ?? cfg.industry) === ind.id)) {
        register({ company: cfg, plants: layoutCompany(cfg, industries, `公司 ${id}`), directory: [] });
        rebuilt.push(id);
      }
    }
    return {
      id: ind.id, name: ind.name, flow: ind.flow,
      products: Object.keys(ind.products), machines: Object.keys(ind.machines), routes: Object.keys(ind.routes), rebuilt,
    };
  });
}

function addCompany(json: string): string {
  return result(() => {
    const raw = JSON.parse(json) as { id?: string };
    const cfg = validateCompany(raw, `公司 ${raw?.id ?? ''}`);
    if (builtinCompanies.has(cfg.id)) throw new ConfigError(`公司 ${cfg.id}`, [`${cfg.id} 是内置公司，换一个 id`]);
    const plants = layoutCompany(cfg, industries, `公司 ${cfg.id}`);
    register({ company: cfg, plants, directory: [] });
    runtimeCompanies.set(cfg.id, cfg);
    return {
      id: cfg.id, name: cfg.name,
      plants: plants.map((p) => ({ id: p.id, name: p.name, site: p.site, lines: p.lines.map((l) => l.name), machines: p.lines.reduce((n, l) => n + l.stations.length, 0) })),
    };
  });
}

/** Validates an industry pack without loading it: what addIndustry would say. */
function checkIndustry(json: string): string {
  return result(() => {
    const raw = JSON.parse(json) as { id?: string };
    const ind = validateIndustry(raw, `行业包 ${raw?.id ?? ''}`);
    if (builtinIndustries.has(ind.id)) throw new ConfigError(`行业包 ${ind.id}`, [`${ind.id} 是内置行业包，换一个 id`]);
    return {
      id: ind.id, name: ind.name, flow: ind.flow, unit: ind.unit,
      products: Object.fromEntries(Object.entries(ind.products).map(([k, p]) => [k, { name: p.name, stages: Object.keys(p.stages), partNo: p.partNo, variants: (p.variants ?? []).map((v) => v.partNo) }])),
      machines: Object.fromEntries(Object.entries(ind.machines).map(([k, m]) => [k, `${m.label} (${m.look.archetype}, ${m.cycle} 秒)`])),
      routes: Object.fromEntries(Object.entries(ind.routes).map(([k, r]) => [k, r.map((st) => (typeof st === 'string' ? st : st.machine))])),
    };
  });
}

/** Validates and lays out a company without running it: what addCompany would say. */
function checkCompany(json: string): string {
  return result(() => {
    const raw = JSON.parse(json) as { id?: string };
    const cfg = validateCompany(raw, `公司 ${raw?.id ?? ''}`);
    if (builtinCompanies.has(cfg.id)) throw new ConfigError(`公司 ${cfg.id}`, [`${cfg.id} 是内置公司，换一个 id`]);
    const plants = layoutCompany(cfg, industries, `公司 ${cfg.id}`);
    return {
      id: cfg.id, name: cfg.name,
      plants: plants.map((p) => ({ id: p.id, name: p.name, site: p.site, lines: p.lines.map((l) => l.name), machines: p.lines.reduce((n, l) => n + l.stations.length, 0) })),
    };
  });
}

/** A running company's config as it was given (built in or created). */
function companyConfig(id: string): string {
  const c = companies.get(id);
  return c ? JSON.stringify(c.bundle.company) : 'null';
}

/**
 * A company's process as the knowledge base keeps it: plants, their lines in order, each line's
 * product and stations, and who supplies and buys each product.
 */
function processModel(id: string): string {
  const c = companies.get(id);
  if (!c) return 'null';
  const cfg = c.bundle.company;
  return JSON.stringify({
    id: cfg.id, name: cfg.name,
    plants: c.plants.map(({ layout: p }) => ({
      id: p.id, name: p.name, hall: p.hall, site: p.site, industry: p.industry.name, unit: p.unit,
      warehouse: p.warehouse.name, finishedGoods: p.fg.name,
      carriers: p.freight.map((f) => f.carrier),
      lines: p.lines.map((l) => ({
        id: l.id, name: l.name, note: l.note,
        product: {
          key: l.product.key, name: l.product.name, stages: l.product.order, partNo: l.product.partNo, material: l.product.material,
          weight: l.product.weight, customers: (l.product.customers ?? []).map((x) => x.name), suppliers: (l.product.suppliers ?? []).map((x) => x.name),
          variants: (l.product.variants ?? []).map((v) => v.partNo),
        },
        stations: l.stations.map((st) => ({ id: st.id, label: st.machine.label, name: st.name, code: st.machine.code, cycle: st.cycle, makes: st.makes ?? '', kind: st.machine.look.archetype })),
      })),
    })),
  });
}

function removeCompany(id: string): string {
  return result(() => {
    if (builtinCompanies.has(id)) throw new ConfigError(`公司 ${id}`, [`${id} 是内置公司，不能删除`]);
    if (!companies.delete(id)) throw new ConfigError(`公司 ${id}`, ['没有这个公司']);
    runtimeCompanies.delete(id);
    pending.delete(id);
    return { id };
  });
}

function industryList(): string {
  return JSON.stringify([...industries.values()].map((i) => ({
    id: i.id, name: i.name, flow: i.flow, unit: i.unit, builtin: builtinIndustries.has(i.id),
    products: Object.keys(i.products), machines: Object.fromEntries(Object.entries(i.machines).map(([k, m]) => [k, `${m.label} (${m.look.archetype})`])),
  })));
}

function industry(id: string): string {
  const i = industries.get(id);
  return i ? JSON.stringify(i) : 'null';
}

function schema(kind: string): string {
  return JSON.stringify(z.toJSONSchema(kind === 'company' ? CompanySchema : IndustrySchema, { io: 'input', unrepresentable: 'any' }));
}

// ---------- ERP orders ----------
//
// A company whose ERP sends production orders: the server keeps them, routes each to a line here,
// and every couple of seconds hands over each line's queue and collects what started and finished.

interface Feed { mode: 'simulate' | 'idle'; queue: Map<string, ExtOrder[]>; events: OrderEvent[] }
const feeds = new Map<string, Feed>();

function attachFeed(id: string, plants: PlantSim[]) {
  for (const p of plants) {
    p.orders = {
      take: (line) => {
        const f = feeds.get(id);
        if (!f) return undefined;
        const o = f.queue.get(`${p.layout.id}/${line.id}`)?.shift();
        return o ?? (f.mode === 'idle' ? null : undefined);
      },
      event: (e) => feeds.get(id)?.events.push(e),
    };
  }
}

/**
 * The line an order goes to: the one it names, else among those that make its part number (or
 * product), the one with the least work ahead of it.
 */
function routeOrder(c: string, json: string): string {
  return result(() => {
    const co = get(c);
    if (!co) throw new Error(`没有公司 ${c}`);
    // ahead: units the server has queued for each line ("plant/line"), when it knows better than the feed
    const o = JSON.parse(json) as { plant?: string; line?: string; partNo?: string; product?: string; ahead?: Record<string, number> };
    let cands = co.plants.flatMap((p) => p.lines.map((l) => ({ p, l })));
    if (o.plant) {
      cands = cands.filter(({ p }) => p.layout.id === o.plant || p.layout.name === o.plant);
      if (!cands.length) throw new Error(`没有工厂「${o.plant}」`);
    }
    if (o.line) {
      cands = cands.filter(({ p, l }) => l.id === o.line || `${p.layout.id}/${l.id}` === o.line || l.name === o.line);
      if (!cands.length) throw new Error(`没有产线「${o.line}」`);
    } else if (o.partNo || o.product) {
      cands = cands.filter(({ p, l }) => {
        const pr = l.layout.product;
        return o.partNo ? pr.partNo === o.partNo || p.variants(l).some((v) => v.partNo === o.partNo) : pr.key === o.product || pr.name === o.product;
      });
      if (!cands.length) throw new Error(`没有产线做${o.partNo ? `料号 ${o.partNo}` : `「${o.product}」`}`);
    } else {
      throw new Error('工单要写明做什么（partNo 或 product）或在哪条线做（line）');
    }
    const f = feeds.get(c);
    const ahead = ({ p, l }: (typeof cands)[number]) => {
      const k = `${p.layout.id}/${l.id}`;
      const queued = o.ahead ? (o.ahead[k] ?? 0) : (f?.queue.get(k) ?? []).reduce((n, x) => n + x.qty, 0);
      const units = queued + (l.job ? Math.max(0, l.job.qty - l.job.good) : 0);
      return units * Math.max(...l.stations.map((a) => a.cycle));
    };
    const best = cands.reduce((a, b) => (ahead(b) < ahead(a) ? b : a));
    return { plant: best.p.layout.id, plantName: best.p.layout.name, line: best.l.id, lineName: best.l.name };
  });
}

/**
 * Hands over the ERP's queue ({mode, queue: {"plant/line": orders}, active: order numbers not
 * finished or withdrawn}; empty to leave it) and returns what started and finished since the last
 * call and how far the running orders are. Lines drop orders no longer active, and an order
 * the simulation made up gives way to the ERP's.
 */
function syncOrders(c: string, json: string): string {
  const co = get(c);
  if (!co) return 'null';
  let f = feeds.get(c);
  if (!f) feeds.set(c, (f = { mode: 'simulate', queue: new Map(), events: [] }));
  const events = f.events.splice(0);
  if (json) {
    const s = JSON.parse(json) as { mode: Feed['mode']; queue: Record<string, ExtOrder[]>; active: string[] };
    const busy = new Set([...events.map((e) => e.no), ...co.plants.flatMap((p) => p.lines.flatMap((l) => (l.job?.ext ? [l.job.no] : [])))]);
    f.mode = s.mode;
    f.queue = new Map(Object.entries(s.queue).map(([k, os]) => [k, os.filter((o) => !busy.has(o.no))]));
    const active = new Set(s.active);
    for (const p of co.plants) {
      for (const l of p.lines) {
        if (l.job?.ext && !active.has(l.job.no)) p.dropOrder(l.job.no);
        else if (l.job && !l.job.ext && (f.mode === 'idle' || f.queue.get(`${p.layout.id}/${l.id}`)?.length)) {
          l.last = l.job;
          l.job = undefined;
        }
      }
    }
  }
  const running = co.plants.flatMap((p) => p.lines.flatMap((l) => (l.job?.ext ? [{ no: l.job.no, plant: p.layout.id, line: l.id, good: l.job.good, scrap: l.job.scrap, plantName: p.layout.name, lineName: l.name }] : [])));
  return JSON.stringify({ events, running });
}

/** Stops taking ERP orders: lines make up their own again. */
function stopOrders(c: string) {
  feeds.delete(c);
}

// ---------- calibration (parameters fitted from measurements) ----------

/** company → "plant/asset" → the parameters that replace the machine's template's */
const calibrations = new Map<string, Record<string, Calibration>>();

/** Puts the company's calibration on its machines (and the template back on the rest); says which keys name no machine. */
function applyCalibration(c: string): string[] {
  const co = get(c);
  if (!co) return [];
  const want = calibrations.get(c) ?? {};
  for (const p of co.plants) {
    for (const a of p.assets.values()) {
      const cal = want[`${p.layout.id}/${a.id}`];
      if (cal) p.calibrate(a.id, cal);
      else if (a.calib) p.calibrate(a.id, {});
    }
  }
  return Object.keys(want).filter((k) => {
    const [plant, asset] = k.split('/');
    return !co.byId[plant]?.assets.has(asset);
  });
}

/** Sets a company's calibration ({"plant/asset": parameters}); it stays with the company when it is rebuilt or saved. */
function calibrate(c: string, json: string): string {
  return result(() => {
    if (!get(c)) throw new Error(`没有公司 ${c}`);
    calibrations.set(c, JSON.parse(json) as Record<string, Calibration>);
    return { missing: applyCalibration(c) };
  });
}

/** What each reporting machine's model got wrong since the last call, per field. */
function residuals(c: string): string {
  const co = get(c);
  if (!co) return 'null';
  return JSON.stringify(co.plants.flatMap((p) => p.takeResiduals().map((r) => ({ plant: p.layout.id, ...r }))));
}

/** Every machine's parameters: the template's, the calibration in force, and whether it is reporting now. */
function machineParams(c: string): string {
  const co = get(c);
  if (!co) return 'null';
  return JSON.stringify(co.plants.flatMap((p) => p.lines.flatMap((l) => l.stations.flatMap((a) => {
    const s = a.spec;
    if (!isMachineSpec(s)) return [];
    const template: Calibration = a.template ?? {
      idleRise: s.idleRise, runRise: s.runRise, tau: s.tau ?? 40, pIdle: s.pIdle, pRun: s.pRun, vib: s.vib, cycle: a.cycle,
      mtbf: a.machine?.upkeep.mtbf ?? 3.33, repair: a.machine?.upkeep.repair ?? [1, 3],
    };
    return [{ plant: p.layout.id, plantName: p.layout.name, line: l.name, asset: a.id, name: a.name, label: s.label, template, calib: a.calib ?? null, live: !!liveFields(a) }];
  }))));
}

/** Sim time not yet stepped, for companies advanced in coarse steps. */
const pending = new Map<string, number>();

/**
 * Advances every company by dt seconds of wall time. Companies someone is watching step in
 * 0.1 s increments (smooth for the browsers following them); the rest in 0.5 s increments,
 * which keeps the same throughput and KPIs at a fifth of the cost.
 */
function step(dt: number, watched = '') {
  const hot = new Set(watched.split(',').filter(Boolean));
  for (const [id, c] of companies) {
    const h = hot.has(id) ? 0.1 : 0.5;
    let left = (pending.get(id) ?? 0) + dt * c.timeScale;
    while (left >= h - 1e-9) {
      for (const p of c.plants) p.step(h);
      left -= h;
    }
    pending.set(id, left);
    for (const p of c.plants) if (p.events.length > MAX_EVENTS) p.events.splice(0, p.events.length - MAX_EVENTS);
  }
}

const get = (c: string) => companies.get(c);

function frame(c: string): string {
  const co = get(c);
  if (!co) return 'null';
  return JSON.stringify({ company: c, timeScale: co.timeScale, plants: Object.fromEntries(co.plants.map((p) => [p.layout.id, p.exportState()])) });
}

/** One plant's replicated state (drains its events), or "null". */
function plantFrame(c: string, plant: string): string {
  const p = get(c)?.byId[plant];
  return p ? JSON.stringify(p.exportState()) : 'null';
}

function timeScale(c: string): number {
  return get(c)?.timeScale ?? 1;
}

function snapshot(c: string): string {
  const co = get(c);
  if (!co) return 'null';
  return JSON.stringify({ company: { id: c, name: co.bundle.company.name }, timeScale: co.timeScale, plants: co.plants.map((p) => p.snapshot()) });
}

/**
 * One asset as the command guard judges a command for it: its state, temperature and limits,
 * speed, active alarms, and which source its readings come from ("" when simulated). "null"
 * when there is no such asset.
 */
function assetState(c: string, plant: string, asset: string): string {
  const p = get(c)?.byId[plant];
  const a = p?.assets.get(asset);
  if (!p || !a) return 'null';
  const r1 = (v: number) => Math.round(v * 10) / 10;
  return JSON.stringify({
    id: a.id, name: a.name, type: a.type, state: a.fault ? 'fault' : a.state, fault: a.fault?.text ?? '',
    temp: r1(a.temp), warn: a.spec?.warn ?? 0, trip: a.spec?.trip ?? 0, speed: r1(a.speed), health: r1(a.health),
    maint: a.maint > 0, changing: (a.changing ?? 0) > 0,
    alarms: p.alarms.filter((x) => x.active && x.asset === a.id).map((x) => x.text),
    live: a.live?.src ?? '',
  });
}

function command(c: string, plant: string, asset: string, cmd: Command, arg: number | null): string {
  const p = get(c)?.byId[plant];
  if (!p) return JSON.stringify({ ok: false, msg: `找不到 ${c}/${plant}` });
  return JSON.stringify(p.command(asset, cmd, arg ?? undefined));
}

function ingest(c: string, plant: string, asset: string, field: string, value: number | string | boolean, src: string): boolean {
  return get(c)?.byId[plant]?.ingest(asset, field, value, src) ?? false;
}

function hist(c: string, plant: string, asset: string): string {
  const a = get(c)?.byId[plant]?.assets.get(asset);
  return a ? JSON.stringify(a.hist) : 'null';
}

function setTimeScale(c: string, scale: number): boolean {
  const co = get(c);
  if (!co) return false;
  co.timeScale = Math.max(0, Math.min(20, scale));
  return true;
}

const SAVE_VERSION = 1;

/** The company's full state for persistence. */
function save(c: string): string {
  const co = get(c);
  if (!co) return 'null';
  return JSON.stringify({ v: SAVE_VERSION, company: c, timeScale: co.timeScale, calib: calibrations.get(c), plants: Object.fromEntries(co.plants.map((p) => [p.layout.id, p.saveState()])) });
}

/** Carries a company on from a saved state; plants whose structure changed start fresh. */
function restore(c: string, json: string): string {
  const co = get(c);
  const restored: string[] = [];
  const skipped: string[] = [];
  if (!co) return JSON.stringify({ restored, skipped: ['(unknown company)'] });
  let d: { v: number; timeScale?: number; calib?: Record<string, Calibration>; plants: Record<string, SavedPlant> };
  try {
    d = JSON.parse(json);
  } catch (e) {
    return JSON.stringify({ restored, skipped: [`(unreadable: ${(e as Error).message})`] });
  }
  if (d.v !== SAVE_VERSION) return JSON.stringify({ restored, skipped: [`(save version ${d.v})`] });
  co.timeScale = d.timeScale ?? 1;
  for (const p of co.plants) {
    const saved = d.plants[p.layout.id];
    if (saved && p.restoreState(saved)) restored.push(p.layout.id);
    else skipped.push(p.layout.id);
  }
  // a copy of the plant (a what-if) runs on the same fitted parameters
  if (d.calib) calibrations.set(c, d.calib);
  applyCalibration(c);
  return JSON.stringify({ restored, skipped });
}

/** A plant's machines in protocol order, with what is needed to map fault codes and readings. */
function machines(c: string, plant: string): string {
  const p = get(c)?.byId[plant];
  if (!p) return 'null';
  return JSON.stringify(p.machines().map((a) => ({
    id: a.id, name: a.name, faults: a.spec?.faults ?? [], measured: !!a.measure, tooling: !!a.tool,
    // what a plausible reading of it is: trip temperature, running power, vibration, the measure's spec
    trip: a.spec?.trip, power: a.spec?.pRun, vib: a.spec?.vib,
    measure: a.measure ? { target: a.measure.target, lo: a.measure.lo, hi: a.measure.hi, sigma: a.measure.sigma } : undefined,
  })));
}

/** A plant's vehicles and inventory items, in protocol order. */
function fleet(c: string, plant: string): string {
  const p = get(c)?.byId[plant];
  if (!p) return 'null';
  return JSON.stringify({
    vehicles: p.vehicles().map((a) => ({ id: a.id, name: a.name })),
    inventory: p.inventory.map((i) => ({ key: i.key, label: i.label, unit: i.unit })),
    warehouse: p.wh.id,
  });
}

/** Equipment mode (device emulator): every plant queues its measured units for publishing. */
function playEquipment() {
  for (const c of companies.values()) for (const p of c.plants) p.playEquipment();
}

/** What a plant's equipment reports right now, plus units measured since the last call. */
function telemetry(c: string, plant: string): string {
  const p = get(c)?.byId[plant];
  return p ? JSON.stringify(p.telemetry()) : 'null';
}

// ---------- history (the advisor's database) ----------

/** companies whose plants keep an hour-by-hour record */
const recording = new Set<string>();

function attach(c: string, now: number) {
  const co = get(c);
  if (!co) return;
  co.plants.forEach((p, i) => { p.history = new History(p, p.layout.id, i + 1, now - p.elapsed * 1000); });
}

/** Starts recording a company; `now` is the wall-clock ms the plants' current moment maps to. */
function historyOn(c: string, now: number): boolean {
  if (!get(c)) return false;
  recording.add(c);
  attach(c, now);
  return true;
}

/** Closed hours and finished events of every plant since the last call. */
function history(c: string, flush = false): string {
  const co = get(c);
  if (!co) return 'null';
  return JSON.stringify(Object.fromEntries(co.plants.map((p) => [p.layout.id, p.history?.drain(flush) ?? History.empty()])));
}

/** What the history refers to: plants, lines, machines and parts. */
function historyDims(c: string): string {
  const co = get(c);
  if (!co) return 'null';
  const parts = new Map<string, object>();
  const customers = new Map<string, object>();
  const plants = co.plants.map((p) => {
    const L = p.layout;
    for (const [li, l] of L.lines.entries()) {
      const pr = l.product;
      for (const cu of pr.customers ?? []) customers.set(cu.name, { name: cu.name, transit: cu.transit ?? 24 });
      parts.set(pr.key, { id: pr.key, name: pr.name, family: pr.name, partNo: pr.partNo ?? '', material: pr.material ?? '', weight: pr.weight ?? null, input: pr.input ?? null, customer: pr.customer ?? '' });
      // each part number the family runs, as work orders and lots refer to it
      for (const v of p.variants(p.lines[li])) {
        parts.set(`${pr.key}:${v.partNo}`, {
          id: `${pr.key}:${v.partNo}`, name: v.name, family: pr.name, partNo: v.partNo, material: pr.material ?? '',
          weight: v.weight ?? pr.weight ?? null, input: v.input ?? pr.input ?? null, customer: pr.customer ?? '', price: v.price ?? pr.price ?? null,
        });
      }
    }
    return {
      id: L.id, name: L.name, hall: L.hall,
      lines: p.lines.map((l) => ({
        id: `${L.id}-${l.id}`, name: l.name, process: l.layout.note, product: l.layout.product.key,
        idealCycle: Math.max(...l.stations.map((a) => a.cycle)), orderQty: l.layout.order.qty,
        machines: l.stations.map((a, i) => ({
          id: `${L.id}-${a.id}`, code: a.id, name: a.name, kind: a.machine!.label, seq: i + 1, cycle: a.cycle,
          tool: a.tool?.label ?? null, toolLife: a.tool?.life ?? null, feature: a.measure?.label ?? null, unit: a.measure?.unit ?? null,
        })),
      })),
      inventory: p.inventory.map((i) => ({ key: i.key, label: i.label, unit: i.unit, cap: i.cap })),
    };
  });
  return JSON.stringify({ company: { id: c, name: co.bundle.company.name }, plants, parts: [...parts.values()], customers: [...customers.values()] });
}

/** Sets a company's plants' wall clock: `ms` is where they are now (a backfill starts in the past). */
function setClock(c: string, ms: number): boolean {
  const co = get(c);
  if (!co) return false;
  for (const p of co.plants) p.epoch = ms - p.elapsed * 1000;
  return true;
}

/** Runs one company `seconds` ahead in steps of `dt`, nobody watching (history backfill, what-if runs). */
function advance(c: string, seconds: number, dt: number): boolean {
  const co = get(c);
  if (!co) return false;
  for (const p of co.plants) p.headless = true;
  for (let t = 0; t < seconds - 1e-9; t += dt) for (const p of co.plants) p.step(dt);
  for (const p of co.plants) p.events.length = 0;
  return true;
}

/** Runs a company ahead by seconds as if they had passed, events and orders included (tests). */
function fastForward(c: string, seconds: number): boolean {
  const co = get(c);
  if (!co) return false;
  for (let t = 0; t < seconds - 1e-9; t += 0.5) for (const p of co.plants) p.step(0.5);
  for (const p of co.plants) if (p.events.length > MAX_EVENTS) p.events.splice(0, p.events.length - MAX_EVENTS);
  return true;
}

// ---------- what-if (the advisor tries a plan on a copy of the plant) ----------

interface Change { match: string; command: Command; arg?: number }

/** A small seeded generator, so a plan and its baseline see the same luck. */
function seeded(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Runs some lines of a plant `hours` ahead twice from the same state — as they are, and with the
 * changes applied — and sums up both. Meant for an offline engine restored from the live state.
 * A change `match`es an asset id (HP-201), an id prefix (HP: every die press) or a line name.
 */
function whatIf(c: string, plantId: string, linesJson: string, hours: number, dt: number, changesJson: string, seed: number, runs = 1): string {
  return result(() => {
    const p = get(c)?.byId[plantId];
    if (!p) throw new Error(`找不到 ${c}/${plantId}`);
    // Go sends null for a list it was not given: all lines, no change
    const names = (JSON.parse(linesJson || '[]') ?? []) as string[];
    const lines = names.length ? p.lines.filter((l) => names.includes(l.name) || names.includes(l.id)) : p.lines;
    if (!lines.length) throw new Error(`${plantId} 没有这些产线：${names.join('、')}；有 ${p.lines.map((l) => l.name).join('、')}`);
    const ids = new Set(lines.map((l) => l.id));
    const changes = (JSON.parse(changesJson || '[]') ?? []) as Change[];
    const saved = p.saveState();
    const random = Math.random;
    // the plant goes on as it was once the trial runs are done: recording, reporting, taking orders
    const { headless, history } = p;
    const run = (apply: boolean, seed: number) => {
      p.restoreState(saved);
      for (const a of p.assets.values()) a.live = undefined;
      Math.random = seeded(seed);
      const applied: string[] = [];
      if (apply) {
        for (const ch of changes) {
          const hit = lines.filter((l) => l.name === ch.match).flatMap((l) => l.stations);
          const targets = hit.length ? hit : lines.flatMap((l) => l.stations).filter((a) => a.id === ch.match || a.id.startsWith(`${ch.match}-`));
          if (!targets.length) throw new Error(`没有匹配 ${ch.match} 的设备`);
          for (const a of targets) {
            const r = p.command(a.id, ch.command, ch.arg);
            applied.push(r.msg);
          }
        }
      }
      p.headless = true;
      p.history = new History(p, plantId, 1, 0);
      p.history.only = ids;
      for (let t = 0; t < hours * 3600 - 1e-9; t += dt) p.stepLines(ids, dt);
      const b = p.history.drain(true);
      p.history = undefined;
      return { applied, ...sum(b, lines.map((l) => [`${plantId}-${l.id}`, l.name] as [string, string])) };
    };
    try {
      // one run decides nothing: a breakdown in one and not the other swamps the change. Several
      // seeds, each shared by the pair, and the mean of each side.
      const n = Math.max(1, Math.min(8, Math.round(runs)));
      const base: ReturnType<typeof run>[] = [];
      const plan: ReturnType<typeof run>[] = [];
      for (let i = 0; i < n; i++) {
        base.push(run(false, seed + i));
        plan.push(run(true, seed + i));
      }
      const before = average(base);
      const after = average(plan);
      return {
        company: c, plant: plantId, hours, runs: n, seeds: [seed, seed + n - 1], lines: lines.map((l) => l.name), changes: plan[0].applied,
        说明: '现状 = 不做任何改动；方案 = 应用 changes 之后；两边各跑 runs 次（同一组随机种子）取平均。变化 = 方案 − 现状（每组种子各减一次再平均）。每条线的 p10 / p90 是这几次里的第 10 和第 90 百分位（次数少时接近最低和最高）：变化的 p10 到 p90 都在 0 的同一边，差异才大于随机波动。每次的 OEE 和废品率列在 每次 里。',
        校准: calibratedIn(lines),
        现状: { ...before, lines: banded(before.lines, base.map((b) => b.lines)) },
        方案: { ...after, lines: banded(after.lines, plan.map((b) => b.lines)) },
        变化: banded(difference(before, after), base.map((b, i) => difference(b, plan[i]))),
        每次: base.map((b, i) => ({ seed: seed + i, 现状: b.lines.map((l) => ({ line: l.line, oee: l.oee, scrapRate: l.scrapRate })), 方案: plan[i].lines.map((l) => ({ line: l.line, oee: l.oee, scrapRate: l.scrapRate })) })),
      };
    } finally {
      Math.random = random;
      p.restoreState(saved);
      p.headless = headless;
      p.history = history;
    }
  });
}

const PARAM_LABEL: Record<keyof Calibration, string> = {
  idleRise: '待机温升', runRise: '运行温升', tau: '升温时间常数', pIdle: '待机功率', pRun: '运行功率', vib: '振动', cycle: '节拍', mtbf: '故障间隔', repair: '修复时间',
};

/** The machines of these lines that run on fitted parameters, and which ones. */
function calibratedIn(lines: PlantSim['lines']) {
  return lines.flatMap((l) => l.stations.filter((a) => a.calib).map((a) => `${a.id}：${Object.keys(a.calib!).map((k) => PARAM_LABEL[k as keyof Calibration] ?? k).join('、')}按实测拟合`));
}

type Summary = ReturnType<typeof sum>;

/** The mean of several runs' summaries: numbers averaged, counts per category averaged. */
function average(xs: Summary[]): Summary {
  const r4 = (x: number) => Math.round(x * 1e4) / 1e4;
  const mean = (vs: (number | null)[]) => { const v = vs.filter((x): x is number => x !== null); return v.length ? r4(v.reduce((a, b) => a + b, 0) / v.length) : null; };
  const meanMap = (ms: Record<string, number>[]) => {
    const out: Record<string, number> = {};
    for (const m of ms) for (const [k, v] of Object.entries(m)) out[k] = (out[k] ?? 0) + v / ms.length;
    for (const k in out) out[k] = Math.round(out[k] * 10) / 10;
    return out;
  };
  return {
    lines: xs[0].lines.map((l, i) => {
      const ls = xs.map((x) => x.lines[i]);
      const out: Record<string, unknown> = { line: l.line };
      for (const k of Object.keys(l) as (keyof typeof l)[]) {
        if (k === 'line') continue;
        out[k] = k === 'downtimeMinutes' || k === 'defects' ? meanMap(ls.map((x) => x[k] as Record<string, number>)) : mean(ls.map((x) => x[k] as number | null));
      }
      return out as typeof l;
    }),
    spares: xs[0].spares.map((s, i) => ({ ...s, min: Math.min(...xs.map((x) => x.spares[i]?.min ?? s.min)), stockoutHours: mean(xs.map((x) => x.spares[i]?.stockoutHours ?? 0)) ?? 0 })),
  };
}

/** The p-th quantile of xs, between the nearest ranks: of three runs, P10 and P90 sit near the lowest and highest. */
function quantile(xs: number[], p: number) {
  const v = [...xs].sort((a, b) => a - b);
  const i = (v.length - 1) * p;
  const lo = Math.floor(i);
  return v[lo] + (v[Math.min(lo + 1, v.length - 1)] - v[lo]) * (i - lo);
}

/**
 * Each line's mean with how far the runs spread: p10 and p90 of every number over the runs. A
 * mean alone hides whether one lucky run made the difference.
 */
function banded<L extends { line: string }>(mean: L[], runs: L[][]) {
  const r4 = (x: number) => Math.round(x * 1e4) / 1e4;
  const at = (i: number, p: number) => {
    const out: Record<string, number> = {};
    for (const k of Object.keys(mean[i])) {
      const vs = runs.map((r) => (r[i] as Record<string, unknown> | undefined)?.[k]).filter((v): v is number => typeof v === 'number');
      if (vs.length) out[k] = r4(quantile(vs, p));
    }
    return out;
  };
  return mean.map((l, i) => ({ ...l, p10: at(i, 0.1), p90: at(i, 0.9) }));
}

/** after − before, line by line, for the headline numbers. */
function difference(before: Summary, after: Summary) {
  const r4 = (x: number) => Math.round(x * 1e4) / 1e4;
  const d = (a: number | null, b: number | null) => (a === null || b === null ? null : r4(b - a));
  return before.lines.map((b, i) => {
    const a = after.lines[i];
    return {
      line: b.line, produced: d(b.produced, a.produced), good: d(b.good, a.good), scrap: d(b.scrap, a.scrap),
      scrapRate: d(b.scrapRate, a.scrapRate), firstPassYield: d(b.firstPassYield, a.firstPassYield),
      availability: d(b.availability, a.availability), oee: d(b.oee, a.oee), toolChanges: d(b.toolChanges, a.toolChanges),
    };
  });
}

/** What a what-if run comes to, per line and for the spares. */
function sum(b: ReturnType<History['drain']>, lines: [string, string][]) {
  const r4 = (x: number) => Math.round(x * 1e4) / 1e4;
  const per = lines.map(([id, name]) => {
    const lh = b.lineHours.filter((x) => x.line === id);
    const add = (k: keyof (typeof lh)[number]) => lh.reduce((s, x) => s + (x[k] as number), 0);
    const produced = add('produced'), good = add('good'), scrap = add('scrap'), planned = add('planned'), run = add('run');
    const goodIdeal = lh.reduce((s, x) => s + x.good * x.idealCycle, 0);
    const changes = b.toolChanges.filter((x) => x.line === id);
    const down = b.downtime.filter((x) => x.line === id);
    const mins = (x: { start: string; end: string }) => (Date.parse(x.end.replace(' ', 'T')) - Date.parse(x.start.replace(' ', 'T'))) / 60000;
    const defects: Record<string, number> = {};
    for (const d of b.defects.filter((x) => x.line === id)) defects[d.type] = (defects[d.type] ?? 0) + 1;
    const downtime: Record<string, number> = {};
    for (const d of down) downtime[d.category] = Math.round((downtime[d.category] ?? 0) + mins(d));
    return {
      line: name, produced, good, scrap, rework: add('rework'), goodTonnes: r4(add('goodKg') / 1000),
      firstPassYield: produced ? r4(good / produced) : null, scrapRate: produced ? r4(scrap / produced) : null,
      availability: planned ? r4(run / planned) : null, oee: planned ? r4(goodIdeal / planned) : null,
      toolChanges: changes.length, avgToolHits: changes.length ? Math.round(changes.reduce((s, x) => s + x.hits, 0) / changes.length) : null,
      downtimeMinutes: downtime, defects,
    };
  });
  const spares: Record<string, { label: string; min: number; end: number; stockoutHours: number }> = {};
  for (const s of b.stock) {
    const o = (spares[s.item] ??= { label: s.label, min: s.min, end: s.end, stockoutHours: 0 });
    o.min = Math.min(o.min, s.min);
    o.end = s.end;
    if (s.min === 0) o.stockoutHours++;
  }
  return { lines: per, spares: Object.values(spares) };
}

function list(): string {
  return JSON.stringify([...companies.values()].map((c) => ({
    id: c.bundle.company.id, name: c.bundle.company.name, short: c.bundle.company.short, industry: c.bundle.company.industry,
    builtin: builtinCompanies.has(c.bundle.company.id),
    plants: c.plants.map((p) => ({
      id: p.layout.id, name: p.layout.name, hall: p.layout.hall, site: p.layout.site,
      lines: p.layout.lines.length, machines: p.layout.lines.reduce((n, l) => n + l.stations.length, 0),
    })),
  })));
}

(globalThis as Record<string, unknown>).SF = {
  init, step, frame, plantFrame, timeScale, snapshot, command, assetState, ingest, hist, setTimeScale, list, save, restore, machines, fleet, playEquipment, telemetry,
  addIndustry, addCompany, checkIndustry, checkCompany, companyConfig, processModel, keep, builtinTemplates, builtinTemplate, checkTemplate, removeCompany, industryList, industry, schema,
  historyOn, history, historyDims, advance, whatIf, setClock, routeOrder, syncOrders, stopOrders, fastForward, calibrate, residuals, machineParams,
};
