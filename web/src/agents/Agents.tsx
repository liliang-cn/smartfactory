// 助手: the company's own assistants — pick one, talk to it, and see what it works on.
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { toast } from 'sonner';
import {
  ArrowUpIcon, BookOpenTextIcon, CheckIcon, ChevronDownIcon, CircleStopIcon, HeadsetIcon, HistoryIcon, LoaderCircleIcon,
  NotebookPenIcon, PlusIcon, Settings2Icon, SquarePenIcon, Trash2Icon, UploadIcon, XIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import { runtime, useUI } from '@/state/store';
import * as api from './api';
import type { Assistant, Assistants, Doc, Kind, Note, Ticket } from './api';
import { remembered, useAgents, type Step, type Turn } from './store';

// ---- kinds -----------------------------------------------------------------------------

const KIND: Record<Kind, { icon: typeof BookOpenTextIcon; tile: string; work: string; starters: [string, boolean][] }> = {
  docs: {
    icon: BookOpenTextIcon, tile: 'bg-brand-soft text-primary', work: '资料库',
    starters: [['资料库里有哪些文件？', true], ['设备保养有哪些要求？', true]],
  },
  notes: {
    icon: NotebookPenIcon, tile: 'bg-success-soft text-success', work: '研发笔记',
    starters: [['最近记了哪些笔记？', true], ['记一下：', false]],
  },
  service: {
    icon: HeadsetIcon, tile: 'bg-warning-soft text-warning', work: '售后工单',
    starters: [['还有哪些工单没关闭？', true], ['登记一个客户反馈：', false]],
  },
};

function KindTile({ kind, className }: { kind: Kind; className?: string }) {
  const K = KIND[kind];
  return <span className={cn('grid size-9 shrink-0 place-items-center rounded-xl', K.tile, className)}><K.icon className="size-[18px]" /></span>;
}

const day = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : `${d.getMonth() + 1}-${String(d.getDate()).padStart(2, '0')}`;
};
const when = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
};

// ---- markdown ---------------------------------------------------------------------------

function Md({ text, className }: { text: string; className?: string }) {
  return (
    <div className={cn('text-[14px] leading-relaxed break-words', className)}>
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          p: ({ children }) => <p className="my-2 first:mt-0 last:mb-0">{children}</p>,
          strong: ({ children }) => <strong className="font-extrabold text-foreground">{children}</strong>,
          ul: ({ children }) => <ul className="my-2 grid list-disc gap-1 pl-5 marker:text-primary">{children}</ul>,
          ol: ({ children }) => <ol className="my-2 grid list-decimal gap-1 pl-5 marker:font-bold marker:text-primary">{children}</ol>,
          h1: ({ children }) => <h3 className="mt-4 mb-1.5 text-[16px] font-extrabold first:mt-0">{children}</h3>,
          h2: ({ children }) => <h3 className="mt-4 mb-1.5 text-[15px] font-extrabold first:mt-0">{children}</h3>,
          h3: ({ children }) => <h4 className="mt-3 mb-1 text-[14px] font-extrabold first:mt-0">{children}</h4>,
          code: ({ children }) => <code className="rounded-md bg-muted px-1 py-0.5 font-mono text-[12.5px]">{children}</code>,
          pre: ({ children }) => <pre className="my-2 overflow-auto rounded-xl bg-surface p-3 text-[12px]">{children}</pre>,
          a: ({ children, href }) => <a href={href} target="_blank" rel="noreferrer" className="font-semibold text-primary underline-offset-4 hover:underline">{children}</a>,
          blockquote: ({ children }) => <blockquote className="my-2 border-l-[3px] border-primary/40 pl-3 text-muted-foreground">{children}</blockquote>,
          table: ({ children }) => <div className="my-3 overflow-x-auto rounded-xl border"><Table className="text-[13px]">{children}</Table></div>,
          thead: ({ children }) => <TableHeader className="bg-surface">{children}</TableHeader>,
          tbody: ({ children }) => <TableBody>{children}</TableBody>,
          tr: ({ children }) => <TableRow>{children}</TableRow>,
          th: ({ children }) => <TableHead className="h-8 px-3 text-[12px] font-bold text-muted-foreground">{children}</TableHead>,
          td: ({ children }) => <TableCell className="px-3 py-1.5 whitespace-normal tabular-nums">{children}</TableCell>,
        }}
      >
        {text}
      </Markdown>
    </div>
  );
}

// ---- what a step did ----------------------------------------------------------------------

const TOOL: Record<string, string> = {
  list_docs: '列出文档', search_docs: '搜索文档', read_doc: '阅读文档',
  search_notes: '搜索笔记', list_notes: '最近笔记', save_note: '保存笔记', update_note: '更新笔记',
  twin_machines: '查看设备', machine_anomalies: '读数异常', search_tickets: '搜索工单', get_ticket: '查看工单', create_ticket: '登记工单', update_ticket: '处理工单',
};

const s = (v: unknown) => (v == null ? '' : typeof v === 'string' ? v : Array.isArray(v) ? v.join('、') : JSON.stringify(v));

function refused(r: unknown): string | null {
  if (typeof r === 'string' && /只有运营人员|^error|失败|没有/i.test(r) && !r.trimStart().startsWith('{') && !r.trimStart().startsWith('[')) return r.replace(/^error:\s*/i, '');
  if (r && typeof r === 'object' && !Array.isArray(r) && 'error' in r) return s((r as { error: unknown }).error);
  return null;
}

function summary(st: Step, docs: Doc[]): string {
  const a = st.args ?? {};
  const head = (() => {
    switch (st.tool) {
      case 'search_docs': case 'search_notes': case 'search_tickets': return s(a.query) || s(a.status);
      case 'read_doc': return docs.find((d) => d.id === a.doc)?.title ?? '';
      case 'save_note': return s(a.title);
      case 'update_note': return s(a.title);
      case 'twin_machines': return s(a.query);
      case 'machine_anomalies': return s(a.asset);
      case 'get_ticket': case 'update_ticket': return [s(a.id), s(a.status)].filter(Boolean).join(' · ');
      case 'create_ticket': return [s(a.customer), s(a.title)].filter(Boolean).join(' · ');
    }
    return '';
  })();
  const r = st.result;
  let tail = '';
  if (st.done && !refused(r)) {
    if (Array.isArray(r)) tail = `${r.length} ${st.tool === 'search_docs' ? '段' : '条'}`;
    else if (r && typeof r === 'object') {
      const o = r as Record<string, unknown>;
      if (Array.isArray(o.machines)) tail = `${o.machines.length} 台`;
      else if (st.tool === 'create_ticket' && o.id) tail = String(o.id);
    }
  }
  return [head, tail].filter(Boolean).join(' → ');
}

function StepDetail({ st }: { st: Step }) {
  const r = st.result;
  const no = refused(r);
  if (no) return <p className="rounded-xl bg-warning-soft px-3 py-2 text-[12.5px] font-semibold text-warning">{no}</p>;
  const parts = Array.isArray(r) ? r : r && typeof r === 'object' && Array.isArray((r as { parts?: unknown }).parts) ? (r as { parts: unknown[] }).parts : null;
  if (parts && parts.length && typeof parts[0] === 'object' && parts[0] && 'heading' in (parts[0] as object)) {
    return (
      <ol className="grid gap-1.5">
        {(parts as { title: string; heading: string; text: string }[]).map((p, i) => (
          <li key={i} className="rounded-xl bg-surface px-3 py-2">
            <b className="block truncate text-[12px] text-primary">{p.heading || p.title}</b>
            <p className="line-clamp-3 text-[12.5px] whitespace-pre-line text-foreground/75">{p.text}</p>
          </li>
        ))}
      </ol>
    );
  }
  const text = typeof r === 'string' ? r : JSON.stringify(r, null, 2);
  return <pre className="max-h-56 overflow-auto rounded-xl bg-surface p-2.5 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-all text-foreground/75">{text}</pre>;
}

function Steps({ t, docs }: { t: Turn; docs: Doc[] }) {
  const running = t.status === 'running';
  const [open, setOpen] = useState(running);
  useEffect(() => setOpen(running), [running]);
  if (!t.steps.length && !running) return null;
  const current = t.steps.find((x) => !x.done);
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className="group flex items-center gap-2 rounded-full bg-card px-3 py-1.5 text-[12.5px] font-bold text-muted-foreground shadow-sm outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/40">
        {running ? <LoaderCircleIcon className="size-3.5 animate-spin text-primary" /> : <CheckIcon className="size-3.5 stroke-[3] text-success" />}
        <span className={cn(running && 'text-primary')}>
          {running ? (current ? TOOL[current.tool] ?? current.tool : t.text ? '回答' : '思考') : `${t.steps.length} 步`}
        </span>
        {running && t.steps.length > 0 && <span className="tabular-nums">{t.steps.length}</span>}
        <ChevronDownIcon className="size-3.5 transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <ol className="mt-2 grid gap-1 rounded-2xl bg-card p-1.5 shadow-float">
          {t.steps.map((st, i) => {
            const no = st.done && !!refused(st.result);
            return (
              <Collapsible key={i} asChild>
                <li className="group/step">
                  <CollapsibleTrigger disabled={!st.done} className="flex w-full items-center gap-2.5 rounded-xl px-2 py-1.5 text-left outline-none hover:bg-surface focus-visible:ring-2 focus-visible:ring-ring/40 disabled:hover:bg-transparent">
                    <span className={cn('grid size-6 shrink-0 place-items-center rounded-full text-[11px] font-extrabold tabular-nums',
                      !st.done ? 'bg-brand-soft text-primary' : no ? 'bg-warning-soft text-warning' : 'bg-success-soft text-success')}>
                      {!st.done ? <LoaderCircleIcon className="size-3.5 animate-spin" /> : no ? <XIcon className="size-3.5 stroke-[3]" /> : i + 1}
                    </span>
                    <b className="shrink-0 text-[13px]">{TOOL[st.tool] ?? st.tool}</b>
                    <span className="min-w-0 flex-1 truncate text-[12.5px] text-muted-foreground">{summary(st, docs)}</span>
                    <ChevronDownIcon className={cn('size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/step:rotate-180', !st.done && 'invisible')} />
                  </CollapsibleTrigger>
                  <CollapsibleContent className="pt-1 pr-1 pb-2 pl-10 max-sm:pl-2"><StepDetail st={st} /></CollapsibleContent>
                </li>
              </Collapsible>
            );
          })}
        </ol>
      </CollapsibleContent>
    </Collapsible>
  );
}

function TurnView({ t, docs }: { t: Turn; docs: Doc[] }) {
  return (
    <article className="grid gap-2.5">
      <div className="flex justify-end">
        <p className="max-w-[85%] rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-[14px] leading-relaxed whitespace-pre-wrap text-primary-foreground shadow-float">{t.q}</p>
      </div>
      <div className="grid justify-items-start gap-2">
        <Steps t={t} docs={docs} />
        {t.text && <div className="w-full rounded-2xl bg-card px-4.5 py-4 shadow-float max-md:px-3.5"><Md text={t.text} /></div>}
        {t.status === 'stopped' && !t.text && <p className="text-[12.5px] font-semibold text-muted-foreground">已停止</p>}
        {t.error && <p className="w-full rounded-2xl bg-danger-soft px-3.5 py-2.5 text-[12.5px] break-all text-destructive">{t.error.length > 240 ? `${t.error.slice(0, 240)}…` : t.error}</p>}
      </div>
    </article>
  );
}

// ---- the conversation -------------------------------------------------------------------------

function Chat({ company, a, info, docs }: { company: string; a: Assistant; info: Assistants; docs: Doc[] }) {
  const turns = useAgents((x) => x.turns);
  const busy = useAgents((x) => !!x.ctrl);
  const conv = useAgents((x) => x.conv);
  const rev = useAgents((x) => x.rev);
  const [q, setQ] = useState('');
  const [convs, setConvs] = useState<api.Conversation[]>([]);
  const box = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const last = turns.at(-1);
  useEffect(() => {
    api.getConversations(company, a.id).then(setConvs).catch(() => setConvs([]));
  }, [company, a.id, rev, conv]);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 320 || last?.status === 'running') el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
  }, [turns.length, last?.steps.length, last?.text.length, last?.status]);
  useEffect(() => { setQ(''); }, [a.id]);
  const submit = (text = q) => {
    const v = text.trim();
    if (!v || busy || !info.ready) return;
    useAgents.getState().send(company, v);
    setQ('');
  };
  const K = KIND[a.kind];
  return (
    <section className="flex h-full min-h-0 flex-col">
      <header className="flex items-center gap-2 pb-3 max-lg:hidden">
        <KindTile kind={a.kind} />
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-[16px] font-extrabold">{a.name}</h2>
          <p className="truncate text-[12px] text-muted-foreground">{info.templates.find((t) => t.kind === a.kind)?.title}</p>
        </div>
        <ChatActions company={company} a={a} info={info} convs={convs} />
      </header>
      <div ref={box} className="min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1 [scrollbar-width:thin]">
        <div className="grid content-start gap-5 pb-4">
          {turns.length === 0 && (
            <div className="grid place-items-center gap-4 pt-[10vh] max-md:pt-8">
              <KindTile kind={a.kind} className="size-14 rounded-2xl [&>svg]:size-7" />
              <div className="flex max-w-[560px] flex-wrap justify-center gap-2">
                {K.starters.map(([text, go]) => (
                  <button key={text} type="button" disabled={busy || !info.ready}
                    onClick={() => { if (go) submit(text); else { setQ(text); input.current?.focus(); } }}
                    className="rounded-full bg-card px-3.5 py-2 text-[13px] font-semibold shadow-float transition-colors hover:bg-brand-soft hover:text-primary disabled:opacity-50">
                    {text}
                  </button>
                ))}
              </div>
            </div>
          )}
          {turns.map((t) => <TurnView key={t.id} t={t} docs={docs} />)}
        </div>
      </div>
      <div className="pt-2 pb-[calc(12px+env(safe-area-inset-bottom))]">
        <div className="flex items-end gap-2 rounded-2xl bg-card p-2 shadow-float">
          <Textarea
            ref={input}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); }
            }}
            rows={1}
            disabled={!info.ready}
            placeholder={info.ready ? `问${a.name}` : '服务器没有配置大模型'}
            aria-label="消息"
            className="max-h-36 min-h-10 flex-1 resize-none border-0 bg-transparent px-2 py-2 text-[14px] shadow-none focus-visible:ring-0"
          />
          {busy
            ? <Button variant="outline" className="h-10 shrink-0 gap-1.5 rounded-xl px-3" onClick={() => useAgents.getState().stop()}><CircleStopIcon className="size-4" />停止</Button>
            : <Button className="size-10 shrink-0 rounded-xl" size="icon" aria-label="发送" disabled={!q.trim() || !info.ready} onClick={() => submit()}><ArrowUpIcon className="size-5" /></Button>}
        </div>
      </div>
    </section>
  );
}

function ChatActions({ company, a, info, convs }: { company: string; a: Assistant; info: Assistants; convs: api.Conversation[] }) {
  const busy = useAgents((x) => !!x.ctrl);
  const conv = useAgents((x) => x.conv);
  const [edit, setEdit] = useState(false);
  return (
    <div className="flex shrink-0 items-center gap-1">
      <Button variant="ghost" size="icon" className="size-9 rounded-xl text-muted-foreground" aria-label="新对话" title="新对话" disabled={busy}
        onClick={() => useAgents.getState().newChat()}>
        <SquarePenIcon className="size-4" />
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="size-9 rounded-xl text-muted-foreground" aria-label="历史对话" title="历史对话" disabled={busy}>
            <HistoryIcon className="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-72">
          <DropdownMenuLabel>历史对话</DropdownMenuLabel>
          <DropdownMenuSeparator />
          {convs.length === 0 && <p className="px-2 py-3 text-center text-[12.5px] text-muted-foreground">还没有对话</p>}
          <div className="max-h-80 overflow-y-auto">
            {convs.map((c) => (
              <DropdownMenuItem key={c.id} className={cn('group gap-2', c.id === conv && 'bg-brand-soft')}
                onSelect={() => { useAgents.getState().openConv(company, a.id, c.id).catch((e: Error) => toast.error(e.message)); }}>
                <span className="min-w-0 flex-1 truncate">{c.title}</span>
                <span className="shrink-0 text-[11.5px] text-muted-foreground tabular-nums">{day(c.updated)}</span>
                <button type="button" aria-label="删除对话" className="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground opacity-0 group-hover:opacity-100 hover:bg-danger-soft hover:text-destructive"
                  onClick={(e) => {
                    e.stopPropagation();
                    e.preventDefault();
                    api.deleteConversation(company, a.id, c.id).then(() => {
                      if (useAgents.getState().conv === c.id) useAgents.getState().newChat();
                      useAgents.getState().refresh();
                    }).catch((err: Error) => toast.error(err.message));
                  }}>
                  <Trash2Icon className="size-3.5" />
                </button>
              </DropdownMenuItem>
            ))}
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
      {api.can(info.role, 'admin') && (
        <>
          <Button variant="ghost" size="icon" className="size-9 rounded-xl text-muted-foreground" aria-label="设置" title="设置" onClick={() => setEdit(true)}>
            <Settings2Icon className="size-4" />
          </Button>
          <AssistantDialog company={company} info={info} a={a} open={edit} onOpenChange={setEdit} />
        </>
      )}
    </div>
  );
}

// ---- making and changing an assistant ------------------------------------------------------

function AssistantDialog({ company, info, a, open, onOpenChange, onSaved }: {
  company: string; info: Assistants; a?: Assistant; open: boolean; onOpenChange(v: boolean): void; onSaved?(a: Assistant): void;
}) {
  const [kind, setKind] = useState<Kind>(a?.kind ?? 'docs');
  const [name, setName] = useState(a?.name ?? '');
  const [ins, setIns] = useState(a?.instructions ?? '');
  const [sure, setSure] = useState(false);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    if (!open) return;
    setKind(a?.kind ?? 'docs');
    setName(a?.name ?? '');
    setIns(a?.instructions ?? '');
    setSure(false);
  }, [open, a]);
  const reload = useReload();
  const save = async () => {
    setSaving(true);
    try {
      const saved = a
        ? await api.updateAssistant(company, a.id, { name, instructions: ins })
        : await api.createAssistant(company, { kind, name, instructions: ins });
      await reload();
      onSaved?.(saved);
      onOpenChange(false);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!a) return;
    if (!sure) { setSure(true); return; }
    try {
      await api.deleteAssistant(company, a.id);
      onOpenChange(false);
      await reload();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="gap-4 rounded-2xl p-5 sm:max-w-lg">
        <DialogHeader><DialogTitle className="text-[16px] font-extrabold">{a ? a.name : '新建助手'}</DialogTitle></DialogHeader>
        {!a && (
          <div role="radiogroup" aria-label="类型" className="grid grid-cols-3 gap-2">
            {info.templates.map((t) => (
              <button key={t.kind} type="button" role="radio" aria-checked={kind === t.kind}
                onClick={() => { setKind(t.kind); if (!name || info.templates.some((x) => x.title === name)) setName(t.title); }}
                className={cn('grid justify-items-center gap-2 rounded-xl border-2 px-2 py-3 text-[13px] font-bold transition-colors',
                  kind === t.kind ? 'border-primary bg-brand-soft text-primary' : 'border-transparent bg-surface hover:border-border')}>
                <KindTile kind={t.kind} />
                {t.title}
              </button>
            ))}
          </div>
        )}
        <label className="grid gap-1.5">
          <span className="text-[12.5px] font-bold text-muted-foreground">名称</span>
          <Input value={name} maxLength={20} onChange={(e) => setName(e.target.value)} className="h-10 rounded-xl" />
        </label>
        <label className="grid gap-1.5">
          <span className="text-[12.5px] font-bold text-muted-foreground">要求</span>
          <Textarea value={ins} maxLength={4000} rows={5} onChange={(e) => setIns(e.target.value)} placeholder="例如：答复落款写「售后服务部」" className="min-h-28 rounded-xl text-[13.5px]" />
        </label>
        <DialogFooter className="flex-row items-center gap-2 sm:justify-between">
          {a ? (
            <Button variant="ghost" className={cn('gap-1.5 rounded-xl text-destructive hover:bg-danger-soft hover:text-destructive', sure && 'bg-danger-soft')} onClick={remove}>
              <Trash2Icon className="size-4" />{sure ? '确认删除' : '删除'}
            </Button>
          ) : <span />}
          <Button className="rounded-xl px-5" disabled={!name.trim() || saving} onClick={save}>
            {saving && <LoaderCircleIcon className="size-4 animate-spin" />}{a ? '保存' : '创建'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---- what the assistants work on -------------------------------------------------------------

function Work({ company, kind, info, docs, reloadDocs }: { company: string; kind: Kind; info: Assistants; docs: Doc[]; reloadDocs(): void }) {
  const rev = useAgents((x) => x.rev);
  const [notes, setNotes] = useState<Note[]>([]);
  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [doc, setDoc] = useState<Doc | null>(null);
  const [note, setNote] = useState<Note | null>(null);
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const file = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (kind === 'notes') api.getNotes(company).then(setNotes).catch(() => setNotes([]));
    if (kind === 'service') api.getTickets(company).then(setTickets).catch(() => setTickets([]));
  }, [company, kind, rev]);
  const upload = async (files: FileList | null) => {
    for (const f of Array.from(files ?? [])) {
      try {
        const r = await api.uploadFile(company, f);
        if (!r.ok) throw new Error(r.error);
        const d = r.doc!;
        const what = [d.pages ? `${d.pages} 页` : '', `${d.parts} 段`].filter(Boolean).join('，');
        toast.success(r.outcome === 'updated' ? `${f.name}：已更新为第 ${d.version} 版，${what}` : `已加入资料库：${f.name}，${what}`, { description: d.problems?.join('；') });
      } catch (e) {
        toast.error(`${f.name}：${(e as Error).message}`);
      }
    }
    reloadDocs();
  };
  const count = kind === 'docs' ? docs.length : kind === 'notes' ? notes.length : tickets.filter((t) => t.status !== '已关闭').length;
  return (
    <aside className="flex h-full min-h-0 flex-col rounded-2xl bg-card shadow-float">
      <header className="flex items-center gap-2 border-b px-4 py-3">
        <h3 className="text-[14px] font-extrabold">{KIND[kind].work}</h3>
        <span className="rounded-md bg-muted px-1.5 py-0.5 text-[11.5px] font-bold text-muted-foreground tabular-nums">{count}{kind === 'service' ? ' 未关闭' : ''}</span>
        {kind === 'docs' && api.can(info.role, 'admin') && (
          <>
            <input ref={file} type="file" accept={api.DOC_ACCEPT} multiple hidden onChange={(e) => { upload(e.target.files); e.target.value = ''; }} />
            <Button size="sm" variant="ghost" className="ml-auto h-8 gap-1.5 rounded-lg text-primary hover:bg-brand-soft hover:text-primary" onClick={() => file.current?.click()}>
              <UploadIcon className="size-3.5" />上传
            </Button>
          </>
        )}
      </header>
      <ul className="min-h-0 flex-1 overflow-y-auto p-1.5 [scrollbar-width:thin]">
        {kind === 'docs' && docs.map((d) => (
          <li key={d.id}>
            <button type="button" onClick={() => setDoc(d)} className="grid w-full gap-0.5 rounded-xl px-3 py-2.5 text-left hover:bg-surface">
              <b className="truncate text-[13.5px]">{d.title}</b>
              <span className="text-[12px] text-muted-foreground tabular-nums">{d.parts} 节 · {(d.size / 1000).toFixed(1)} 千字 · {day(d.created)}</span>
            </button>
          </li>
        ))}
        {kind === 'notes' && notes.map((n) => (
          <li key={n.id}>
            <button type="button" onClick={() => setNote(n)} className="grid w-full gap-1 rounded-xl px-3 py-2.5 text-left hover:bg-surface">
              <span className="flex items-baseline gap-2">
                <b className="min-w-0 flex-1 truncate text-[13.5px]">{n.title}</b>
                <span className="shrink-0 text-[11.5px] text-muted-foreground tabular-nums">{day(n.updated)}</span>
              </span>
              <span className="flex flex-wrap gap-1">
                {n.tags.map((t) => <span key={t} className="rounded-md bg-success-soft px-1.5 py-px text-[11px] font-bold text-success">{t}</span>)}
                {n.assets.map((t) => <span key={t} className="rounded-md bg-muted px-1.5 py-px font-mono text-[11px] font-bold text-muted-foreground">{t}</span>)}
              </span>
            </button>
          </li>
        ))}
        {kind === 'service' && tickets.map((t) => (
          <li key={t.id}>
            <button type="button" onClick={() => setTicket(t)} className={cn('grid w-full gap-1 rounded-xl px-3 py-2.5 text-left hover:bg-surface', t.status === '已关闭' && 'opacity-55')}>
              <span className="flex items-center gap-2">
                <Severity v={t.severity} />
                <b className="min-w-0 flex-1 truncate text-[13.5px]">{t.title}</b>
              </span>
              <span className="flex items-center gap-2 text-[12px] text-muted-foreground">
                <span className="font-mono tabular-nums">{t.id}</span>
                <span className="truncate">{t.customer}</span>
                <Status v={t.status} className="ml-auto" />
              </span>
            </button>
          </li>
        ))}
        {count === 0 && kind !== 'service' && (
          <li className="grid place-items-center gap-2 py-10 text-muted-foreground">
            <KindTile kind={kind} />
          </li>
        )}
      </ul>
      <DocDialog company={company} doc={doc} info={info} onClose={() => setDoc(null)} onDeleted={reloadDocs} />
      <NoteDialog company={company} note={note} info={info} onClose={() => setNote(null)} onDeleted={() => useAgents.getState().refresh()} />
      <TicketDialog company={company} ticket={ticket} info={info} onClose={() => setTicket(null)} onChanged={(t) => { setTicket(t); useAgents.getState().refresh(); }} />
    </aside>
  );
}

function Severity({ v }: { v: string }) {
  return (
    <span className={cn('grid size-5 shrink-0 place-items-center rounded-md text-[11px] font-extrabold',
      v === '高' ? 'bg-danger-soft text-destructive' : v === '中' ? 'bg-warning-soft text-warning' : 'bg-muted text-muted-foreground')}>{v}</span>
  );
}

function Status({ v, className }: { v: string; className?: string }) {
  return (
    <span className={cn('shrink-0 rounded-md px-1.5 py-px text-[11px] font-bold',
      v === '已关闭' ? 'bg-success-soft text-success' : v === '待处理' ? 'bg-danger-soft text-destructive' : 'bg-brand-soft text-primary', className)}>{v}</span>
  );
}

function DocDialog({ company, doc, info, onClose, onDeleted }: { company: string; doc: Doc | null; info: Assistants; onClose(): void; onDeleted(): void }) {
  const [body, setBody] = useState<string | null>(null);
  const [sure, setSure] = useState(false);
  useEffect(() => {
    setBody(null);
    setSure(false);
    if (doc) api.getDoc(company, doc.id).then((v) => setBody(v.body)).catch((e: Error) => setBody(`> ${e.message}`));
  }, [company, doc]);
  return (
    <Dialog open={!!doc} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="flex max-h-[88dvh] flex-col gap-3 rounded-2xl p-0 sm:max-w-3xl">
        <DialogHeader className="border-b px-5 pt-5 pb-3">
          <DialogTitle className="pr-8 text-[16px] font-extrabold">{doc?.title}</DialogTitle>
          <p className="text-[12px] text-muted-foreground tabular-nums">{doc && `${doc.parts} 节 · ${(doc.size / 1000).toFixed(1)} 千字 · ${doc.by} · ${when(doc.created)}`}</p>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-2">
          {body == null ? <LoaderCircleIcon className="mx-auto my-10 size-5 animate-spin text-muted-foreground" /> : <Md text={body} />}
        </div>
        {api.can(info.role, 'admin') && doc && (
          <DialogFooter className="border-t px-5 py-3">
            <Button variant="ghost" className={cn('gap-1.5 rounded-xl text-destructive hover:bg-danger-soft hover:text-destructive', sure && 'bg-danger-soft')}
              onClick={() => {
                if (!sure) { setSure(true); return; }
                api.deleteDoc(company, doc.id).then(() => { onClose(); onDeleted(); }).catch((e: Error) => toast.error(e.message));
              }}>
              <Trash2Icon className="size-4" />{sure ? '确认删除' : '删除'}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** Close the assistants and frame the machine in the twin. */
function showMachine(id: string) {
  const p = runtime().plants.find((x) => x.sim.assets.has(id));
  if (!p) return;
  useAgents.getState().setOpen(false);
  const ui = useUI.getState();
  const switching = ui.plantId !== p.layout.id;
  if (switching) ui.setPlant(p.layout.id);
  setTimeout(() => useUI.getState().select(id, true), switching ? 400 : 0);
}

function NoteDialog({ company, note, info, onClose, onDeleted }: { company: string; note: Note | null; info: Assistants; onClose(): void; onDeleted(): void }) {
  const [sure, setSure] = useState(false);
  useEffect(() => setSure(false), [note]);
  return (
    <Dialog open={!!note} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="flex max-h-[88dvh] flex-col gap-3 rounded-2xl p-0 sm:max-w-2xl">
        <DialogHeader className="border-b px-5 pt-5 pb-3">
          <DialogTitle className="pr-8 text-[16px] leading-snug font-extrabold">{note?.title}</DialogTitle>
          <div className="flex flex-wrap items-center gap-1.5 text-[12px] text-muted-foreground">
            {note?.tags.map((t) => <span key={t} className="rounded-md bg-success-soft px-1.5 py-px text-[11px] font-bold text-success">{t}</span>)}
            {note?.assets.map((t) => (
              <button key={t} type="button" onClick={() => showMachine(t)} title="在孪生里查看"
                className="rounded-md bg-muted px-1.5 py-px font-mono text-[11px] font-bold text-muted-foreground hover:bg-brand-soft hover:text-primary">{t}</button>
            ))}
            <span className="ml-auto tabular-nums">{note && `${note.by} · ${when(note.updated)}`}</span>
          </div>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-4">{note && <Md text={note.body} />}</div>
        {api.can(info.role, 'admin') && note && (
          <DialogFooter className="border-t px-5 py-3">
            <Button variant="ghost" className={cn('gap-1.5 rounded-xl text-destructive hover:bg-danger-soft hover:text-destructive', sure && 'bg-danger-soft')}
              onClick={() => {
                if (!sure) { setSure(true); return; }
                api.deleteNote(company, note.id).then(() => { onClose(); onDeleted(); }).catch((e: Error) => toast.error(e.message));
              }}>
              <Trash2Icon className="size-4" />{sure ? '确认删除' : '删除'}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

function TicketDialog({ company, ticket, info, onClose, onChanged }: { company: string; ticket: Ticket | null; info: Assistants; onClose(): void; onChanged(t: Ticket): void }) {
  const t = ticket;
  return (
    <Dialog open={!!t} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="flex max-h-[88dvh] flex-col gap-3 rounded-2xl p-0 sm:max-w-2xl">
        {t && (
          <>
            <DialogHeader className="border-b px-5 pt-5 pb-3">
              <div className="flex items-center gap-2 pr-8">
                <Severity v={t.severity} />
                <DialogTitle className="min-w-0 flex-1 text-[16px] leading-snug font-extrabold">{t.title}</DialogTitle>
              </div>
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="font-mono text-[12px] text-muted-foreground tabular-nums">{t.id}</span>
                {api.can(info.role, 'member')
                  ? info.statuses.map((st) => (
                    <button key={st} type="button" disabled={st === t.status}
                      onClick={() => api.setTicketStatus(company, t.id, st).then(onChanged).catch((e: Error) => toast.error(e.message))}
                      className={cn('rounded-md px-1.5 py-px text-[11px] font-bold', st === t.status ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground hover:bg-brand-soft hover:text-primary')}>
                      {st}
                    </button>
                  ))
                  : <Status v={t.status} />}
              </div>
            </DialogHeader>
            <div className="grid min-h-0 flex-1 content-start gap-4 overflow-y-auto px-5 pb-5">
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[13px]">
                <dt className="font-bold text-muted-foreground">客户</dt><dd>{t.customer}</dd>
                <dt className="font-bold text-muted-foreground">产品</dt><dd>{t.product}</dd>
                {t.batch && <><dt className="font-bold text-muted-foreground">批次</dt><dd>{t.batch}</dd></>}
                <dt className="font-bold text-muted-foreground">登记</dt><dd className="tabular-nums">{when(t.created)}</dd>
              </dl>
              <p className="rounded-xl bg-surface px-3.5 py-3 text-[13.5px] leading-relaxed whitespace-pre-wrap">{t.detail}</p>
              {t.reply && (
                <div className="grid gap-1.5">
                  <h4 className="text-[12.5px] font-bold text-muted-foreground">答复草稿</h4>
                  <div className="rounded-xl border px-3.5 py-3"><Md text={t.reply} className="text-[13.5px]" /></div>
                </div>
              )}
              {t.log.length > 0 && (
                <ol className="grid gap-2 border-l-2 border-border pl-4">
                  {t.log.map((l, i) => (
                    <li key={i} className="relative text-[13px]">
                      <span className="absolute top-1.5 -left-[21px] size-2 rounded-full bg-primary/60" />
                      <span className="text-[11.5px] text-muted-foreground tabular-nums">{when(l.at)} · {l.by}</span>
                      <p>{l.text}</p>
                    </li>
                  ))}
                </ol>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

// ---- the view ------------------------------------------------------------------------------------

let reloadInfo: () => Promise<void> = async () => {};
const useReload = () => () => reloadInfo();

export default function AgentsView() {
  const company = runtime().bundle.company.id;
  const agent = useAgents((x) => x.agent);
  const [info, setInfo] = useState<Assistants | null>(null);
  const [docs, setDocs] = useState<Doc[]>([]);
  const [failed, setFailed] = useState<string | null>(null);
  const [making, setMaking] = useState(false);
  const [pane, setPane] = useState<'chat' | 'work'>('chat');
  const rev = useAgents((x) => x.rev);
  reloadInfo = async () => {
    try {
      const v = await api.getAssistants(company);
      setInfo(v);
      setFailed(null);
      const cur = useAgents.getState().agent;
      if (!v.agents.some((a) => a.id === cur)) {
        const keep = remembered(company);
        const next = v.agents.find((a) => a.id === keep) ?? v.agents[0];
        if (next) useAgents.getState().select(company, next.id);
      }
    } catch (e) {
      setFailed((e as Error).message);
    }
  };
  const loadDocs = () => { api.getDocs(company).then(setDocs).catch(() => setDocs([])); };
  useEffect(() => { reloadInfo(); }, [company]);
  useEffect(loadDocs, [company, rev]);
  const a = info?.agents.find((x) => x.id === agent);
  return (
    <div className="absolute inset-x-0 top-16 bottom-0 z-[25] bg-stage max-md:top-[calc(54px+env(safe-area-inset-top))]">
      {failed && <p className="mx-auto mt-16 w-fit rounded-2xl bg-danger-soft px-4 py-3 text-[13px] font-semibold text-destructive">{failed}</p>}
      {info && (
        <div className="mx-auto grid h-full max-w-[1480px] grid-cols-[248px_minmax(0,1fr)_340px] grid-rows-[minmax(0,1fr)] gap-4 px-5 pt-4 lg:pb-4 max-xl:grid-cols-[232px_minmax(0,1fr)_300px] max-lg:flex max-lg:flex-col max-lg:gap-2 max-lg:px-2.5 max-lg:pt-2.5">
          <nav aria-label="助手" className="flex min-h-0 flex-col gap-1 rounded-2xl bg-card p-1.5 shadow-float max-lg:flex-row max-lg:items-center max-lg:overflow-x-auto max-lg:[scrollbar-width:none]">
            {info.agents.map((x) => (
              <button key={x.id} type="button" aria-current={x.id === agent} onClick={() => useAgents.getState().select(company, x.id)}
                className={cn('flex shrink-0 items-center gap-2.5 rounded-xl px-2 py-2 text-left transition-colors hover:bg-surface max-lg:py-1.5 max-lg:pr-3',
                  x.id === agent && 'bg-brand-soft hover:bg-brand-soft')}>
                <KindTile kind={x.kind} className="max-lg:size-7 max-lg:rounded-lg max-lg:[&>svg]:size-4" />
                <span className="grid min-w-0 leading-tight">
                  <b className={cn('truncate text-[13.5px]', x.id === agent && 'text-primary')}>{x.name}</b>
                  <span className="truncate text-[11.5px] text-muted-foreground max-lg:hidden">{info.templates.find((t) => t.kind === x.kind)?.title}</span>
                </span>
              </button>
            ))}
            {api.can(info.role, 'admin') && (
              <>
                <button type="button" onClick={() => setMaking(true)}
                  className="mt-auto flex shrink-0 items-center gap-2.5 rounded-xl px-2 py-2 text-[13px] font-bold text-muted-foreground hover:bg-surface hover:text-primary max-lg:mt-0 max-lg:ml-auto">
                  <span className="grid size-9 place-items-center rounded-xl border-2 border-dashed max-lg:size-7"><PlusIcon className="size-4" /></span>
                  <span className="max-lg:hidden">新建助手</span>
                </button>
                <AssistantDialog company={company} info={info} open={making} onOpenChange={setMaking}
                  onSaved={(n) => useAgents.getState().select(company, n.id)} />
              </>
            )}
          </nav>
          {a && (
            <>
              <div className="flex items-center gap-2 lg:hidden">
                <div role="tablist" className="flex gap-1 rounded-[14px] bg-card p-1 shadow-float">
                  {([['chat', '对话'], ['work', KIND[a.kind].work]] as const).map(([v, label]) => (
                    <button key={v} type="button" role="tab" aria-selected={pane === v} onClick={() => setPane(v)}
                      className={cn('h-8 rounded-[10px] px-3 text-[13px] font-extrabold text-muted-foreground', pane === v && 'bg-primary text-primary-foreground')}>
                      {label}
                    </button>
                  ))}
                </div>
                <div className="ml-auto"><ChatActionsMobile company={company} a={a} info={info} /></div>
              </div>
              <div className={cn('min-h-0 flex-1 lg:contents', pane !== 'chat' && 'max-lg:hidden')}>
                <Chat key={a.id} company={company} a={a} info={info} docs={docs} />
              </div>
              <div className={cn('min-h-0 flex-1 pb-4 lg:contents', pane !== 'work' && 'max-lg:hidden')}>
                <Work company={company} kind={a.kind} info={info} docs={docs} reloadDocs={loadDocs} />
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

function ChatActionsMobile({ company, a, info }: { company: string; a: Assistant; info: Assistants }) {
  const [convs, setConvs] = useState<api.Conversation[]>([]);
  const rev = useAgents((x) => x.rev);
  const conv = useAgents((x) => x.conv);
  useEffect(() => {
    api.getConversations(company, a.id).then(setConvs).catch(() => setConvs([]));
  }, [company, a.id, rev, conv]);
  return <ChatActions company={company} a={a} info={info} convs={convs} />;
}
