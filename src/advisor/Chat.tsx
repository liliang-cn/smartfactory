// 顾问: ask the advisor agent and watch its loop, one row per round.
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import {
  ArrowUpIcon, CheckIcon, ChevronDownIcon, CircleStopIcon, LoaderCircleIcon, ShieldCheckIcon, SparklesIcon, Trash2Icon, XIcon,
} from 'lucide-react';
import { create } from 'zustand';
import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import { runtime } from '@/state/store';
import { ask, type AskEvent } from './api';
import { day, type Model } from './model';
import { ResultTable, SqlFold } from './parts';

// ---- the conversation, kept per company for this browser ------------------------

/** One round: the model deciding (since) and the tool running (start → end). */
interface Step { tool: string; args?: Record<string, unknown>; result?: unknown; since?: number; start: number; end?: number }
interface Turn {
  id: string; q: string; status: 'running' | 'done' | 'error' | 'stopped'; start: number; end?: number;
  steps: Step[]; idleSince?: number; verify?: { start: number; end?: number; text?: string }; answer?: string; error?: string;
}

const KEY = (c: string) => `sf-advisor-${c}`;

function loadTurns(c: string): Turn[] {
  try {
    const t = JSON.parse(localStorage.getItem(KEY(c)) ?? '[]') as Turn[];
    return t.map((x) => (x.status === 'running' ? { ...x, status: 'stopped', end: x.end ?? Date.now() } : x));
  } catch { return []; }
}

interface ChatState {
  turns: Record<string, Turn[]>;
  ctrl: AbortController | null;
  of(c: string): Turn[];
  send(c: string, q: string): void;
  stop(): void;
  clear(c: string): void;
}

const useChat = create<ChatState>()((set, get) => {
  const save = (c: string) => {
    try { localStorage.setItem(KEY(c), JSON.stringify(get().turns[c].slice(-20))); } catch { /* storage full or blocked */ }
  };
  const patch = (c: string, id: string, f: (t: Turn) => Turn) => {
    set((s) => ({ turns: { ...s.turns, [c]: (s.turns[c] ?? []).map((t) => (t.id === id ? f(t) : t)) } }));
    save(c);
  };
  const apply = (t: Turn, ev: AskEvent): Turn => {
    const now = Date.now();
    switch (ev.kind) {
      case 'thinking':
        return t.steps.some((s) => s.end === undefined) ? t : { ...t, idleSince: t.idleSince ?? now };
      case 'tool_call':
        return { ...t, idleSince: undefined, steps: [...t.steps, { tool: ev.tool, args: ev.args, since: t.idleSince ?? t.steps.at(-1)?.end ?? t.start, start: now }] };
      case 'tool_result': {
        const steps = [...t.steps];
        let i = steps.length - 1;
        while (i >= 0 && !(steps[i].tool === ev.tool && steps[i].end === undefined)) i--;
        if (i < 0) return t;
        steps[i] = { ...steps[i], result: ev.result, end: now };
        return { ...t, steps, idleSince: now };
      }
      case 'verify':
        return { ...t, idleSince: undefined, verify: { start: now } };
      case 'verified':
        return { ...t, verify: { start: t.verify?.start ?? now, end: now, text: ev.text ?? '' } };
      case 'complete': {
        const text = ev.text ?? '';
        if (/^error:/i.test(text)) return { ...t, status: 'error', error: text.replace(/^error:\s*/i, ''), end: now, idleSince: undefined };
        return { ...t, status: 'done', answer: text, end: now, idleSince: undefined };
      }
      case 'error':
        return { ...t, status: 'error', error: ev.text || '运行出错', end: now, idleSince: undefined };
    }
    return t;
  };
  return {
    turns: {},
    ctrl: null,
    of(c) {
      const have = get().turns[c];
      if (have) return have;
      const t = loadTurns(c);
      set((s) => ({ turns: { ...s.turns, [c]: t } }));
      return t;
    },
    send(c, q) {
      if (get().ctrl) return;
      const id = Math.random().toString(36).slice(2, 10);
      const turn: Turn = { id, q, status: 'running', start: Date.now(), steps: [], idleSince: Date.now() };
      const ctrl = new AbortController();
      set((s) => ({ ctrl, turns: { ...s.turns, [c]: [...(s.turns[c] ?? loadTurns(c)), turn] } }));
      save(c);
      ask(c, q, ctrl.signal, (ev) => patch(c, id, (t) => apply(t, ev)))
        .then(() => patch(c, id, (t) => (t.status === 'running' ? { ...t, status: 'error', error: '连接中断', end: Date.now() } : t)))
        .catch((e: Error) => patch(c, id, (t) => (t.status === 'running'
          ? { ...t, status: ctrl.signal.aborted ? 'stopped' : 'error', error: ctrl.signal.aborted ? undefined : e.message, end: Date.now(), idleSince: undefined }
          : t)))
        .finally(() => set({ ctrl: null }));
    },
    stop() { get().ctrl?.abort(); },
    clear(c) {
      set((s) => ({ turns: { ...s.turns, [c]: [] } }));
      save(c);
    },
  };
});

function useNow(on: boolean) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [on]);
  return now;
}

const secs = (ms: number) => {
  const s = Math.max(0, Math.round(ms / 1000));
  return s < 60 ? `${s} 秒` : `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
};

// ---- what a round did, in words ---------------------------------------------------

const TOOLS: Record<string, string> = {
  query_metric: '查询指标', get_dimensions: '查看维度', list_metrics: '列出指标', describe_warehouse: '查看数据表',
  sf__simulate_plan: '孪生推演', sf__plant_status: '现场状态', sf__get_asset: '设备详情', sf__read_tags: '实时数据', sf__list_alarms: '报警',
  sf__list_sites: '工厂清单', sf__list_sources: '数据接入',
  consult_add_goal: '记录目标', consult_add_finding: '记录结论', consult_propose_plan: '提出计划', consult_list: '查看档案',
  task_complete: '完成', search_available_tools: '查找工具', health_check: '数据体检',
};

const toolName = (t: string) => TOOLS[t] ?? TOOLS[`sf__${t.split('__').pop()}`] ?? t;
const bare = (t: string) => t.split('__').pop()!;

const str = (v: unknown): string => (v == null ? '' : typeof v === 'string' ? v : Array.isArray(v) ? v.map(str).join('、') : JSON.stringify(v));
const plantName = (id: unknown) => runtime().byId[String(id)]?.layout.name ?? str(id);

function describeQuery(a: Record<string, unknown>, m: Model) {
  const parts: string[] = [];
  const metrics = (a.metrics as string[] | undefined) ?? [];
  if (metrics.length) parts.push(metrics.map(m.label).join('、'));
  const grain = ({ day: '按天', week: '按周', month: '按月', quarter: '按季', year: '按年' } as Record<string, string>)[String(a.time_grain ?? a.grain ?? '')];
  const by = ((a.group_by as string[] | undefined) ?? []).filter((d) => d !== 'time').map(m.label);
  if (by.length) parts.push(`按 ${by.join('、')}`);
  if (grain) parts.push(grain);
  for (const f of ((a.filters ?? a.where) as { dimension?: string; metric?: string; op: string; values?: unknown[] }[] | undefined) ?? []) {
    if (f.dimension === 'time') continue;
    const v = str(f.values);
    parts.push(f.op === '=' || f.op === 'in' ? v : `${m.label(f.dimension ?? f.metric ?? '')} ${f.op === 'not in' || f.op === '!=' ? '≠' : f.op} ${v}`);
  }
  const t = a.time as { from?: string; to?: string; last_days?: number } | undefined;
  if (t?.last_days) parts.push(`近 ${t.last_days} 天`);
  else if (t?.from || t?.to) parts.push(`${day(t.from) || '…'}–${day(t.to) || '今天'}`);
  if (a.limit) parts.push(`前 ${a.limit}`);
  return parts.join(' · ');
}

const COMMANDS: Record<string, string> = { toollimit: '换模点', speed: '节拍', retool: '立即换模', start: '启动', stop: '停机', reset: '复位' };

function describe(tool: string, a: Record<string, unknown> | undefined, m: Model): string {
  if (!a) return '';
  switch (bare(tool)) {
    case 'query_metric': return describeQuery(a, m);
    case 'get_dimensions': return a.metric ? m.label(String(a.metric)) : '';
    case 'consult_add_goal': return str(a.what);
    case 'consult_add_finding': return str(a.says);
    case 'consult_propose_plan': return str(a.does);
    case 'search_available_tools': return str(a.query ?? a.q);
    case 'plant_status': return plantName(a.plant);
    case 'get_asset': return [str(a.asset), plantName(a.plant)].filter(Boolean).join(' · ');
    case 'read_tags': return [str(a.asset), a.plant ? plantName(a.plant) : ''].filter(Boolean).join(' · ');
    case 'list_alarms': return [a.plant ? plantName(a.plant) : '全部工厂', a.activeOnly ? '未恢复' : ''].filter(Boolean).join(' · ');
    case 'simulate_plan': {
      const ch = ((a.changes as { match?: string; command?: string; arg?: number }[] | undefined) ?? [])
        .map((c) => `${c.match ?? ''} ${COMMANDS[c.command ?? ''] ?? c.command ?? ''}${typeof c.arg === 'number' ? ` ${Math.round(c.arg * 100)}%` : ''}`.trim());
      return [str(a.lines) || plantName(a.plant), ch.join('、') || '现状', a.hours ? `${a.hours} 小时` : '', a.runs ? `× ${a.runs}` : ''].filter(Boolean).join(' · ');
    }
    case 'list_metrics': case 'consult_list': case 'task_complete': return '';
  }
  return Object.entries(a).filter(([, v]) => typeof v === 'string' || typeof v === 'number').map(([, v]) => String(v)).join(' · ');
}

// ---- the step list -------------------------------------------------------------------

function StepResult({ s, model }: { s: Step; model: Model }) {
  const r = s.result as Record<string, unknown> | string | undefined;
  if (r == null) return null;
  if (typeof r === 'object' && Array.isArray(r.columns) && Array.isArray(r.rows)) {
    const rows = r.rows as unknown[][];
    const count = typeof r.row_count === 'number' ? r.row_count : rows.length;
    return (
      <div className="grid gap-1.5">
        <ResultTable columns={r.columns as string[]} rows={rows} model={model} />
        <div className="flex items-center gap-3">
          {typeof r.sql === 'string' && <SqlFold sql={r.sql} />}
          <span className="ml-auto text-[11.5px] text-muted-foreground tabular-nums">{Math.min(10, rows.length)} / {count} 行</span>
        </div>
      </div>
    );
  }
  const err = typeof r === 'object' ? (r.error ?? r.refused) : undefined;
  let text = typeof r === 'string' ? r : JSON.stringify(r, null, 2);
  if (typeof r === 'string') {
    try { text = JSON.stringify(JSON.parse(r), null, 2); } catch { /* truncated JSON stays as is */ }
  }
  return (
    <div className="grid gap-1.5">
      {err != null && <p className="text-[12.5px] font-semibold text-destructive">{str(err)}</p>}
      <pre className="max-h-56 overflow-auto rounded-xl bg-surface p-2.5 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-all text-foreground/75">{text}</pre>
    </div>
  );
}

function StepRow({ s, i, model, now }: { s: Step; i: number; model: Model; now: number }) {
  const running = s.end === undefined;
  const desc = describe(s.tool, s.args, model);
  const r = s.result as Record<string, unknown> | undefined;
  const failed = !!(r && typeof r === 'object' && (r.error || r.refused));
  const pending = !!(r && typeof r === 'object' && r.status === 'running');
  return (
    <Collapsible asChild>
      <li className="group/step relative">
        <CollapsibleTrigger disabled={running} className="flex w-full items-center gap-2.5 rounded-xl px-2 py-1.5 text-left outline-none hover:bg-surface focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-default disabled:hover:bg-transparent">
          <span className={cn('relative z-10 grid size-6 shrink-0 place-items-center rounded-full text-[11px] font-extrabold tabular-nums',
            running ? 'bg-brand-soft text-primary' : failed ? 'bg-danger-soft text-destructive' : pending ? 'bg-warning-soft text-warning' : 'bg-success-soft text-success')}>
            {running ? <LoaderCircleIcon className="size-3.5 animate-spin" /> : failed ? <XIcon className="size-3.5 stroke-[3]" /> : i + 1}
          </span>
          <span className="grid min-w-0 flex-1 leading-tight sm:flex sm:items-baseline sm:gap-2">
            <b className={cn('shrink-0 text-[13px]', running && 'text-primary')}>{toolName(s.tool)}</b>
            {desc && <span className="truncate text-[12.5px] text-muted-foreground">{desc}</span>}
          </span>
          <span title={`${secs((s.end ?? now) - s.start)}`} className={cn('shrink-0 text-[11.5px] tabular-nums', running ? 'font-bold text-primary' : 'text-muted-foreground')}>
            {secs((s.end ?? now) - (s.since ?? s.start))}
          </span>
          <ChevronDownIcon className={cn('size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/step:rotate-180', running && 'invisible')} />
        </CollapsibleTrigger>
        <CollapsibleContent className="pt-1 pr-1 pb-2 pl-10 max-sm:pl-2">
          <StepResult s={s} model={model} />
        </CollapsibleContent>
      </li>
    </Collapsible>
  );
}

function Loop({ t, model }: { t: Turn; model: Model }) {
  const running = t.status === 'running';
  const now = useNow(running);
  const [open, setOpen] = useState(running);
  useEffect(() => { if (running) setOpen(true); }, [running]);
  const v = t.verify;
  const fixes = (v?.text ?? '').split('\n').filter((x) => x.trim());
  const current = t.steps.find((s) => s.end === undefined);
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="overflow-hidden rounded-2xl bg-card shadow-float">
      <CollapsibleTrigger className="group flex w-full items-center gap-2.5 px-3.5 py-2.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/40">
        <span className={cn('grid size-7 shrink-0 place-items-center rounded-lg',
          running ? 'bg-primary text-primary-foreground' : t.status === 'done' ? 'bg-success-soft text-success' : 'bg-danger-soft text-destructive')}>
          {running ? <SparklesIcon className="size-4 animate-pulse" /> : t.status === 'done' ? <CheckIcon className="size-4 stroke-[3]" /> : <XIcon className="size-4 stroke-[3]" />}
        </span>
        <span className="min-w-0 flex-1 truncate text-[13px] font-bold">
          {running ? (current ? toolName(current.tool) : v && !v.end ? '核对' : '思考') : t.status === 'done' ? '完成' : t.status === 'stopped' ? '已停止' : '出错'}
        </span>
        <span className="flex shrink-0 items-center gap-2 text-[12px] text-muted-foreground tabular-nums">
          <span className="rounded-md bg-muted px-1.5 py-0.5 font-bold">{t.steps.length} 轮</span>
          <span className={cn(running && 'font-bold text-primary')}>{secs((t.end ?? now) - t.start)}</span>
        </span>
        <ChevronDownIcon className="size-4 text-muted-foreground transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      {running && <div className="h-0.5 animate-pulse bg-primary/50" />}
      <CollapsibleContent>
        <ol className="relative grid gap-px border-t px-1.5 py-1.5">
          <span aria-hidden className="absolute top-4 bottom-4 left-[19.5px] w-px bg-border" />
          {t.steps.map((s, i) => <StepRow key={i} s={s} i={i} model={model} now={now} />)}
          {running && !current && !v && (
            <li className="flex items-center gap-2.5 px-2 py-1.5">
              <span className="relative z-10 grid size-6 place-items-center rounded-full bg-card ring-2 ring-primary/25">
                <span className="size-2 animate-ping rounded-full bg-primary/70 motion-reduce:animate-none" />
              </span>
              <b className="text-[13px] text-primary">思考</b>
              <span className="ml-auto pr-6 text-[11.5px] font-bold text-primary tabular-nums">{secs(now - (t.idleSince ?? t.start))}</span>
            </li>
          )}
          {v && (
            <li className="flex items-center gap-2.5 px-2 py-1.5">
              <span className={cn('relative z-10 grid size-6 place-items-center rounded-full', v.end ? (fixes.length ? 'bg-warning-soft text-warning' : 'bg-success-soft text-success') : 'bg-brand-soft text-primary')}>
                {v.end ? <ShieldCheckIcon className="size-3.5" /> : <LoaderCircleIcon className="size-3.5 animate-spin" />}
              </span>
              <b className={cn('text-[13px]', !v.end && 'text-primary')}>核对</b>
              <span className="truncate text-[12.5px] text-muted-foreground">{v.end ? (fixes.length ? `修正 ${fixes.length} 处` : '无修正') : ''}</span>
              <span className={cn('ml-auto pr-6 text-[11.5px] tabular-nums', v.end ? 'text-muted-foreground' : 'font-bold text-primary')}>{secs((v.end ?? now) - v.start)}</span>
            </li>
          )}
        </ol>
      </CollapsibleContent>
    </Collapsible>
  );
}

// ---- the answer ------------------------------------------------------------------------

function Answer({ text }: { text: string }) {
  return (
    <div className="rounded-2xl bg-card px-4.5 py-4 text-[14px] leading-relaxed shadow-float max-md:px-3.5">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          p: ({ children }) => <p className="my-2 first:mt-0 last:mb-0">{children}</p>,
          strong: ({ children }) => <strong className="font-extrabold text-foreground">{children}</strong>,
          ul: ({ children }) => <ul className="my-2 grid list-disc gap-1 pl-5 marker:text-primary">{children}</ul>,
          ol: ({ children }) => <ol className="my-2 grid list-decimal gap-1 pl-5 marker:font-bold marker:text-primary">{children}</ol>,
          h1: ({ children }) => <h3 className="mt-3 mb-1.5 text-[15.5px] font-extrabold">{children}</h3>,
          h2: ({ children }) => <h3 className="mt-3 mb-1.5 text-[15px] font-extrabold">{children}</h3>,
          h3: ({ children }) => <h4 className="mt-3 mb-1 text-[14px] font-extrabold">{children}</h4>,
          code: ({ children }) => <code className="rounded-md bg-muted px-1 py-0.5 font-mono text-[12.5px]">{children}</code>,
          pre: ({ children }) => <pre className="my-2 overflow-auto rounded-xl bg-surface p-3 text-[12px]">{children}</pre>,
          a: ({ children, href }) => <a href={href} target="_blank" rel="noreferrer" className="font-semibold text-primary underline-offset-4 hover:underline">{children}</a>,
          blockquote: ({ children }) => <blockquote className="my-2 border-l-[3px] border-primary/40 pl-3 text-muted-foreground">{children}</blockquote>,
          table: ({ children }) => <div className="my-3 overflow-hidden rounded-xl border"><Table className="text-[13px]">{children}</Table></div>,
          thead: ({ children }) => <TableHeader className="bg-surface">{children}</TableHeader>,
          tbody: ({ children }) => <TableBody>{children}</TableBody>,
          tr: ({ children }) => <TableRow>{children}</TableRow>,
          th: ({ children }) => <TableHead className="h-8 px-3 text-[12px] font-bold text-muted-foreground">{children}</TableHead>,
          td: ({ children }) => <TableCell className="px-3 py-1.5 tabular-nums">{children}</TableCell>,
        }}
      >
        {text}
      </Markdown>
    </div>
  );
}

function TurnView({ t, model }: { t: Turn; model: Model }) {
  const fixes = (t.verify?.text ?? '').split('\n').filter((x) => x.trim());
  return (
    <article className="grid gap-2.5">
      <div className="flex justify-end">
        <p className="max-w-[85%] rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-[14px] leading-relaxed text-primary-foreground shadow-float">{t.q}</p>
      </div>
      <Loop t={t} model={model} />
      {t.answer && <Answer text={t.answer} />}
      {t.answer && fixes.length > 0 && (
        <div className="flex gap-2.5 rounded-2xl bg-warning-soft px-3.5 py-2.5 text-[12.5px]">
          <ShieldCheckIcon className="mt-0.5 size-4 shrink-0 text-warning" />
          <div className="min-w-0">
            <b className="text-warning">核对修正 {fixes.length} 处</b>
            <ul className="mt-1 grid gap-0.5 text-foreground/80">{fixes.map((f, i) => <li key={i}>{f}</li>)}</ul>
          </div>
        </div>
      )}
      {t.error && <p className="rounded-2xl bg-danger-soft px-3.5 py-2.5 text-[12.5px] break-all text-destructive">{t.error.length > 240 ? `${t.error.slice(0, 240)}…` : t.error}</p>}
    </article>
  );
}

const NONE: Turn[] = [];

const STARTERS = [
  '近 30 天哪条产线 OEE 最低？损失在哪？',
  '连杆 2 线废品率为什么高？',
  '哪个钢厂的炉批废品率最高？',
  '换模点提前到模具寿命的 85% 值得吗？',
];

export function Chat({ company, model }: { company: string; model: Model }) {
  useEffect(() => { useChat.getState().of(company); }, [company]);
  const turns = useChat((s) => s.turns[company]) ?? NONE;
  const busy = useChat((s) => !!s.ctrl);
  const [q, setQ] = useState('');
  const end = useRef<HTMLDivElement>(null);
  const last = turns.at(-1);
  const sig = `${turns.length}/${last?.steps.length}/${last?.status}/${last?.verify?.end ?? ''}`;
  useLayoutEffect(() => {
    const box = end.current?.closest('[data-biz-scroll]');
    if (!box) return;
    const near = box.scrollHeight - box.scrollTop - box.clientHeight < 260;
    if (near || last?.status === 'running') end.current?.scrollIntoView({ block: 'end', behavior: 'smooth' });
  }, [sig, last?.status]);
  useEffect(() => {
    end.current?.scrollIntoView({ block: 'end' });
  }, []);
  const submit = (text = q) => {
    const v = text.trim();
    if (!v || busy) return;
    useChat.getState().send(company, v);
    setQ('');
  };
  return (
    <div className="mx-auto grid min-h-[calc(100dvh-140px)] w-full max-w-[880px] grid-rows-[1fr_auto] max-md:min-h-[calc(100dvh-108px-env(safe-area-inset-top))]">
      <div className="grid content-start gap-5 pb-4">
        {turns.length === 0 && (
          <div className="grid place-items-center gap-4 pt-[12vh] max-md:pt-8">
            <span className="grid size-14 place-items-center rounded-2xl bg-primary text-primary-foreground shadow-float"><SparklesIcon className="size-7" /></span>
            <div className="flex max-w-[640px] flex-wrap justify-center gap-2">
              {STARTERS.map((s) => (
                <button key={s} type="button" onClick={() => submit(s)} disabled={busy}
                  className="rounded-full bg-card px-3.5 py-2 text-[13px] font-semibold shadow-float transition-colors hover:bg-brand-soft hover:text-primary disabled:opacity-50">
                  {s}
                </button>
              ))}
            </div>
          </div>
        )}
        {turns.map((t) => <TurnView key={t.id} t={t} model={model} />)}
        <div ref={end} />
      </div>
      <div className="sticky bottom-0 z-10 bg-gradient-to-t from-stage from-70% to-transparent pt-4 pb-[calc(12px+env(safe-area-inset-bottom))]">
        {turns.length > 0 && !busy && (
          <div className="mb-2 flex gap-1.5 overflow-x-auto [scrollbar-width:none]">
            {STARTERS.map((s) => (
              <button key={s} type="button" onClick={() => submit(s)}
                className="shrink-0 rounded-full bg-card px-3 py-1.5 text-[12px] font-semibold text-muted-foreground shadow-sm hover:bg-brand-soft hover:text-primary">
                {s}
              </button>
            ))}
          </div>
        )}
        <div className="flex items-end gap-2 rounded-2xl bg-card p-2 shadow-float">
          <Textarea
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); }
            }}
            rows={1}
            placeholder="问顾问"
            aria-label="问题"
            className="max-h-36 min-h-10 flex-1 resize-none border-0 bg-transparent px-2 py-2 text-[14px] shadow-none focus-visible:ring-0"
          />
          {turns.length > 0 && !busy && (
            <Button variant="ghost" size="icon" className="size-10 shrink-0 rounded-xl text-muted-foreground" aria-label="清空对话" title="清空对话"
              onClick={() => useChat.getState().clear(company)}>
              <Trash2Icon className="size-4" />
            </Button>
          )}
          {busy
            ? <Button variant="outline" className="h-10 shrink-0 gap-1.5 rounded-xl px-3" onClick={() => useChat.getState().stop()}><CircleStopIcon className="size-4" />停止</Button>
            : <Button className="size-10 shrink-0 rounded-xl" size="icon" aria-label="发送" disabled={!q.trim()} onClick={() => submit()}><ArrowUpIcon className="size-5" /></Button>}
        </div>
      </div>
    </div>
  );
}

/** Whether a question is being answered, for the tab badge. */
export const useAsking = () => useChat((s) => !!s.ctrl);
