// The console's calls for a company's stores, dies and new parts (库房、模具、研发).
import { call } from './api';

export interface StoreCategory { key: string; label: string; kind: 'bar' | 'part' | 'product' | 'other'; unit: string; fields?: string[] }
export interface HeatStock {
  no: string; item: string; itemName: string; grade?: string; size?: string; mill: string; cert?: string; unit: string;
  received: number; issued: number; counted: number; used: number; left: number; at: string;
}
export interface StoreItem {
  id: string; cat: string; code: string; name: string; attrs?: Record<string, string>; unit: string; safety: number; place?: string;
  stock: number; low: boolean; heats?: HeatStock[]; updated: string;
}
export interface StoreMove {
  id: number; at: string; item: string; itemName?: string; kind: 'in' | 'out' | 'count'; qty: number; delta: number;
  heat?: string; mill?: string; cert?: string; ref?: string; who: string; note?: string;
}
export interface StoresOverview { categories: StoreCategory[]; items: StoreItem[]; heats: HeatStock[]; moves: StoreMove[]; operator: boolean }
export interface MoveInput { item: string; kind: 'in' | 'out' | 'count'; qty: number; heat?: string; mill?: string; cert?: string; ref?: string; note?: string }
export interface HeatUse { heat: string; mill?: string; plant: string; line: string; lineName: string; order?: string; pieces: number; kg: number; first: string; last: string; stored: boolean }
export interface HeatTrace { query: string; heat?: HeatStock; moves: StoreMove[]; uses: HeatUse[]; note?: string }

export type DiePlace = 'press' | 'store' | 'repair' | 'trial' | 'scrap';
export interface Die {
  no: string; kind: string; product?: string; drawing?: string; life: number; strokes: number; resinks?: number;
  place: DiePlace; plant?: string; asset?: string; assetName?: string; project?: string; note?: string; updated: string; left: number;
}
export interface DieEvent { at: string; kind: string; text: string; who: string; strokes: number }
export interface PressNow {
  plant: string; plantName: string; line: string; lineName: string; product: string; id: string; name: string; kind: string;
  twinUsed: number; twinLife: number; changing?: boolean; die?: Die;
}
export interface DiesOverview { dies: Die[]; presses: PressNow[]; places: Record<string, string>; operator: boolean }
export interface DieAction { do: 'mount' | 'unmount' | 'repair' | 'scrap' | 'release'; plant?: string; asset?: string; to?: string; life?: number; resunk?: boolean; note?: string }

export interface Trial { id: string; project: string; at: string; die?: string; params?: string; qty: number; good: number; result: string; findings?: string; who: string }
export interface Project {
  id: string; part: string; name: string; customer?: string; grade?: string; weight?: number; drawing?: string; dieDesign?: string;
  stage: string; owner?: string; due?: string; docs?: string[]; note?: string; released?: string; releasedBy?: string;
  created: string; updated: string; dies?: Die[]; trials?: Trial[];
}
export interface RnDOverview { projects: Project[]; stages: string[]; results: string[]; operator: boolean }

const storesPath = (c: string) => `/api/stores/${encodeURIComponent(c)}`;
export const stores = (c: string) => call<StoresOverview>('GET', storesPath(c));
export const saveStoreCategory = (c: string, cat: Partial<StoreCategory>) => call<StoreCategory>('POST', `${storesPath(c)}/categories`, cat);
export const saveStoreItem = (c: string, it: Partial<StoreItem>) => call<StoreItem>('POST', `${storesPath(c)}/items`, it);
export const recordMove = (c: string, m: MoveInput) => call<StoreMove>('POST', `${storesPath(c)}/moves`, m);
export const storeMoves = (c: string, q: URLSearchParams) => call<StoreMove[]>('GET', `${storesPath(c)}/moves?${q}`);
export const traceHeat = (c: string, q: string) => call<HeatTrace>('GET', `${storesPath(c)}/trace?q=${encodeURIComponent(q)}`);

const diesPath = (c: string) => `/api/dies/${encodeURIComponent(c)}`;
export const dies = (c: string) => call<DiesOverview>('GET', diesPath(c));
export const dieDetail = (c: string, no: string) => call<{ die: Die; history: DieEvent[] }>('GET', `${diesPath(c)}/${encodeURIComponent(no)}`);
export const createDie = (c: string, d: Partial<Die>) => call<Die>('POST', diesPath(c), d);
export const dieAct = (c: string, no: string, a: DieAction) => call<Die>('POST', `${diesPath(c)}/${encodeURIComponent(no)}/act`, a);

const rndPath = (c: string) => `/api/rnd/${encodeURIComponent(c)}`;
export const rnd = (c: string) => call<RnDOverview>('GET', rndPath(c));
export const saveProject = (c: string, p: Partial<Project>) => call<Project>('POST', rndPath(c), p);
export const addTrial = (c: string, id: string, t: Partial<Trial>) => call<Project>('POST', `${rndPath(c)}/${encodeURIComponent(id)}/trials`, t);
export interface SignOff { bom?: string; drawing?: string; why?: string }
export const releaseProject = (c: string, id: string, so: SignOff = {}) => call<Project>('POST', `${rndPath(c)}/${encodeURIComponent(id)}/release`, so);
export const draftProjectBOM = (c: string, id: string) => call<BOM>('POST', `${rndPath(c)}/${encodeURIComponent(id)}/bom`);

// ---------- BOM (物料清单) ----------

export type LineKind = 'make' | 'buy' | 'std' | 'raw' | 'aux';
export type BOMStatus = '草稿' | '生效' | '停用';
export interface BOMLine {
  id: string; parent?: string; code: string; name: string; kind: LineKind; spec?: string; grade?: string;
  qty: number; unit: string; scrap?: number; op?: string; note?: string;
}
export interface BOM {
  id: string; part: string; name: string; rev: string; drawing?: string; status: BOMStatus; effective?: string; unit?: string;
  lines: BOMLine[]; note?: string; source?: string; project?: string; by?: string; released?: string; releasedBy?: string; created: string; updated: string;
}
export interface ECO {
  id: string; part: string; name: string; from?: string; to: string; bom: string; drawingFrom?: string; drawingTo?: string;
  changes: string[]; why: string; project?: string; by: string; at: string;
}
export interface PartRow {
  part: string; name: string; product: string; family?: string; material?: string; weight?: number; input?: number;
  active?: string; activeId?: string; effective?: string; drawing?: string; drafts: number; revisions: number; lines: number; listed: boolean;
}
export interface BOMOverview { parts: PartRow[]; ecos: ECO[]; kinds: [LineKind, string][]; operator: boolean }
export interface Usage { part: string; name: string; rev: string; code: string; item: string; kind: LineKind; per: number; unit: string; level: number; via?: string; op?: string }
export interface BOMPart { part: string; revisions: BOM[]; ecos: ECO[]; usedIn: Usage[] }
export interface BOMRow extends BOMLine { level: number; per: number; total: number; leaf: boolean; sub?: string; rev?: string }
export interface Need {
  code: string; name: string; kind: LineKind; spec?: string; grade?: string; unit: string; need: number;
  item?: string; itemName?: string; stock: number; storeUnit?: string; have: number; short: number; problem?: string; ops?: string[];
}
export interface Explosion {
  part: string; name: string; rev: string; status: BOMStatus; bom: string; effective?: string; drawing?: string; qty: number; unit: string;
  rows: BOMRow[]; needs: Need[]; short: number; unkept: number;
}
export interface Kit { no: string; part: string; qty: number; rev?: string; name?: string; ready: boolean; needs: Need[]; short: number }
export interface Shortage extends Need { orders: string[] }
export interface Kitting { orders: Kit[]; short: Shortage[]; totals: Shortage[] }
export interface BOMImported { bom: BOM; lines: number; columns: string[]; problems: string[] }

const bomPath = (c: string) => `/api/bom/${encodeURIComponent(c)}`;
export const bomOverview = (c: string) => call<BOMOverview>('GET', bomPath(c));
export const bomPart = (c: string, part: string) => call<BOMPart>('GET', `${bomPath(c)}/parts/${encodeURIComponent(part)}`);
export const saveBOM = (c: string, b: Partial<BOM>) => (b.id ? call<BOM>('PUT', `${bomPath(c)}/${encodeURIComponent(b.id)}`, b) : call<BOM>('POST', bomPath(c), b));
export const deleteBOM = (c: string, id: string) => call<BOM>('DELETE', `${bomPath(c)}/${encodeURIComponent(id)}`);
export const copyBOM = (c: string, id: string) => call<BOM>('POST', `${bomPath(c)}/${encodeURIComponent(id)}/copy`);
export const releaseBOM = (c: string, id: string, r: { why: string; effective?: string; drawing?: string }) => call<{ bom: BOM; eco: ECO }>('POST', `${bomPath(c)}/${encodeURIComponent(id)}/release`, r);
export const retireBOM = (c: string, id: string) => call<BOM>('POST', `${bomPath(c)}/${encodeURIComponent(id)}/retire`);
export const explodeBOM = (c: string, part: string, qty: number, rev = '') =>
  call<Explosion>('GET', `${bomPath(c)}/explode?part=${encodeURIComponent(part)}&qty=${qty}&rev=${encodeURIComponent(rev)}`);
export const whereUsed = (c: string, q: string) => call<Usage[]>('GET', `${bomPath(c)}/where?q=${encodeURIComponent(q)}`);
export const kitting = (c: string) => call<Kitting>('GET', `${bomPath(c)}/kitting`);
export async function importBOM(c: string, file: File, head: { part?: string; name?: string; rev?: string; drawing?: string }) {
  const buf = new Uint8Array(await file.arrayBuffer());
  let bin = '';
  for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000));
  return call<BOMImported>('POST', `${bomPath(c)}/import`, { ...head, file: file.name, data: btoa(bin) });
}

export const KIND_TONE: Record<LineKind, string> = {
  make: 'bg-brand-soft text-primary', buy: 'bg-surface text-foreground', std: 'bg-success-soft text-success', raw: 'bg-warning-soft text-warning', aux: 'bg-muted text-muted-foreground',
};
export const STATUS_TONE: Record<BOMStatus, string> = { 草稿: 'bg-surface text-muted-foreground', 生效: 'bg-success-soft text-success', 停用: 'bg-muted text-muted-foreground line-through' };
/** how much, with the unit, as a person writes it */
export const qty = (v: number, unit: string) => `${num(Math.round(v * 1000) / 1000)} ${unit}`;

/** A problem list from a refused call, or its message. */
export function problemsOf(e: unknown): string[] {
  const err = e as { problems?: string[]; message?: string };
  return err.problems?.length ? err.problems : [err.message ?? '没成功'];
}

export const num = (v: number) => (Number.isInteger(v) ? v.toLocaleString() : v.toLocaleString(undefined, { maximumFractionDigits: 3 }));
export const day = (iso: string) => new Date(iso).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' });
