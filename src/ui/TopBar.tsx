import { useEffect, useState } from 'react';
import { BarChart3Icon, BellIcon, BoxIcon, CheckIcon, ChevronDownIcon, PauseIcon, RadioTowerIcon, SearchIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Command, CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList,
} from '@/components/ui/command';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Kbd } from '@/components/ui/kbd';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { cn } from '@/lib/utils';
import { useBiz } from '@/advisor/store';
import { useLive } from '@/live/client';
import { liveFields } from '@/sim/plant';
import { groupsOf, runtime, usePlant, useTick, useUI } from '@/state/store';
import { CHIP, STATE_LABEL, hhmm } from './status';

function Brand() {
  return (
    <div className="flex items-center gap-2.5 text-[19px] lg:min-w-[168px] font-extrabold tracking-tight max-md:hidden">
      <svg viewBox="0 0 32 32" className="size-[30px] max-md:size-[26px]" aria-hidden>
        <path d="M16 3 28 9.5v13L16 29 4 22.5v-13z" fill="#2f5bea" />
        <path d="M16 3 28 9.5 16 16 4 9.5z" fill="#6f8ff5" />
        <path d="M16 16v13L4 22.5v-13z" fill="#1f3fb0" />
      </svg>
      <span className="max-lg:hidden">SmartFactory</span>
    </div>
  );
}

function SiteBadge({ text, tone = 'primary' }: { text: string; tone?: 'primary' | 'ink' }) {
  return (
    <span className={cn('grid h-[30px] min-w-[34px] shrink-0 place-items-center rounded-lg px-1 text-xs font-extrabold text-primary-foreground', tone === 'ink' ? 'bg-foreground' : 'bg-primary')}>
      {text}
    </span>
  );
}

/** Company switch reloads with another config; plant switch is instant (all plants are simulated). */
function SiteSwitcher() {
  useTick();
  const plant = usePlant();
  const rt = runtime();
  const { company, directory } = rt.bundle;
  const L = plant.layout;
  const goCompany = (id: string) => {
    if (id === company.id) return;
    const url = new URL(location.href);
    url.searchParams.set('company', id);
    url.searchParams.delete('plant');
    location.href = url.toString();
  };
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" className="h-11 gap-2 rounded-xl pl-1.5 pr-2.5 max-md:h-[38px] max-md:pr-1.5" aria-label="切换公司">
            <SiteBadge text={company.short} tone="ink" />
            <b className="max-w-36 truncate text-[13.5px] max-lg:hidden">{company.name}</b>
            <ChevronDownIcon className="text-muted-foreground max-md:hidden" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-[300px] rounded-2xl p-2 shadow-float max-md:w-[calc(100vw-20px)]">
          <DropdownMenuLabel className="text-xs text-muted-foreground">切换公司</DropdownMenuLabel>
          {directory.map((c) => (
            <DropdownMenuItem key={c.id} onSelect={() => goCompany(c.id)} className={cn('gap-2.5 rounded-xl p-2', c.id === company.id && 'bg-accent')}>
              <SiteBadge text={c.short} tone="ink" />
              <b className="text-[13.5px]">{c.name}</b>
              {c.id === company.id && <CheckIcon className="ml-auto text-primary" />}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      <span className="text-muted-foreground/50 max-md:hidden">/</span>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" className="h-11 min-w-0 shrink gap-2.5 rounded-xl pl-1.5 pr-3 max-md:h-[38px] max-md:pr-1.5" aria-label="切换工厂">
            <SiteBadge text={L.badge} />
            <span className="grid min-w-0 text-left leading-tight max-lg:hidden">
              <b className="truncate text-[13.5px]">{L.name} {L.hall}</b>
              <small className="truncate text-[11.5px] font-normal text-muted-foreground max-xl:hidden">{L.sub}</small>
            </span>
            <ChevronDownIcon className="text-muted-foreground max-md:hidden" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-[360px] rounded-2xl p-2 shadow-float max-md:w-[calc(100vw-20px)]">
          <DropdownMenuLabel className="flex justify-between text-xs text-muted-foreground">
            <span>{company.name}，{rt.plants.length} 个工厂，{rt.plants.reduce((n, p) => n + p.layout.lines.length, 0)} 条产线</span>
          </DropdownMenuLabel>
          {rt.plants.map((p) => {
            const k = p.sim.kpis();
            const alarms = p.sim.activeAlarms().filter((a) => !a.acked).length;
            return (
              <DropdownMenuItem key={p.layout.id} onSelect={() => useUI.getState().setPlant(p.layout.id)} className={cn('gap-2.5 rounded-xl p-2', p === plant && 'bg-accent')}>
                <SiteBadge text={p.layout.badge} />
                <span className="grid min-w-0 flex-1 leading-tight">
                  <b className="truncate text-[13.5px]">{p.layout.name} {p.layout.hall}</b>
                  <small className="truncate text-xs text-muted-foreground">{p.layout.sub}</small>
                </span>
                <span className="grid justify-items-end text-[11px] leading-tight tabular-nums">
                  <b className="text-[12.5px]">OEE {(k.oee * 100).toFixed(0)}%</b>
                  <span className={alarms ? 'font-bold text-destructive' : 'text-muted-foreground'}>{alarms ? `${alarms} 条报警` : '无报警'}</span>
                </span>
              </DropdownMenuItem>
            );
          })}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

function Search() {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.key === '/' && !(e.target as HTMLElement).closest('input,textarea')) || (e.key === 'k' && (e.metaKey || e.ctrlKey))) {
        e.preventDefault();
        setOpen(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  const plant = usePlant();
  const assets = [...plant.sim.assets.values()];
  const example = assets.find((a) => a.type === 'station');
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex h-10 w-[min(300px,22vw)] min-w-0 items-center gap-2 rounded-xl border bg-background px-3 text-[13.5px] text-muted-foreground transition-colors hover:bg-surface max-md:h-[38px] max-md:w-auto max-md:flex-1 max-md:px-2.5"
      >
        <SearchIcon className="size-[17px]" />
        <span className="flex-1 truncate text-left"><span className="max-md:hidden">搜索设备、产线、AGV</span><span className="md:hidden">搜索</span></span>
        <Kbd className="max-md:hidden">/</Kbd>
      </button>
      <CommandDialog open={open} onOpenChange={setOpen} title="搜索" description="按编号或名称查找设备">
        {/* this CommandDialog does not provide the cmdk root itself */}
        <Command>
          <CommandInput placeholder={`输入编号或名称，如 ${example?.id ?? 'CV-1'}、${example?.spec?.label ?? '输送线'}、AGV`} />
          <CommandList className="max-h-[60dvh]">
            <CommandEmpty>没有匹配的设备，试试 RB、AGV、TRK 这样的编号前缀</CommandEmpty>
            {groupsOf(plant.layout).map((g) => (
              <CommandGroup key={g.id} heading={`${g.name}${g.note ? ` ${g.note}` : ''}`}>
                {assets.filter((a) => a.group === g.id).map((a) => (
                  <CommandItem
                    key={a.id}
                    value={`${a.id} ${a.name} ${a.spec?.label ?? ''} ${a.line?.name ?? ''}`}
                    onSelect={() => { setOpen(false); useUI.getState().select(a.id, true); }}
                    className="gap-2.5 rounded-lg py-2"
                  >
                    <span className={cn('rounded-md px-1.5 py-0.5 text-[11px] font-bold', CHIP[a.state])}>{STATE_LABEL[a.state]}</span>
                    <b className="font-bold">{a.id}</b>
                    <span className="truncate text-muted-foreground">{a.name}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
      </CommandDialog>
    </>
  );
}

function SpeedToggle() {
  const timeScale = useUI((s) => s.timeScale);
  return (
    <ToggleGroup
      type="single"
      value={String(timeScale)}
      onValueChange={(v) => v && useUI.getState().setTimeScale(Number(v))}
      className="rounded-xl bg-muted p-[3px]"
      spacing={0}
      aria-label="仿真速度"
    >
      {[['0', <PauseIcon key="p" className="size-3 fill-current" />], ['1', '1×'], ['5', '5×'], ['20', '20×']].map(([v, label]) => (
        <ToggleGroupItem
          key={v as string}
          value={v as string}
          aria-label={v === '0' ? '暂停' : `${v} 倍速`}
          className="h-[30px] min-w-[38px] rounded-lg px-2.5 text-[12.5px] font-bold text-muted-foreground data-[state=on]:bg-background data-[state=on]:text-primary data-[state=on]:shadow-sm max-md:h-7 max-md:min-w-0 max-md:px-1.5 max-md:text-xs"
        >
          {label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

function LiveClock() {
  useTick();
  const { sim } = usePlant();
  const timeScale = useUI((s) => s.timeScale);
  const paused = timeScale === 0;
  return (
    <div className={cn('flex h-[34px] items-center gap-2 whitespace-nowrap rounded-[10px] px-3 text-[13px] font-bold max-md:hidden', paused ? 'bg-warning-soft text-warning' : 'bg-success-soft text-success')}>
      <span className="relative flex size-2">
        {!paused && <span className="absolute inline-flex size-full animate-ping-slow rounded-full bg-success opacity-60 motion-reduce:hidden" />}
        <span className={cn('relative inline-flex size-2 rounded-full', paused ? 'bg-warning' : 'bg-success')} />
      </span>
      <span className="max-lg:hidden">{paused ? '已暂停' : timeScale === 1 ? '实时' : `${timeScale} 倍速`}</span>
      <time className="tabular-nums text-foreground">{hhmm(sim.clock, true)}</time>
    </div>
  );
}

const PROTO: Record<string, string> = { mqtt: 'MQTT', modbus: 'Modbus TCP', opcua: 'OPC UA' };

/**
 * Shown when the app is served by the SmartFactory server: the protocol links to real equipment
 * (shared by the whole server) and which machines each one feeds.
 */
/** The company's business view (figures, advisor, consulting record), for companies that have an advisor. */
let advisors: Promise<Record<string, string>> | null = null;
function BusinessEntry() {
  const company = runtime().bundle.company.id;
  const [has, setHas] = useState(false);
  const open = useBiz((s) => s.open);
  useEffect(() => {
    advisors ??= fetch('/api/advisors').then((r) => (r.ok ? r.json() : {})).catch(() => ({}));
    advisors.then((m) => {
      setHas(!!m?.[company]);
      if (!m?.[company]) useBiz.getState().setOpen(false);
    });
  }, [company]);
  if (!has) return null;
  return (
    <ToggleGroup
      type="single"
      value={open ? 'biz' : 'twin'}
      onValueChange={(v) => v && useBiz.getState().setOpen(v === 'biz')}
      className="rounded-xl bg-muted p-[3px]"
      spacing={0}
      aria-label="视图"
    >
      {([['twin', <BoxIcon key="t" className="size-4" />, '孪生'], ['biz', <BarChart3Icon key="b" className="size-4" />, '经营']] as const).map(([v, icon, label]) => (
        <ToggleGroupItem
          key={v}
          value={v}
          aria-label={label}
          className="h-[30px] gap-1.5 rounded-lg px-3 text-[12.5px] font-bold text-muted-foreground data-[state=on]:bg-primary data-[state=on]:text-primary-foreground data-[state=on]:shadow-sm max-md:h-7 max-md:px-2"
        >
          {icon}
          <span className="max-md:hidden">{label}</span>
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

function LiveLinks() {
  useTick();
  const { status, sources } = useLive();
  const { layout, sim } = usePlant();
  if (status !== 'on' || !sources.length) return null;
  const up = sources.filter((s) => s.connected);
  const rt = runtime();
  const here = [...sim.assets.values()].filter((a) => liveFields(a)).length;
  const place = (c: string, p: string) =>
    c === rt.bundle.company.id
      ? rt.byId[p]?.layout.name ?? p
      : `${rt.bundle.directory.find((d) => d.id === c)?.name ?? c} ${p}`;
  const go = (c: string, p: string, a: string) => {
    if (c !== rt.bundle.company.id) {
      location.href = `?company=${encodeURIComponent(c)}&plant=${encodeURIComponent(p)}&asset=${encodeURIComponent(a)}`;
      return;
    }
    const ui = useUI.getState();
    const switching = ui.plantId !== p;
    if (switching) ui.setPlant(p);
    setTimeout(() => useUI.getState().select(a, true), switching ? 400 : 0);
  };
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          className={cn('h-[34px] gap-1.5 rounded-[10px] px-2.5 text-[13px] font-bold max-md:hidden', here ? 'bg-brand-soft text-primary hover:bg-brand-soft/80' : 'bg-surface text-muted-foreground hover:bg-surface/80')}
          aria-label="真实设备数据"
          title={here ? `本厂 ${here} 台设备接入了真实数据` : '本厂设备都在仿真；真实数据接在别的厂'}
        >
          <RadioTowerIcon className="size-4" />
          <span className="max-lg:hidden">实时</span>
          <span className="tabular-nums">{here ? `${here} 台` : `${up.length}/${sources.length}`}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-[320px] rounded-2xl p-2 shadow-float">
        <DropdownMenuLabel className="grid gap-0.5 leading-snug">
          <span className="text-[13px] font-extrabold">真实设备数据</span>
          <span className="text-xs font-normal text-muted-foreground">
            {up.length}/{sources.length} 路连接在线 ·{' '}
            {here ? `${layout.name}有 ${here} 台设备在用真实数据` : `${layout.name}的设备都在仿真`}
          </span>
        </DropdownMenuLabel>
        {sources.map((s) => (
          <div key={s.name} className="mt-1 rounded-xl px-2 py-1.5">
            <div className="flex items-center gap-2.5">
              <span className={cn('size-2 shrink-0 rounded-full', s.connected ? 'bg-success' : 'bg-destructive')} />
              <span className="grid min-w-0 flex-1 leading-tight">
                <b className="truncate text-[13px]">{s.name} <span className="font-semibold text-muted-foreground">{PROTO[s.type] ?? s.type}</span></b>
                <small className="truncate text-[11.5px] text-muted-foreground">{s.connected ? `${s.url} · 收到 ${s.received} 条` : s.error || '未连接'}</small>
              </span>
            </div>
            {(s.targets ?? []).map((t, i) => {
              const [c, p, a] = t.split('/');
              const local = c === rt.bundle.company.id && p === layout.id;
              return (
                <DropdownMenuItem key={t} onSelect={() => go(c, p, a)} className="mt-0.5 ml-4 rounded-lg py-1 text-[12.5px]">
                  <span className={cn('truncate', local ? 'font-bold text-primary' : 'text-muted-foreground')}>→ {s.labels?.[i] ?? `${place(c, p)} · ${a}`}</span>
                </DropdownMenuItem>
              );
            })}
          </div>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function AlarmBell() {
  useTick();
  const { sim } = usePlant();
  const unacked = sim.alarms.filter((a) => !a.acked).length;
  return (
    <Button variant="ghost" size="icon-lg" className="relative rounded-xl max-md:hidden" aria-label={`报警，${unacked} 条未确认`} onClick={() => useUI.getState().setFleetTab('alarms', true)}>
      <BellIcon className="size-5" />
      {unacked > 0 && (
        <b className="absolute right-0.5 top-1 grid h-[17px] min-w-[17px] place-items-center rounded-full border-2 border-background bg-destructive px-1 text-[10.5px] text-white">
          {unacked > 99 ? '99+' : unacked}
        </b>
      )}
    </Button>
  );
}

export function TopBar() {
  return (
    <header className="absolute inset-x-0 top-0 z-30 flex h-16 items-center gap-3.5 bg-background/92 px-5 shadow-[0_1px_0_var(--border)] backdrop-blur-md max-md:h-[calc(54px+env(safe-area-inset-top))] max-md:gap-2 max-md:px-2.5 max-md:pt-[env(safe-area-inset-top)]">
      <Brand />
      <Search />
      <SiteSwitcher />
      <div className="flex-1 max-md:hidden" />
      <SpeedToggle />
      <LiveLinks />
      <BusinessEntry />
      <LiveClock />
      <AlarmBell />
      <div className="flex items-center gap-2.5 max-xl:hidden">
        <span className="grid size-[38px] place-items-center rounded-full bg-gradient-to-br from-amber-300 to-orange-400 font-extrabold text-white">早</span>
        <span className="grid leading-tight max-xl:hidden">
          <b className="text-[13.5px]">早班值班长</b>
          <small className="text-xs text-muted-foreground">生产运营</small>
        </span>
      </div>
    </header>
  );
}
