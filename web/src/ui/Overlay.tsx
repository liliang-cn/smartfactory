// Floating map chrome: KPI cards, view-mode switch with legend, camera buttons, hover tip.
import { useRef } from 'react';
import { BadgeCheckIcon, BellIcon, GaugeIcon, OctagonAlertIcon, HomeIcon, MinusIcon, PackageIcon, PlusIcon, RotateCcwIcon, RotateCwIcon, ZapIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import type { Kpis as KpiData } from '@/sim/types';
import { runtime, useCampus, usePlant, useTick, useUI, type CamOp, type ViewMode } from '@/state/store';
import { sourceOf } from '@/sim/history';
import { groupFigures } from './group';
import { STATE_LABEL, pct, rate } from './status';

function KpiCard({ icon, label, value, delta, sub, tone }: { icon: React.ReactNode; label: React.ReactNode; value: string; delta?: number | null; sub: React.ReactNode; tone?: 'bad' }) {
  return (
    <div className="flex min-w-[186px] snap-start items-center gap-3 rounded-2xl bg-card py-3 pl-3 pr-4 shadow-float max-md:min-w-[158px] max-md:py-2 max-md:pl-2">
      <span className={cn('grid size-10 shrink-0 place-items-center rounded-xl bg-brand-soft text-primary max-md:size-8 [&>svg]:size-[22px] max-md:[&>svg]:size-[18px]', tone === 'bad' && 'bg-danger-soft text-destructive')}>{icon}</span>
      <span className="grid gap-px leading-tight">
        <small className="whitespace-nowrap text-xs text-muted-foreground">{label}</small>
        <span className="flex items-baseline gap-2 whitespace-nowrap">
          <b className="text-[23px] font-extrabold tracking-tight tabular-nums max-md:text-lg">{value}</b>
          {delta != null && Math.abs(delta) >= 0.05 && (
            <em className={cn('text-xs font-bold not-italic tabular-nums', delta > 0 ? 'text-success' : 'text-destructive')}>
              {delta > 0 ? '↑' : '↓'} {Math.abs(delta).toFixed(Math.abs(delta) < 1 ? 1 : 0)}
            </em>
          )}
        </span>
        <span className="whitespace-nowrap text-[11.5px] text-muted-foreground/80 tabular-nums max-md:hidden">{sub}</span>
      </span>
    </div>
  );
}

/** Where the plant's figures come from right now: the equipment, the simulation, or both. */
function Provenance({ share }: { share: number }) {
  const s = sourceOf(share);
  return (
    <span className={cn('ml-1.5 rounded px-1 py-px text-[10.5px] font-bold', s === 'simulated' ? 'bg-surface text-muted-foreground' : 'bg-success-soft text-success')}>
      {s === 'measured' ? '实测' : s === 'simulated' ? '仿真' : `混合 ${Math.round(share * 100)}%`}
    </span>
  );
}

export function Kpis() {
  useTick();
  const { sim, layout } = usePlant();
  const k = sim.kpis();
  // Compare against the snapshot from a sim-minute ago.
  const prev = useRef<{ at: number; snap: KpiData; base: KpiData; plant: string } | null>(null);
  if (!prev.current || prev.current.plant !== layout.id) prev.current = { at: sim.elapsed, snap: k, base: k, plant: layout.id };
  if (sim.elapsed - prev.current.at >= 60) prev.current = { at: sim.elapsed, snap: k, base: prev.current.snap, plant: layout.id };
  const base = prev.current.base;
  return (
    <section className="absolute left-5 top-20 z-20 flex gap-3 max-md:inset-x-0 max-md:top-[calc(62px+env(safe-area-inset-top))] max-md:snap-x max-md:snap-mandatory max-md:gap-2 max-md:overflow-x-auto max-md:px-2.5 max-md:pb-2.5 max-md:[scrollbar-width:none]">
      <KpiCard icon={<GaugeIcon />} label={<>{layout.terms.oee ?? '综合设备效率 OEE'}<Provenance share={k.measured} /></>} value={pct(k.oee, 1)} delta={(k.oee - base.oee) * 100}
        sub={`可用 ${pct(k.avail)} 性能 ${pct(k.perf)} 质量 ${pct(k.quality)}`} />
      <KpiCard icon={<PackageIcon />} label={layout.terms.throughput ?? '每小时产量'} value={rate(k.tp)} delta={k.tp - base.tp} sub={`${layout.unit}，${k.span > 3600 ? `近 ${Math.round(k.span / 3600)} 小时折算` : '近 60 分钟'} ${layout.lines.length} ${layout.terms.line ?? '条线'}合计`} />
      <KpiCard icon={<BadgeCheckIcon />} label={layout.qualityLabel} value={pct(k.quality, 1)} delta={(k.quality - base.quality) * 100}
        sub={`合格 ${k.good}，不合格 ${k.scrap}`} />
      <div className="max-xl:hidden max-md:contents">
        <KpiCard icon={<ZapIcon />} label={layout.terms.power ?? '实时功率'} value={`${k.power.toFixed(0)} kW`} sub={`本班能耗 ${k.energy.toFixed(1)} kWh`} />
      </div>
    </section>
  );
}

/** Per-plant breakdown under a group figure: badge and value for each plant. */
function Breakdown({ items }: { items: { id: string; badge: string; value: string; bad?: boolean }[] }) {
  return (
    <span className="flex gap-2">
      {items.map((x) => (
        <span key={x.id} className="flex items-baseline gap-1">
          <span className="text-muted-foreground/70">{x.badge}</span>
          <b className={cn('font-bold text-foreground/80', x.bad && 'text-destructive')}>{x.value}</b>
        </span>
      ))}
    </span>
  );
}

/** The company's figures across its plants, each with the plants side by side. */
export function GroupKpis() {
  useTick();
  const plants = runtime().plants;
  const g = groupFigures(plants);
  const L = plants[0].layout;
  const items = (fmt: (f: (typeof g.per)[number]['f']) => string, bad?: (f: (typeof g.per)[number]['f']) => boolean) =>
    g.per.map(({ p, f }) => ({ id: p.layout.id, badge: p.layout.badge, value: fmt(f), bad: bad?.(f) }));
  return (
    <section className="absolute left-5 top-20 z-20 flex gap-3 max-md:inset-x-0 max-md:top-[calc(62px+env(safe-area-inset-top))] max-md:snap-x max-md:snap-mandatory max-md:gap-2 max-md:overflow-x-auto max-md:px-2.5 max-md:pb-2.5 max-md:[scrollbar-width:none]">
      <KpiCard icon={<GaugeIcon />} label={`集团 ${L.terms.oee ?? '综合设备效率 OEE'}`} value={pct(g.oee, 1)} sub={<Breakdown items={items((f) => pct(f.oee))} />} />
      <KpiCard icon={<PackageIcon />} label={L.terms.throughput ?? '每小时产量'} value={rate(g.tp)} sub={<Breakdown items={items((f) => rate(f.tp))} />} />
      <KpiCard icon={<BellIcon />} label="报警" value={`${g.alarms}`} tone={g.alarms ? 'bad' : undefined} sub={<Breakdown items={items((f) => `${f.alarms}`, (f) => f.alarms > 0)} />} />
      <KpiCard icon={<OctagonAlertIcon />} label="故障设备" value={`${g.faults}`} tone={g.faults ? 'bad' : undefined} sub={<Breakdown items={items((f) => `${f.faults}`, (f) => f.faults > 0)} />} />
    </section>
  );
}

const RAMP = 'linear-gradient(90deg,#4f7cff,#69c8ff,#7fe0a8,#ffd24a,#ff8a3d,#ef4444)';

export function ViewSwitch() {
  const mode = useUI((s) => s.mode);
  const labels = useUI((s) => s.labels);
  const campus = useCampus();
  return (
    <div className="absolute left-5 top-[166px] z-20 grid justify-items-start gap-2.5 max-md:left-2.5 max-md:top-[calc(132px+env(safe-area-inset-top))]">
      <div className="flex items-center gap-2 rounded-[13px] bg-card p-[5px] shadow-float">
        <ToggleGroup type="single" value={mode} onValueChange={(v) => v && useUI.getState().setMode(v as ViewMode)} spacing={0} aria-label="视图">
          {([['live', '实况'], ['heat', '温度'], ['util', '稼动率']] as const).map(([v, label]) => (
            <ToggleGroupItem key={v} value={v} className="h-[30px] rounded-lg px-2.5 text-[12.5px] font-bold text-muted-foreground data-[state=on]:bg-brand-soft data-[state=on]:text-primary">
              {label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <span className={cn('h-5 w-px bg-border', campus && 'hidden')} />
        <Button
          variant="ghost"
          size="sm"
          aria-pressed={labels}
          onClick={() => useUI.getState().toggleLabels()}
          className={cn('h-[30px] rounded-lg text-[12.5px] font-bold text-muted-foreground', labels && 'bg-brand-soft text-primary hover:bg-brand-soft', campus && 'hidden')}
        >
          全部标签
        </Button>
      </div>
      {mode !== 'live' && (
        <div className="flex items-center gap-2 rounded-xl bg-card px-3 py-2 text-[11.5px] text-muted-foreground shadow-float">
          <span>{mode === 'heat' ? '常温' : '0%'}</span>
          <i className="h-2 w-36 rounded" style={{ background: RAMP }} />
          <span>{mode === 'heat' ? '停机温度' : '100%'}</span>
        </div>
      )}
    </div>
  );
}

const CAM: [CamOp, string, React.ReactNode][] = [
  ['in', '放大', <PlusIcon key="i" />],
  ['out', '缩小', <MinusIcon key="o" />],
  ['left', '向左旋转', <RotateCcwIcon key="l" />],
  ['right', '向右旋转', <RotateCwIcon key="r" />],
  ['home', '全景 (H)', <HomeIcon key="h" />],
];

export function MapControls() {
  const hasInspector = useUI((s) => !!s.selected);
  return (
    <div
      className={cn(
        'absolute right-5 top-20 z-20 grid gap-0.5 rounded-[14px] bg-card p-[5px] shadow-float transition-[right]',
        hasInspector && 'right-[400px] max-md:hidden',
        'max-md:right-2.5 max-md:top-auto max-md:bottom-[calc(76px+env(safe-area-inset-bottom))]',
      )}
    >
      {CAM.map(([op, label, icon]) => (
        <Tooltip key={op}>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon-lg" aria-label={label} onClick={() => useUI.getState().camera(op)} className={cn('rounded-[9px]', (op === 'in' || op === 'out') && 'max-md:hidden')}>
              {icon}
            </Button>
          </TooltipTrigger>
          <TooltipContent side="left">{label}</TooltipContent>
        </Tooltip>
      ))}
    </div>
  );
}

export function HoverTip() {
  const tip = useUI((s) => s.tip);
  useTick();
  const { sim, layout } = usePlant();
  if (!tip) return null;
  const a = sim.assets.get(tip.id);
  if (!a) return null;
  const extra = a.spec?.runRise !== undefined ? `${a.temp.toFixed(0)}°C  ${a.power.toFixed(1)} kW`
    : a.type === 'agv' ? `电量 ${a.battery.toFixed(0)}%`
    : a.type === 'buffer' || a.type === 'warehouse' ? `库存 ${a.stock}`
    : a.type === 'truck' ? a.task : `${a.count} ${layout.unit}`;
  return (
    <div className="pointer-events-none fixed z-40 max-w-60 translate-x-3.5 translate-y-3.5 rounded-[10px] bg-foreground px-3 py-2 text-xs leading-snug text-background shadow-float max-md:hidden" style={{ left: tip.x, top: tip.y }}>
      <b className="text-[12.5px]">{a.id}</b> {a.name}
      <br />
      <small className="text-background/70">{STATE_LABEL[a.state]}  {extra}</small>
    </div>
  );
}
