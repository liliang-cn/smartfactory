// 生产计划: the open orders laid out on the lines — a Gantt by line, the figures, each order with
// why it is late — re-planned for a goal, moved by hand (drag on a desktop, a dialog on a phone),
// locked where they are, compared with the plan in force and published to the lines' queues; and
// a plan checked on the simulation, an urgent order tried before it is taken.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';
import {
  CalendarClockIcon, CircleAlertIcon, FlaskConicalIcon, HammerIcon, LoaderCircleIcon, LockIcon, LockOpenIcon, MoveIcon, PackageXIcon, PlusIcon,
  SendIcon, Trash2Icon, WandSparklesIcon, ZapIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { call, ApiError, type Company } from './api';
import { Panel } from './Panel';

type Goal = 'due' | 'balance' | 'setup';
interface Why { kind: 'capacity' | 'material' | 'die' | 'due' | 'setup' | 'down'; text: string }
interface Planned {
  no: string; part: string; name?: string; customer?: string; qty: number; due?: string; line: string; lineName: string; seq: number;
  setupStart: string; start: string; end: string; setupMin: number; lateMin: number; late: boolean;
  why?: Why[]; flags?: Why[]; dies?: string[]; running?: boolean; locked?: boolean; held?: boolean; lines?: string[];
}
interface LineKPI {
  key: string; name: string; plantName: string; orders: number; util: number; busyUntil?: string; setups: number; setupHours: number;
  rate: number; rateSource: string; setupMin: number; setupSource: string; down?: string;
}
interface KPI { planned: number; held: number; onTime: number; onTimePct: number; late: number; lateHours: number; setups: number; setupHours: number; util: number; end?: string; cost: number }
interface Result { goal: Goal; now: string; orders: Planned[]; held: Planned[]; lines: LineKPI[]; kpi: KPI; unplaceable?: Planned[]; evals: number; millis: number }
interface Plan { id: string; status: string; goal: Goal; created: string; by: string; published?: string; note?: string; result: Result }
interface Change { no: string; part: string; from: string; fromName: string; fromSeq: number; to: string; toName: string; toSeq: number }
interface Diff { against: string; line: Change[]; order: Change[]; held: string[]; released: string[]; count: number; text: string; kpi: KPI; baseKpi: KPI; baseLabel: string }
interface Stop { line: string; from: number; to: number; reason?: string }
interface Settings { goal: Goal; material: 'hold' | 'plan'; arrive: Record<string, string>; stops: Stop[]; locks: Record<string, { line: string; start: string }> }
interface CheckRow { no: string; line: string; planStart: string; planEnd: string; runStart?: string; runEnd?: string; devHours?: number; due?: string; good?: number; qty?: number; open?: boolean }
interface WhatIf {
  status: 'running' | 'done' | 'failed'; plan: string; planId: string; days: number; started: string; took?: number; error?: string;
  result?: { hours: number; orders: CheckRow[]; finished: number; due: number; meanDev: number; maxDev: number; planOnTime: number; runOnTime: number; withDue: number; verdict: string };
}
interface Overview { draft: Plan | null; published: Plan | null; current: Result; diff: Diff | null; suggest: string[]; settings: Settings; goals: { id: Goal; label: string; note: string }[]; whatIf?: WhatIf; edit: boolean }
interface Push { no: string; customer?: string; part: string; line: string; was: string; now: string; delayMin: number; due?: string; late: boolean; wasLate: boolean }
interface Impact { order: Planned; onTime: boolean; earliest?: string; pushed: Push[]; newlyLate: number; before: KPI; after: KPI; against: string; note: string }

const path = (c: string, p = '') => `/api/plan/${encodeURIComponent(c)}${p}`;
const GOAL: Record<Goal, string> = { due: '交期优先', balance: '平衡', setup: '少换型' };

const pad = (n: number) => String(n).padStart(2, '0');
const when = (iso?: string) => {
  if (!iso) return '';
  const d = new Date(iso);
  return `${d.getMonth() + 1}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
};
const hrs = (min: number) => (min < 60 ? `${Math.round(min)} 分钟` : min < 48 * 60 ? `${(min / 60).toFixed(1)} 小时` : `${(min / 1440).toFixed(1)} 天`);
const localInput = (iso?: string) => {
  if (!iso) return '';
  const d = new Date(iso);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

/** A part's colour on the Gantt: the same part, the same colour. */
function hue(part: string) {
  let h = 0;
  for (const ch of part) h = (h * 31 + ch.charCodeAt(0)) % 360;
  return h;
}
const partColor = (part: string) => `oklch(0.84 0.09 ${hue(part)})`;
const partEdge = (part: string) => `oklch(0.62 0.13 ${hue(part)})`;

const errText = (e: unknown) => (e instanceof ApiError && e.problems.length ? e.problems.join('；') : (e as Error).message);

export function PlanPanel({ c }: { c: Company }) {
  const [ov, setOv] = useState<Overview | null>(null);
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState('');
  const [goal, setGoal] = useState<Goal>('due');
  const [pick, setPick] = useState<Planned | null>(null);
  const [moving, setMoving] = useState<Planned | null>(null);
  const [publishing, setPublishing] = useState(false);
  const load = useCallback(() => call<Overview>('GET', path(c.id)).then((v) => { setOv(v); setError(undefined); }, (e) => setError(errText(e))), [c.id]);
  useEffect(() => { load(); const t = setInterval(load, 20_000); return () => clearInterval(t); }, [load]);
  useEffect(() => { if (ov) setGoal(ov.settings.goal); }, [ov?.settings.goal]); // eslint-disable-line react-hooks/exhaustive-deps

  const act = async (label: string, fn: () => Promise<unknown>, ok?: string) => {
    setBusy(label);
    try {
      await fn();
      if (ok) toast.success(ok);
      await load();
    } catch (e) { toast.error(errText(e)); } finally { setBusy(''); }
  };
  if (error) return <Panel><p className="text-[13px] text-destructive">生产计划读不出来：{error}</p></Panel>;
  if (!ov) return <Panel><p className="flex items-center gap-2 text-[13px] text-muted-foreground"><LoaderCircleIcon className="size-4 animate-spin" />在排……</p></Panel>;

  const shown = ov.draft?.result ?? ov.current;
  const which = ov.draft ? 'draft' : ov.published ? 'published' : 'queue';
  const replan = () => act('replan', () => call('POST', path(c.id, '/replan'), { goal }), `已按「${GOAL[goal]}」排好一份草稿`);
  const move = (no: string, line: string, at: number) => act('move', () => call('POST', path(c.id, '/move'), { no, line, at }), `已把 ${no} 挪过去，草稿里看看效果`);
  const lock = (o: Planned, on: boolean) => act('lock', () => call('POST', path(c.id, '/lock'), { no: o.no, lock: on }), on ? `${o.no} 锁定在${o.lineName}，重新排也不动它` : `${o.no} 解锁了`);
  const saveSettings = (st: Partial<Settings>) => act('settings', () => call('PUT', path(c.id, '/settings'), { ...ov.settings, ...st }), '已保存，重新排一次才会用上');
  const empty = shown.orders.length === 0 && shown.held.length === 0;

  return (
    <div className="grid min-w-0 gap-5">
      <Panel
        title={<span className="flex flex-wrap items-center gap-2">生产计划<Tag which={which} plan={ov.draft ?? ov.published} /></span>}
        aside={ov.edit && (
          <div className="flex flex-wrap items-center justify-end gap-2">
            <div className="flex rounded-lg bg-surface p-0.5" role="radiogroup" aria-label="排产目标">
              {ov.goals.map((g) => (
                <button key={g.id} type="button" role="radio" aria-checked={goal === g.id} title={g.note}
                  className={cn('h-8 rounded-md px-2.5 text-[12.5px] font-bold text-muted-foreground', goal === g.id && 'bg-card text-foreground shadow-sm')}
                  onClick={() => setGoal(g.id)}>{g.label}</button>
              ))}
            </div>
            <Button className="h-9 rounded-lg font-bold" disabled={!!busy} onClick={replan}>
              {busy === 'replan' ? <LoaderCircleIcon className="animate-spin" /> : <WandSparklesIcon />}重新排
            </Button>
            {ov.draft && <Button variant="outline" className="h-9 rounded-lg font-bold" disabled={!!busy} onClick={() => setPublishing(true)}><SendIcon />发布</Button>}
            {ov.draft && <Button variant="ghost" size="icon" className="size-9 rounded-lg text-muted-foreground" title="丢掉草稿" aria-label="丢掉草稿" disabled={!!busy}
              onClick={() => act('discard', () => call('DELETE', path(c.id, '/draft')), '草稿丢掉了')}><Trash2Icon /></Button>}
          </div>
        )}
      >
        {(ov.suggest ?? []).length > 0 && (
          <div className="mb-4 flex gap-2 rounded-xl bg-warning-soft px-3 py-2.5 text-[12.5px] text-warning">
            <CircleAlertIcon className="mt-0.5 size-4 flex-none" />
            <span className="min-w-0">{(ov.suggest ?? []).join('；')}</span>
          </div>
        )}
        {ov.draft && ov.diff && <p className="mb-3 text-[12.5px] text-muted-foreground">草稿：{ov.draft.note}（{ov.draft.by}，{when(ov.draft.created)}）。{ov.diff.text}</p>}
        {!ov.draft && ov.published && <p className="mb-3 text-[12.5px] text-muted-foreground">产线在按这份计划做：{ov.published.by} {when(ov.published.published)} 发布（{GOAL[ov.published.goal]}）。下面是按现在的情况重新算的时间。</p>}
        {!ov.draft && !ov.published && !empty && <p className="mb-3 text-[12.5px] text-muted-foreground">还没排过计划，这是各线按交期先后排队的样子。{ov.edit ? '点「重新排」排一份更好的。' : ''}</p>}
        {empty
          ? <p className="text-[13px] text-muted-foreground">没有排队的工单，没什么要排的。工单从 ERP 来，或者在「工单」页录入；来了以后这里按产线排出先后和完工时间。</p>
          : <KpiStrip k={shown.kpi} base={ov.draft && ov.diff ? ov.diff.baseKpi : undefined} />}
      </Panel>

      {!empty && (
        <Panel title="甘特图" aside={<span className="text-[12px] text-muted-foreground max-sm:hidden">{ov.edit ? '拖动工单换线、换先后；点一下看详情、锁定' : '点一下看详情'}</span>}>
          <Gantt r={shown} edit={ov.edit} busy={!!busy} onPick={setPick} onMove={move} />
          <Legend />
        </Panel>
      )}

      {!empty && <OrderList r={shown} edit={ov.edit} onPick={setPick} onLock={lock} onMove={setMoving} />}

      {!empty && (shown.held.length > 0 || ov.edit) && (
        <Material r={shown} settings={ov.settings} edit={ov.edit} busy={!!busy} onSave={saveSettings} />
      )}

      {(!empty || ov.settings.stops.length > 0) && <LinesPanel r={shown} settings={ov.settings} edit={ov.edit} onSave={saveSettings} />}

      <div className="grid min-w-0 gap-5 lg:grid-cols-2">
        {!empty && <WhatIfPanel c={c.id} ov={ov} onDone={load} />}
        <RushPanel c={c.id} />
      </div>

      {pick && <OrderDialog o={pick} r={shown} edit={ov.edit} onClose={() => setPick(null)} onLock={(on) => { lock(pick, on); setPick(null); }} onMove={() => { setMoving(pick); setPick(null); }} />}
      {moving && <MoveDialog o={moving} r={shown} onClose={() => setMoving(null)} onMove={(line, at) => { move(moving.no, line, at); setMoving(null); }} />}
      {publishing && ov.draft && <PublishDialog c={c.id} ov={ov} onClose={() => setPublishing(false)} onDone={load} />}
    </div>
  );
}

function Tag({ which, plan }: { which: string; plan: Plan | null }) {
  const t = { draft: ['草稿，还没发布', 'bg-warning-soft text-warning'], published: ['发布中', 'bg-success-soft text-success'], queue: ['按交期排队', 'bg-surface text-muted-foreground'] }[which]!;
  return <span className={cn('rounded-md px-2 py-0.5 text-[12px] font-bold', t[1])} title={plan ? `${plan.id}` : undefined}>{t[0]}</span>;
}

function KpiStrip({ k, base }: { k: KPI; base?: KPI }) {
  const delta = (now: number, was: number | undefined, unit: string, better: 'up' | 'down', digits = 0) => {
    if (was === undefined) return null;
    const d = now - was;
    if (Math.abs(d) < (digits ? 0.05 : 0.5)) return <small className="text-muted-foreground">和现在一样</small>;
    const good = better === 'up' ? d > 0 : d < 0;
    return <small className={good ? 'text-success' : 'text-destructive'}>{d > 0 ? '多' : '少'} {Math.abs(d).toFixed(digits)}{unit}</small>;
  };
  const items = [
    { label: '按期交', value: `${k.onTime}/${k.planned}`, sub: `${k.onTimePct}%`, d: delta(k.onTime, base?.onTime, ' 张', 'up'), tone: k.late ? '' : 'text-success' },
    { label: '误期', value: `${k.late} 张`, sub: `共 ${k.lateHours} 小时`, d: delta(k.lateHours, base?.lateHours, ' 小时', 'down', 1), tone: k.late ? 'text-destructive' : '' },
    { label: '换型', value: `${k.setups} 次`, sub: `共 ${k.setupHours} 小时`, d: delta(k.setups, base?.setups, ' 次', 'down') },
    { label: '平均负荷', value: `${Math.round(k.util * 100)}%`, sub: '有活的线', d: null },
    { label: '全部做完', value: k.end ? when(k.end) : '—', sub: k.held ? `另有 ${k.held} 张缺料没排` : '没有缺料暂缓', d: null, tone: k.held ? 'text-warning' : '' },
  ];
  return (
    <div className="grid grid-cols-5 gap-2 max-lg:grid-cols-3 max-sm:grid-cols-2">
      {items.map((x) => (
        <div key={x.label} className="grid content-start gap-0.5 rounded-xl bg-surface px-3 py-2.5">
          <span className="text-[12px] text-muted-foreground">{x.label}</span>
          <b className={cn('text-[18px] leading-tight tabular-nums', x.tone)}>{x.value}</b>
          <span className="text-[12px] text-muted-foreground tabular-nums">{x.sub}</span>
          {x.d}
        </div>
      ))}
    </div>
  );
}

const ZOOM = [{ id: 'day', label: '按小时', pph: 36 }, { id: 'week', label: '按天', pph: 9 }, { id: 'month', label: '按周', pph: 2.5 }] as const;
const ROW = 46;

/** The Gantt: a row per line, time across, a bar per order, its changeover before it. */
function Gantt({ r, edit, busy, onPick, onMove }: { r: Result; edit: boolean; busy: boolean; onPick(o: Planned): void; onMove(no: string, line: string, at: number): void }) {
  const [zoom, setZoom] = useState<(typeof ZOOM)[number]['id']>('week');
  const box = useRef<HTMLDivElement>(null);
  // a short plan stretches to the box; a long one scrolls inside it
  const [boxW, setBoxW] = useState(0);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setBoxW(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const [drag, setDrag] = useState<{ no: string; x0: number; y0: number; dx: number; dy: number } | null>(null);
  const rows = useMemo(() => {
    const used = new Set(r.orders.flatMap((o) => [o.line, ...(o.lines ?? [])]));
    return r.lines.filter((l) => used.has(l.key));
  }, [r]);
  const now = Date.parse(r.now);
  const t0 = Math.floor(now / 3_600_000) * 3_600_000 - 3_600_000;
  const last = Math.max(now + 24 * 3_600_000, ...r.orders.map((o) => Date.parse(o.end)));
  const t1 = Math.min(last + 4 * 3_600_000, now + 60 * 86_400_000);
  const pph = Math.max(ZOOM.find((z) => z.id === zoom)!.pph, boxW > 0 ? boxW / ((t1 - t0) / 3_600_000) : 0);
  const x = (t: number) => ((t - t0) / 3_600_000) * pph;
  const width = Math.max(x(t1), 320);
  const days: number[] = [];
  for (let d = new Date(t0); d.getTime() < t1; d.setDate(d.getDate() + 1)) {
    const m = new Date(d); m.setHours(0, 0, 0, 0);
    if (m.getTime() >= t0) days.push(m.getTime());
    d = new Date(m);
  }
  const hours: number[] = [];
  if (pph >= 20) for (let t = t0; t < t1; t += 3_600_000) hours.push(t);
  const rowOf = (key: string) => rows.findIndex((l) => l.key === key);

  const down = (e: React.PointerEvent, o: Planned) => {
    if (!edit || busy || o.running || o.locked || e.pointerType === 'touch') return;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
    setDrag({ no: o.no, x0: e.clientX, y0: e.clientY, dx: 0, dy: 0 });
  };
  const moveP = (e: React.PointerEvent) => { if (drag) setDrag({ ...drag, dx: e.clientX - drag.x0, dy: e.clientY - drag.y0 }); };
  const up = (o: Planned) => {
    if (!drag || drag.no !== o.no) { onPick(o); return; }
    const d = drag;
    setDrag(null);
    if (Math.abs(d.dx) < 5 && Math.abs(d.dy) < 5) { onPick(o); return; }
    const row = Math.max(0, Math.min(rows.length - 1, rowOf(o.line) + Math.round(d.dy / ROW)));
    const target = rows[row];
    if (!(o.lines ?? [o.line]).includes(target.key)) { toast.error(`${target.name}做不了 ${o.part}`); return; }
    const at = Date.parse(o.setupStart) + (d.dx / pph) * 3_600_000;
    const ahead = r.orders.filter((x) => x.line === target.key && !x.running && x.no !== o.no && Date.parse(x.setupStart) < at).length;
    onMove(o.no, target.key, ahead);
  };

  return (
    <div className="grid min-w-0 gap-2">
      <div className="flex items-center gap-1 text-[12px]">
        {ZOOM.map((z) => (
          <button key={z.id} type="button" className={cn('h-7 rounded-md px-2 font-bold text-muted-foreground', zoom === z.id && 'bg-surface text-foreground')} onClick={() => setZoom(z.id)}>{z.label}</button>
        ))}
        <span className="ml-auto text-muted-foreground tabular-nums">{when(r.now)} 起</span>
      </div>
      <div className="flex min-w-0 overflow-hidden rounded-xl border">
        <div className="w-[104px] flex-none border-r bg-card max-sm:w-[76px]">
          <div className="h-8 border-b" />
          {rows.map((l) => (
            <div key={l.key} className="flex flex-col justify-center border-b px-2 last:border-b-0" style={{ height: ROW }}>
              <b className="truncate text-[12.5px] leading-tight">{l.name}</b>
              <small className={cn('text-[11px] tabular-nums', l.util > 0.9 ? 'text-destructive' : 'text-muted-foreground')}>负荷 {Math.round(l.util * 100)}%</small>
            </div>
          ))}
        </div>
        <div ref={box} className="min-w-0 flex-1 overflow-x-auto overscroll-x-contain">
          <div className="relative" style={{ width, height: 32 + rows.length * ROW }}>
            {/* the time axis */}
            {days.map((d) => (
              <div key={d} className="absolute top-0 bottom-0 border-l border-border/70" style={{ left: x(d) }}>
                <span className="absolute top-1.5 left-1 text-[11px] font-bold whitespace-nowrap text-muted-foreground">{new Date(d).getMonth() + 1}-{pad(new Date(d).getDate())} 周{'日一二三四五六'[new Date(d).getDay()]}</span>
              </div>
            ))}
            {hours.map((h) => new Date(h).getHours() % 2 === 0 && (
              <span key={h} className="absolute top-[18px] text-[10px] text-muted-foreground/80 tabular-nums" style={{ left: x(h) + 2 }}>{pad(new Date(h).getHours())}</span>
            ))}
            <div className="absolute top-8 right-0 left-0 border-t" />
            {rows.map((l, i) => i > 0 && <div key={l.key} className="absolute right-0 left-0 border-t border-border/60" style={{ top: 32 + i * ROW }} />)}
            <div className="absolute top-0 bottom-0 w-px bg-primary" style={{ left: x(now) }} title="现在" />
            {r.orders.map((o) => {
              const row = rowOf(o.line);
              if (row < 0) return null;
              const s0 = Date.parse(o.setupStart), s = Date.parse(o.start), e = Date.parse(o.end);
              const dragging = drag?.no === o.no;
              const top = 32 + row * ROW + 7;
              const w = Math.max(3, x(e) - x(s));
              const flagged = (o.flags ?? []).some((f) => f.kind === 'material') ;
              const die = (o.flags ?? []).some((f) => f.kind === 'die');
              return (
                <div key={o.no} className={cn('absolute', dragging && 'z-10 opacity-80')} style={{ left: x(s0), top, transform: dragging ? `translate(${drag.dx}px, ${drag.dy}px)` : undefined }}>
                  {s > s0 && (
                    <div className="absolute top-0 h-8 rounded-l-md border border-dashed border-muted-foreground/50 bg-[repeating-linear-gradient(135deg,var(--muted)_0_4px,transparent_4px_8px)]"
                      style={{ left: 0, width: x(s) - x(s0) }} title={`换型 ${o.setupMin} 分钟`} />
                  )}
                  <button type="button"
                    className={cn('absolute top-0 flex h-8 items-center gap-1 overflow-hidden rounded-md border px-1.5 text-left text-[11.5px] font-bold text-foreground shadow-sm select-none',
                      o.late && 'ring-2 ring-destructive', o.running && 'opacity-90', edit && !o.running && !o.locked && 'cursor-grab active:cursor-grabbing')}
                    style={{ left: x(s) - x(s0), width: w, background: partColor(o.part), borderColor: o.late ? 'var(--destructive)' : partEdge(o.part) }}
                    title={`${o.no} ${o.part} ×${o.qty}\n${when(o.start)} – ${when(o.end)}${o.due ? `\n交期 ${when(o.due)}` : ''}${o.late ? `\n晚 ${hrs(o.lateMin)}` : ''}`}
                    onPointerDown={(ev) => down(ev, o)} onPointerMove={moveP} onPointerUp={() => up(o)} onPointerCancel={() => setDrag(null)}
                    aria-label={`${o.no}，${o.lineName}，${when(o.start)} 到 ${when(o.end)}${o.late ? '，误期' : ''}`}>
                    {o.locked && <LockIcon className="size-3 flex-none" />}
                    {o.running && <span className="size-1.5 flex-none rounded-full bg-primary" />}
                    {flagged && <PackageXIcon className="size-3 flex-none text-warning" />}
                    {die && <HammerIcon className="size-3 flex-none text-warning" />}
                    {w > 46 && <span className="truncate">{o.no}</span>}
                    {o.late && w > 90 && <span className="ml-auto flex-none rounded bg-destructive px-1 text-[10.5px] text-white">晚{hrs(o.lateMin)}</span>}
                  </button>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}

function Legend() {
  return (
    <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1.5 text-[12px] text-muted-foreground">
      <span className="flex items-center gap-1.5"><span className="h-3 w-5 rounded-sm border border-dashed border-muted-foreground/50 bg-[repeating-linear-gradient(135deg,var(--muted)_0_3px,transparent_3px_6px)]" />换型</span>
      <span className="flex items-center gap-1.5"><span className="h-3 w-5 rounded-sm bg-card ring-2 ring-destructive" />误期</span>
      <span className="flex items-center gap-1.5"><PackageXIcon className="size-3.5 text-warning" />缺料</span>
      <span className="flex items-center gap-1.5"><HammerIcon className="size-3.5 text-warning" />模具要注意</span>
      <span className="flex items-center gap-1.5"><LockIcon className="size-3.5" />锁定</span>
      <span className="flex items-center gap-1.5"><span className="size-2 rounded-full bg-primary" />在做</span>
      <span>颜色按料号</span>
    </div>
  );
}

function Reasons({ o, max = 3 }: { o: Planned; max?: number }) {
  const list = [...(o.late ? o.why ?? [] : []), ...(o.flags ?? [])].slice(0, max);
  if (!list.length) return <span className="text-muted-foreground">—</span>;
  const tone: Record<Why['kind'], string> = { capacity: 'text-foreground', material: 'text-warning', die: 'text-warning', due: 'text-destructive', setup: 'text-muted-foreground', down: 'text-destructive' };
  return <ul className="grid gap-0.5">{list.map((w, i) => <li key={i} className={cn('text-[12px] leading-snug', tone[w.kind])}>{w.text}</li>)}</ul>;
}

function OrderList({ r, edit, onPick, onLock, onMove }: { r: Result; edit: boolean; onPick(o: Planned): void; onLock(o: Planned, on: boolean): void; onMove(o: Planned): void }) {
  const [only, setOnly] = useState<'all' | 'late' | 'flag'>('all');
  const list = r.orders.filter((o) => only === 'all' || (only === 'late' ? o.late : (o.flags ?? []).length > 0));
  const byTime = [...list].sort((a, b) => Date.parse(a.start) - Date.parse(b.start));
  return (
    <Panel title="工单" aside={
      <div className="flex rounded-lg bg-surface p-0.5 text-[12.5px]">
        {([['all', '全部'], ['late', `误期 ${r.kpi.late}`], ['flag', '有提醒']] as const).map(([k, l]) => (
          <button key={k} type="button" className={cn('h-7 rounded-md px-2.5 font-bold text-muted-foreground', only === k && 'bg-card text-foreground shadow-sm')} onClick={() => setOnly(k)}>{l}</button>
        ))}
      </div>
    }>
      <Table>
        <TableHeader>
          <TableRow className="text-[12px]">
            <TableHead>工单</TableHead><TableHead className="max-sm:hidden">产线</TableHead><TableHead>开工 → 完工</TableHead><TableHead className="max-md:hidden">交期</TableHead>
            <TableHead className="w-[32%] max-lg:hidden">晚的原因、提醒</TableHead>{edit && <TableHead />}
          </TableRow>
        </TableHeader>
        <TableBody>
          {byTime.map((o) => (
            <TableRow key={o.no} className="cursor-pointer" onClick={() => onPick(o)}>
              <TableCell className="whitespace-normal">
                <b className="text-[13px]">{o.no}</b>{o.locked && <LockIcon className="ml-1 inline size-3" />}
                <small className="block text-muted-foreground">{o.part} × {o.qty}{o.customer ? `，${o.customer}` : ''}</small>
                <small className="block text-muted-foreground sm:hidden">{o.lineName}</small>
              </TableCell>
              <TableCell className="text-[12.5px] max-sm:hidden">{o.lineName}<small className="block text-muted-foreground">{o.running ? '在做' : `第 ${o.seq} 个`}{o.setupMin > 0 ? `，换型 ${o.setupMin} 分` : ''}</small></TableCell>
              <TableCell className="text-[12.5px] tabular-nums whitespace-normal">
                {when(o.start)}<span className="text-muted-foreground"> → </span>{when(o.end)}
                {o.late && <small className="block font-bold text-destructive">晚 {hrs(o.lateMin)}</small>}
              </TableCell>
              <TableCell className="text-[12.5px] tabular-nums max-md:hidden">{when(o.due) || <span className="text-muted-foreground">没写</span>}</TableCell>
              <TableCell className="whitespace-normal max-lg:hidden"><Reasons o={o} /></TableCell>
              {edit && (
                <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                  {!o.running && (
                    <div className="flex justify-end gap-0.5">
                      {!o.locked && <Button size="icon-sm" variant="ghost" className="rounded-lg" title="挪到别处" aria-label={`挪 ${o.no}`} onClick={() => onMove(o)}><MoveIcon /></Button>}
                      <Button size="icon-sm" variant="ghost" className="rounded-lg" title={o.locked ? '解锁' : '锁定在这里'} aria-label={o.locked ? `解锁 ${o.no}` : `锁定 ${o.no}`} onClick={() => onLock(o, !o.locked)}>
                        {o.locked ? <LockOpenIcon /> : <LockIcon />}
                      </Button>
                    </div>
                  )}
                </TableCell>
              )}
            </TableRow>
          ))}
          {byTime.length === 0 && <TableRow><TableCell colSpan={6} className="text-[12.5px] text-muted-foreground">没有这样的工单</TableCell></TableRow>}
        </TableBody>
      </Table>
      {(r.unplaceable ?? []).length > 0 && <p className="mt-3 rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive">没有产线能做：{r.unplaceable!.map((o) => `${o.no}（${o.part}）`).join('、')}</p>}
    </Panel>
  );
}

function OrderDialog({ o, r, edit, onClose, onLock, onMove }: { o: Planned; r: Result; edit: boolean; onClose(): void; onLock(on: boolean): void; onMove(): void }) {
  const names = new Map(r.lines.map((l) => [l.key, l.name]));
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-[480px]">
        <DialogHeader>
          <DialogTitle>{o.no}{o.locked && <LockIcon className="ml-1.5 inline size-4" />}</DialogTitle>
          <DialogDescription>{o.part}{o.name ? ` ${o.name}` : ''} × {o.qty}{o.customer ? `，${o.customer}` : ''}</DialogDescription>
        </DialogHeader>
        <dl className="grid grid-cols-[72px_1fr] gap-x-3 gap-y-1.5 text-[13px]">
          <dt className="text-muted-foreground">产线</dt><dd>{o.lineName}{o.running ? '（在做）' : `，第 ${o.seq} 个`}</dd>
          {o.setupMin > 0 && <><dt className="text-muted-foreground">换型</dt><dd className="tabular-nums">{when(o.setupStart)} 起 {o.setupMin} 分钟</dd></>}
          <dt className="text-muted-foreground">开工</dt><dd className="tabular-nums">{when(o.start)}</dd>
          <dt className="text-muted-foreground">完工</dt><dd className="tabular-nums">{when(o.end)}</dd>
          <dt className="text-muted-foreground">交期</dt><dd className="tabular-nums">{when(o.due) || '没写'}{o.late && <b className="ml-2 text-destructive">晚 {hrs(o.lateMin)}</b>}</dd>
          {(o.dies ?? []).length > 0 && <><dt className="text-muted-foreground">模具</dt><dd>{o.dies!.join('、')}</dd></>}
          {(o.lines ?? []).length > 1 && <><dt className="text-muted-foreground">也能在</dt><dd>{o.lines!.filter((k) => k !== o.line).map((k) => names.get(k) ?? k).join('、')}</dd></>}
        </dl>
        {o.late && (o.why ?? []).length > 0 && <div className="rounded-xl bg-danger-soft px-3 py-2"><b className="text-[12.5px] text-destructive">为什么晚</b><ul className="mt-1 grid gap-0.5 text-[12.5px]">{o.why!.map((w, i) => <li key={i}>{w.text}</li>)}</ul></div>}
        {(o.flags ?? []).length > 0 && <div className="rounded-xl bg-warning-soft px-3 py-2"><b className="text-[12.5px] text-warning">要注意</b><ul className="mt-1 grid gap-0.5 text-[12.5px]">{o.flags!.map((w, i) => <li key={i}>{w.text}</li>)}</ul></div>}
        {edit && !o.running && (
          <DialogFooter className="gap-2">
            {!o.locked && <Button variant="outline" className="rounded-lg font-bold" onClick={onMove}><MoveIcon />挪到别处</Button>}
            <Button className="rounded-lg font-bold" onClick={() => onLock(!o.locked)}>{o.locked ? <><LockOpenIcon />解锁</> : <><LockIcon />锁定在这里</>}</Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** Moving an order without dragging: a line it can run on, and which order it goes before. */
function MoveDialog({ o, r, onClose, onMove }: { o: Planned; r: Result; onClose(): void; onMove(line: string, at: number): void }) {
  const lines = r.lines.filter((l) => (o.lines ?? [o.line]).includes(l.key));
  const [line, setLine] = useState(o.line);
  const others = r.orders.filter((x) => x.line === line && !x.running && x.no !== o.no);
  const [at, setAt] = useState(String(Math.max(0, others.findIndex((x) => Date.parse(x.start) > Date.parse(o.start)))));
  useEffect(() => { setAt(String(others.length)); }, [line]); // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="sm:max-w-[440px]">
        <DialogHeader><DialogTitle>挪 {o.no}</DialogTitle><DialogDescription>{o.part} × {o.qty}，现在在{o.lineName}第 {o.seq} 个</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <label className="grid gap-1.5 text-[12.5px] font-bold">放到哪条线
            <Select value={line} onValueChange={setLine}>
              <SelectTrigger className="h-10 w-full rounded-lg"><SelectValue /></SelectTrigger>
              <SelectContent>{lines.map((l) => <SelectItem key={l.key} value={l.key}>{l.plantName} {l.name}（负荷 {Math.round(l.util * 100)}%）</SelectItem>)}</SelectContent>
            </Select>
          </label>
          <label className="grid gap-1.5 text-[12.5px] font-bold">排在哪
            <Select value={at} onValueChange={setAt}>
              <SelectTrigger className="h-10 w-full rounded-lg"><SelectValue /></SelectTrigger>
              <SelectContent>
                {others.map((x, i) => <SelectItem key={x.no} value={String(i)}>排在 {x.no}（{x.part}，{when(x.start)}）前面</SelectItem>)}
                <SelectItem value={String(others.length)}>排在最后</SelectItem>
              </SelectContent>
            </Select>
          </label>
          <p className="text-[12px] text-muted-foreground">挪完是草稿，时间重新算；满意了再发布。想让它重新排也不动，挪完点锁定。</p>
        </div>
        <DialogFooter><Button variant="outline" className="rounded-lg" onClick={onClose}>取消</Button><Button className="rounded-lg font-bold" onClick={() => onMove(line, Number(at))}>挪过去</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PublishDialog({ c, ov, onClose, onDone }: { c: string; ov: Overview; onClose(): void; onDone(): void }) {
  const d = ov.diff!;
  const [busy, setBusy] = useState(false);
  const publish = async () => {
    setBusy(true);
    try {
      const res = await call<{ changed: number }>('POST', path(c, '/publish'));
      toast.success(`已发布：产线按这份计划的顺序做（${res.changed} 张工单定了线和先后）`);
      onClose();
      onDone();
    } catch (e) { toast.error(errText(e)); } finally { setBusy(false); }
  };
  const rows = [...d.line.map((x) => ({ ...x, kind: '换线' })), ...d.order.map((x) => ({ ...x, kind: '换先后' }))];
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>发布这份计划</DialogTitle>
          <DialogDescription>发布后，各线排队的工单按这份计划定线、定先后，产线马上照着做。{d.text}。</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-2 text-[12.5px]">
          <div className="rounded-xl bg-surface px-3 py-2"><span className="text-muted-foreground">{d.baseLabel}</span><b className="block tabular-nums">按期 {d.baseKpi.onTime}/{d.baseKpi.planned}，误期 {d.baseKpi.lateHours} 小时，换型 {d.baseKpi.setups} 次</b></div>
          <div className="rounded-xl bg-brand-soft px-3 py-2"><span className="text-muted-foreground">这份草稿</span><b className="block tabular-nums">按期 {d.kpi.onTime}/{d.kpi.planned}，误期 {d.kpi.lateHours} 小时，换型 {d.kpi.setups} 次</b></div>
        </div>
        {rows.length > 0 && (
          <ul className="grid max-h-[40dvh] gap-1 overflow-y-auto text-[12.5px]">
            {rows.map((x) => (
              <li key={x.no} className="flex flex-wrap gap-x-2 rounded-lg px-2 py-1 odd:bg-surface">
                <b>{x.no}</b><span className="text-muted-foreground">{x.part}</span>
                <span className="ml-auto tabular-nums">{x.kind === '换线' ? `${x.fromName} → ${x.toName}` : `${x.toName}：第 ${x.fromSeq} → 第 ${x.toSeq} 个`}</span>
              </li>
            ))}
          </ul>
        )}
        {d.held.length > 0 && <p className="rounded-xl bg-warning-soft px-3 py-2 text-[12.5px] text-warning">缺料先不让产线做：{d.held.join('、')}（到料后重新排、再发布）</p>}
        <DialogFooter><Button variant="outline" className="rounded-lg" onClick={onClose}>再看看</Button><Button className="rounded-lg font-bold" disabled={busy} onClick={publish}>{busy ? <LoaderCircleIcon className="animate-spin" /> : <SendIcon />}发布</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Orders short of material: held back or planned as they are, and when their material comes. */
function Material({ r, settings, edit, busy, onSave }: { r: Result; settings: Settings; edit: boolean; busy: boolean; onSave(st: Partial<Settings>): void }) {
  const short = [...r.held, ...r.orders.filter((o) => (o.flags ?? []).some((f) => f.kind === 'material'))];
  const [arrive, setArrive] = useState<Record<string, string>>({});
  useEffect(() => { setArrive(Object.fromEntries(Object.entries(settings.arrive).map(([k, v]) => [k, localInput(v)]))); }, [settings.arrive]);
  const save = () => {
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(arrive)) if (v) out[k] = new Date(v).toISOString();
    onSave({ arrive: out });
  };
  return (
    <Panel title={<span className="flex items-center gap-2"><PackageXIcon className="size-4 text-warning" />缺料</span>} aside={edit && (
      <div className="flex rounded-lg bg-surface p-0.5 text-[12.5px]">
        {([['hold', '缺料先不排'], ['plan', '缺料也照排']] as const).map(([k, l]) => (
          <button key={k} type="button" disabled={busy} className={cn('h-7 rounded-md px-2.5 font-bold text-muted-foreground', settings.material === k && 'bg-card text-foreground shadow-sm')}
            onClick={() => settings.material !== k && onSave({ material: k })}>{l}</button>
        ))}
      </div>
    )}>
      {short.length === 0 ? (
        <p className="text-[12.5px] text-muted-foreground">排队的工单按 BOM 展开，库里的料都够（没有 BOM 的料号不算）。</p>
      ) : (
        <div className="grid gap-2">
          <p className="text-[12.5px] text-muted-foreground">
            {settings.material === 'hold' ? '缺料又不知道什么时候到的工单先不排，也不让产线做。' : '缺料的工单照排，但料不到开不了工。'}
            知道什么时候到料就填上，计划会从那时起排。
          </p>
          {short.map((o) => (
            <div key={o.no} className="grid grid-cols-[1fr_auto] items-center gap-2 rounded-xl bg-surface px-3 py-2 max-sm:grid-cols-1">
              <div className="min-w-0 text-[12.5px]">
                <b>{o.no}</b> <span className="text-muted-foreground">{o.part} × {o.qty}</span>
                {o.held && <span className="ml-1.5 rounded bg-warning-soft px-1.5 text-[11.5px] font-bold text-warning">先不排</span>}
                <span className="block text-warning">{(o.flags ?? []).find((f) => f.kind === 'material')?.text ?? o.why?.[0]?.text}</span>
              </div>
              {edit && (
                <label className="flex items-center gap-1.5 text-[12px] text-muted-foreground">预计到料
                  <Input type="datetime-local" className="h-8 w-[190px] rounded-lg text-[12.5px]" value={arrive[o.no] ?? ''} onChange={(e) => setArrive({ ...arrive, [o.no]: e.target.value })} />
                </label>
              )}
            </div>
          ))}
          {edit && <div><Button variant="outline" className="h-8 rounded-lg font-bold" disabled={busy} onClick={save}><CalendarClockIcon />保存到料时间</Button></div>}
        </div>
      )}
    </Panel>
  );
}

/** Each line: how loaded, how fast and why that fast, how long a changeover takes; planned stops. */
function LinesPanel({ r, settings, edit, onSave }: { r: Result; settings: Settings; edit: boolean; onSave(st: Partial<Settings>): void }) {
  const lines = r.lines.filter((l) => l.orders > 0 || settings.stops.some((s) => s.line === l.key));
  const [add, setAdd] = useState<{ line: string; from: string; to: string; reason: string } | null>(null);
  const names = new Map(r.lines.map((l) => [l.key, l.name]));
  return (
    <Panel title="产线负荷" aside={edit && <Button variant="outline" className="h-8 rounded-lg font-bold" onClick={() => setAdd({ line: r.lines[0]?.key ?? '', from: '', to: '', reason: '计划检修' })}><PlusIcon />停线计划</Button>}>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-2 max-sm:grid-cols-1">
        {lines.map((l) => (
          <div key={l.key} className="grid gap-1.5 rounded-xl bg-surface px-3 py-2.5">
            <div className="flex items-baseline gap-2"><b className="text-[13px]">{l.name}</b><small className="text-muted-foreground">{l.plantName}</small><b className={cn('ml-auto text-[13px] tabular-nums', l.util > 0.9 && 'text-destructive')}>{Math.round(l.util * 100)}%</b></div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className={cn('h-full rounded-full', l.util > 0.9 ? 'bg-destructive' : 'bg-primary')} style={{ width: `${Math.min(100, l.util * 100)}%` }} /></div>
            <small className="text-[11.5px] leading-snug text-muted-foreground">{l.orders} 张，排到 {when(l.busyUntil)}；换型 {l.setups} 次</small>
            <small className="text-[11.5px] leading-snug text-muted-foreground">每件 {l.rate.toFixed(0)} 秒：{l.rateSource}</small>
            <small className="text-[11.5px] leading-snug text-muted-foreground">换型 {l.setupMin} 分钟：{l.setupSource}</small>
            {l.down && <small className="text-[11.5px] font-bold text-destructive">现在停着：{l.down}</small>}
          </div>
        ))}
      </div>
      {settings.stops.length > 0 && (
        <ul className="mt-3 grid gap-1 text-[12.5px]">
          {settings.stops.map((s, i) => (
            <li key={i} className="flex items-center gap-2 rounded-lg bg-warning-soft px-3 py-1.5">
              <span className="min-w-0"><b>{names.get(s.line) ?? s.line}</b> {when(new Date(s.from * 1000).toISOString())} – {when(new Date(s.to * 1000).toISOString())} 停线{s.reason ? `：${s.reason}` : ''}</span>
              {edit && <Button size="icon-sm" variant="ghost" className="ml-auto rounded-lg" aria-label="删掉这个停线计划" onClick={() => onSave({ stops: settings.stops.filter((_, j) => j !== i) })}><Trash2Icon /></Button>}
            </li>
          ))}
        </ul>
      )}
      {add && (
        <Dialog open onOpenChange={(v) => !v && setAdd(null)}>
          <DialogContent className="sm:max-w-[420px]">
            <DialogHeader><DialogTitle>停线计划</DialogTitle><DialogDescription>检修、试模、停电：这段时间这条线不排活。</DialogDescription></DialogHeader>
            <div className="grid gap-3 text-[12.5px] font-bold">
              <label className="grid gap-1.5">哪条线
                <Select value={add.line} onValueChange={(v) => setAdd({ ...add, line: v })}>
                  <SelectTrigger className="h-10 w-full rounded-lg"><SelectValue /></SelectTrigger>
                  <SelectContent>{r.lines.map((l) => <SelectItem key={l.key} value={l.key}>{l.plantName} {l.name}</SelectItem>)}</SelectContent>
                </Select>
              </label>
              <label className="grid gap-1.5">从<Input type="datetime-local" className="h-10 rounded-lg" value={add.from} onChange={(e) => setAdd({ ...add, from: e.target.value })} /></label>
              <label className="grid gap-1.5">到<Input type="datetime-local" className="h-10 rounded-lg" value={add.to} onChange={(e) => setAdd({ ...add, to: e.target.value })} /></label>
              <label className="grid gap-1.5">为什么<Input className="h-10 rounded-lg" value={add.reason} onChange={(e) => setAdd({ ...add, reason: e.target.value })} /></label>
            </div>
            <DialogFooter>
              <Button variant="outline" className="rounded-lg" onClick={() => setAdd(null)}>取消</Button>
              <Button className="rounded-lg font-bold" disabled={!add.from || !add.to} onClick={() => {
                onSave({ stops: [...settings.stops, { line: add.line, from: Math.round(Date.parse(add.from) / 1000), to: Math.round(Date.parse(add.to) / 1000), reason: add.reason }] });
                setAdd(null);
              }}>加上</Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </Panel>
  );
}

/** A plan run on the simulation for some days: planned against simulated finish times. */
function WhatIfPanel({ c, ov, onDone }: { c: string; ov: Overview; onDone(): void }) {
  const [w, setW] = useState<WhatIf | undefined>(ov.whatIf);
  const [which, setWhich] = useState<'draft' | 'published'>(ov.draft ? 'draft' : 'published');
  const [days, setDays] = useState('2');
  useEffect(() => { setW(ov.whatIf); }, [ov.whatIf]);
  useEffect(() => {
    if (w?.status !== 'running') return;
    const t = setInterval(() => call<WhatIf | null>('GET', path(c, '/whatif')).then((v) => {
      if (v) setW(v);
      if (v && v.status !== 'running') onDone();
    }, () => {}), 3000);
    return () => clearInterval(t);
  }, [w?.status, c, onDone]);
  const start = async () => {
    try { setW(await call<WhatIf>('POST', path(c, '/whatif'), { plan: which, days: Number(days) })); } catch (e) { toast.error(errText(e)); }
  };
  const can = (which === 'draft' && ov.draft) || (which === 'published' && ov.published);
  const res = w?.result;
  return (
    <Panel title={<span className="flex items-center gap-2"><FlaskConicalIcon className="size-4" />仿真推演</span>}>
      <p className="mb-3 text-[12.5px] text-muted-foreground">把计划放到孪生里按真实节拍、故障、换模跑几天，看计划的完工时间准不准。不影响现场。</p>
      <div className="flex flex-wrap items-center gap-2">
        <Select value={which} onValueChange={(v) => setWhich(v as 'draft' | 'published')}>
          <SelectTrigger className="h-9 w-[132px] rounded-lg"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value="draft" disabled={!ov.draft}>草稿</SelectItem><SelectItem value="published" disabled={!ov.published}>发布的计划</SelectItem></SelectContent>
        </Select>
        <Select value={days} onValueChange={setDays}>
          <SelectTrigger className="h-9 w-[100px] rounded-lg"><SelectValue /></SelectTrigger>
          <SelectContent>{['1', '2', '3', '7'].map((d) => <SelectItem key={d} value={d}>跑 {d} 天</SelectItem>)}</SelectContent>
        </Select>
        <Button className="h-9 rounded-lg font-bold" disabled={!can || w?.status === 'running'} onClick={start}>
          {w?.status === 'running' ? <LoaderCircleIcon className="animate-spin" /> : <FlaskConicalIcon />}{w?.status === 'running' ? '推演中' : '开始推演'}
        </Button>
      </div>
      {!can && <p className="mt-2 text-[12px] text-muted-foreground">先排一份草稿或发布一份计划。</p>}
      {w?.status === 'failed' && <p className="mt-3 text-[12.5px] text-destructive">推演没成：{w.error}</p>}
      {res && w?.status === 'done' && (
        <div className="mt-3 grid gap-2">
          <p className={cn('rounded-xl px-3 py-2 text-[12.5px] font-bold', res.meanDev <= 1 && res.finished >= res.due * 0.8 ? 'bg-success-soft text-success' : 'bg-warning-soft text-warning')}>{res.verdict}</p>
          <p className="text-[12px] text-muted-foreground tabular-nums">{w.plan === 'draft' ? '草稿' : '发布的计划'}，仿真了 {res.hours} 小时（用时 {w.took} 秒）：做完 {res.finished} 张；有交期的 {res.withDue} 张里，计划按期 {res.planOnTime}，仿真按期 {res.runOnTime}</p>
          <div className="max-h-[260px] overflow-y-auto">
            <Table>
              <TableHeader><TableRow className="text-[12px]"><TableHead>工单</TableHead><TableHead>计划完工</TableHead><TableHead>仿真完工</TableHead><TableHead className="text-right">差</TableHead></TableRow></TableHeader>
              <TableBody>
                {res.orders.map((o) => (
                  <TableRow key={o.no} className="text-[12.5px] tabular-nums">
                    <TableCell><b>{o.no}</b><small className="block text-muted-foreground">{o.line}</small></TableCell>
                    <TableCell>{when(o.planEnd)}</TableCell>
                    <TableCell>{o.runEnd ? when(o.runEnd) : o.open ? <span className="text-muted-foreground">没做完 {o.good}/{o.qty}</span> : <span className="text-muted-foreground">没开始</span>}</TableCell>
                    <TableCell className={cn('text-right', o.devHours !== undefined && Math.abs(o.devHours) > 2 && 'font-bold text-warning')}>{o.devHours !== undefined ? `${o.devHours > 0 ? '+' : ''}${o.devHours} 时` : '—'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </div>
      )}
    </Panel>
  );
}

/** Trying an urgent order: where it would go, when it would be done, whom it pushes back. */
function RushPanel({ c }: { c: string }) {
  const [part, setPart] = useState('');
  const [qty, setQty] = useState('500');
  const [due, setDue] = useState(() => localInput(new Date(Date.now() + 3 * 86_400_000).toISOString()).slice(0, 10) + 'T17:00');
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<Impact | null>(null);
  const run = async () => {
    setBusy(true);
    try { setRes(await call<Impact>('POST', path(c, '/rush'), { part, qty: Number(qty), due: due.replace('T', ' ') })); } catch (e) { toast.error(errText(e)); setRes(null); } finally { setBusy(false); }
  };
  return (
    <Panel title={<span className="flex items-center gap-2"><ZapIcon className="size-4" />插单推演</span>}>
      <p className="mb-3 text-[12.5px] text-muted-foreground">来了急单先试一下：插在哪最不耽误事、赶不赶得上、会把谁往后推。只是推算，不改计划。</p>
      <div className="grid grid-cols-[1fr_90px] gap-2 sm:grid-cols-[1fr_90px_190px_auto]">
        <Input className="h-9 rounded-lg" placeholder="料号，如 QZ-B20-4" value={part} onChange={(e) => setPart(e.target.value)} aria-label="料号" />
        <Input className="h-9 rounded-lg" type="number" value={qty} onChange={(e) => setQty(e.target.value)} aria-label="数量" />
        <Input className="h-9 rounded-lg max-sm:col-span-2" type="datetime-local" value={due} onChange={(e) => setDue(e.target.value)} aria-label="交期" />
        <Button className="h-9 rounded-lg font-bold max-sm:col-span-2" disabled={busy || !part.trim() || !(Number(qty) > 0)} onClick={run}>{busy ? <LoaderCircleIcon className="animate-spin" /> : <ZapIcon />}试一下</Button>
      </div>
      {res && (
        <div className="mt-3 grid gap-2 text-[12.5px]">
          <p className={cn('rounded-xl px-3 py-2 font-bold', res.onTime && res.newlyLate === 0 ? 'bg-success-soft text-success' : 'bg-warning-soft text-warning')}>{res.note}</p>
          <p className="text-muted-foreground tabular-nums">和{res.against}比：按期 {res.before.onTime}/{res.before.planned} → {res.after.onTime}/{res.after.planned}，误期 {res.before.lateHours} → {res.after.lateHours} 小时，换型 {res.before.setups} → {res.after.setups} 次</p>
          {res.pushed.length > 0 && (
            <ul className="grid max-h-[200px] gap-1 overflow-y-auto">
              {res.pushed.map((p) => (
                <li key={p.no} className="flex flex-wrap gap-x-2 rounded-lg px-2 py-1 odd:bg-surface">
                  <b>{p.no}</b><span className="text-muted-foreground">{p.part}{p.customer ? `，${p.customer}` : ''}，{p.line}</span>
                  <span className={cn('ml-auto tabular-nums', p.late && !p.wasLate && 'font-bold text-destructive')}>推后 {hrs(p.delayMin)}{p.late && !p.wasLate ? '，会误期' : ''}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Panel>
  );
}
