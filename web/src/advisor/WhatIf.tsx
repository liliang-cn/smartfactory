// A what-if (the twin's simulate_plan) as the advisor's step shows it: per line, the plant as it
// is and with the plan, each a mean with the band its runs spread over (P10 to P90), and the
// change with its own band. A change whose band sits on one side of zero is more than luck.
import { cn } from '@/lib/utils';

type Nums = Record<string, number | null | undefined>;
type Line = Nums & { line: string; p10?: Nums; p90?: Nums };
export interface WhatIf {
  hours?: number; runs?: number;
  现状: { lines: Line[] }; 方案: { lines: Line[] }; 变化: Line[];
}

/** The what-if in a step's result: the tool's answer ({status, result}) or the result itself, parsed if it came as text. */
export function whatIfOf(r: unknown): WhatIf | null {
  let v = r;
  if (typeof v === 'string') {
    try { v = JSON.parse(v); } catch { return null; }
  }
  if (!v || typeof v !== 'object') return null;
  const o = v as Record<string, unknown>;
  const w = (o.result ?? o) as Partial<WhatIf>;
  return Array.isArray(w.现状?.lines) && Array.isArray(w.方案?.lines) && Array.isArray(w.变化) ? (w as WhatIf) : null;
}

const pct = (x: number) => `${(x * 100).toFixed(1)}%`;
const int = (x: number) => Math.round(x).toLocaleString();
/** what is shown, how it reads, and which way is better */
const METRICS: { key: string; label: string; fmt(x: number): string; up: boolean }[] = [
  { key: 'oee', label: 'OEE', fmt: pct, up: true },
  { key: 'produced', label: '产量', fmt: int, up: true },
  { key: 'firstPassYield', label: '一次合格率', fmt: pct, up: true },
  { key: 'scrapRate', label: '废品率', fmt: pct, up: false },
  { key: 'availability', label: '可用率', fmt: pct, up: true },
];

const n = (v: unknown) => (typeof v === 'number' ? v : null);

/** A side's mean as a dot on its P10–P90 bar, on a scale both sides share. */
function Band({ mean, lo, hi, min, max, tone }: { mean: number; lo: number; hi: number; min: number; max: number; tone: string }) {
  const x = (v: number) => (max > min ? ((v - min) / (max - min)) * 100 : 50);
  return (
    <span className="relative mt-1 block h-1.5 rounded-full bg-muted" aria-hidden>
      <i className={cn('absolute inset-y-0 rounded-full opacity-35', tone)} style={{ left: `${x(lo)}%`, width: `${Math.max(x(hi) - x(lo), 1.5)}%` }} />
      <i className={cn('absolute top-1/2 size-2 -translate-x-1/2 -translate-y-1/2 rounded-full', tone)} style={{ left: `${x(mean)}%` }} />
    </span>
  );
}

export function WhatIfResult({ w }: { w: WhatIf }) {
  return (
    <div className="grid gap-2.5">
      <p className="text-[12px] text-muted-foreground">
        {w.hours ? `往前推 ${w.hours} 小时` : ''}{w.runs ? `，${w.runs} 组随机种子` : ''}。点是平均，条是 P10–P90（几次里第 10 到第 90 百分位）；变化的区间都在 0 的一边，才不是运气。
      </p>
      {w.变化.map((d, i) => {
        const a = w.现状.lines[i];
        const b = w.方案.lines[i];
        if (!a || !b) return null;
        return (
          <div key={d.line} className="overflow-hidden rounded-xl border">
            <b className="block bg-surface px-2.5 py-1.5 text-[12.5px]">{d.line}</b>
            <table className="w-full text-[12px] tabular-nums">
              <thead>
                <tr className="text-[11px] text-muted-foreground">
                  <th className="px-2.5 py-1 text-left font-semibold">指标</th>
                  <th className="px-2.5 py-1 text-left font-semibold">现状</th>
                  <th className="px-2.5 py-1 text-left font-semibold">方案</th>
                  <th className="px-2.5 py-1 text-right font-semibold">变化（P10 … P90）</th>
                </tr>
              </thead>
              <tbody>
                {METRICS.map((m) => {
                  const ma = n(a[m.key]), mb = n(b[m.key]), md = n(d[m.key]);
                  if (ma === null || mb === null) return null;
                  const la = n(a.p10?.[m.key]) ?? ma, ha = n(a.p90?.[m.key]) ?? ma;
                  const lb = n(b.p10?.[m.key]) ?? mb, hb = n(b.p90?.[m.key]) ?? mb;
                  const ld = n(d.p10?.[m.key]), hd = n(d.p90?.[m.key]);
                  const min = Math.min(la, lb), max = Math.max(ha, hb);
                  // one side of zero: a change, better or worse; across it: not told from luck
                  const sure = ld !== null && hd !== null && (ld > 0 || hd < 0) && md !== null && md !== 0;
                  const better = md !== null && (m.up ? md > 0 : md < 0);
                  const sign = (x: number) => `${x > 0 ? '+' : x < 0 ? '−' : '±'}${m.fmt(Math.abs(x))}`;
                  return (
                    <tr key={m.key} className="border-t">
                      <td className="px-2.5 py-1.5 font-bold">{m.label}</td>
                      <td className="w-[30%] px-2.5 py-1.5" title={`P10 ${m.fmt(la)}，P90 ${m.fmt(ha)}`}>
                        {m.fmt(ma)}<Band mean={ma} lo={la} hi={ha} min={min} max={max} tone="bg-muted-foreground" />
                      </td>
                      <td className="w-[30%] px-2.5 py-1.5" title={`P10 ${m.fmt(lb)}，P90 ${m.fmt(hb)}`}>
                        {m.fmt(mb)}<Band mean={mb} lo={lb} hi={hb} min={min} max={max} tone="bg-primary" />
                      </td>
                      <td className="px-2.5 py-1.5 text-right">
                        {md !== null && <b className={cn(sure ? (better ? 'text-success' : 'text-destructive') : 'text-muted-foreground')}>{sign(md)}</b>}
                        {ld !== null && hd !== null && <small className="ml-1 text-[11px] text-muted-foreground">{sign(ld)} … {sign(hd)}</small>}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        );
      })}
    </div>
  );
}
