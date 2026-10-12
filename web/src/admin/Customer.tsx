// One customer: what their twin is made of, their assistants and documents, the equipment that
// feeds it, who holds a token, and what was done.
import { lazy, Suspense, useEffect, useState } from 'react';
import { toast } from 'sonner';
import {
  BookOpenTextIcon, BotIcon, BoxIcon, ClipboardCheckIcon, ClipboardListIcon, CopyIcon, ExternalLinkIcon, FileJsonIcon, HeadsetIcon, KeyRoundIcon, NotebookPenIcon,
  PencilIcon, PlugZapIcon, ScrollTextIcon, ShieldCheckIcon, SlidersHorizontalIcon, Trash2Icon, TrendingUpIcon,
} from 'lucide-react';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import type { PlantLayout } from '@/core/layout';
import * as api from './api';
import { AccountList } from './Accounts';
import { ActivityList } from './Activity';
import { Blueprint, BlueprintLegend } from './Blueprint';
import { PageHead, useConsole } from './Console';
import { Calibration } from './Calib';
import { ChecksPanel } from './Checks';
import { GuardPanel } from './Guard';
import { OrderBoard, ERPPanel } from './Orders';
import { Panel } from './Panel';
import { Badge, totals } from './Customers';
import { forget, useCompany } from './layouts';
import { go, query, usePath } from './router';

const KnowledgeBase = lazy(() => import('./Knowledge'));

const TABS = [
  { id: 'scene', label: '场景', icon: BoxIcon },
  { id: 'orders', label: '工单', icon: ClipboardListIcon },
  { id: 'ai', label: '助手', icon: BotIcon },
  { id: 'knowledge', label: '知识', icon: BookOpenTextIcon },
  { id: 'connect', label: '接入', icon: PlugZapIcon },
  { id: 'calib', label: '校准', icon: SlidersHorizontalIcon },
  { id: 'guard', label: '指令', icon: ShieldCheckIcon },
  { id: 'checks', label: '点检', icon: ClipboardCheckIcon },
  { id: 'accounts', label: '账号', icon: KeyRoundIcon },
  { id: 'activity', label: '记录', icon: ScrollTextIcon },
] as const;
type Tab = (typeof TABS)[number]['id'];

const fmtDay = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString('zh-CN', { year: 'numeric', month: 'numeric', day: 'numeric' });
};

function PlantPanel({ plant, info }: { plant: PlantLayout; info?: api.Company['plants'][number] }) {
  return (
    <Panel
      title={<span className="flex items-center gap-2.5"><span className="grid h-7 min-w-8 place-items-center rounded-lg bg-primary px-1 text-[12px] font-extrabold text-primary-foreground">{plant.badge}</span>{plant.name}<small className="text-[12.5px] font-semibold text-muted-foreground">{plant.hall}{plant.site === 'outdoor' ? '，户外' : ''}</small></span>}
      aside={<span className="text-[12.5px] text-muted-foreground tabular-nums">{plant.lines.length} 条线，{info?.machines ?? plant.lines.reduce((n, l) => n + l.stations.length, 0)} 台设备</span>}
    >
      <div className="overflow-x-auto rounded-xl bg-[color-mix(in_oklab,var(--primary)_4%,var(--card))] p-3">
        <Blueprint plant={plant} className="mx-auto max-h-[460px] min-w-[560px]" />
      </div>
      <div className="mt-3"><BlueprintLegend plants={[plant]} /></div>
      <Table className="mt-4">
        <TableHeader>
          <TableRow className="text-[12px]">
            <TableHead>产线</TableHead><TableHead>产品</TableHead><TableHead className="max-md:hidden">工序</TableHead><TableHead className="text-right">节拍</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {plant.lines.map((l) => (
            <TableRow key={l.id} className="text-[13px]">
              <TableCell className="font-bold">{l.name}{l.note && <span className="ml-1.5 font-normal text-muted-foreground">{l.note}</span>}</TableCell>
              <TableCell>{l.product.name}</TableCell>
              <TableCell className="max-w-[420px] truncate text-muted-foreground max-md:hidden" title={l.stations.map((s) => s.name).join(' → ')}>{l.stations.map((s) => s.name).join(' → ')}</TableCell>
              <TableCell className="text-right tabular-nums">{Math.max(...l.stations.map((s) => s.cycle))} 秒</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}

const KIND_ICON = { docs: BookOpenTextIcon, notes: NotebookPenIcon, service: HeadsetIcon } as const;
const KIND_TILE = { docs: 'bg-brand-soft text-primary', notes: 'bg-success-soft text-success', service: 'bg-warning-soft text-warning' } as const;

function AI({ c }: { c: api.Company }) {
  const { ov } = useConsole();
  const [info, setInfo] = useState<Awaited<ReturnType<typeof api.assistants>> | null>(null);
  const [error, setError] = useState<string>();
  useEffect(() => { if (ov.assistants) api.assistants(c.id).then(setInfo, (e: Error) => setError(e.message)); }, [c.id, ov.assistants]);
  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
      <Panel title="助手" aside={<Button asChild variant="outline" size="sm" className="rounded-lg font-bold"><a href={`/?company=${encodeURIComponent(c.id)}&view=agents`}>去对话<ExternalLinkIcon /></a></Button>}>
        {!ov.assistants && <p className="text-[13px] text-muted-foreground">这台服务器没有开启助手（没有数据目录）。</p>}
        {error && <p className="text-[13px] text-destructive">{error}</p>}
        {info && !info.ready && <p className="mb-3 rounded-xl bg-warning-soft px-3 py-2 text-[12.5px] font-semibold text-warning">服务器没配大模型，助手现在答不了：设置 LLM_BASE_URL、LLM_API_KEY、LLM_MODEL。</p>}
        {info && (
          <ul className="grid gap-2">
            {info.agents.map((a) => {
              const Icon = KIND_ICON[a.kind];
              return (
                <li key={a.id} className="flex items-center gap-3 rounded-xl border px-3 py-2.5">
                  <span className={cn('grid size-9 shrink-0 place-items-center rounded-xl', KIND_TILE[a.kind])}><Icon className="size-[18px]" /></span>
                  <span className="grid min-w-0 flex-1 leading-tight">
                    <b className="truncate text-[14px]">{a.name}</b>
                    <small className="truncate text-[12px] text-muted-foreground">{info.templates.find((t) => t.kind === a.kind)?.title}{a.instructions ? `，另有要求：${a.instructions}` : ''}</small>
                  </span>
                  <small className="shrink-0 text-[12px] text-muted-foreground tabular-nums">{fmtDay(a.updated)}</small>
                </li>
              );
            })}
          </ul>
        )}
      </Panel>
      <Panel title="经营顾问">
        {c.advisor ? (
          <div className="grid gap-3">
            <p className="flex items-center gap-2 text-[13.5px]"><TrendingUpIcon className="size-4 text-warning" />已接入，生产历史写进它的数据库，看板和问答在孪生的「经营」里。</p>
            <Button asChild variant="outline" size="sm" className="w-fit rounded-lg font-bold"><a href={`/?company=${encodeURIComponent(c.id)}&view=biz`}>打开经营<ExternalLinkIcon /></a></Button>
          </div>
        ) : (
          <p className="text-[13px] leading-relaxed text-muted-foreground">没有接。接法：在服务器配置的 history 里给 {c.id} 配一个 Postgres 和顾问地址，重启后生产历史按小时写入。</p>
        )}
      </Panel>
    </div>
  );
}

const PROTO: Record<string, string> = { mqtt: 'MQTT', modbus: 'Modbus TCP', opcua: 'OPC UA' };

function Connect({ c }: { c: api.Company }) {
  const [sources, setSources] = useState<Awaited<ReturnType<typeof api.health>>['sources'] | null>(null);
  useEffect(() => { api.health().then((h) => setSources(h.sources), () => setSources([])); }, []);
  const mine = (sources ?? []).filter((s) => s.targets.some((t) => t.startsWith(`${c.id}/`)));
  const topic = `sf/${c.id}/{厂}/{设备}/{字段}`;
  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_380px]">
      <Panel title="设备数据">
        {sources && !mine.length && <p className="text-[13px] text-muted-foreground">还没有设备数据接进来，孪生里的设备都在仿真。</p>}
        <ul className="grid gap-2">
          {mine.map((s) => {
            const here = s.targets.filter((t) => t.startsWith(`${c.id}/`));
            return (
              <li key={s.name} className="rounded-xl border px-3 py-2.5">
                <div className="flex items-center gap-2.5">
                  <span className={cn('size-2 shrink-0 rounded-full', s.connected ? 'bg-success' : 'bg-destructive')} />
                  <b className="text-[14px]">{s.name}</b>
                  <span className="rounded-md bg-surface px-1.5 py-px text-[11.5px] font-bold text-muted-foreground">{PROTO[s.type] ?? s.type}</span>
                  <small className="ml-auto text-[12px] text-muted-foreground tabular-nums">{s.connected ? `收到 ${s.received} 条` : s.error || '没连上'}</small>
                </div>
                <p className="mt-1 truncate font-mono text-[12px] text-muted-foreground">{s.url}</p>
                <p className="mt-1 text-[12.5px]">{here.length} 台设备：{here.slice(0, 6).map((t) => t.split('/')[2]).join('、')}{here.length > 6 ? ' 等' : ''}</p>
                {!!s.flagged && (
                  <p className="mt-1.5 rounded-lg bg-warning-soft px-2 py-1 text-[12px] text-warning">
                    {s.flagged} 条读数可疑，存了但没用在孪生里{s.lastFlag ? `，最近一条：${s.lastFlag}` : ''}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      </Panel>
      <Panel title="点位">
        <p className="text-[13px] leading-relaxed text-muted-foreground">设备按约定发 MQTT 就能接上，不用配置：</p>
        <button type="button" onClick={() => { navigator.clipboard.writeText(topic); toast.success('已复制'); }}
          className="mt-2 flex w-full items-center gap-2 rounded-xl bg-surface px-3 py-2 text-left font-mono text-[12.5px] hover:bg-brand-soft">
          <span className="min-w-0 flex-1 truncate">{topic}</span><CopyIcon className="size-3.5 shrink-0 text-muted-foreground" />
        </button>
        <p className="mt-4 text-[13px] text-muted-foreground">每个厂的 MQTT 主题、Modbus 寄存器、OPC UA 节点：</p>
        <ul className="mt-2 grid gap-1.5">
          {c.plants.map((p) => (
            <li key={p.id}>
              <a href={`/api/devices/${encodeURIComponent(c.id)}/${encodeURIComponent(p.id)}`} target="_blank" rel="noreferrer"
                className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-[13px] font-bold hover:bg-surface hover:text-primary">
                <FileJsonIcon className="size-4 text-muted-foreground" />{p.name} 点位表<span className="ml-auto text-[12px] font-normal text-muted-foreground">{p.machines} 台设备</span>
              </a>
            </li>
          ))}
        </ul>
      </Panel>
    </div>
  );
}

function Remove({ c, open, onOpenChange }: { c: api.Company; open: boolean; onOpenChange(o: boolean): void }) {
  const { reload } = useConsole();
  const [busy, setBusy] = useState(false);
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>删除 {c.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            它的 {c.plants.length} 个工厂停止仿真，配置删掉，打开它的链接会回到默认公司。助手、资料和操作记录留着，用同一个标识重建会接上。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>不删</AlertDialogCancel>
          <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" disabled={busy}
            onClick={async (e) => {
              e.preventDefault();
              setBusy(true);
              try {
                await api.deleteCompany(c.id);
                forget(c.id);
                await reload();
                toast.success(`${c.name} 已删除`);
                go('/admin');
              } catch (err) {
                toast.error((err as Error).message);
                setBusy(false);
              }
            }}>删除</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export default function Customer({ id, home = false }: { id: string; home?: boolean }) {
  const { ov } = useConsole();
  const path = usePath();
  const c = ov.companies.find((x) => x.id === id);
  const { data, error } = useCompany(c ? id : null);
  const [removing, setRemoving] = useState(false);
  const tab = (query(path).get('tab') ?? 'scene') as Tab;
  if (!c) {
    return <PageHead title="找不到这家公司" sub={`没有 ${id}，或者你的令牌不包括它`} back={{ to: '/admin', label: '公司' }} />;
  }
  const t = totals(c);
  const industry = ov.industries.find((i) => i.id === c.industry);
  return (
    <>
      <PageHead
        back={home ? undefined : { to: '/admin', label: '公司' }}
        title={<span className="flex items-center gap-3"><Badge text={c.short} className="h-10 min-w-11 text-[14px]" />{c.name}</span>}
        sub={`${industry?.name ?? c.industry}，${t.plants} 个工厂，${t.lines} 条产线，${t.machines} 台设备${c.builtin ? '，随应用内置' : ''}`}
      >
        <Button asChild variant="outline" className="h-10 rounded-xl font-bold"><a href={`/?company=${encodeURIComponent(c.id)}`}>打开孪生<ExternalLinkIcon /></a></Button>
        {!c.builtin && <Button variant="outline" className="h-10 rounded-xl font-bold" onClick={() => go(`/admin/customers/${encodeURIComponent(c.id)}/edit`)}><PencilIcon />改场景</Button>}
        {ov.me.everywhere && !ov.runs && <Button variant="outline" className="h-10 rounded-xl font-bold" onClick={() => go(`/admin/new?from=${encodeURIComponent(c.id)}`)}><CopyIcon />照它新建</Button>}
        {!c.builtin && <Button variant="ghost" size="icon" className="size-10 rounded-xl text-muted-foreground hover:bg-danger-soft hover:text-destructive" aria-label="删除" title="删除" onClick={() => setRemoving(true)}><Trash2Icon /></Button>}
      </PageHead>
      <Remove c={c} open={removing} onOpenChange={setRemoving} />
      <Tabs value={tab} onValueChange={(v) => go(`${home ? '/admin' : `/admin/customers/${encodeURIComponent(c.id)}`}${v === 'scene' ? '' : `?tab=${v}`}`, true)}>
        <TabsList variant="line" className="mb-5 h-10 w-full justify-start gap-1 overflow-x-auto border-b px-0 [scrollbar-width:none] max-sm:mb-3">
          {TABS.map((x) => (
            <TabsTrigger key={x.id} value={x.id} className="h-10 flex-none gap-1.5 px-3 text-[13.5px] font-bold data-active:text-primary">
              <x.icon />{x.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value="scene" className="grid gap-5">
          {error && <Panel><p className="text-[13px] text-destructive">配置读不出来：{error}</p></Panel>}
          {data?.plants.map((p) => <PlantPanel key={p.id} plant={p} info={c.plants.find((x) => x.id === p.id)} />)}
        </TabsContent>
        <TabsContent value="ai"><AI c={c} /></TabsContent>
        <TabsContent value="knowledge" className="grid gap-5">
          <Suspense fallback={null}><KnowledgeBase company={c.id} admin={ov.me.role === 'admin'} /></Suspense>
        </TabsContent>
        <TabsContent value="orders"><OrderBoard c={c} /></TabsContent>
        <TabsContent value="connect" className="grid gap-5"><ERPPanel c={c} /><Connect c={c} /></TabsContent>
        <TabsContent value="calib"><Calibration c={c} /></TabsContent>
        <TabsContent value="guard"><GuardPanel c={c} /></TabsContent>
        <TabsContent value="checks"><ChecksPanel c={c} /></TabsContent>
        <TabsContent value="accounts"><AccountList company={c.id} /></TabsContent>
        <TabsContent value="activity"><ActivityList company={c.id} /></TabsContent>
      </Tabs>
    </>
  );
}
