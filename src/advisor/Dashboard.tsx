// 经营数据: the company's recent figures from the governed semantic layer, by section.
import { createContext, useContext, useMemo, useState } from 'react';
import { ArrowDownIcon, ArrowUpDownIcon, ArrowUpIcon, ChevronRightIcon } from 'lucide-react';
import { Area, AreaChart, Bar, BarChart, CartesianGrid, LabelList, Line, LineChart, XAxis, YAxis } from 'recharts';
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { runtime, useUI } from '@/state/store';
import type { Query, Where } from './api';
import { day, isoDay, money, percent, plainNumber, type Model } from './model';
import { ChartCard, num, records, Skeleton, useAnswer, useMerged } from './parts';
import { useBiz, type BizSection } from './store';

const SERIES_COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-5)', 'var(--chart-4)', '#0ea5b7', '#8a94b3'];

export interface Window { from: string; to: string; where: Where[]; prev: Where[] }

export function useWindow(): Window {
  const plant = useBiz((s) => s.plant);
  const days = useBiz((s) => s.days);
  return useMemo(() => {
    const now = new Date();
    const at = (n: number) => isoDay(new Date(now.getFullYear(), now.getMonth(), now.getDate() - n));
    const [to, from, pfrom] = [at(0), at(days), at(days * 2)];
    const p: Where[] = plant ? [{ dimension: 'plant_name', op: '=', values: [plant] }] : [];
    return {
      from, to,
      where: [{ dimension: 'time', op: '>=', values: [from] }, { dimension: 'time', op: '<', values: [to] }, ...p],
      prev: [{ dimension: 'time', op: '>=', values: [pfrom] }, { dimension: 'time', op: '<', values: [from] }, ...p],
    };
  }, [plant, days]);
}

interface Ctx { company: string; w: Window; model: Model }
const DashCtx = createContext<Ctx>(null!);
const useCtx = () => useContext(DashCtx);

/** Axis and bar labels: large counts in 万, money in 万 / 亿. */
function short(model: Model, metric: string, x: number) {
  if (model.isShare(metric)) return percent(x, Math.abs(x) < 0.1 ? 1 : 0);
  if (model.unit(metric) === '元' || Math.abs(x) >= 10000) return money(x).join('');
  return plainNumber(x);
}

/** Room for the widest tick label recharts is likely to pick below the top value. */
const axisWidth = (model: Model, metric: string, top: number) =>
  Math.max(36, ...[1, 0.8, 0.75, 0.6].map((f) => short(model, metric, top * f).length * 7.2 + 10));

const md = (t: unknown) => {
  const s = String(t).slice(0, 10);
  return `${Number(s.slice(5, 7))}/${Number(s.slice(8, 10))}`;
};

const byName = (a: string, b: string) => a.localeCompare(b, 'zh-CN', { numeric: true });
const SHIFT_ORDER = ['早班', '白班', '中班', '夜班'];

export function Seg<T extends string | number>({ value, items, onChange, className }: { value: T; items: [T, string][]; onChange: (v: T) => void; className?: string }) {
  return (
    <div role="tablist" className={cn('flex shrink-0 gap-0.5 rounded-xl bg-card p-[3px] shadow-float', className)}>
      {items.map(([v, label]) => (
        <button key={String(v)} type="button" role="tab" aria-selected={v === value} onClick={() => onChange(v)}
          className={cn('h-8 shrink-0 rounded-lg px-3 text-[12.5px] font-bold whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground max-md:h-[30px] max-md:px-2.5',
            v === value && 'bg-brand-soft text-primary hover:text-primary')}>
          {label}
        </button>
      ))}
    </div>
  );
}

export function Filters({ className }: { className?: string }) {
  const plant = useBiz((s) => s.plant);
  const days = useBiz((s) => s.days);
  const w = useWindow();
  const plants = runtime().plants.map((p) => p.layout.name);
  const last = new Date(`${w.to}T00:00:00`);
  last.setDate(last.getDate() - 1);
  return (
    <div className={cn('flex min-w-0 items-center gap-2 max-md:w-full', className)}>
      <Seg value={plant} items={[['', '全部'], ...plants.map((p) => [p, p] as [string, string])]} onChange={(v) => useBiz.getState().setPlant(v)}
        className="min-w-0 overflow-x-auto [scrollbar-width:none] max-md:shrink" />
      <Seg value={days} items={[[7, '7 天'], [30, '30 天']]} onChange={(v) => useBiz.getState().setDays(v)} />
      <span className="text-xs whitespace-nowrap text-muted-foreground tabular-nums max-lg:hidden">{day(w.from)}–{day(isoDay(last))}</span>
    </div>
  );
}

// ---- headline figures ---------------------------------------------------------------

type KpiSpec = [metric: string, better: 'up' | 'down', title?: string];

function Kpi({ metric, title, better, now, before, series }: {
  metric: string; title: string; better: 'up' | 'down'; now: unknown; before: unknown; series: { v: number }[];
}) {
  const { model } = useCtx();
  const d = model.display(metric, now);
  const n = now == null ? null : num(now);
  const b = before == null ? null : num(before);
  // rates move in percentage points, everything else relative to the previous window
  const pts = model.isShare(metric);
  const change = n != null && b ? (pts ? n - b : (n - b) / Math.abs(b)) : null;
  const good = change != null && (better === 'up' ? change > 0 : change < 0);
  const id = `k-${metric}`;
  return (
    <div className="relative min-w-0 overflow-hidden rounded-2xl bg-card px-3.5 pt-3 pb-2 shadow-float max-md:px-3">
      <small className="block truncate text-xs text-muted-foreground">{title}</small>
      <div className="mt-0.5 flex min-w-0 items-baseline gap-1.5">
        <b className={cn('truncate text-[22px] leading-tight font-extrabold tracking-tight tabular-nums max-md:text-lg', n == null && 'text-muted-foreground/40')}>{d.text}</b>
        {d.unit && <span className="shrink-0 text-[11.5px] font-semibold text-muted-foreground">{d.unit}</span>}
        {change != null && Math.abs(change) >= 0.0005 && (
          <em className={cn('ml-auto flex shrink-0 items-center text-[11.5px] font-bold not-italic tabular-nums', good ? 'text-success' : 'text-destructive')}>
            {change > 0 ? <ArrowUpIcon className="size-3" /> : <ArrowDownIcon className="size-3" />}
            {Math.abs(change * 100).toFixed(Math.abs(change) >= 0.995 ? 0 : 1)}{pts ? 'pp' : '%'}
          </em>
        )}
      </div>
      <ChartContainer config={{ v: { label: title, color: good || change == null ? 'var(--primary)' : 'var(--destructive)' } }} className="mt-1 aspect-auto h-9 w-full" initialDimension={{ width: 160, height: 36 }}>
        <AreaChart data={series} margin={{ top: 2, bottom: 0, left: 0, right: 0 }}>
          <defs>
            <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--color-v)" stopOpacity={0.25} />
              <stop offset="100%" stopColor="var(--color-v)" stopOpacity={0} />
            </linearGradient>
          </defs>
          <YAxis hide domain={['dataMin', 'dataMax']} />
          <Area dataKey="v" type="monotone" stroke="var(--color-v)" strokeWidth={1.6} fill={`url(#${id})`} isAnimationActive={false} />
        </AreaChart>
      </ChartContainer>
    </div>
  );
}

function KpiRow({ items: all }: { items: KpiSpec[] }) {
  const { company, w, model } = useCtx();
  const items = all.filter(([m]) => !model.ready || model.has(m));
  const metrics = items.map((k) => k[0]);
  const now = useMerged(company, metrics, { where: w.where });
  const before = useMerged(company, metrics, { where: w.prev });
  const daily = useMerged(company, metrics, { group_by: ['time'], grain: 'day', where: w.where });
  const cur = now.rows[0] ?? {};
  const prev = before.rows[0] ?? {};
  const days = [...daily.rows].sort((a, b) => String(a.time).localeCompare(String(b.time)));
  return (
    <div className={cn('grid grid-cols-2 gap-3 max-md:gap-2', items.length > 6 ? 'sm:grid-cols-4 2xl:grid-cols-8' : 'sm:grid-cols-3 xl:grid-cols-6')}>
      {items.map(([m, better, title]) => now.loading
        ? <Skeleton key={m} className="h-[104px] rounded-2xl bg-card/70" />
        : <Kpi key={m} metric={m} title={title ?? model.label(m)} better={better} now={cur[m]} before={prev[m]}
          series={days.filter((d) => d[m] != null).map((d) => ({ v: num(d[m]) }))} />)}
    </div>
  );
}

// ---- trends -----------------------------------------------------------------------------

/** A daily metric, one series per value of a dimension (or a single series). */
function Trend({ metric, series, title, kind = 'line', className }: { metric: string; series?: string; title: string; kind?: 'line' | 'bar'; className?: string }) {
  const { company, w, model } = useCtx();
  const r = useAnswer(company, { metrics: [metric], group_by: series ? ['time', series] : ['time'], grain: 'day', where: w.where });
  const { data, names, config, lo, hi, axisW } = useMemo(() => {
    const rows = records(r.data);
    const names = series ? [...new Set(rows.map((x) => String(x[series])))] : [model.label(metric)];
    const order = runtime().plants.map((p) => p.layout.name);
    names.sort((a, b) => {
      const ia = order.indexOf(a); const ib = order.indexOf(b);
      return ia >= 0 && ib >= 0 ? ia - ib : byName(a, b);
    });
    const byDay = new Map<string, Record<string, unknown>>();
    for (const x of rows) {
      const t = String(x.time).slice(0, 10);
      const o = byDay.get(t) ?? { t };
      o[`s${series ? names.indexOf(String(x[series])) : 0}`] = num(x[metric]);
      byDay.set(t, o);
    }
    const data = [...byDay.values()].sort((a, b) => String(a.t).localeCompare(String(b.t)));
    const config: ChartConfig = Object.fromEntries(names.map((p, i) => [`s${i}`, { label: p, color: SERIES_COLORS[i % SERIES_COLORS.length] }]));
    const vals = rows.map((x) => num(x[metric]));
    let lo = 0;
    let hi: number | 'auto' = 'auto';
    if (model.isShare(metric) && vals.length && kind === 'line') {
      const step = Math.max(...vals) - Math.min(...vals) > 0.2 ? 0.1 : 0.01;
      lo = Math.max(0, Math.floor(Math.min(...vals) / step) * step);
      hi = Math.ceil(Math.max(...vals) / step) * step;
    } else if (kind === 'line' && vals.length && Math.min(...vals) > 0) {
      lo = Math.floor(Math.min(...vals) * 0.95);
    }
    // stacked bars reach the daily total
    const top = kind === 'bar' ? Math.max(0, ...data.map((d) => Object.keys(d).filter((k) => k !== 't').reduce((a, k) => a + num(d[k]), 0))) : Math.max(0, ...vals);
    return { data, names, config, lo, hi, axisW: axisWidth(model, metric, typeof hi === 'number' ? hi : top) };
  }, [r.data, metric, series, model, kind]);
  const tooltip = (
    <ChartTooltip content={<ChartTooltipContent labelFormatter={(_, p) => day(String(p?.[0]?.payload?.t ?? ''))} formatter={(v, n) => (
      <span className="flex w-full items-center gap-2">
        <i className="size-2.5 rounded-[3px]" style={{ background: `var(--color-${n})` }} />
        <span className="text-muted-foreground">{config[String(n)]?.label}</span>
        <b className="ml-auto font-mono tabular-nums">{model.fmt(metric, v)}</b>
      </span>
    )} />} />
  );
  const axes = (
    <>
      <CartesianGrid vertical={false} strokeDasharray="3 3" />
      <XAxis dataKey="t" tickFormatter={md} tickLine={false} axisLine={false} minTickGap={18} tickMargin={6} />
      <YAxis domain={[lo, hi]} tickFormatter={(v: number) => short(model, metric, v)} tickLine={false} axisLine={false} width={axisW} />
    </>
  );
  return (
    <ChartCard title={title} sql={r.data?.sql} loading={r.loading} error={r.error} empty={!data.length} className={className}>
      <ChartContainer config={config} className="aspect-auto h-[250px] w-full max-md:h-[210px]">
        {kind === 'bar' ? (
          <BarChart data={data} margin={{ top: 6, right: 4, left: 0, bottom: 0 }}>
            {axes}{tooltip}
            {names.length > 1 && <ChartLegend content={<ChartLegendContent />} />}
            {names.map((_, i) => <Bar key={i} dataKey={`s${i}`} stackId="a" fill={`var(--color-s${i})`} radius={i === names.length - 1 ? [4, 4, 0, 0] : 0} isAnimationActive={false} />)}
          </BarChart>
        ) : (
          <LineChart data={data} margin={{ top: 6, right: 8, left: 0, bottom: 0 }}>
            {axes}{tooltip}
            {names.length > 1 && <ChartLegend content={<ChartLegendContent />} />}
            {names.map((_, i) => <Line key={i} dataKey={`s${i}`} type="monotone" stroke={`var(--color-s${i})`} strokeWidth={2.2} dot={false} activeDot={{ r: 4 }} connectNulls />)}
          </LineChart>
        )}
      </ChartContainer>
    </ChartCard>
  );
}

/** Several money-like metrics stacked per day (e.g. electricity and gas). */
function Stacked({ metrics, title, className }: { metrics: string[]; title: string; className?: string }) {
  const { company, w, model } = useCtx();
  const r = useMerged(company, metrics, { group_by: ['time'], grain: 'day', where: w.where });
  const data = [...r.rows].sort((a, b) => String(a.time).localeCompare(String(b.time))).map((x) => ({ ...x, t: String(x.time).slice(0, 10) }));
  const config: ChartConfig = Object.fromEntries(metrics.map((m, i) => [m, { label: model.label(m), color: SERIES_COLORS[i] }]));
  const top = Math.max(0, ...data.map((d) => metrics.reduce((a, m) => a + num((d as Record<string, unknown>)[m]), 0)));
  return (
    <ChartCard title={title} sql={r.sql} loading={r.loading} error={r.failed ? '—' : undefined} empty={!data.length} className={className}>
      <ChartContainer config={config} className="aspect-auto h-[250px] w-full max-md:h-[210px]">
        <BarChart data={data} margin={{ top: 6, right: 4, left: 0, bottom: 0 }}>
          <CartesianGrid vertical={false} strokeDasharray="3 3" />
          <XAxis dataKey="t" tickFormatter={md} tickLine={false} axisLine={false} minTickGap={18} tickMargin={6} />
          <YAxis tickFormatter={(v: number) => short(model, metrics[0], v)} tickLine={false} axisLine={false} width={axisWidth(model, metrics[0], top)} />
          <ChartTooltip content={<ChartTooltipContent labelFormatter={(_, p) => day(String(p?.[0]?.payload?.t ?? ''))} formatter={(v, n) => (
            <span className="flex w-full items-center gap-2">
              <i className="size-2.5 rounded-[3px]" style={{ background: `var(--color-${n})` }} />
              <span className="text-muted-foreground">{model.label(String(n))}</span>
              <b className="ml-auto font-mono tabular-nums">{model.fmt(String(n), v)}</b>
            </span>
          )} />} />
          <ChartLegend content={<ChartLegendContent />} />
          {metrics.map((m, i) => <Bar key={m} dataKey={m} stackId="a" fill={`var(--color-${m})`} radius={i === metrics.length - 1 ? [4, 4, 0, 0] : 0} isAnimationActive={false} />)}
        </BarChart>
      </ChartContainer>
    </ChartCard>
  );
}

// ---- breakdowns ----------------------------------------------------------------------------

/** Horizontal bars, biggest first. */
function Bars({ rows, metric, by, color = 'var(--chart-1)', max, valueText }: {
  rows: Record<string, unknown>[]; metric: string; by: string; color?: string; max?: number; valueText?: (r: Record<string, unknown>) => string;
}) {
  const { model } = useCtx();
  const data = rows
    .map((r) => ({ name: String(r[by] ?? '—'), v: num(r[metric]), r }))
    .sort((a, b) => b.v - a.v)
    .slice(0, max ?? rows.length)
    .map((x) => ({ ...x, label: valueText ? valueText(x.r) : model.fmt(metric, x.v) }));
  const longest = Math.max(...data.map((d) => d.name.length), 2);
  const label = Math.max(...data.map((d) => d.label.length), 2);
  return (
    <ChartContainer config={{ v: { label: model.label(metric), color } }} className="aspect-auto w-full" style={{ height: data.length * 27 + 8 }}>
      <BarChart data={data} layout="vertical" margin={{ top: 0, right: label * 7.5 + 12, left: 0, bottom: 0 }} barCategoryGap={5}>
        <XAxis type="number" hide domain={[0, 'dataMax']} />
        <YAxis type="category" dataKey="name" tickLine={false} axisLine={false} width={Math.min(150, longest * 12.5 + 8)} interval={0} tick={{ fontSize: 12 }} />
        <ChartTooltip cursor={false} content={<ChartTooltipContent hideIndicator formatter={(v) => (
          <span className="flex gap-3"><span className="text-muted-foreground">{model.label(metric)}</span><b className="font-mono tabular-nums">{model.fmt(metric, v)}</b></span>
        )} />} />
        <Bar dataKey="v" fill="var(--color-v)" radius={5} isAnimationActive={false}>
          <LabelList dataKey="label" position="right" offset={6} className="fill-foreground text-[11.5px] font-semibold tabular-nums" />
        </Bar>
      </BarChart>
    </ChartContainer>
  );
}

function BarCard({ q, metric, by, title, max, color, className, valueText }: {
  q: Omit<Query, 'where'>; metric: string; by: string; title: string; max?: number; color?: string; className?: string;
  valueText?: (r: Record<string, unknown>) => string;
}) {
  const { company, w } = useCtx();
  const r = useAnswer(company, { ...q, where: w.where });
  const rows = records(r.data).filter((x) => num(x[metric]) > 0);
  return (
    <ChartCard title={title} sql={r.data?.sql} loading={r.loading} error={r.error} empty={!rows.length} className={className}>
      <Bars rows={rows} metric={metric} by={by} max={max} color={color} valueText={valueText} />
    </ChartCard>
  );
}

/** Jumps to a line in the 3D scene and selects its first machine. */
function goLine(plantName: string, lineName: string) {
  const rt = runtime();
  const p = rt.plants.find((x) => x.layout.name === plantName && x.sim.lines.some((l) => l.name === lineName))
    ?? rt.plants.find((x) => x.sim.lines.some((l) => l.name === lineName));
  if (!p) return;
  useBiz.getState().setOpen(false);
  const ui = useUI.getState();
  const switching = ui.plantId !== p.layout.id;
  if (switching) ui.setPlant(p.layout.id);
  setTimeout(() => {
    const sim = runtime().byId[p.layout.id].sim;
    const line = sim.lines.find((l) => l.name === lineName);
    if (!line) return;
    useUI.getState().setTrackerLine(line.id);
    const a = [...sim.assets.values()].find((x) => x.line?.id === line.id && x.spec?.runRise !== undefined);
    if (a) useUI.getState().select(a.id, true);
  }, switching ? 450 : 0);
}

const knownLine = (ln: string) => runtime().plants.some((p) => p.sim.lines.some((l) => l.name === ln));

/** A sortable table: one row per value of the dimensions, one column per metric. */
function Breakdown({ by, metrics: all, title, sort: initial, bad, onRow, className }: {
  by: string[]; metrics: string[]; title: string; sort?: { k: string; asc: boolean };
  /** metrics where higher is worse, highlighted near the top */
  bad?: string[]; onRow?: (r: Record<string, unknown>) => (() => void) | undefined; className?: string;
}) {
  const { company, w, model } = useCtx();
  const metrics = all.filter((m) => !model.ready || model.has(m));
  const r = useMerged(company, metrics, { group_by: by, where: w.where });
  const [sort, setSort] = useState(initial ?? { k: metrics[0], asc: false });
  const rows = useMemo(() => [...r.rows].sort((a, b) => {
    const d = (a[sort.k] == null ? -Infinity : num(a[sort.k])) - (b[sort.k] == null ? -Infinity : num(b[sort.k]));
    return (sort.asc ? d : -d) || byName(String(a[by[0]]), String(b[by[0]]));
  }), [r.rows, sort, by]);
  const top = Object.fromEntries((bad ?? []).map((m) => [m, Math.max(...rows.map((x) => num(x[m])), 0)]));
  const lead = metrics[0];
  return (
    <ChartCard title={title} aside={<span className="text-xs text-muted-foreground tabular-nums">{rows.length}</span>}
      sql={r.sql} loading={r.loading} error={r.failed ? '—' : undefined} empty={!rows.length} className={className}>
      <div className="-mx-1 overflow-x-auto">
        <Table className="text-[12.5px]">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="h-9 px-2 text-[11.5px] font-bold text-muted-foreground">{model.label(by[by.length - 1])}</TableHead>
              {metrics.map((k) => (
                <TableHead key={k} className="h-9 px-2 text-right">
                  <button type="button" onClick={() => setSort((s) => ({ k, asc: s.k === k ? !s.asc : false }))}
                    className={cn('inline-flex items-center gap-1 text-[11.5px] font-bold whitespace-nowrap text-muted-foreground hover:text-foreground', sort.k === k && 'text-primary hover:text-primary')}>
                    {model.label(k)}
                    {sort.k === k ? (sort.asc ? <ArrowUpIcon className="size-3" /> : <ArrowDownIcon className="size-3" />) : <ArrowUpDownIcon className="size-3 opacity-40" />}
                  </button>
                </TableHead>
              ))}
              {onRow && <TableHead className="w-6 px-0" />}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((x) => {
              const go = onRow?.(x);
              const lv = num(x[lead]);
              return (
                <TableRow key={by.map((d) => String(x[d])).join('/')} onClick={go} className={cn(go && 'cursor-pointer hover:bg-brand-soft/60')}>
                  <TableCell className="px-2 py-2">
                    <span className="flex items-center gap-2 whitespace-nowrap">
                      <b className="font-bold">{String(x[by[by.length - 1]])}</b>
                      {by.slice(0, -1).map((d) => <small key={d} className="text-[11px] text-muted-foreground">{String(x[d])}</small>)}
                    </span>
                  </TableCell>
                  {metrics.map((k, i) => (
                    <TableCell key={k} className={cn('px-2 py-2 text-right tabular-nums whitespace-nowrap', bad?.includes(k) && top[k] > 0 && num(x[k]) >= top[k] * 0.8 && 'font-bold text-destructive')}>
                      {i === 0 && model.isShare(k) && x[k] != null ? (
                        <span className="inline-flex items-center gap-2">
                          <span className="h-[5px] w-12 overflow-hidden rounded-full bg-[#e2e7f4] max-md:hidden">
                            <i className={cn('block h-full rounded-full', lv < 0.65 ? 'bg-destructive' : lv < 0.75 ? 'bg-warning' : 'bg-success')} style={{ width: `${Math.min(1, lv) * 100}%` }} />
                          </span>
                          <b>{model.fmt(k, x[k])}</b>
                        </span>
                      ) : x[k] == null ? <span className="text-muted-foreground/50">—</span> : model.fmt(k, x[k])}
                    </TableCell>
                  ))}
                  {onRow && <TableCell className="px-0 py-2">{go && <ChevronRightIcon className="size-3.5 text-muted-foreground" />}</TableCell>}
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </ChartCard>
  );
}

/** A few categories side by side: a share bar on the first metric, then each one's figures. */
function Split({ by, metrics, title, aside, order, className }: { by: string; metrics: string[]; title: string; aside?: boolean; order?: string[]; className?: string }) {
  const { company, w, model } = useCtx();
  const r = useMerged(company, metrics, { group_by: [by], where: w.where });
  const lead = metrics[0];
  const rows = [...r.rows].sort((a, b) => {
    if (order) {
      const ia = order.indexOf(String(a[by])); const ib = order.indexOf(String(b[by]));
      if (ia >= 0 || ib >= 0) return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
    }
    return num(b[lead]) - num(a[lead]);
  });
  const total = rows.reduce((s, x) => s + num(x[lead]), 0);
  const colors = ['var(--chart-1)', 'var(--chart-5)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)'];
  return (
    <ChartCard title={title} aside={aside && total > 0 && <span className="text-xs font-bold tabular-nums">{model.label(lead)} {model.fmt(lead, total)}</span>}
      sql={r.sql} loading={r.loading} error={r.failed ? '—' : undefined} empty={!rows.length} className={className}>
      <div className="flex h-3 overflow-hidden rounded-full bg-muted">
        {rows.map((x, i) => <i key={i} className="block h-full" style={{ width: `${(num(x[lead]) / Math.max(1e-9, total)) * 100}%`, background: colors[i % colors.length] }} />)}
      </div>
      <div className="mt-3 grid gap-2" style={{ gridTemplateColumns: `repeat(${Math.min(rows.length, 3)}, minmax(0,1fr))` }}>
        {rows.map((x, i) => (
          <div key={i} className="min-w-0 rounded-xl bg-surface px-3 py-2.5">
            <span className="flex items-center gap-1.5 text-[12.5px] font-bold">
              <i className="size-2.5 shrink-0 rounded-[3px]" style={{ background: colors[i % colors.length] }} />
              <span className="truncate">{String(x[by])}</span>
            </span>
            <dl className="mt-1.5 grid gap-1 text-xs">
              {metrics.map((m) => (
                <div key={m} className="flex justify-between gap-2">
                  <dt className="truncate text-muted-foreground">{model.label(m)}</dt>
                  <dd className="shrink-0 font-extrabold tabular-nums">{model.fmt(m, x[m])}</dd>
                </div>
              ))}
            </dl>
          </div>
        ))}
      </div>
    </ChartCard>
  );
}

function Downtime({ className }: { className?: string }) {
  const { company, w, model } = useCtx();
  const cat = useAnswer(company, { metrics: ['downtime_minutes'], group_by: ['downtime_category'], where: w.where });
  const why = useAnswer(company, { metrics: ['downtime_minutes'], group_by: ['downtime_reason'], where: w.where });
  const cats = records(cat.data);
  const total = cats.reduce((s, x) => s + num(x.downtime_minutes), 0);
  return (
    <ChartCard title={model.label('downtime_minutes')} aside={total > 0 && <span className="text-xs font-bold tabular-nums">{money(total).join('')}</span>}
      sql={[cat.data?.sql ?? '', why.data?.sql ?? '']} loading={cat.loading || why.loading} error={cat.error ?? why.error} empty={!cats.length} className={className}>
      <h4 className="mb-1 text-xs font-bold text-muted-foreground">{model.label('downtime_category')}</h4>
      <Bars rows={cats} metric="downtime_minutes" by="downtime_category" color="var(--chart-3)"
        valueText={(r) => `${money(num(r.downtime_minutes)).join('')}·${total ? Math.round((num(r.downtime_minutes) / total) * 100) : 0}%`} />
      <h4 className="mt-3 mb-1 text-xs font-bold text-muted-foreground">{model.label('downtime_reason')} Top 10</h4>
      <Bars rows={records(why.data)} metric="downtime_minutes" by="downtime_reason" max={10} color="var(--chart-4)" />
    </ChartCard>
  );
}

function Heats({ className }: { className?: string }) {
  const { company, w, model } = useCtx();
  const r = useAnswer(company, { metrics: ['lot_scrap_rate', 'lot_scrap', 'lot_produced'], group_by: ['heat_no', 'supplier'], where: w.where });
  const rows = records(r.data).filter((x) => num(x.lot_produced) > 100).sort((a, b) => num(b.lot_scrap_rate) - num(a.lot_scrap_rate)).slice(0, 8);
  const top = num(rows[0]?.lot_scrap_rate);
  return (
    <ChartCard title={`${model.label('heat_no')} Top 8`} sql={r.data?.sql} loading={r.loading} error={r.error} empty={!rows.length} className={className}>
      <div className="-mx-1 overflow-x-auto">
        <Table className="text-[12.5px]">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              {['heat_no', 'supplier', 'lot_scrap_rate', 'lot_scrap', 'lot_produced'].map((c, i) => (
                <TableHead key={c} className={cn('h-8 px-2 text-[11.5px] font-bold whitespace-nowrap text-muted-foreground', i > 1 && 'text-right')}>{model.label(c)}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((x) => (
              <TableRow key={`${x.heat_no}/${x.supplier}`}>
                <TableCell className="px-2 py-1.5 font-mono text-[12px] font-bold">{String(x.heat_no)}</TableCell>
                <TableCell className="px-2 py-1.5 whitespace-nowrap">{String(x.supplier)}</TableCell>
                <TableCell className="px-2 py-1.5 text-right">
                  <span className="relative inline-block min-w-14 rounded-md px-1.5 py-0.5 font-bold text-destructive tabular-nums">
                    <i className="absolute inset-0 rounded-md bg-destructive/12" style={{ opacity: 0.25 + (0.75 * num(x.lot_scrap_rate)) / Math.max(top, 1e-9) }} />
                    <span className="relative">{model.fmt('lot_scrap_rate', x.lot_scrap_rate)}</span>
                  </span>
                </TableCell>
                <TableCell className="px-2 py-1.5 text-right tabular-nums">{plainNumber(num(x.lot_scrap))}</TableCell>
                <TableCell className="px-2 py-1.5 text-right tabular-nums">{plainNumber(num(x.lot_produced))}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </ChartCard>
  );
}

// ---- sections -------------------------------------------------------------------------

const GRID = 'grid grid-cols-1 gap-3 lg:grid-cols-2 xl:grid-cols-3 max-md:gap-2.5';
const WIDE = 'lg:col-span-2';
const SIDE = 'max-xl:lg:col-span-2';

function Production() {
  const { model } = useCtx();
  return (
    <>
      <KpiRow items={[
        ['oee', 'up', 'OEE'], ['availability', 'up', '时间开动率'], ['performance', 'up', '性能开动率'], ['first_pass_yield', 'up', '一次合格率'],
        ['scrap_rate', 'down', '废品率'], ['on_time_rate', 'up', '准时完工率'], ['kwh_per_tonne', 'down', '吨锻件电耗'], ['good_tonnes', 'up', '合格吨位'],
      ]} />
      <div className={GRID}>
        <Trend metric="oee" series="plant_name" title={`每日${model.label('oee')}`} className={WIDE} />
        <Split by="change_trigger" metrics={['tool_changes', 'avg_changeover_minutes']} title={model.label('change_trigger')} aside className={SIDE} />
        <Breakdown by={['plant_name', 'line_name']} metrics={['oee', 'availability', 'performance', 'scrap_rate', 'good_tonnes']} title={model.label('line_name')}
          sort={{ k: 'oee', asc: true }} bad={['scrap_rate']} className={WIDE}
          onRow={(x) => (knownLine(String(x.line_name)) ? () => goLine(String(x.plant_name), String(x.line_name)) : undefined)} />
        <Downtime className={SIDE} />
      </div>
    </>
  );
}

function Quality() {
  const { model } = useCtx();
  return (
    <>
      <KpiRow items={[
        ['first_pass_yield', 'up'], ['scrap_rate', 'down'], ['defects', 'down'],
        ['customer_ppm', 'down'], ['complaints', 'down'], ['open_complaints', 'down'],
      ]} />
      <div className={GRID}>
        <Trend metric="scrap_rate" series="plant_name" title={`每日${model.label('scrap_rate')}`} className={WIDE} />
        <BarCard title={model.label('supplier')} metric="lot_scrap_rate" by="supplier" color="var(--chart-4)"
          q={{ metrics: ['lot_scrap_rate'], group_by: ['supplier'] }} className={SIDE} />
        <BarCard title={model.label('defect_type')} metric="defects" by="defect_type" color="var(--chart-5)"
          q={{ metrics: ['defects'], group_by: ['defect_type'] }} className={WIDE} />
        <Heats className={SIDE} />
        <Breakdown by={['customer']} metrics={['customer_ppm', 'complaints', 'complained_units', 'open_complaints']} title={model.label('customer')} bad={['customer_ppm']} className={WIDE} />
        <BarCard title={model.label('complaint_type')} metric="complaints" by="complaint_type" color="var(--chart-4)"
          q={{ metrics: ['complaints'], group_by: ['complaint_type'] }} className={SIDE} />
      </div>
    </>
  );
}

function Sales() {
  const { model } = useCtx();
  return (
    <>
      <KpiRow items={[
        ['revenue', 'up'], ['contribution_margin', 'up'], ['contribution_margin_rate', 'up'], ['shipped_tonnes', 'up'],
        ['shipments', 'up'], ['delivery_on_time_rate', 'up'], ['avg_transit_hours', 'down'], ['customer_ppm', 'down'],
      ]} />
      <div className={GRID}>
        <Trend metric="revenue" kind="bar" title={`每日${model.label('revenue')}`} className={WIDE} />
        <BarCard title={model.label('shipment_part')} metric="revenue" by="shipment_part" max={10} q={{ metrics: ['revenue'], group_by: ['shipment_part'] }} className={SIDE} />
        <Breakdown by={['customer']} metrics={['revenue', 'contribution_margin', 'contribution_margin_rate', 'shipped_tonnes', 'shipments', 'delivery_on_time_rate', 'avg_transit_hours']}
          title={model.label('customer')} className="lg:col-span-2 xl:col-span-3" />
      </div>
    </>
  );
}

function Logistics() {
  const { model } = useCtx();
  return (
    <>
      <KpiRow items={[
        ['shipments', 'up'], ['trucks', 'down'], ['shipped_tonnes', 'up'], ['deliveries', 'up'], ['delivery_on_time_rate', 'up'], ['avg_transit_hours', 'down'],
      ]} />
      <div className={GRID}>
        <Trend metric="shipped_tonnes" kind="bar" title={`每日${model.label('shipped_tonnes')}`} className={WIDE} />
        <Split by="shipment_status" metrics={['shipments', 'shipped_tonnes']} title={model.label('shipment_status')} className={SIDE} />
        <Breakdown by={['carrier']} metrics={['delivery_on_time_rate', 'avg_transit_hours', 'shipments', 'trucks', 'shipped_tonnes']}
          title={model.label('carrier')} sort={{ k: 'delivery_on_time_rate', asc: true }} className={WIDE} />
        <BarCard title={`${model.label('carrier')} · ${model.label('avg_transit_hours')}`} metric="avg_transit_hours" by="carrier" color="var(--chart-3)"
          q={{ metrics: ['avg_transit_hours'], group_by: ['carrier'] }} className={SIDE} />
      </div>
    </>
  );
}

function Purchase() {
  const { model } = useCtx();
  return (
    <>
      <KpiRow items={[
        ['purchase_amount', 'down'], ['purchase_tonnes', 'up'], ['avg_steel_price', 'down'], ['purchase_lots', 'up'],
        ['supplier_on_time_rate', 'up'], ['late_purchases', 'down'], ['incoming_rejects', 'down'], ['incoming_reject_rate', 'down'],
      ]} />
      <div className={GRID}>
        <Trend metric="avg_steel_price" series="purchase_material" title={model.label('avg_steel_price')} className={WIDE} />
        <BarCard title={model.label('purchase_material')} metric="purchase_amount" by="purchase_material" q={{ metrics: ['purchase_amount'], group_by: ['purchase_material'] }} className={SIDE} />
        <Breakdown by={['purchase_supplier']} metrics={['purchase_amount', 'purchase_tonnes', 'avg_steel_price', 'supplier_on_time_rate', 'incoming_reject_rate']}
          title={model.label('purchase_supplier')} bad={['incoming_reject_rate']} className={WIDE} />
        <BarCard title={model.label('reject_reason')} metric="incoming_rejects" by="reject_reason" color="var(--chart-4)"
          q={{ metrics: ['incoming_rejects'], group_by: ['reject_reason'] }} className={SIDE} />
      </div>
    </>
  );
}

function Cost() {
  const { model } = useCtx();
  return (
    <>
      <KpiRow items={[
        ['energy_cost', 'down'], ['power_cost', 'down'], ['fuel_cost', 'down'], ['avg_power_price', 'down'],
        ['energy_cost_per_tonne', 'down'], ['tooling_cost', 'down'], ['gross_margin', 'up'], ['gross_margin_rate', 'up'],
      ]} />
      <div className={GRID}>
        <Stacked metrics={['power_cost', 'fuel_cost']} title={`每日${model.label('energy_cost')}`} className={WIDE} />
        <Split by="shift" metrics={['avg_power_price', 'power_cost', 'energy_kwh']} order={SHIFT_ORDER} title={model.label('shift')} className={SIDE} />
        <Breakdown by={['plant_name']} metrics={['gross_margin', 'gross_margin_rate', 'energy_cost', 'energy_cost_per_tonne', 'tooling_cost']} title={model.label('plant_name')} className={WIDE} />
        <Trend metric="gross_margin_rate" title={`每日${model.label('gross_margin_rate')}`} className={SIDE} />
      </div>
    </>
  );
}

export const SECTIONS: [BizSection, string][] = [
  ['prod', '生产'], ['quality', '质量'], ['sales', '客户与交付'], ['logistics', '物流'], ['purchase', '采购'], ['cost', '成本'],
];

const VIEWS: Record<BizSection, () => React.ReactNode> = {
  prod: Production, quality: Quality, sales: Sales, logistics: Logistics, purchase: Purchase, cost: Cost,
};

export function Dashboard({ company, model }: { company: string; model: Model }) {
  const w = useWindow();
  const section = useBiz((s) => s.section);
  const View = VIEWS[section] ?? Production;
  const ctx = useMemo(() => ({ company, w, model }), [company, w, model]);
  return (
    <DashCtx.Provider value={ctx}>
      <div className="grid gap-3 max-md:gap-2.5">
        <Filters className="md:hidden" />
        <View />
      </div>
    </DashCtx.Provider>
  );
}
