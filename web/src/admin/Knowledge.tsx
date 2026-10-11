// 知识: a company's knowledge base — its process as a graph, its production orders and its
// documents linked to them — to look through, ask, and rebuild.
import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { ArrowUpIcon, BookOpenTextIcon, LoaderCircleIcon, RefreshCwIcon, SearchIcon, WorkflowIcon } from 'lucide-react';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import * as api from './api';
import { GraphView, KIND } from './GraphView';
import { Library } from './Library';

const REL: Record<string, [string, string]> = {
  has_plant: ['有工厂', '属于公司'], has_line: ['有产线', '在工厂'], makes: ['生产', '由这条线生产'],
  has_station: ['有工位', '在产线'], is_a: ['是', '这种设备的工位'], feeds: ['下游', '上游'], supplies: ['供应', '供应商'], sold_to: ['卖给', '买它'],
  for_customer: ['客户', '工单'], of_product: ['产品', '工单'], runs_on: ['产线', '工单'],
};
const relLabel = (type: string, out: boolean) => REL[type]?.[out ? 0 : 1] ?? type;

const FROM: Record<api.Passage['from'], { label: string; tone: string }> = {
  doc: { label: '文档', tone: 'bg-success-soft text-success' },
  process: { label: '工艺', tone: 'bg-brand-soft text-primary' },
  order: { label: '工单', tone: 'bg-danger-soft text-destructive' },
};

const STATUS: Record<api.Order['status'], string> = { queued: '排队', running: '生产中', done: '已完成', cancelled: '已撤回' };

/** An order as the ERP connection has it now: where it runs, for whom, how far it is. */
function OrderCard({ company, no }: { company: string; no: string }) {
  const [o, setO] = useState<api.Order | null>(null);
  const [error, setError] = useState<string>();
  useEffect(() => { setO(null); setError(undefined); api.order(company, no).then(setO, (e: Error) => setError(e.message)); }, [company, no]);
  if (error) return <p className="text-[12.5px] text-destructive">{error}</p>;
  if (!o) return <div className="h-16" />;
  const pct = Math.min(100, (o.good / Math.max(1, o.qty)) * 100);
  const late = !!o.due && (o.status === 'queued' || o.status === 'running') && Date.parse(o.due) < Date.now();
  const day = (iso?: string) => (iso ? new Date(iso).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '');
  return (
    <div className="grid gap-2 rounded-xl border p-3">
      <div className="flex items-center gap-2 text-[13px]">
        <b>{STATUS[o.status]}</b>
        <span className="ml-auto text-muted-foreground tabular-nums">{o.good} / {o.qty}{o.scrap > 0 && <span className="text-destructive">，报废 {o.scrap}</span>}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className={cn('h-full rounded-full', o.status === 'done' ? 'bg-success' : 'bg-primary')} style={{ width: `${pct}%` }} /></div>
      <dl className="grid grid-cols-[48px_minmax(0,1fr)] gap-x-2 gap-y-1 text-[12.5px]">
        <dt className="text-muted-foreground">产线</dt><dd className="truncate">{o.problem || `${o.plantName} ${o.lineName}`}</dd>
        <dt className="text-muted-foreground">做</dt><dd className="truncate font-mono">{o.partNo || o.product}{o.name ? ` ${o.name}` : ''}</dd>
        {o.customer && <><dt className="text-muted-foreground">客户</dt><dd className="truncate">{o.customer}</dd></>}
        {o.due && <><dt className="text-muted-foreground">交期</dt><dd className={cn(late && 'font-bold text-destructive')}>{day(o.due)}{late ? '，已过' : ''}</dd></>}
      </dl>
    </div>
  );
}

function Chip({ name, kind, onPick }: { name: string; kind?: api.EntityKind; onPick(name: string): void }) {
  return (
    <button type="button" onClick={() => onPick(name)}
      className="inline-flex max-w-full items-center gap-1.5 rounded-lg bg-surface px-2 py-0.5 text-[12.5px] font-bold hover:bg-brand-soft hover:text-primary">
      {kind && <span className="size-2 shrink-0 rounded-full" style={{ background: KIND[kind].color }} />}
      <span className="truncate">{name}</span>
    </button>
  );
}

function EntityCard({ company, name, onPick }: { company: string; name: string; onPick(name: string): void }) {
  const [a, setA] = useState<api.EntityAnswer | null>(null);
  useEffect(() => { setA(null); api.knowledgeEntity(company, name).then(setA, () => setA({ candidates: [] })); }, [company, name]);
  if (!a) return <div className="grid h-32 place-items-center"><LoaderCircleIcon className="animate-spin text-muted-foreground" /></div>;
  const e = a.entity;
  if (!e) {
    return (
      <div className="grid gap-2">
        <p className="text-[13px] text-muted-foreground">工艺图里没有「{name}」{a.candidates.length ? '，可能是：' : '。'}</p>
        <div className="flex flex-wrap gap-1.5">{a.candidates.map((c) => <Chip key={c.name + c.kind} name={c.name} kind={c.kind as api.EntityKind} onPick={onPick} />)}</div>
      </div>
    );
  }
  // group the relations the way they read from this thing
  const groups = new Map<string, typeof e.links>();
  for (const l of e.links) {
    const k = relLabel(l.type, l.out);
    groups.set(k, [...(groups.get(k) ?? []), l]);
  }
  return (
    <div className="grid gap-3">
      <div className="flex items-center gap-2.5">
        <span className="grid size-9 place-items-center rounded-xl text-white" style={{ background: KIND[e.kind].color }}><WorkflowIcon className="size-4" /></span>
        <span className="grid min-w-0 leading-tight">
          <b className="truncate text-[15px] font-extrabold">{e.name}</b>
          <small className="text-[12px] text-muted-foreground">{KIND[e.kind].label}，{e.links.length} 条关系</small>
        </span>
      </div>
      {e.kind === 'order' && <OrderCard company={company} no={e.name} />}
      <dl className="grid gap-2">
        {[...groups].map(([k, ls]) => (
          <div key={k} className="grid grid-cols-[72px_minmax(0,1fr)] items-start gap-2">
            <dt className="pt-0.5 text-[12px] font-bold text-muted-foreground">{k}</dt>
            <dd className="flex flex-wrap gap-1.5">
              {ls.slice(0, 24).map((l) => <Chip key={l.name + l.type} name={l.name} kind={l.kind as api.EntityKind} onPick={onPick} />)}
              {ls.length > 24 && <span className="text-[12px] text-muted-foreground">等 {ls.length} 个</span>}
            </dd>
          </div>
        ))}
      </dl>
      {e.mentions.length > 0 && (
        <div className="grid gap-1.5 border-t pt-3">
          <span className="text-[12px] font-bold text-muted-foreground">提到它的段落</span>
          <ul className="grid gap-1 text-[12.5px]">
            {e.mentions.slice(0, 12).map((m) => <li key={m} className="flex gap-1.5"><BookOpenTextIcon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />{m}</li>)}
          </ul>
        </div>
      )}
    </div>
  );
}

function Ask({ company, onPick }: { company: string; onPick(name: string): void }) {
  const [q, setQ] = useState('');
  const [busy, setBusy] = useState(false);
  const [a, setA] = useState<api.KnowledgeAnswer | null>(null);
  const [open, setOpen] = useState<number | null>(null);
  const ask = async () => {
    if (!q.trim() || busy) return;
    setBusy(true);
    try {
      setA(await api.knowledgeSearch(company, q.trim()));
      setOpen(0);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="grid gap-3">
      <form className="relative" onSubmit={(e) => { e.preventDefault(); ask(); }}>
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="问知识库，比如：曲轴 1 线在做什么" className="h-11 rounded-xl pr-11 text-[14px]" aria-label="问知识库" />
        <Button type="submit" size="icon-sm" className="absolute top-1/2 right-1.5 size-8 -translate-y-1/2 rounded-lg" disabled={!q.trim() || busy} aria-label="搜索">
          {busy ? <LoaderCircleIcon className="animate-spin" /> : <ArrowUpIcon />}
        </Button>
      </form>
      {a && (
        <div className="grid gap-2">
          <p className="text-[12px] text-muted-foreground">
            {a.mode === 'ppr' ? '从问题里提到的东西出发，沿知识图谱找到' : '按词找到'} {a.passages.length} 段
          </p>
          <ol className="grid gap-2">
            {a.passages.map((p, i) => (
              <li key={i} className="rounded-xl border p-3">
                <button type="button" className="flex w-full items-start gap-2 text-left" onClick={() => setOpen(open === i ? null : i)}>
                  <span className={cn('mt-px shrink-0 rounded-md px-1.5 py-px text-[11px] font-bold', FROM[p.from].tone)}>{FROM[p.from].label}</span>
                  <b className="min-w-0 flex-1 text-[13px] leading-snug">{p.title}</b>
                </button>
                <p className={cn('mt-1.5 text-[12.5px] leading-relaxed whitespace-pre-line text-muted-foreground', open !== i && 'line-clamp-3')}>{p.text}</p>
                {open === i && p.entities && p.entities.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1.5">{p.entities.slice(0, 12).map((n) => <Chip key={n} name={n} onPick={onPick} />)}</div>
                )}
              </li>
            ))}
          </ol>
          {!a.passages.length && <p className="py-4 text-center text-[13px] text-muted-foreground">知识库里没有找到</p>}
        </div>
      )}
    </div>
  );
}

export default function Knowledge({ company, admin }: { company: string; admin: boolean }) {
  const [st, setSt] = useState<api.KnowledgeOverview | null>(null);
  const [graph, setGraph] = useState<api.KnowledgeGraph | null>(null);
  const [error, setError] = useState<string>();
  const [selected, setSelected] = useState<string | null>(null);
  const [find, setFind] = useState('');
  const [rebuilding, setRebuilding] = useState<'ask' | 'busy' | null>(null);
  const load = useCallback(() => {
    api.knowledge(company).then(setSt, (e: Error) => setError(e.message));
    api.knowledgeGraph(company).then(setGraph, () => {});
  }, [company]);
  useEffect(load, [load]);
  const pick = (name: string) => setSelected(name);
  const matches = find.trim() && graph ? graph.nodes.filter((n) => n.name.toLowerCase().includes(find.trim().toLowerCase())).slice(0, 8) : [];

  if (error) return <section className="rounded-[18px] bg-card p-5 shadow-float"><p className="text-[13px] text-destructive">{error}</p></section>;
  const k = st?.kinds ?? {};
  const counts: [api.EntityKind, number][] = (['plant', 'line', 'station', 'machine', 'product', 'supplier', 'customer', 'order'] as const).map((x) => [x, k[x] ?? 0]);
  return (
    <div className="grid gap-5">
      <section className="flex flex-wrap items-center gap-x-5 gap-y-2 rounded-[18px] bg-card px-5 py-4 shadow-float">
        {counts.filter(([, n]) => n > 0).map(([kind, n]) => (
          <span key={kind} className="flex items-baseline gap-1.5">
            <span className="size-2.5 translate-y-[-1px] self-center rounded-full" style={{ background: KIND[kind].color }} />
            <b className="text-[17px] font-extrabold tabular-nums">{n}</b>
            <span className="text-[12.5px] text-muted-foreground">{KIND[kind].label}</span>
          </span>
        ))}
        <span className="h-5 w-px bg-border max-sm:hidden" />
        <span className="text-[12.5px] text-muted-foreground">
          {st ? <>工艺 <b className="text-foreground tabular-nums">{st.process}</b> 段，{st.orders > 0 && <>工单 <b className="text-foreground tabular-nums">{st.orders}</b> 张，</>}文档 <b className="text-foreground tabular-nums">{st.docs}</b> 段（资料库 {st.library} 篇{st.library > st.indexed ? `，${st.library - st.indexed} 篇还没入库` : ''}）</> : ' '}
        </span>
        <div className="ml-auto flex items-center gap-2">
          {admin && <Button variant="outline" size="sm" className="rounded-lg font-bold" disabled={rebuilding === 'busy'} onClick={() => setRebuilding('ask')}><RefreshCwIcon className={cn(rebuilding === 'busy' && 'animate-spin')} />重建</Button>}
        </div>
      </section>

      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_400px]">
        <section className="relative rounded-[18px] bg-card p-3 shadow-float">
          <div className="absolute top-5 left-5 z-10 w-[min(280px,calc(100%-40px))]">
            <div className="relative">
              <Input value={find} onChange={(e) => setFind(e.target.value)} placeholder="找工位、设备、产品、工单" className="h-9 rounded-xl bg-card/95 pl-9 shadow-sm backdrop-blur" aria-label="在图里找" />
              <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            </div>
            {matches.length > 0 && (
              <ul className="mt-1 overflow-hidden rounded-xl border bg-card shadow-float">
                {matches.map((n) => (
                  <li key={n.name}>
                    <button type="button" onClick={() => { setSelected(n.name); setFind(''); }} className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-surface">
                      <span className="size-2 rounded-full" style={{ background: KIND[n.kind].color }} /><b className="truncate">{n.name}</b>
                      <span className="ml-auto text-[11.5px] text-muted-foreground">{KIND[n.kind].label}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
          {graph ? <GraphView graph={graph} selected={selected} onSelect={setSelected} className="h-[560px] max-sm:h-[420px]" />
            : <div className="grid h-[560px] place-items-center"><LoaderCircleIcon className="animate-spin text-muted-foreground" /></div>}
        </section>
        <div className="grid content-start gap-5">
          {selected && (
            <section className="rounded-[18px] bg-card p-5 shadow-float"><EntityCard company={company} name={selected} onPick={pick} /></section>
          )}
          <section className="rounded-[18px] bg-card p-5 shadow-float">
            <h2 className="mb-3 text-[15px] font-extrabold">问知识库</h2>
            <Ask company={company} onPick={pick} />
          </section>
        </div>
      </div>

      <Library company={company} admin={admin} onChange={load} />

      <AlertDialog open={rebuilding === 'ask'} onOpenChange={(o) => !o && setRebuilding(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>重建知识库？</AlertDialogTitle>
            <AlertDialogDescription>按现在的场景重写工艺图，资料库的文档全部重新切段、重新和工艺关联。场景或文档改过以后会自动更新，一般不用手动重建。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>不了</AlertDialogCancel>
            <AlertDialogAction onClick={async () => {
              setRebuilding('busy');
              try {
                const r = await api.rebuildKnowledge(company);
                toast.success(`知识库已重建，${r.docs} 篇文档重新入库`);
                load();
              } catch (e) {
                toast.error((e as Error).message);
              } finally {
                setRebuilding(null);
              }
            }}>重建</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
