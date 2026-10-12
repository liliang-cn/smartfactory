// 成效: what the twin and its AI did for the company over a period — the advisor's plans taken and
// what they changed, the AI's proposals and commands, calibration, orders on time, and the plant
// week by week. Every number shows how many records it was counted from (n) and from what; where
// there is nothing to count, it says 没有数据. A CSV and a printed page carry the same.
import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { ArrowDownToLineIcon, CircleAlertIcon, FactoryIcon, LightbulbIcon, LoaderCircleIcon, PrinterIcon, ReceiptTextIcon, SlidersHorizontalIcon, SparklesIcon, TrendingUpIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import * as api from './api';
import { Panel } from './Panel';

const PRESETS = [7, 30, 90] as const;

const num = (v: number) => {
  const a = Math.abs(v);
  const s = v.toFixed(a >= 100 ? 0 : a >= 10 ? 1 : 2);
  return s.includes('.') ? s.replace(/\.?0+$/, '') : s;
};
/** A value as the report writes it: fractions as %, the rest with their unit. */
export const show = (v: number | null | undefined, unit?: string) => {
  if (v === null || v === undefined) return null;
  if (unit === '%') return `${(v * 100).toFixed(1)}%`;
  return unit ? `${num(v)} ${unit}` : num(v);
};
const day = (iso: string) => new Date(iso).toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' });
const date = (iso: string) => new Date(iso).toLocaleDateString('zh-CN', { year: 'numeric', month: 'numeric', day: 'numeric' });
const ymd = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;

const NoData = () => <span className="text-muted-foreground">没有数据</span>;

function SourceTag({ source }: { source?: string }) {
  if (!source) return null;
  return source === 'measured'
    ? <span className="rounded-md bg-success-soft px-1.5 py-0.5 text-[11.5px] font-bold text-success">实测</span>
    : <span className="rounded-md bg-warning-soft px-1.5 py-0.5 text-[11.5px] font-bold text-warning">仿真</span>;
}

/** One number: what it is, the number (or 没有数据), and n with what it was counted from. */
function Tile({ f }: { f: api.ValueFigure }) {
  const v = show(f.value, f.unit);
  return (
    <li className="grid content-start gap-1 rounded-xl bg-surface p-3 break-inside-avoid">
      <span className="flex items-center gap-1.5 text-[12.5px] font-bold text-muted-foreground">{f.label}<SourceTag source={f.source} /></span>
      <b className={cn('text-[22px] leading-tight font-extrabold tabular-nums', !v && 'text-[16px] font-bold')}>{v ?? <NoData />}</b>
      <small className="text-[11.5px] leading-snug text-muted-foreground"><b className="tabular-nums">n = {f.n}</b> · {f.basis}</small>
    </li>
  );
}

function Figures({ s }: { s: api.ValueSection }) {
  if (s.missing) {
    return (
      <div className="grid gap-2">
        <p className="flex items-start gap-2 rounded-xl bg-surface p-3 text-[13px]"><CircleAlertIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />{s.missing}</p>
        {s.figures.some((f) => f.value !== null) ? <Grid figures={s.figures} /> : (
          <p className="text-[12px] text-muted-foreground">{s.figures.map((f) => f.label).join('、')}：没有数据</p>
        )}
      </div>
    );
  }
  return (
    <div className="grid gap-2.5">
      <Grid figures={s.figures} />
      {s.note && <p className="text-[12.5px] text-muted-foreground">{s.note}</p>}
    </div>
  );
}

const Grid = ({ figures }: { figures: api.ValueFigure[] }) => (
  <ul className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-5 print:grid-cols-4">
    {figures.map((f) => <Tile key={f.key} f={f} />)}
  </ul>
);

const Title = ({ icon, children }: { icon: ReactNode; children: ReactNode }) => (
  <span className="flex items-center gap-2"><span className="text-primary [&_svg]:size-4">{icon}</span>{children}</span>
);

const OUTCOME: Record<string, [string, string]> = {
  achieved: ['达成', 'bg-success-soft text-success'], missed: ['未达成', 'bg-danger-soft text-destructive'],
  guard_broken: ['护栏破了', 'bg-warning-soft text-warning'], no_data: ['无数据', 'bg-surface text-muted-foreground'],
};

function Plans({ plans }: { plans: api.PlanResult[] }) {
  if (!plans.length) return null;
  return (
    <div className="mt-4 grid gap-2">
      <h3 className="text-[13px] font-extrabold">这段时间采纳或量过的计划</h3>
      <ul className="grid gap-2">
        {plans.map((p) => {
          const o = p.accepted ? OUTCOME[p.outcome ?? ''] : undefined;
          return (
            <li key={p.id} className="grid gap-1.5 rounded-xl border p-3 break-inside-avoid">
              <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                <b className="text-[13.5px]">{p.does}</b>
                <small className="font-mono text-[11.5px] text-muted-foreground">{p.id}</small>
                <span className={cn('ml-auto rounded-md px-1.5 py-0.5 text-[11.5px] font-bold', o?.[1] ?? 'bg-brand-soft text-primary')}>{o?.[0] ?? '还没验收'}</span>
              </div>
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-[13px] tabular-nums">
                <span className="text-muted-foreground">{p.label}，要{p.want === 'down' ? '降' : '升'}</span>
                <span>基线 <b>{show(p.baseline, p.unit) ?? '—'}</b></span>
                <span>→ 量出 <b>{show(p.measured, p.unit) ?? '还没量'}</b></span>
                {p.target !== undefined && <span className="text-muted-foreground">目标 {show(p.target, p.unit)}</span>}
                {p.change !== undefined && (
                  <b className={cn(p.better ? 'text-success' : 'text-destructive')}>{p.change > 0 ? '+' : ''}{(p.change * 100).toFixed(1)}%</b>
                )}
              </div>
              <small className="text-[11.5px] text-muted-foreground">采纳于 {date(p.adopted)}{p.measured !== undefined ? `，量了 ${p.dataDays} 天的数据` : ''}</small>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/** A small bar per week; a week without data is an empty slot. */
function WeekBars({ weeks, pick, unit, label, tone }: { weeks: api.ValueWeek[]; pick(w: api.ValueWeek): number | null; unit?: string; label: string; tone: string }) {
  const vals = weeks.map(pick);
  const top = Math.max(...vals.map((v) => v ?? 0), 1e-9);
  return (
    <figure className="grid gap-1.5 break-inside-avoid">
      <figcaption className="text-[12px] font-bold text-muted-foreground">{label}</figcaption>
      <div className="flex h-28 items-end gap-1" role="img" aria-label={`${label}：${weeks.map((w, i) => `${day(w.start)} 起 ${show(vals[i], unit) ?? '没有数据'}`).join('；')}`}>
        {weeks.map((w, i) => {
          const v = vals[i];
          return (
            <div key={w.start} className="flex h-full min-w-0 flex-1 flex-col items-center justify-end gap-0.5">
              <span className="max-w-full truncate text-[10.5px] text-muted-foreground tabular-nums">{v === null ? '—' : show(v, unit === '%' ? '%' : undefined)?.replace(/\.0%$/, '%')}</span>
              {v === null
                ? <span className="h-1 w-full rounded-sm border border-dashed border-border" />
                : <span className="w-full rounded-t-md" style={{ height: `${Math.max(3, (v / top) * 80)}%`, background: tone }} />}
            </div>
          );
        })}
      </div>
      <div className="flex gap-1">
        {weeks.map((w) => <span key={w.start} className="min-w-0 flex-1 truncate text-center text-[10.5px] text-muted-foreground tabular-nums">{day(w.start)}</span>)}
      </div>
    </figure>
  );
}

function Plant({ p }: { p: api.ValueReport['plant'] }) {
  const c = p.compare;
  const shown = p.source ? p.weeks : [];
  return (
    <div className="grid gap-4">
      <Figures s={p} />
      {shown.length > 0 && (
        <div className="grid gap-2">
          <h3 className="flex items-center gap-2 text-[13px] font-extrabold">每周（周一起）<SourceTag source={p.source} /></h3>
          <div className="grid gap-4 sm:grid-cols-3">
            <WeekBars weeks={shown} pick={(w) => w.oee} unit="%" label="OEE" tone="var(--chart-1)" />
            <WeekBars weeks={shown} pick={(w) => w.outputPerDay} label="日均产量（件）" tone="var(--chart-2)" />
            <WeekBars weeks={shown} pick={(w) => w.downtimePerDay} label="日均设备停机（h）" tone="var(--chart-3)" />
          </div>
          <p className="text-[11.5px] text-muted-foreground">
            产量和停机按有数据的天平均，第一周和最后一周只算落在这段时间里的天（{shown.map((w) => `${day(w.start)} 起 ${w.days} 天`).join('，')}）。虚线是这周没有{p.source === 'measured' ? '实测' : ''}数据。
          </p>
        </div>
      )}
      {c && (
        <div className="grid gap-2 rounded-xl border p-3 break-inside-avoid">
          <h3 className="flex flex-wrap items-center gap-2 text-[13px] font-extrabold">第一条采纳的计划前后<SourceTag source={c.source} /></h3>
          <p className="text-[12.5px]"><b>{c.does}</b> <span className="text-muted-foreground">{c.plan}，采纳于 {date(c.adopted)}{c.days > 0 ? `，前后各 ${c.days} 天，采纳当天不算` : ''}</span></p>
          {c.days > 0 && c.source && (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[420px] text-[13px] tabular-nums">
                <thead>
                  <tr className="text-[12px] text-muted-foreground">
                    <th className="py-1 text-left font-semibold" /><th className="text-right font-semibold">OEE</th><th className="text-right font-semibold">日均产量</th>
                    <th className="text-right font-semibold">日均停机 h</th><th className="text-right font-semibold">有数据的天</th>
                  </tr>
                </thead>
                <tbody>
                  {([['采纳前', c.before], ['采纳后', c.after]] as const).map(([name, s]) => (
                    <tr key={name} className="border-t">
                      <td className="py-1.5 font-bold">{name}<small className="ml-1.5 font-normal text-muted-foreground">{day(s.from)}–{day(new Date(new Date(s.to).getTime() - 1).toISOString())}</small></td>
                      <td className="text-right">{show(s.oee, '%') ?? <NoData />}</td>
                      <td className="text-right">{show(s.outputPerDay) ?? <NoData />}</td>
                      <td className="text-right">{show(s.downtimePerDay) ?? <NoData />}</td>
                      <td className="text-right">{s.days}<small className="ml-1 text-muted-foreground">（{s.lineHours} 产线小时）</small></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <p className="flex items-start gap-1.5 rounded-lg bg-warning-soft/60 px-2.5 py-2 text-[12px] leading-relaxed"><CircleAlertIcon className="mt-0.5 size-3.5 shrink-0 text-warning" />{c.note}</p>
        </div>
      )}
    </div>
  );
}

function Calibration({ c }: { c: api.ValueReport['calibration'] }) {
  return (
    <div className="grid gap-3">
      <Figures s={c} />
      {c.fields.length > 0 && (
        <ul className="grid gap-1.5 text-[13px] sm:grid-cols-3">
          {c.fields.map((f) => (
            <li key={f.field} className="rounded-xl border p-3 tabular-nums break-inside-avoid">
              <span className="text-[12px] font-bold text-muted-foreground">{f.label}回放误差，模板 → 拟合</span>
              <div><s className="text-muted-foreground">{num(f.before)}</s> → <b className={f.after < f.before ? 'text-success' : 'text-destructive'}>{num(f.after)}</b> {f.unit}</div>
              <small className="text-[11.5px] text-muted-foreground">n = {f.n} 个读数，{f.machines} 台设备</small>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function PeriodPicker({ period, onChange }: { period: api.ValuePeriod; onChange(p: api.ValuePeriod): void }) {
  const custom = !('days' in period);
  const [from, setFrom] = useState(custom ? period.from : ymd(new Date(Date.now() - 29 * 864e5)));
  const [to, setTo] = useState(custom ? period.to : ymd(new Date()));
  const [open, setOpen] = useState(custom);
  return (
    <div className="flex flex-wrap items-center gap-1.5 print:hidden">
      {PRESETS.map((d) => (
        <Button key={d} size="sm" variant={'days' in period && period.days === d && !open ? 'default' : 'outline'} className="h-8 rounded-lg px-2.5 font-bold"
          onClick={() => { setOpen(false); onChange({ days: d }); }}>近 {d} 天</Button>
      ))}
      <Button size="sm" variant={open ? 'default' : 'outline'} className="h-8 rounded-lg px-2.5 font-bold" onClick={() => setOpen(true)}>自定</Button>
      {open && (
        <form className="flex flex-wrap items-center gap-1.5" onSubmit={(e) => { e.preventDefault(); if (from && to) onChange({ from, to }); }}>
          <Input type="date" value={from} max={to} onChange={(e) => setFrom(e.target.value)} className="h-8 w-[140px] rounded-lg text-[13px]" aria-label="第一天" />
          <span className="text-muted-foreground">至</span>
          <Input type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} className="h-8 w-[140px] rounded-lg text-[13px]" aria-label="最后一天" />
          <Button type="submit" size="sm" className="h-8 rounded-lg font-bold">看</Button>
        </form>
      )}
    </div>
  );
}

export function ValuePanel({ c }: { c: api.Company }) {
  const [period, setPeriod] = useState<api.ValuePeriod>({ days: 30 });
  const [v, setV] = useState<api.ValueReport | null>(null);
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const load = useCallback(() => {
    setLoading(true);
    api.value(c.id, period).then((r) => { setV(r); setError(undefined); }, (e: Error) => setError(e.message)).finally(() => setLoading(false));
  }, [c.id, period]);
  useEffect(load, [load]);
  const csv = async () => {
    setSaving(true);
    try { await api.downloadValue(c.id, period); } catch (e) { toast.error((e as Error).message); } finally { setSaving(false); }
  };
  const last = v ? new Date(new Date(v.period.to).getTime() - 1).toISOString() : '';
  return (
    <div className="grid gap-5" data-print>
      <Panel
        title={<Title icon={<TrendingUpIcon />}>成效</Title>}
        aside={(
          <div className="flex items-center gap-1.5 print:hidden">
            <Button variant="outline" size="sm" className="h-8 rounded-lg font-bold" disabled={!v || saving} onClick={csv}>{saving ? <LoaderCircleIcon className="animate-spin" /> : <ArrowDownToLineIcon />}导出 CSV</Button>
            <Button variant="outline" size="sm" className="h-8 rounded-lg font-bold" disabled={!v} onClick={() => window.print()}><PrinterIcon />打印</Button>
          </div>
        )}
      >
        <div className="grid gap-3">
          <PeriodPicker period={period} onChange={setPeriod} />
          {v && (
            <p className="text-[13px] leading-relaxed">
              <b>{v.name}</b>，{v.period.label}（{date(v.period.from)} 至 {date(last)}）。
              <span className="text-muted-foreground">每个数下面的 n 是它从多少条记录算出来的，后面是依据；没有可算的记录就写「没有数据」，不估。生成于 {new Date(v.at).toLocaleString('zh-CN')}。</span>
              {loading && <LoaderCircleIcon className="ml-1.5 inline size-3.5 animate-spin text-muted-foreground" />}
            </p>
          )}
          {error && <p className="text-[13px] text-destructive">{error}</p>}
          {!v && !error && <div className="h-24" />}
        </div>
      </Panel>
      {v && (
        <>
          <Panel title={<Title icon={<LightbulbIcon />}>顾问的建议</Title>}>
            <Figures s={v.suggestions} />
            <Plans plans={v.suggestions.plans} />
          </Panel>
          <Panel title={<Title icon={<FactoryIcon />}>工厂</Title>}><Plant p={v.plant} /></Panel>
          <Panel title={<Title icon={<SparklesIcon />}>AI 的提议和指令</Title>}><Figures s={v.ai} /></Panel>
          <Panel title={<Title icon={<SlidersHorizontalIcon />}>仿真校准</Title>}><Calibration c={v.calibration} /></Panel>
          <Panel title={<Title icon={<ReceiptTextIcon />}>工单</Title>}><Figures s={v.orders} /></Panel>
        </>
      )}
    </div>
  );
}
