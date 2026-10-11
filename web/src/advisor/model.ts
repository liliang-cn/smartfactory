// Names in the semantic model are identifiers (scrap_rate, plant_name). People read the model's
// own Chinese synonyms instead; nothing on these pages prints an identifier.
import { useEffect } from 'react';
import { create } from 'zustand';
import { getDimensions, getMetrics, type DimensionInfo, type Filter, type Guard, type MetricInfo } from './api';

const CJK = /[㐀-鿿]/;

export function toNumber(v: unknown): number | null {
  if (typeof v === 'number') return Number.isFinite(v) ? v : null;
  if (typeof v === 'string' && /^-?\d+(\.\d+)?(e-?\d+)?$/i.test(v.trim())) return Number(v);
  return null;
}

export function plainNumber(x: number) {
  if (x !== 0 && Math.abs(x) < 1) return x.toLocaleString('zh-CN', { maximumSignificantDigits: 3 });
  if (Math.abs(x) >= 100) return x.toLocaleString('zh-CN', { maximumFractionDigits: 0 });
  return x.toLocaleString('zh-CN', { maximumFractionDigits: 1 });
}

export function percent(x: number, digits?: number) {
  const p = x * 100;
  const d = digits ?? (Math.abs(p) < 10 ? 2 : 1);
  return `${p.toFixed(d)}%`;
}

/** A relative change such as `by: 0.25` → 25%. */
export const share = (by: number) => `${Math.round(by * 1000) / 10}%`;

/** 2026-09-07 or 2026-09-07T00:00:00Z → 9月7日 (with the year when it is not this year). */
export function day(t?: string) {
  if (!t) return '';
  const d = new Date(t.length === 10 ? `${t}T00:00:00` : t);
  if (Number.isNaN(d.getTime())) return t;
  return `${d.getFullYear() === new Date().getFullYear() ? '' : `${d.getFullYear()}年`}${d.getMonth() + 1}月${d.getDate()}日`;
}

export function dayTime(t?: string) {
  if (!t) return '';
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return t;
  return `${day(t)} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

/** Local calendar date, YYYY-MM-DD. */
export function isoDay(d: Date) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

/** Money in 万 (or 亿) once it is that large. */
export function money(x: number): [string, string] {
  if (Math.abs(x) >= 1e8) return [(x / 1e8).toFixed(2), '亿'];
  if (Math.abs(x) >= 1e4) {
    const w = x / 1e4;
    return [w.toLocaleString('zh-CN', { maximumFractionDigits: Math.abs(w) >= 1000 ? 0 : 1 }), '万'];
  }
  return [plainNumber(x), ''];
}

const UNIT = /（(?:[^）]*?[，,]\s*)?(元\/吨|元\/kWh|元|小时|分钟|吨|kWh)）/;

export interface Model {
  ready: boolean;
  /** the unit a metric is counted in, from its description (元, 元/吨, 小时 …) */
  unit: (name: string) => string;
  /** a value and its unit, for a headline figure */
  display: (name: string, v: unknown) => { text: string; unit: string };
  has: (name: string) => boolean;
  label: (name: string) => string;
  isMetric: (name: string) => boolean;
  isShare: (name: string) => boolean;
  fmt: (name: string, v: unknown) => string;
  cell: (column: string, v: unknown) => string;
  scope: (f: Filter) => { dim: string; text: string };
  guard: (g: Guard) => string;
}

function build(metrics: MetricInfo[], dims: DimensionInfo[], ready: boolean): Model {
  const m = new Map(metrics.map((x) => [x.name, x]));
  const d = new Map(dims.map((x) => [x.name, x]));
  // the first Chinese synonym; a leading acronym people say as is (OEE, MTTR) counts too
  const firstCJK = (l?: string[]) => (l?.[0] && /^[A-Z]{2,5}$/.test(l[0]) ? l[0] : l?.find((s) => CJK.test(s)));
  const label = (name: string) => {
    const mi = m.get(name);
    if (mi) return firstCJK(mi.synonyms) ?? mi.description.split(/[。；，（(]/)[0] ?? '指标';
    const di = d.get(name);
    if (di) return firstCJK(di.synonyms) ?? (di.type === 'time' ? '日期' : '分组');
    if (name === 'time') return '日期';
    if (name === 'row_count') return '行数';
    return ready ? '数值' : '…';
  };
  const isShare = (name: string) => /(rate|yield|oee|availability|performance|_used)$/i.test(name);
  const unit = (name: string) => m.get(name)?.description.match(UNIT)?.[1] ?? '';
  const fmt = (name: string, v: unknown) => {
    const x = toNumber(v);
    if (x === null) return v === null || v === undefined ? '—' : String(v);
    if (isShare(name) && Math.abs(x) <= 1.5) return percent(x);
    if (unit(name) === '元') return money(x).join('');
    return plainNumber(x);
  };
  const display = (name: string, v: unknown) => {
    const x = toNumber(v);
    if (x === null) return { text: '—', unit: '' };
    if (isShare(name) && Math.abs(x) <= 1.5) return { text: percent(x, 1), unit: '' };
    const u = unit(name);
    if (u === '元') { const [t, w] = money(x); return { text: t, unit: `${w}元` }; }
    return { text: plainNumber(x), unit: u };
  };
  const cell = (column: string, v: unknown) => {
    if (v === null || v === undefined || v === '') return '—';
    if (m.has(column)) return fmt(column, v);
    const s = String(v);
    const dd = s.match(/^(\d{4}-\d{2}-\d{2})T00:00:00(\.0+)?(Z|[+-]00:?00)?$/);
    if (dd) return dd[1];
    const ts = s.match(/^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})/);
    if (ts) return `${ts[1]} ${ts[2]}`;
    if (d.has(column)) return s;
    const x = toNumber(v);
    return x === null ? s : plainNumber(x);
  };
  const values = (vs?: unknown[]) => (vs ?? []).map(String).join('、');
  const scope = (f: Filter): { dim: string; text: string } => {
    if (f.and || f.or) {
      const parts = (f.and ?? f.or ?? []).map((x) => { const s = scope(x); return `${s.dim} ${s.text}`; });
      return { dim: f.and ? '同时' : '其一', text: parts.join(f.and ? '，' : ' 或 ') };
    }
    const v = values(f.values);
    const ops: Record<string, string> = {
      '=': v, in: v, '!=': `≠ ${v}`, 'not in': `≠ ${v}`, not_in: `≠ ${v}`, '>': `> ${v}`, '>=': `≥ ${v}`, '<': `< ${v}`, '<=': `≤ ${v}`, like: `含 ${v}`,
    };
    return { dim: label(f.dimension ?? f.metric ?? ''), text: ops[f.op] ?? v };
  };
  const guard = (g: Guard) => {
    const parts: string[] = [];
    if (g.tolerance != null) parts.push(`${g.worse_is === 'down' ? '降幅' : '升幅'} ≤ ${share(g.tolerance)}`);
    if (g.limit != null) parts.push(`${g.worse_is === 'down' ? '≥' : '≤'} ${fmt(g.metric, g.limit)}`);
    return `${label(g.metric)} ${parts.join('，')}`;
  };
  return { ready, unit, display, has: (n) => m.has(n) || d.has(n), label, isMetric: (n) => m.has(n), isShare, fmt, cell, scope, guard };
}

interface ModelState { company: string; model: Model; load(company: string): void }

export const useModelStore = create<ModelState>()((set, get) => ({
  company: '',
  model: build([], [], false),
  load(company) {
    if (get().company === company) return;
    set({ company, model: build([], [], false) });
    Promise.allSettled([getMetrics(company), getDimensions(company)]).then(([a, b]) => {
      if (get().company !== company) return;
      set({
        model: build(
          a.status === 'fulfilled' ? a.value.metrics ?? [] : [],
          b.status === 'fulfilled' ? b.value.dimensions ?? [] : [],
          true,
        ),
      });
    });
  },
}));

export function useModel(company: string) {
  useEffect(() => useModelStore.getState().load(company), [company]);
  return useModelStore((s) => s.model);
}
