// 运行状况: how the server running the factory is doing — its CPU and memory over the last hour,
// the disk the data is on and what takes it, what the simulation costs, who is connected, the
// language model's use, and how the browsers watching the twin draw it. What is off comes first,
// each with what to do about it.
import { useCallback, useEffect, useState } from 'react';
import { CircleAlertIcon, LoaderCircleIcon, RefreshCwIcon, TriangleAlertIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import * as api from './api';
import { Panel } from './Panel';

const MB = 1024 * 1024;
export const bytes = (n: number) => {
  if (!n) return '0';
  if (n >= 1024 * MB) return `${(n / 1024 / MB).toFixed(1)} GB`;
  if (n >= MB) return `${(n / MB).toFixed(n >= 100 * MB ? 0 : 1)} MB`;
  return `${Math.max(1, Math.round(n / 1024))} KB`;
};
const pct = (v: number) => `${v.toFixed(v >= 10 ? 0 : 1)}%`;
const hhmm = (iso: string) => new Date(iso).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
const since = (iso: string) => {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))} 分钟`;
  if (s < 86400 * 2) return `${(s / 3600).toFixed(1)} 小时`;
  return `${Math.round(s / 86400)} 天`;
};

/** What the files in the data directory are, in the words of the console's pages. */
const PART: Record<string, string> = {
  'series.db': '设备读数', series: '设备读数', knowledge: '知识库', agents: '助手、资料库和对话', 'checks.db': '点检记录', 'audit.db': '操作记录',
  'accounts.db': '账号和令牌', 'anomaly.db': '读数异常', 'calib.db': '校准', 'devices.db': '设备接入', 'erp.db': '工单', 'guard.db': '指令',
  'tables.db': '桌台', configs: '工厂配置', templates: '模板', 'initial-admin-token': '首个管理员令牌',
};
const partName = (n: string) => PART[n] ?? (n.endsWith('.json') ? `仿真进度（${n.replace(/\.json$/, '')}）` : n);

function Tile({ label, value, note, tone }: { label: string; value: React.ReactNode; note?: React.ReactNode; tone?: 'bad' | 'watch' }) {
  return (
    <li className="grid content-start gap-1 rounded-xl bg-surface p-3">
      <span className="text-[12.5px] font-bold text-muted-foreground">{label}</span>
      <b className={cn('text-[22px] leading-tight font-extrabold tabular-nums', tone === 'bad' && 'text-destructive', tone === 'watch' && 'text-warning')}>{value}</b>
      {note && <small className="text-[11.5px] leading-snug text-muted-foreground">{note}</small>}
    </li>
  );
}

/** An hour of one figure as a line over a soft area, with its scale's top and the time axis. */
function Trend({ samples, pick, label, unit, tone, top }: { samples: api.HealthSample[]; pick(s: api.HealthSample): number; label: string; unit: (v: number) => string; tone: string; top?: number }) {
  const pts = samples.map(pick).map((v) => (v < 0 ? null : v));
  const known = pts.filter((v): v is number => v !== null);
  const W = 300, H = 90;
  if (known.length < 2) {
    return (
      <figure className="grid gap-1.5">
        <figcaption className="text-[12px] font-bold text-muted-foreground">{label}</figcaption>
        <p className="grid h-[90px] place-items-center rounded-xl bg-surface text-[12px] text-muted-foreground">再等一分钟，攒够几个点就画出来</p>
      </figure>
    );
  }
  const hi = Math.max(top ?? 0, ...known) * 1.1 || 1;
  const x = (i: number) => (i / Math.max(1, pts.length - 1)) * W;
  const y = (v: number) => H - (v / hi) * H;
  let line = '';
  pts.forEach((v, i) => { if (v !== null) line += `${line && pts[i - 1] !== null ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`; });
  const first = pts.findIndex((v) => v !== null);
  const last = pts.length - 1 - [...pts].reverse().findIndex((v) => v !== null);
  const area = `${line}L${x(last).toFixed(1)},${H}L${x(first).toFixed(1)},${H}Z`;
  const now = known[known.length - 1];
  return (
    <figure className="grid gap-1.5">
      <figcaption className="flex items-baseline gap-2 text-[12px] font-bold text-muted-foreground">
        {label}<b className="ml-auto text-[13px] text-foreground tabular-nums">{unit(now)}</b>
      </figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="h-[90px] w-full overflow-visible rounded-xl bg-surface"
        role="img" aria-label={`${label}，最近一小时：最低 ${unit(Math.min(...known))}，最高 ${unit(Math.max(...known))}，现在 ${unit(now)}`}>
        {top !== undefined && top < hi && <line x1="0" x2={W} y1={y(top)} y2={y(top)} stroke="var(--border)" strokeDasharray="4 4" vectorEffect="non-scaling-stroke" />}
        <path d={area} fill={tone} opacity="0.14" />
        <path d={line} fill="none" stroke={tone} strokeWidth="2" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
      </svg>
      <div className="flex justify-between text-[10.5px] text-muted-foreground tabular-nums">
        <span>{hhmm(samples[0].at)}</span><span>最高 {unit(Math.max(...known))}</span><span>{hhmm(samples[samples.length - 1].at)}</span>
      </div>
    </figure>
  );
}

/** The data directory's parts as one stacked bar, the biggest first, then a list. */
function DiskUse({ d }: { d: api.Health['disk'] }) {
  const parts = (d.parts ?? []).filter((p) => p.bytes > 0);
  const tones = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)'];
  const shown = parts.slice(0, 5);
  const rest = parts.slice(5).reduce((n, p) => n + p.bytes, 0);
  const free = d.size ? (d.free / d.size) * 100 : null;
  return (
    <div className="grid gap-3">
      <p className="text-[13px]">
        数据一共 <b className="tabular-nums">{bytes(d.total)}</b>
        {d.size > 0 && <>，所在的磁盘还剩 <b className={cn('tabular-nums', free !== null && free < 10 && 'text-destructive')}>{bytes(d.free)}</b>（共 {bytes(d.size)}，剩 {pct(free!)}）</>}
      </p>
      {d.total > 0 && (
        <div className="flex h-3 overflow-hidden rounded-full bg-surface" role="img" aria-label={parts.map((p) => `${partName(p.name)} ${bytes(p.bytes)}`).join('，')}>
          {shown.map((p, i) => <span key={p.name} style={{ width: `${(p.bytes / d.total) * 100}%`, background: tones[i] }} />)}
          {rest > 0 && <span style={{ width: `${(rest / d.total) * 100}%` }} className="bg-border" />}
        </div>
      )}
      <ul className="grid gap-1 text-[12.5px] sm:grid-cols-2">
        {parts.map((p, i) => (
          <li key={p.name} className="flex items-center gap-2">
            <span className={cn('size-2.5 shrink-0 rounded-sm', i >= 5 && 'bg-border')} style={i < 5 ? { background: tones[i] } : undefined} />
            <span className="min-w-0 flex-1 truncate">{partName(p.name)}</span>
            <b className="tabular-nums">{bytes(p.bytes)}</b>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function HealthPanel() {
  const [h, setH] = useState<api.Health | null>(null);
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    setBusy(true);
    try {
      setH(await api.vitals());
      setError(undefined);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, 10_000);
    return () => clearInterval(t);
  }, [load]);

  if (!h) {
    return (
      <Panel>
        {error ? <p className="text-[13px] text-destructive">运行状况读不出来：{error}</p>
          : <div className="grid h-40 place-items-center"><LoaderCircleIcon className="animate-spin text-muted-foreground" /></div>}
      </Panel>
    );
  }
  const p = h.process, e = h.engine, b = h.browsers;
  const cores = h.host.cpus;
  const memUsed = h.host.memTotal && h.host.memFree ? 100 - (h.host.memFree / h.host.memTotal) * 100 : null;
  const stepShare = e.budgetMs ? (e.stepMs / e.budgetMs) * 100 : 0;
  return (
    <div className="grid gap-5">
      <Panel title="现在怎么样" aside={
        <Button variant="ghost" size="sm" className="h-8 rounded-lg text-muted-foreground" onClick={load} disabled={busy}>
          <RefreshCwIcon className={cn('size-3.5', busy && 'animate-spin')} />每 10 秒自动刷新
        </Button>
      }>
        {h.warnings.length > 0 ? (
          <ul className="mb-4 grid gap-2">
            {h.warnings.map((w) => (
              <li key={w.text} className={cn('flex items-start gap-2.5 rounded-xl p-3', w.level === 'bad' ? 'bg-danger-soft' : 'bg-warning-soft')}>
                {w.level === 'bad' ? <CircleAlertIcon className="mt-0.5 size-4 shrink-0 text-destructive" /> : <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-warning" />}
                <span className="grid gap-0.5"><b className="text-[13.5px]">{w.text}</b><span className="text-[12.5px] text-muted-foreground">{w.hint}</span></span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="mb-4 rounded-xl bg-success-soft p-3 text-[13px] font-bold text-success">一切正常</p>
        )}
        <ul className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <Tile label="CPU" value={p.cpu < 0 ? '—' : pct(p.cpu)} tone={p.cpu > cores * 80 ? 'bad' : undefined}
            note={p.cpu < 0 ? '这个系统不提供' : `最近一分钟；100% 是一个核占满，这台机器 ${cores} 个核`} />
          <Tile label="内存" value={bytes(p.rss)}
            note={h.host.memTotal ? `机器共 ${bytes(h.host.memTotal)}${memUsed !== null ? `，已用 ${pct(memUsed)}` : ''}` : undefined} />
          <Tile label="仿真每步" value={`${e.stepMs.toFixed(1)} ms`} tone={stepShare > 80 ? 'bad' : stepShare > 50 ? 'watch' : undefined}
            note={`每 ${e.budgetMs} ms 走一步，用了 ${pct(stepShare)}；${e.machines} 台设备`} />
          <Tile label="磁盘剩余" value={h.disk.size ? bytes(h.disk.free) : '—'} tone={h.disk.size && h.disk.free / h.disk.size < 0.1 ? 'bad' : undefined}
            note={`数据占 ${bytes(h.disk.total)}`} />
          <Tile label="在看孪生" value={h.viewers} note={b.count ? `${b.count} 个页面报了帧率，中位 ${Math.round(b.medianFps)} 帧/秒` : '还没有页面报过帧率'} />
          <Tile label="在线设备" value={h.devices} note="通过 MQTT 连着的" />
          <Tile label="大模型" value={h.model.configured ? (h.model.callsHour ?? 0) : '没配'}
            note={h.model.configured ? `近一小时的请求，${h.model.name}` : '助手和 AI 搭建用不了：在 /etc/smartfactory/env 填 LLM_ 开头的三项'} />
          <Tile label="已运行" value={since(h.started)} note={`${h.version} · ${h.host.os}`} />
        </ul>
      </Panel>

      <Panel title="最近一小时">
        <div className="grid gap-5 md:grid-cols-3">
          <Trend samples={h.samples} pick={(s) => s.cpu} label="CPU" unit={pct} tone="var(--chart-1)" top={100} />
          <Trend samples={h.samples} pick={(s) => s.rss} label="内存" unit={bytes} tone="var(--chart-2)" />
          <Trend samples={h.samples} pick={(s) => s.stepMs} label="仿真每步" unit={(v) => `${v.toFixed(1)} ms`} tone="var(--chart-3)" top={e.budgetMs} />
        </div>
      </Panel>

      <div className="grid gap-5 lg:grid-cols-2">
        <Panel title="数据占了多少">
          <DiskUse d={h.disk} />
        </Panel>
        <Panel title="仿真算的是什么">
          <p className="mb-3 text-[12.5px] text-muted-foreground">所有工厂一起每 {e.budgetMs} ms 算一步，设备越多越花时间。</p>
          <table className="w-full text-[13px]">
            <thead><tr className="text-left text-[12px] text-muted-foreground"><th className="pb-1.5 font-bold">工厂</th><th className="pb-1.5 text-right font-bold">产线</th><th className="pb-1.5 text-right font-bold">设备</th><th className="pb-1.5 text-right font-bold">在看这家公司</th></tr></thead>
            <tbody>
              {e.plants.map((x) => (
                <tr key={`${x.company}/${x.plant}`} className="border-t">
                  <td className="py-1.5">{x.name}</td>
                  <td className="py-1.5 text-right tabular-nums">{x.lines}</td>
                  <td className="py-1.5 text-right tabular-nums">{x.machines}</td>
                  <td className="py-1.5 text-right tabular-nums">{x.viewers}</td>
                </tr>
              ))}
              {!e.plants.length && <tr><td colSpan={4} className="py-3 text-center text-muted-foreground">还没有工厂</td></tr>}
            </tbody>
          </table>
        </Panel>
      </div>

      <Panel title="看孪生的电脑和手机">
        {b.count === 0 ? (
          <p className="text-[13px] text-muted-foreground">打开孪生的页面每分钟报一次画得多快、占多少内存，这里就会有数。只报这些，不报是谁在看。</p>
        ) : (
          <div className="grid gap-3">
            <ul className="grid grid-cols-2 gap-3 md:grid-cols-4">
              <Tile label="页面" value={b.count} note={b.mobile ? `其中手机 ${b.mobile} 个` : '都是电脑'} />
              <Tile label="中位帧率" value={`${Math.round(b.medianFps)}`} tone={b.medianFps < 24 ? 'watch' : undefined} note="帧/秒，30 以上就流畅" />
              <Tile label="中位内存" value={b.medianHeapMB ? `${Math.round(b.medianHeapMB)} MB` : '—'} note="页面的 JavaScript 内存" />
              <Tile label="卡的" value={b.slow.length} tone={b.slow.length ? 'watch' : undefined} note="帧率低于 24" />
            </ul>
            {b.slow.length > 0 && (
              <ul className="grid gap-1 text-[12.5px]">
                {b.slow.map((r) => (
                  <li key={r.id} className="flex flex-wrap gap-x-3 rounded-lg bg-surface px-3 py-1.5 tabular-nums">
                    <b>{Math.round(r.fps)} 帧/秒</b><span>{r.mobile ? '手机' : '电脑'}</span><span>{r.width}×{r.height} @{r.dpr}x</span>
                    {r.heapMB > 0 && <span>{Math.round(r.heapMB)} MB</span>}{r.calls > 0 && <span>每帧画 {r.calls} 次</span>}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </Panel>
    </div>
  );
}
