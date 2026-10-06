// Bottom panels: production-order tracker (desktop) and the fleet list. On phones the fleet
// list is a bottom sheet whose tab bar stays visible; orders move into a tab there.
import { BoxIcon, CheckIcon, ChevronRightIcon, ClipboardListIcon, FileTextIcon, ListIcon, SettingsIcon, TruckIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { PlantSim } from '@/sim/plant';
import type { Asset, Line } from '@/sim/types';
import { groupsOf, usePlant, useTick, useUI } from '@/state/store';
import { CHIP, STATE_LABEL, hhmm, pct } from './status';

const isMachine = (a: Asset) => a.spec?.runRise !== undefined;

function orderProgress(sim: PlantSim, line: Line) {
  // the work order the plant is running (part number, quantity); the layout's sample order otherwise
  const j = line.job;
  const o = j ? { ...line.layout.order, no: j.no, part: j.name, qty: j.qty, issued: hhmm(((j.issued + 8 * 3600_000) % 86_400_000) / 1000) } : line.layout.order;
  const k = sim.lineKpi(line);
  const done = Math.min(o.qty, j ? j.good : line.good);
  const eta = k.tp > 0 ? sim.clock + ((o.qty - done) / k.tp) * 3600 : null;
  return { o, k, done, eta, finished: done >= o.qty };
}

export function Tracker() {
  useTick();
  const { sim, layout } = usePlant();
  const lineId = useUI((s) => s.trackerLine);
  const line = sim.lineById[lineId] ?? sim.lines[0];
  const { o, k, done, eta, finished } = orderProgress(sim, line);
  const w = layout.terms.order?.steps ?? ['工单下达', '物料齐套', '生产中', '生产完成', '质检入库', '发运'];
  const steps: [React.ReactNode, string, string, 'done' | 'now' | ''][] = [
    [<FileTextIcon key="1" />, w[0], o.issued, 'done'],
    [<BoxIcon key="2" />, w[1], o.kitted, 'done'],
    [<SettingsIcon key="3" />, finished ? w[3] : w[2], `${done} / ${o.qty}`, finished ? 'done' : 'now'],
    [<CheckIcon key="4" />, w[4], finished ? '已完成' : eta ? `预计 ${hhmm(eta)}` : '待定', finished ? 'now' : ''],
    [<TruckIcon key="5" />, w[5], `${layout.qualityLabel} ${pct(k.quality, 1)}`, ''],
  ];
  const t = sim.trucks.find((x) => x.kind === 'out')!;
  const ta = t.asset;
  return (
    <section className="absolute bottom-5 left-5 z-20 grid w-[min(790px,calc(100vw-560px))] grid-cols-[1fr_230px] gap-4.5 rounded-2xl bg-card px-4.5 py-4 shadow-float max-xl:grid-cols-1 max-lg:hidden">
      <div>
        <div className="mb-3.5 flex items-center gap-2.5">
          <span className="grid size-[22px] place-items-center rounded-md bg-primary text-primary-foreground"><ClipboardListIcon className="size-3.5" /></span>
          <h3 className="shrink-0 whitespace-nowrap text-[15px] font-extrabold">{layout.terms.order?.title ?? '工单跟踪'}</h3>
          <span className="min-w-0 truncate text-[11.5px] text-muted-foreground">{line.name}  {o.no}  {o.part}{line.lot?.no ? `  炉号 ${line.lot.no}${line.lot.supplier ? `（${line.lot.supplier}）` : ''}` : ''}</span>
          <div className="ml-auto flex max-w-[50%] shrink-0 gap-0.5 overflow-x-auto rounded-lg bg-muted p-[3px] [scrollbar-width:none]">
            {sim.lines.map((l) => (
              <button
                key={l.id}
                type="button"
                title={l.name}
                onClick={() => useUI.getState().setTrackerLine(l.id)}
                className={cn('h-[26px] shrink-0 whitespace-nowrap rounded-md px-2.5 text-xs font-bold text-muted-foreground', l.id === line.id && 'bg-background text-primary shadow-sm')}
              >
                {sim.lines.length > 3 ? l.id : l.name}
              </button>
            ))}
          </div>
        </div>
        <ol className="grid grid-cols-5">
          {steps.map(([icon, label, sub, st], i) => (
            <li key={label} className="relative grid justify-items-center gap-1 text-center">
              {i > 0 && <span className={cn('absolute left-[-50%] right-1/2 top-[17px] h-[3px]', st ? 'bg-primary' : 'bg-[#e2e7f4]')} />}
              <span className={cn('relative grid size-9 place-items-center rounded-full bg-[#e9edf8] text-muted-foreground/70 [&>svg]:size-4',
                st && 'bg-primary text-primary-foreground', st === 'now' && 'ring-[5px] ring-[#dbe4ff]')}>{icon}</span>
              <b className="mt-1 text-[12.5px] font-bold">{label}</b>
              <small className="text-[11.5px] text-muted-foreground/80 tabular-nums">{sub}</small>
            </li>
          ))}
        </ol>
      </div>
      <button
        type="button"
        onClick={() => useUI.getState().select(ta.id, true)}
        className="grid grid-cols-[1fr_auto] content-center gap-x-2 gap-y-1 rounded-2xl bg-surface px-3.5 py-3 text-left transition-colors hover:bg-brand-soft max-xl:hidden"
      >
        <h4 className="text-sm font-extrabold">{ta.id}</h4>
        <ChevronRightIcon className="size-4 text-muted-foreground" />
        <p className="col-span-2 text-xs text-muted-foreground">{ta.carrier}，{ta.state === 'away' ? '成品出厂车' : `装载 ${t.load} ${layout.unit}`}</p>
        <span className={cn('col-span-2 justify-self-start rounded-md px-2 py-0.5 text-[11.5px] font-bold', CHIP[ta.state])}>{STATE_LABEL[ta.state]}</span>
        <p className="col-span-2 text-xs text-muted-foreground">{ta.task}</p>
      </button>
    </section>
  );
}

function Row({ a, children }: { a: Asset; children: React.ReactNode }) {
  const selected = useUI((s) => s.selected === a.id);
  return (
    <button
      type="button"
      onClick={() => useUI.getState().select(a.id, true)}
      className={cn('grid w-full grid-cols-[82px_1fr_auto_16px] items-center gap-2.5 rounded-lg border-b px-1.5 py-2 text-left text-[12.5px] last:border-b-0 hover:bg-surface max-md:py-2.5', selected && 'bg-brand-soft hover:bg-brand-soft')}
    >
      {children}
      <ChevronRightIcon className="size-3.5 text-muted-foreground" />
    </button>
  );
}

const IdCell = ({ id, sub }: { id: string; sub: string }) => (
  <span className="grid leading-tight"><b className="font-extrabold">{id}</b><small className="truncate text-[11px] text-muted-foreground">{sub}</small></span>
);
const MiniBar = ({ label, value, danger }: { label: string; value: number; danger?: boolean }) => (
  <span className="grid justify-items-end gap-[3px] text-[11px] text-muted-foreground">
    <span>{label}</span>
    <span className="h-[5px] w-[52px] overflow-hidden rounded-full bg-[#e2e7f4]">
      <i className={cn('block h-full', danger ? 'bg-destructive' : 'bg-success')} style={{ width: `${Math.min(100, value)}%` }} />
    </span>
  </span>
);

const TABS = [
  ['machines', '设备'],
  ['stock', '库存'],
  ['vehicles', '车辆'],
  ['alarms', '报警'],
  ['log', '事件'],
  ['orders', '工单'],
] as const;

/** Inventory of the plant: lent items as in use / total, consumables as left / capacity. */
export function InventoryList({ sim }: { sim: PlantSim }) {
  return (
    <>
      {sim.inventory.map((it) => {
        const lend = it.kind === 'lend';
        const used = it.cap - it.avail;
        const low = it.avail <= Math.floor(it.cap * it.low);
        return (
          <div key={it.key} className="grid grid-cols-[1fr_auto_88px] items-center gap-2.5 rounded-xl px-1.5 py-1.5">
            <span className="grid min-w-0 leading-tight">
              <b className="truncate text-[13px]">{it.label}</b>
              <small className="text-[11.5px] text-muted-foreground">{lend ? '借用，会归还' : '消耗，靠补货'}</small>
            </span>
            <span className={cn('text-[12.5px] tabular-nums', low ? 'font-bold text-destructive' : 'text-muted-foreground')}>
              {lend ? `在用 ${used} / 共 ${it.cap} ${it.unit}` : `剩 ${it.avail} / ${it.cap} ${it.unit}`}
            </span>
            <MiniBar label={lend ? `可用 ${it.avail}` : `${Math.round((it.avail / Math.max(1, it.cap)) * 100)}%`} value={((lend ? used : it.avail) / Math.max(1, it.cap)) * 100} danger={low} />
          </div>
        );
      })}
    </>
  );
}

export function Fleet() {
  useTick();
  const { sim, layout } = usePlant();
  const groups = groupsOf(layout);
  const groupName = (a: Asset) => groups.find((g) => g.id === a.group)?.name ?? '';
  const tab = useUI((s) => s.fleetTab);
  const open = useUI((s) => s.sheetOpen);
  const all = [...sim.assets.values()];
  const machines = all.filter(isMachine);
  const vehicles = all.filter((a) => a.type === 'agv' || a.type === 'truck');
  const alarms = sim.activeAlarms();
  const counts: Record<string, string> = {
    machines: `${machines.filter((a) => a.state === 'running').length}/${machines.length}`,
    vehicles: `${vehicles.filter((a) => a.state === 'moving' || a.state === 'handling').length}/${vehicles.length}`,
    alarms: `${alarms.length}`,
  };
  const lowStock = sim.inventory.filter((i) => i.avail <= Math.floor(i.cap * i.low)).length;
  if (sim.inventory.length) counts.stock = lowStock ? `${lowStock} 低` : `${sim.inventory.length}`;
  const tabs = TABS.filter(([id]) => id !== 'stock' || sim.inventory.length);
  const unacked = alarms.some((x) => !x.acked);
  const pick = (t: string) => {
    const s = useUI.getState();
    const phone = window.matchMedia('(max-width: 767px)').matches;
    s.setFleetTab(t, phone ? !(s.sheetOpen && s.fleetTab === t) : s.sheetOpen);
  };

  return (
    <section
      className={cn(
        'absolute bottom-5 right-5 z-20 w-[400px] rounded-2xl bg-card px-3.5 pb-2.5 pt-3 shadow-float',
        'max-md:inset-x-0 max-md:bottom-0 max-md:w-auto max-md:rounded-b-none max-md:rounded-t-[20px] max-md:px-2.5 max-md:pt-2 max-md:pb-[calc(8px+env(safe-area-inset-bottom))] max-md:shadow-sheet',
      )}
    >
      <button type="button" aria-label="展开或收起列表" onClick={() => useUI.getState().setSheetOpen(!open)} className="-mt-1 mb-0.5 hidden h-[18px] w-full place-items-center max-md:grid">
        <i className="h-[5px] w-[38px] rounded-full bg-[#d3d9ea]" />
      </button>
      <div className="flex items-center gap-2.5">
        <span className="grid size-[22px] shrink-0 place-items-center rounded-md bg-primary text-primary-foreground max-md:hidden"><ListIcon className="size-3.5" /></span>
        <div role="tablist" className="flex gap-0.5 rounded-[10px] bg-muted p-[3px] max-md:flex-1 max-md:overflow-x-auto max-md:[scrollbar-width:none]">
          {tabs.map(([id, defaultLabel]) => {
            const label = id === 'orders' ? layout.terms.order?.tab ?? defaultLabel : defaultLabel;
            return (
            <button
              key={id}
              type="button"
              role="tab"
              aria-selected={tab === id}
              onClick={() => pick(id)}
              className={cn(
                'h-7 whitespace-nowrap rounded-lg px-2.5 text-[12.5px] font-bold text-muted-foreground max-md:flex-1 max-md:px-1.5',
                tab === id && 'bg-background text-foreground shadow-sm',
                id === 'orders' && 'lg:hidden',
              )}
            >
              {label}
              {counts[id] && <b className={cn('ml-1 font-semibold text-muted-foreground/70', ((id === 'alarms' && unacked) || (id === 'stock' && lowStock > 0)) && 'font-extrabold text-destructive')}>{counts[id]}</b>}
            </button>
            );
          })}
        </div>
        {tab === 'alarms' && unacked && (
          <Button variant="link" size="sm" className="ml-auto px-1 font-bold max-md:hidden" onClick={() => { sim.ackAll(); useUI.getState().bump(); }}>全部确认</Button>
        )}
      </div>

      <div className={cn('-mx-1.5 mt-2 max-h-[196px] overflow-y-auto', 'max-md:mt-0 max-md:max-h-0 max-md:overflow-hidden max-md:transition-[max-height] max-md:duration-300 motion-reduce:transition-none', open && 'max-md:mt-1.5 max-md:max-h-[46dvh] max-md:overflow-y-auto')}>
        {tab === 'machines' && machines.map((a) => (
          <Row key={a.id} a={a}>
            <IdCell id={a.id} sub={groupName(a)} />
            <span className="flex min-w-0 items-center gap-2 text-muted-foreground">
              <span className={cn('rounded-md px-2 py-0.5 text-xs font-bold', CHIP[a.state])}>{STATE_LABEL[a.state]}</span>
              <span className="tabular-nums">{a.temp.toFixed(0)}°C</span>
            </span>
            <MiniBar label={`稼动 ${(a.util * 100).toFixed(0)}%`} value={a.util * 100} />
          </Row>
        ))}
        {tab === 'stock' && <InventoryList sim={sim} />}
        {tab === 'vehicles' && vehicles.map((a) => (
          <Row key={a.id} a={a}>
            <IdCell id={a.id} sub={a.type === 'agv' ? a.name : a.carrier ?? ''} />
            <span className="flex min-w-0 items-center gap-2 text-muted-foreground">
              <i className={cn('size-[7px] shrink-0 rounded-full', a.state === 'fault' ? 'bg-destructive' : a.state === 'moving' ? 'bg-primary' : 'bg-amber-400')} />
              <span className="truncate">{a.task || STATE_LABEL[a.state]}</span>
            </span>
            {a.type === 'agv'
              ? <MiniBar label={`${a.battery.toFixed(0)}%`} value={a.battery} danger={a.battery < 20} />
              : <span className={cn('rounded-md px-2 py-0.5 text-xs font-bold', CHIP[a.state])}>{STATE_LABEL[a.state]}</span>}
          </Row>
        ))}
        {tab === 'alarms' && (alarms.length ? alarms.map((al) => (
          <div key={al.id} className="grid grid-cols-[6px_1fr_auto] items-center gap-2.5 border-b px-1.5 py-2 last:border-b-0">
            <span className={cn('self-stretch rounded-full', al.prio === 1 ? 'bg-destructive' : al.prio === 2 ? 'bg-warning' : 'bg-[#9fb2f0]')} />
            <button type="button" className="grid min-w-0 gap-px text-left leading-snug" onClick={() => useUI.getState().select(al.asset, true)}>
              <b className={cn('truncate text-[12.5px]', !al.active && 'font-semibold text-muted-foreground')}>{al.text}</b>
              <small className="text-[11px] text-muted-foreground tabular-nums">{al.asset}  {hhmm(al.at, true)}{al.active ? '' : '  已恢复'}</small>
            </button>
            {al.acked
              ? <small className="text-[11.5px] text-muted-foreground">已确认</small>
              : <Button variant="outline" size="sm" className="h-[26px]" onClick={() => { sim.ack(al.id); useUI.getState().bump(); }}>确认</Button>}
          </div>
        )) : <p className="px-2 py-4.5 text-center text-[12.5px] text-muted-foreground">没有未处理的报警。在设备详情里点“模拟故障”可以演练处置流程。</p>)}
        {tab === 'log' && sim.log.slice(0, 40).map((x, i) => (
          <button key={i} type="button" onClick={() => useUI.getState().select(x.asset, true)} className="grid w-full grid-cols-[56px_64px_1fr] gap-1.5 border-b px-1.5 py-1.5 text-left text-xs text-muted-foreground last:border-b-0 hover:bg-surface">
            <time className="text-muted-foreground/70 tabular-nums">{hhmm(x.at, true)}</time>
            <b className="font-bold text-foreground">{x.asset}</b>
            <span className={cn(x.kind === 'alarm' && 'text-destructive')}>{x.text}</span>
          </button>
        ))}
        {tab === 'orders' && sim.lines.map((line) => {
          const { o, k, done, eta } = orderProgress(sim, line);
          return (
            <div key={line.id} className="grid gap-1.5 border-b px-1.5 py-2.5 last:border-b-0">
              <div className="flex items-baseline gap-2 text-[13px]">
                <b>{line.name}</b><span className="truncate text-muted-foreground">{o.part}</span>
                <small className="ml-auto text-[11px] text-muted-foreground/70">{o.no}</small>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-[#e2e7f4]"><i className="block h-full rounded-full bg-primary" style={{ width: `${(done / o.qty) * 100}%` }} /></div>
              <div className="flex justify-between text-[11.5px] text-muted-foreground tabular-nums">
                <span>{done} / {o.qty} {layout.unit}</span><span>{layout.qualityLabel} {pct(k.quality, 1)}</span><span>预计完工 {eta ? hhmm(eta) : '待定'}</span>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}
