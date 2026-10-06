// The simulation as the Go server runs it (embedded JS engine): every plant of every company,
// in real time, with no browser involved. Browsers follow it through frames (exportState),
// MCP tools read snapshots of it, and protocol adapters feed it measured values.
// Built by `npm run build:engine` into server/internal/engine/js/engine.js.
import { loadAll, type CompanyBundle } from '@/config/loader';
import { PlantSim } from '@/sim/plant';
import type { Command } from '@/sim/types';

declare const host: { log(msg: string): void };

const companyRaw = import.meta.glob('../config/companies/*.json', { eager: true, import: 'default' });
const industryRaw = import.meta.glob('../config/industries/*.json', { eager: true, import: 'default' });

interface Company { bundle: CompanyBundle; plants: PlantSim[]; byId: Record<string, PlantSim>; timeScale: number }
const companies = new Map<string, Company>();

/** Events nobody collected (no browser watching) are dropped beyond this. */
const MAX_EVENTS = 200;

function init(): string {
  for (const bundle of loadAll(companyRaw, industryRaw, (m) => host.log(m))) {
    const plants = bundle.plants.map((l) => new PlantSim(l));
    companies.set(bundle.company.id, { bundle, plants, byId: Object.fromEntries(plants.map((p) => [p.layout.id, p])), timeScale: 1 });
  }
  return JSON.stringify([...companies.keys()]);
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

function list(): string {
  return JSON.stringify([...companies.values()].map((c) => ({
    id: c.bundle.company.id, name: c.bundle.company.name,
    plants: c.plants.map((p) => ({ id: p.layout.id, name: p.layout.name, hall: p.layout.hall })),
  })));
}

(globalThis as Record<string, unknown>).SF = { init, step, frame, plantFrame, timeScale, snapshot, command, ingest, hist, setTimeScale, list };
