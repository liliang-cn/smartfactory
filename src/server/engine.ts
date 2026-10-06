// The simulation as the Go server runs it (embedded JS engine): every plant of every company,
// in real time, with no browser involved. Browsers follow it through frames (exportState),
// MCP tools read snapshots of it, and protocol adapters feed it measured values.
// Built by `npm run build:engine` into server/internal/engine/js/engine.js.
import { z } from 'zod';
import { ConfigError, layoutCompany, loadAll, validateCompany, validateIndustry, type CompanyBundle } from '@/config/loader';
import { Company as CompanySchema, Industry as IndustrySchema, type CompanyCfg, type IndustryCfg } from '@/config/schema';
import { PlantSim, type SavedPlant } from '@/sim/plant';
import type { Command } from '@/sim/types';

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
  companies.set(bundle.company.id, { bundle, plants, byId: Object.fromEntries(plants.map((p) => [p.layout.id, p])), timeScale: 1 });
  pending.delete(bundle.company.id);
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
  }
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
  return JSON.stringify({ v: SAVE_VERSION, company: c, timeScale: co.timeScale, plants: Object.fromEntries(co.plants.map((p) => [p.layout.id, p.saveState()])) });
}

/** Carries a company on from a saved state; plants whose structure changed start fresh. */
function restore(c: string, json: string): string {
  const co = get(c);
  const restored: string[] = [];
  const skipped: string[] = [];
  if (!co) return JSON.stringify({ restored, skipped: ['(unknown company)'] });
  let d: { v: number; timeScale?: number; plants: Record<string, SavedPlant> };
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
  return JSON.stringify({ restored, skipped });
}

/** A plant's machines in protocol order, with what is needed to map fault codes and readings. */
function machines(c: string, plant: string): string {
  const p = get(c)?.byId[plant];
  if (!p) return 'null';
  return JSON.stringify(p.machines().map((a) => ({ id: a.id, name: a.name, faults: a.spec?.faults ?? [], measured: !!a.measure })));
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

function list(): string {
  return JSON.stringify([...companies.values()].map((c) => ({
    id: c.bundle.company.id, name: c.bundle.company.name,
    plants: c.plants.map((p) => ({ id: p.layout.id, name: p.layout.name, hall: p.layout.hall })),
  })));
}

(globalThis as Record<string, unknown>).SF = {
  init, step, frame, plantFrame, timeScale, snapshot, command, ingest, hist, setTimeScale, list, save, restore, machines, playEquipment, telemetry,
  addIndustry, addCompany, removeCompany, industryList, industry, schema,
};
