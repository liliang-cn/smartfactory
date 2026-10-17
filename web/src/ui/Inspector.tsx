// Details of the selected asset: status, live tiles, trend/SPC chart, what people last wrote down
// on it, controls, recent log.
// Desktop: a card on the right. Phone: a bottom sheet that can collapse to its header.
import { useEffect, useState } from 'react';
import { CrosshairIcon, NavigationIcon, RadioTowerIcon, XIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Slider } from '@/components/ui/slider';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import { liveFields } from '@/sim/plant';
import type { Asset } from '@/sim/types';
import { runtime, usePlant, useTick, useUI } from '@/state/store';
import { fgCapacity } from '@/three/logistics';
import { AssetIcon } from './icons';
import { CHIP, hhmm, stateLabel } from './status';
import { useLive } from '@/live/client';
import { reported } from '@/sim/venue';
import { HistoryChart, RANGES, type Range, type Summary } from './History';
import { TrendChart, spcStats, type Metric } from './TrendChart';
import { InventoryList } from './Panels';
import { ManualRecords } from './ManualRecords';
import { MachineHints } from './Hints';

const isMachine = (a: Asset) => a.spec?.runRise !== undefined;
const isVehicle = (a: Asset) => a.type === 'agv' || a.type === 'truck';

function Tile({ label, wide, children, bar, tone }: { label: string; wide?: boolean; children: React.ReactNode; bar?: number; tone?: 'ok' | 'warn' | 'bad' | 'brand' }) {
  return (
    <div className={cn('min-w-0 rounded-xl bg-surface px-2.5 py-2', wide && 'col-span-2')}>
      <small className="block text-[11.5px] text-muted-foreground">{label}</small>
      <div className="mt-0.5 truncate text-base font-extrabold tracking-tight tabular-nums">{children}</div>
      {bar !== undefined && (
        <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-[#e2e7f4]">
          <i
            className={cn('block h-full rounded-full transition-[width] duration-300', {
              ok: 'bg-success', warn: 'bg-amber-500', bad: 'bg-destructive', brand: 'bg-primary',
            }[tone ?? 'brand'])}
            style={{ width: `${Math.max(0, Math.min(1, bar)) * 100}%` }}
          />
        </div>
      )}
    </div>
  );
}

const Unit = ({ children }: { children: React.ReactNode }) => <u className="ml-0.5 text-xs font-semibold text-muted-foreground no-underline">{children}</u>;

function Tiles({ a }: { a: Asset }) {
  const { sim, layout } = usePlant();
  const w = layout.words;
  if (a.type === 'table') {
    const pl = a.place!;
    const min = Math.max(0, Math.round((sim.elapsed - (a.since ?? 0)) / 60));
    const rep = reported(a);
    return (
      <>
        <Tile label="人数" bar={a.count / pl.seats} tone={a.count > pl.seats ? 'warn' : 'brand'}>{a.count}<Unit>/ {pl.seats} 座</Unit></Tile>
        <Tile label={a.state === 'occupied' ? (pl.overnight ? '已住' : '已坐') : '已有'}>{min}<Unit>分钟</Unit></Tile>
        <Tile label="区域">{pl.area}</Tile>
        <Tile label="数据来源" wide>{rep ? <>实测<Unit>{a.live!.src}，{Math.round((Date.now() - a.live!.at) / 60000)} 分钟前上报</Unit></> : <>仿真<Unit>没有系统上报这{pl.label}</Unit></>}</Tile>
      </>
    );
  }
  if (isMachine(a)) {
    const s = a.spec!;
    const m = a.measure;
    const lim = m && (m.lo !== undefined && m.hi !== undefined ? `${m.target}±${((m.hi - m.lo) / 2).toFixed(m.digits)}` : m.hi !== undefined ? `≤ ${m.hi}` : `≥ ${m.lo}`);
    return (
      <>
        {m && a.reading && (
          <>
            <Tile label={`本件${m.label}`} wide>
              {a.reading.v.toFixed(m.digits)}<Unit>{m.unit}</Unit>
              <em className={cn('ml-1.5 rounded-md px-1.5 py-px align-[2px] text-[11px] font-extrabold not-italic', a.reading.ok ? 'bg-success-soft text-success' : 'bg-danger-soft text-destructive')}>
                {a.reading.ok ? '合格' : '超差'}
              </em>
              <i className="block text-[11px] font-semibold not-italic text-muted-foreground">标准 {lim}</i>
            </Tile>
            <Tile label="本班超差">{a.rejects}<Unit>{layout.unit}</Unit></Tile>
          </>
        )}
        <Tile label="温度" bar={(a.temp - 20) / (s.trip! - 20)} tone={a.temp > s.warn! ? 'bad' : a.temp > s.warn! - 8 ? 'warn' : 'ok'}>
          {a.temp.toFixed(1)}<Unit>°C</Unit>
        </Tile>
        <Tile label="功率">{a.power.toFixed(1)}<Unit>kW</Unit></Tile>
        <Tile label="健康度" bar={a.health} tone={a.health < 0.35 ? 'bad' : a.health < 0.6 ? 'warn' : 'ok'}>
          {(a.health * 100).toFixed(0)}<Unit>%</Unit>
        </Tile>
        <Tile label={w.util} bar={a.util}>{(a.util * 100).toFixed(0)}<Unit>%</Unit></Tile>
        {a.slots ? (
          <Tile label="在用">{a.count}<Unit>/ {a.slots}</Unit></Tile>
        ) : (
          <Tile label={w.cycle}>
            {a.cycle ? a.cycle >= 600 ? <>{(a.cycle / a.speed / 60).toFixed(0)}<Unit>分钟</Unit></> : <>{(a.cycle / a.speed).toFixed(1)}<Unit>秒</Unit></> : <>{(0.55 * a.speed).toFixed(2)}<Unit>m/s</Unit></>}
          </Tile>
        )}
        <Tile label="累计次数">{a.cycles.toLocaleString()}<Unit>次</Unit></Tile>
        {a.tool && (() => {
          const t = a.tool;
          const used = t.used / t.life;
          return (
            <Tile label={`${t.label}寿命`} wide bar={a.changing > 0 ? 0 : 1 - used} tone={used >= 0.9 ? 'bad' : used >= 0.75 ? 'warn' : 'ok'}>
              {a.changing > 0
                ? <>更换中<Unit>约 {Math.ceil(a.changing / 60)} 分钟</Unit></>
                : <>{Math.max(0, Math.round((1 - used) * 100))}<Unit>% 剩余，{t.used.toLocaleString()} / {t.life.toLocaleString()} 次{(t.limit ?? 1) < 1 ? `，计划在 ${Math.round(t.limit * 100)}% 更换` : ''}</Unit></>}
            </Tile>
          );
        })()}
      </>
    );
  }
  if (a.type === 'agv') {
    const g = a.agv!;
    return (
      <>
        <Tile label="电量" bar={a.battery / 100} tone={a.battery < 18 ? 'bad' : a.battery < 35 ? 'warn' : 'ok'}>{a.battery.toFixed(0)}<Unit>%</Unit></Tile>
        <Tile label="速度">{a.vel.toFixed(2)}<Unit>m/s</Unit></Tile>
        <Tile label="功率">{a.power.toFixed(2)}<Unit>kW</Unit></Tile>
        <Tile label="当前任务" wide>{a.task || '空闲'}</Tile>
        <Tile label="载货">{g.cargo ? <>{g.cargo.n}<Unit>{g.cargo.kind === 'raw' ? `件${w.raw}` : `${layout.unit}${w.goods}`}</Unit></> : '空车'}</Tile>
      </>
    );
  }
  if (a.type === 'truck') {
    const t = a.truck!;
    const next = a.kind === 'in' && sim.supplier ? sim.incoming[0] : undefined;
    const label = (k: string) => sim.inventory.find((i) => i.key === k);
    return (
      <>
        <Tile label="承运方">{a.carrier}</Tile>
        {t.vehicle === 'rider' ? (
          <Tile label="取单">{t.load}<Unit>单</Unit></Tile>
        ) : next ? (
          <Tile label="下一趟" wide>{next.no.slice(-4)}<Unit>{hhmm(((next.eta + 8 * 3600_000) % 86_400_000) / 1000)} 到，{Object.entries(next.items).map(([k, n]) => `${label(k)?.label ?? k} ${n}`).join('、')}</Unit></Tile>
        ) : (
          <Tile label="装载">{a.kind === 'in' ? (t.phase === 'away' ? 0 : t.load) : t.load}<Unit>{a.kind === 'in' ? `件${w.raw}` : `${layout.unit}${w.goods}`}</Unit></Tile>
        )}
        <Tile label="方向">{a.kind === 'in' ? '送来' : '取走'}</Tile>
        <Tile label="当前任务" wide>{a.task || stateLabel(layout, a.state)}</Tile>
      </>
    );
  }
  if (a.type === 'buffer' || a.type === 'warehouse') {
    const feeder = a.type === 'buffer'
      ? sim.agvs.find((g) => g.route.some((w) => w.line === a.line!.id && w.act === 'drop'))?.asset
      : sim.trucks.find((t) => t.kind === 'in')?.asset;
    const f = a.stock / a.cap;
    const next = sim.incoming[0];
    const ib = sim.inbound;
    const at = (ms: number) => hhmm(((ms + 8 * 3600_000) % 86_400_000) / 1000);
    // a storeroom of what the site uses (shown below as its inventory): the next delivery is what matters
    if (a.type === 'warehouse' && layout.logistics.store !== 'racks') {
      return (
        <>
          <Tile label="下一趟配送" wide>{next ? <>{at(next.eta)}<Unit>{next.no}，{Object.entries(next.items).map(([k, n]) => `${sim.inventory.find((i) => i.key === k)?.label ?? k} ${n}`).join('、')}</Unit></> : sim.supplier ? '没有在途' : '按需补货'}</Tile>
          <Tile label={w.inTruck}>{feeder?.id ?? '—'}</Tile>
        </>
      );
    }
    return (
      <>
        <Tile label="库存" wide bar={f} tone={f < 0.15 ? 'bad' : f < 0.3 ? 'warn' : 'ok'}>{a.stock}<Unit>/ {a.cap} 件</Unit></Tile>
        <Tile label={a.type === 'buffer' ? `送料${w.vehicle}` : w.inTruck}>{feeder?.id ?? '人工'}</Tile>
        {a.type === 'warehouse' && ib && (
          <Tile label={`下一车${w.raw}`} wide tone={ib.late ? 'bad' : 'ok'} bar={ib.late && sim.wall() >= ib.due ? 1 : undefined}>
            {at(ib.due)}<Unit>{ib.supplier}，{ib.load} 件{ib.late && sim.wall() >= ib.due ? `，延误，约 ${at(ib.at)} 到` : ''}</Unit>
          </Tile>
        )}
      </>
    );
  }
  if (a.type === 'fgzone' && sim.depot) {
    const d = sim.depot;
    const name = (k: string) => layout.lines.find((l) => l.product.key === k)?.product.name ?? k;
    return (
      <>
        <Tile label="在库">{d.batches.reduce((n, b) => n + b.qty, 0)}<Unit>{layout.unit}，{d.batches.length} 批</Unit></Tile>
        <Tile label="留样">{d.samples.length}<Unit>批</Unit></Tile>
        <Tile label="今日过期" tone={d.wasted ? 'bad' : 'ok'}>{d.wasted}<Unit>{layout.unit}</Unit></Tile>
        <div className="col-span-3 grid gap-1 rounded-xl bg-surface px-2.5 py-2 text-xs">
          <small className="text-[11.5px] text-muted-foreground">批次（先进先出，最早的先发）</small>
          {d.batches.slice(0, 8).map((b) => {
            const left = (b.expires - sim.wall()) / 3600_000;
            return (
              <span key={b.no} className="grid grid-cols-[1fr_auto_auto] gap-2 tabular-nums">
                <span className="truncate"><b>{b.no}</b> {name(b.product)}</span>
                <span>{b.qty} {layout.unit}</span>
                <span className={cn(left < 12 ? 'font-bold text-destructive' : 'text-muted-foreground')}>{left > 240 ? `${Math.round(left / 24)} 天` : `${Math.max(0, left).toFixed(0)} 小时`}</span>
              </span>
            );
          })}
        </div>
      </>
    );
  }
  const cap = a.type === 'fgzone' ? fgCapacity(layout.fg) : a.cap;
  const feeder = a.type === 'fgzone'
    ? sim.trucks.find((t) => t.kind === 'out')?.asset
    : sim.agvs.find((g) => g.route.some((w) => w.line === a.line!.id && w.act === 'pick'))?.asset;
  return (
    <>
      <Tile label={a.type === 'fgzone' ? `暂存${w.goods}` : w.outfeed} wide bar={a.count / cap} tone={a.count >= cap ? 'bad' : 'ok'}>{a.count}<Unit>/ {cap} {layout.unit}</Unit></Tile>
      <Tile label={a.type === 'fgzone' ? w.outTruck : `取货${w.vehicle}`}>{feeder?.id ?? '人工'}</Tile>
    </>
  );
}

function Controls({ a }: { a: Asset }) {
  const command = useUI((s) => s.command);
  const { layout } = usePlant();
  const [speed, setSpeed] = useState(Math.round(a.speed * 100));
  useEffect(() => setSpeed(Math.round(a.speed * 100)), [a]);
  if (a.type === 'warehouse') return layout.logistics.store === 'racks' ? <Button className="h-9" onClick={() => command(a.id, 'restock')}>{layout.words.raw}入库 400 件</Button> : null;
  // a counter riders take from, or a depot whose van goes out on the delivery rounds: nothing to ship by hand
  if (a.type === 'fgzone') return layout.logistics.outbound.vehicle === 'rider' || runtime().byId[layout.id]?.sim.depot ? null : <Button className="h-9" onClick={() => command(a.id, 'ship')}>立即发运</Button>;
  if (!isMachine(a) && !isVehicle(a)) return null;
  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap gap-2">
        <Button className="h-9 px-3.5 max-md:h-10" onClick={() => command(a.id, a.enabled ? 'stop' : 'start')}>{a.enabled ? '停止' : '启动'}</Button>
        {a.type !== 'truck' && (
          <Button variant="outline" className="h-9 px-3.5 max-md:h-10" onClick={() => command(a.id, 'maint')}>{a.maint > 0 ? '结束维护' : '维护'}</Button>
        )}
        {a.tool && <Button variant="outline" className="h-9 px-3.5 max-md:h-10" disabled={a.changing > 0} onClick={() => command(a.id, 'retool')}>换{a.tool.label}</Button>}
        <Button variant="outline" className="h-9 px-3.5 max-md:h-10" disabled={!a.fault} onClick={() => command(a.id, 'reset')}>复位</Button>
        <Button variant="destructive" className="h-9 px-3.5 max-md:h-10" disabled={!!a.fault} onClick={() => command(a.id, 'fault')}>模拟故障</Button>
      </div>
      {a.type !== 'truck' && (
        <>
          <label className="grid grid-cols-[auto_1fr_auto] items-center gap-3 text-[12.5px] text-muted-foreground">
            速度
            <Slider min={50} max={150} step={5} value={[speed]} onValueChange={([v]) => setSpeed(v)} onValueCommit={([v]) => command(a.id, 'speed', v / 100)} />
            <output className="min-w-[42px] text-right font-extrabold text-foreground tabular-nums">{speed}%</output>
          </label>
          <p className="text-[11.5px] leading-relaxed text-muted-foreground">
            {a.type === 'agv' ? '提速会更快耗电。' : `提速缩短${layout.words.cycle}，但会升温并加快磨损，故障率和超差率随之上升。`}
          </p>
        </>
      )}
    </div>
  );
}

interface Session { start: number; end: number | null; people: number; source: string }

/**
 * Today's sessions at a place: the server's record (simulated and reported alike) when the page is
 * served by it, otherwise what the simulation in this page has seen.
 */
function PlaceSessions({ a }: { a: Asset }) {
  const { sim, layout } = usePlant();
  const { status } = useLive();
  const [rows, setRows] = useState<Session[] | null>(null);
  const company = runtime().bundle.company.id;
  const day = new Date(Date.now() + 8 * 3600_000).toISOString().slice(0, 10);
  useEffect(() => {
    setRows(null);
    if (status !== 'on') return;
    let gone = false;
    const load = () => fetch(`/api/tables/${encodeURIComponent(company)}/${encodeURIComponent(layout.id)}/${encodeURIComponent(a.id)}/sessions?day=${day}`)
      .then((r) => (r.ok ? r.json() : null)).then((j) => { if (!gone && j) setRows(j.sessions ?? []); }).catch(() => {});
    load();
    const t = setInterval(load, 30_000);
    return () => { gone = true; clearInterval(t); };
  }, [a.id, company, layout.id, status, day]);
  const local = (sim.venue?.sessions ?? []).filter((s) => s.table === a.id);
  const list = (rows ?? local).slice(-12).reverse();
  const at = (ms: number) => hhmm(((ms + 8 * 3600_000) % 86_400_000) / 1000);
  return (
    <div className="mt-3">
      <h3 className="mb-1 text-[13px] font-extrabold">今天这{a.place!.label}</h3>
      {list.length ? (
        <ul className="grid gap-1 text-xs text-muted-foreground">
          {list.map((s, i) => (
            <li key={i} className="grid grid-cols-[92px_1fr_auto] gap-2 tabular-nums">
              <span>{at(s.start)} – {s.end ? at(s.end) : '现在'}</span>
              <span>{s.people} 人{s.end ? `，${Math.round((s.end - s.start) / 60000)} 分钟` : ''}</span>
              <span className={cn('rounded px-1 text-[10.5px] font-bold', s.source === 'sim' ? 'bg-surface' : 'bg-success-soft text-success')}>{s.source === 'sim' ? '仿真' : s.source}</span>
            </li>
          ))}
        </ul>
      ) : <p className="text-xs text-muted-foreground">今天还没有{a.place!.label === '桌' ? '客人' : '入住'}记录</p>}
    </div>
  );
}

/** the field a device reports for what a metric shows */
const FIELD: Record<Metric, string> = { meas: 'measure', temp: 'temp', power: 'power', vib: 'vib', level: 'stock' };

export function Inspector() {
  const { sim, layout } = usePlant();
  const id = useUI((s) => s.selected);
  const follow = useUI((s) => s.follow);
  const tick = useTick();
  const a = id ? sim.assets.get(id) : null;
  const [metric, setMetric] = useState<Metric>('temp');
  const [peek, setPeek] = useState(false);
  const [range, setRange] = useState<Range>('live');
  const [summary, setSummary] = useState<Summary | null>(null);
  useEffect(() => {
    if (!a) return;
    setMetric(a.measure ? 'meas' : isMachine(a) ? 'temp' : 'level');
    setPeek(false);
    setRange('live');
  }, [a]);
  // looking back: a field the device never reported has no history, show one it did
  useEffect(() => {
    const f = a?.live?.f;
    if (range === 'live' || !f || !isMachine(a) || FIELD[metric] in f) return;
    const m = (['meas', 'temp', 'power', 'vib'] as Metric[]).find((x) => FIELD[x] in f && (x !== 'meas' || a.measure));
    if (m) setMetric(m);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range, a]);
  if (!a) return null;

  const where = a.line ? `${a.line.name} ${a.line.note}` : a.group === 'LOG' ? '物流' : '仓储';
  const venueLine = !!a.line && a.line.gate !== undefined;
  const note = a.maint > 0 ? `维护剩余 ${Math.ceil(a.maint)} 秒`
    : a.pause ? `${a.pause}暂停`
    : a.type === 'table' ? (a.state === 'occupied' ? `${a.count} 人` : '')
    : a.type === 'agv' ? `位置 x ${a.agv!.x.toFixed(1)}  z ${a.agv!.z.toFixed(1)}`
    : a.state === 'running' && a.cycle && !a.slots ? `本${venueLine ? '组' : '件'}进度 ${(a.progress * 100).toFixed(0)}%`
    : a.type !== 'station' ? ''
    : a.state === 'idle' ? (venueLine ? '没有客人' : '等待上游来料') : a.state === 'blocked' ? (a.waitFor ? `等${a.waitFor}` : '下游未放行') : '';
  const log = sim.log.filter((x) => x.asset === a.id).slice(0, 4);
  const spc = a.measure && metric === 'meas' ? spcStats(a) : null;
  const last = (a.hist[metric] ?? []).at(-1);
  const unit = { temp: '°C', power: 'kW', vib: 'mm/s', level: a.type === 'agv' ? '%' : '', meas: a.measure?.unit ?? '' }[metric];
  // a machine that has sent readings has them stored: its history can be looked at
  const measured = !!a.live;
  const past = measured && range !== 'live';
  const field = metric === 'level' && a.type === 'agv' ? 'battery' : FIELD[metric];
  const refs: [number, string][] = [];
  if (metric === 'temp' && a.spec?.warn) refs.push([a.spec.warn, '#f2a516'], [a.spec.trip!, '#e5383b']);
  if (metric === 'meas' && a.measure) {
    refs.push([a.measure.target, '#9fb2f0']);
    if (a.measure.lo !== undefined) refs.push([a.measure.lo, '#e5383b']);
    if (a.measure.hi !== undefined) refs.push([a.measure.hi, '#e5383b']);
  }

  return (
    <aside
      className={cn(
        'absolute right-5 top-20 z-30 w-[362px] overflow-y-auto rounded-2xl bg-card p-4 shadow-float max-h-[calc(100dvh-384px)]',
        'max-md:inset-x-0 max-md:bottom-0 max-md:top-auto max-md:w-auto max-md:max-h-[72dvh] max-md:rounded-b-none max-md:rounded-t-[20px] max-md:px-3.5 max-md:pt-2 max-md:pb-[calc(16px+env(safe-area-inset-bottom))] max-md:shadow-sheet',
        peek && 'max-md:overflow-hidden',
      )}
    >
      <button type="button" aria-label="展开或收起详情" onClick={() => setPeek((p) => !p)} className="mx-auto -mt-1 mb-1 hidden h-[18px] w-full place-items-center md:hidden max-md:grid">
        <i className="h-[5px] w-[38px] rounded-full bg-[#d3d9ea]" />
      </button>
      <div className="flex items-start gap-3">
        <span className="grid size-[46px] shrink-0 place-items-center rounded-xl bg-brand-soft"><AssetIcon asset={a} className="size-[26px]" /></span>
        <div className="min-w-0 flex-1 leading-tight">
          <small className="block truncate text-[11px] font-bold text-primary">{where} {a.spec?.label}</small>
          <h2 className="mt-0.5 truncate text-[17px] font-extrabold">{a.name}</h2>
          <p className="text-xs text-muted-foreground">{a.id}</p>
        </div>
        <div className="flex gap-1">
          <Button variant="outline" size="icon" aria-label="聚焦" title="聚焦 (F)" onClick={() => useUI.getState().camera('focus', a.id)} className="max-md:size-9"><CrosshairIcon /></Button>
          {isVehicle(a) && (
            <Button variant={follow ? 'default' : 'outline'} size="icon" aria-label="跟随" aria-pressed={follow} title="跟随" onClick={() => useUI.getState().toggleFollow()} className="max-md:size-9"><NavigationIcon /></Button>
          )}
          <Button variant="outline" size="icon" aria-label="关闭" title="关闭 (Esc)" onClick={() => useUI.getState().select(null)} className="max-md:size-9"><XIcon /></Button>
        </div>
      </div>

      <div className="mt-3.5 mb-3 flex items-center gap-2.5 text-[12.5px] text-muted-foreground">
        <span className={cn('rounded-lg px-2.5 py-1 text-[12.5px] font-bold', CHIP[a.state])}>{stateLabel(layout, a.state)}</span>
        {liveFields(a) && (
          <span
            className="flex shrink-0 items-center gap-1 rounded-lg bg-brand-soft px-2 py-1 text-[12px] font-bold text-primary"
            title={`来自设备的实时数据：${Object.entries(a.live!.f).map(([k, v]) => `${k}=${typeof v === 'number' ? +v.toFixed(2) : v}`).join('，')}`}
          >
            <RadioTowerIcon className="size-3.5" />实时 · {a.live!.src}
          </span>
        )}
        <span className="truncate tabular-nums">{note}</span>
      </div>

      <div className={cn(peek && 'max-md:hidden')}>
        {a.fault && (
          <div className="mb-3 rounded-xl bg-danger-soft px-3 py-2.5 text-[12.5px] leading-relaxed text-red-800">
            <b className="block text-[13px]">{a.fault.text}</b>
            {hhmm(a.fault.at, true)} 触发。排除原因后点“复位”恢复{isMachine(a) ? `，温度需低于 ${a.spec!.trip! - 4}°C` : ''}。
          </div>
        )}
        <div className="grid grid-cols-3 gap-2"><Tiles a={a} /></div>
        {a.type === 'table' && <PlaceSessions a={a} />}
        {isMachine(a) && <MachineHints plant={layout.id} asset={a.id} measured={measured} />}
        {a.type === 'warehouse' && sim.inventory.length > 0 && (
          <div className="mt-3">
            <h3 className="mb-1 text-[13px] font-extrabold">库存</h3>
            <InventoryList sim={sim} />
          </div>
        )}

        <div className={cn('mt-3', a.type === 'table' && 'hidden')}>
          <div className="mb-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            {isMachine(a) ? (
              <Tabs value={metric} onValueChange={(v) => setMetric(v as Metric)}>
                <TabsList className="h-7">
                  {a.measure && <TabsTrigger value="meas" className="px-2 text-xs">实测</TabsTrigger>}
                  <TabsTrigger value="temp" className="px-2 text-xs">温度</TabsTrigger>
                  <TabsTrigger value="power" className="px-2 text-xs">功率</TabsTrigger>
                  <TabsTrigger value="vib" className="px-2 text-xs">振动</TabsTrigger>
                </TabsList>
              </Tabs>
            ) : (
              <b className="text-[13px] text-foreground">{a.type === 'agv' ? '电量趋势' : '库存趋势'}</b>
            )}
            <span className="ml-auto tabular-nums">
              {past ? (summary ? <>{summary.n} 个读数{summary.flagged > 0 && <b className="text-destructive">，{summary.flagged} 个可疑</b>}</> : '') : spc ? (
                <>近 {spc.n} 台  均值 {spc.mean.toFixed(a.measure!.digits)}{'  '}
                  <b className={spc.cpk < 1.33 ? 'text-destructive' : 'text-success'}>Cpk {spc.cpk.toFixed(2)}</b></>
              ) : last !== undefined ? `近 4 分钟  ${last.toFixed(metric === 'vib' ? 2 : 0)} ${unit}` : ''}
            </span>
          </div>
          {past
            ? <HistoryChart company={runtime().bundle.company.id} plant={layout.id} asset={a.id} field={field} range={range} lines={refs} onSummary={setSummary} />
            : <TrendChart asset={a} metric={metric} tick={tick} />}
          {measured && (
            <div className="mt-1 flex gap-1" role="group" aria-label="时间范围">
              {RANGES.map((x) => (
                <button key={x.id} type="button" onClick={() => setRange(x.id)} aria-pressed={range === x.id}
                  className={cn('h-6 rounded-md px-2 text-[11.5px] font-bold', range === x.id ? 'bg-brand-soft text-primary' : 'text-muted-foreground hover:bg-surface')}>
                  {x.label}
                </button>
              ))}
            </div>
          )}
        </div>

        {a.line && a.type !== 'table' && <ManualRecords company={runtime().bundle.company.id} plant={layout.id} asset={a.id} />}

        <div className="mt-3.5 border-t pt-3.5"><Controls a={a} /></div>

        <div className="mt-3.5 border-t pt-3">
          <h3 className="mb-1.5 text-[13px] font-extrabold">最近记录</h3>
          <ul className="grid gap-1.5 text-xs text-muted-foreground">
            {log.length ? log.map((x, i) => (
              <li key={i} className="grid grid-cols-[52px_1fr]">
                <time className="text-muted-foreground/70 tabular-nums">{hhmm(x.at)}</time>
                <span className={cn(x.kind === 'alarm' && 'text-destructive', x.kind === 'op' && 'text-foreground')}>{x.text}</span>
              </li>
            )) : <li>本班暂无记录</li>}
          </ul>
        </div>
      </div>
    </aside>
  );
}
