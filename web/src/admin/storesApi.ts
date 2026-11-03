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
export const releaseProject = (c: string, id: string) => call<Project>('POST', `${rndPath(c)}/${encodeURIComponent(id)}/release`);

/** A problem list from a refused call, or its message. */
export function problemsOf(e: unknown): string[] {
  const err = e as { problems?: string[]; message?: string };
  return err.problems?.length ? err.problems : [err.message ?? '没成功'];
}

export const num = (v: number) => (Number.isInteger(v) ? v.toLocaleString() : v.toLocaleString(undefined, { maximumFractionDigits: 3 }));
export const day = (iso: string) => new Date(iso).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' });
