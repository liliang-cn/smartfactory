// 工作台助手: the console agent, a conversation beside every page. It answers with the console's
// own operations; a change it proposes is a card here, carried out only when confirmed.
import { useEffect, useRef, useState } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { create } from 'zustand';
import { toast } from 'sonner';
import { ArrowUpIcon, CheckIcon, CircleAlertIcon, CircleStopIcon, CopyIcon, KeyRoundIcon, LoaderCircleIcon, SparklesIcon, SquarePenIcon, XIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import * as api from './api';
import { useConsole } from './Console';

interface Step { tool: string; args?: Record<string, unknown>; done: boolean; failed?: boolean }
interface Card { action: api.ConsoleAction; state: 'ask' | 'running' | 'done' | 'declined' | 'failed'; result?: unknown; error?: string }
interface Turn { id: number; q: string; steps: Step[]; text: string; cards: Card[]; status: 'running' | 'done' | 'error' | 'stopped'; error?: string }

interface AgentState {
  open: boolean;
  conv: string | null;
  turns: Turn[];
  ctrl: AbortController | null;
  setOpen(open: boolean): void;
  reset(): void;
  send(text: string): void;
  patch(id: number, f: (t: Turn) => Turn): void;
}

export const useAgent = create<AgentState>()((set, get) => ({
  open: false,
  conv: null,
  turns: [],
  ctrl: null,
  setOpen: (open) => set({ open }),
  reset: () => { get().ctrl?.abort(); set({ conv: null, turns: [], ctrl: null }); },
  patch: (id, f) => set((s) => ({ turns: s.turns.map((t) => (t.id === id ? f(t) : t)) })),
  send(text) {
    if (get().ctrl || !text.trim()) return;
    const id = Date.now();
    const ctrl = new AbortController();
    set((s) => ({ ctrl, turns: [...s.turns, { id, q: text.trim(), steps: [], text: '', cards: [], status: 'running' }] }));
    const patch = (f: (t: Turn) => Turn) => get().patch(id, f);
    api.agent(get().conv, text.trim(), ctrl.signal, (ev) => {
      switch (ev.kind) {
        case 'conversation': set({ conv: ev.id }); break;
        case 'tool_call': patch((t) => ({ ...t, steps: [...t.steps, { tool: ev.tool, args: ev.args, done: false }] })); break;
        case 'tool_result': patch((t) => {
          const steps = [...t.steps];
          const i = steps.findLastIndex((x) => x.tool === ev.tool && !x.done);
          if (i >= 0) steps[i] = { ...steps[i], done: true, failed: typeof ev.result === 'string' && /没有|不包括|要|没通过|不能/.test(ev.result) };
          return { ...t, steps };
        }); break;
        case 'action': patch((t) => ({ ...t, cards: [...t.cards, { action: ev.result, state: 'ask' }] })); break;
        case 'delta': patch((t) => ({ ...t, text: t.text + ev.text })); break;
        case 'reset': patch((t) => ({ ...t, text: '' })); break;
        case 'complete': patch((t) => ({ ...t, text: ev.text || t.text, status: 'done' })); break;
        case 'error': patch((t) => ({ ...t, status: 'error', error: ev.text })); break;
      }
    })
      .then(() => patch((t) => (t.status === 'running' ? { ...t, status: t.text ? 'done' : 'stopped' } : t)))
      .catch((e: Error) => patch((t) => ({ ...t, status: ctrl.signal.aborted ? 'stopped' : 'error', error: ctrl.signal.aborted ? undefined : e.message })))
      .finally(() => set((s) => ({ ctrl: s.ctrl === ctrl ? null : s.ctrl })));
  },
}));

const STEP: Record<string, string> = {
  list_customers: '看公司', get_customer: '读公司配置', list_industries: '看行业包', get_industry: '读行业包', check_company: '检查配置',
  list_tokens: '看令牌', activity: '看操作记录', knowledge_search: '搜知识库', knowledge_entity: '查工艺图',
  propose_create_company: '提出建公司', propose_delete_company: '提出删公司', propose_issue_token: '提出发令牌',
  propose_revoke_token: '提出撤销令牌', propose_rebuild_knowledge: '提出重建知识库',
  list_orders: '看工单', erp_status: '看 ERP 对接', propose_cancel_order: '提出撤回工单',
};
const stepText = (s: Step) => [STEP[s.tool] ?? s.tool, ...['id', 'company', 'name', 'query'].map((k) => s.args?.[k]).filter((v): v is string => typeof v === 'string' && !!v)].join(' ');

const SUGGEST = ['现在在做哪些工单，哪张会延期？', '哪些设备接了真实数据？', '给 ERP 发一个接口令牌', '最近一周谁改过什么？'];

function ActionCard({ card, onChange }: { card: Card; onChange(c: Card): void }) {
  const { reload } = useConsole();
  const run = async () => {
    onChange({ ...card, state: 'running' });
    try {
      const result = await api.doAction(card.action);
      onChange({ ...card, state: 'done', result });
      reload();
    } catch (e) {
      onChange({ ...card, state: 'failed', error: (e as Error).message });
    }
  };
  const token = card.state === 'done' && (card.result as { token?: string } | null)?.token;
  return (
    <div className={cn('rounded-xl border-2 p-3', card.state === 'ask' ? 'border-primary/40 bg-brand-soft/40' : 'border-border bg-card')}>
      <div className="flex items-start gap-2">
        <span className={cn('mt-0.5 grid size-5 shrink-0 place-items-center rounded-full text-white', card.state === 'done' ? 'bg-success' : card.state === 'failed' ? 'bg-destructive' : card.state === 'declined' ? 'bg-muted-foreground/50' : 'bg-primary')}>
          {card.state === 'done' ? <CheckIcon className="size-3" /> : card.state === 'failed' ? <XIcon className="size-3" /> : card.state === 'running' ? <LoaderCircleIcon className="size-3 animate-spin" /> : <SparklesIcon className="size-3" />}
        </span>
        <div className="grid min-w-0 flex-1 gap-0.5">
          <b className={cn('text-[13.5px] leading-snug', card.state === 'declined' && 'text-muted-foreground line-through')}>{card.action.title}</b>
          {card.action.detail && <small className="text-[12px] text-muted-foreground">{card.action.detail}</small>}
          {card.state === 'failed' && <small className="text-[12px] text-destructive">{card.error}</small>}
          {card.state === 'done' && !token && <small className="text-[12px] text-success">已执行</small>}
        </div>
      </div>
      {token && (
        <button type="button" onClick={() => navigator.clipboard.writeText(token).then(() => toast.success('令牌已复制'))}
          className="mt-2.5 flex w-full items-center gap-2 rounded-lg border border-dashed border-primary/40 bg-card px-2.5 py-2 text-left hover:bg-brand-soft/50">
          <KeyRoundIcon className="size-4 shrink-0 text-primary" />
          <code className="min-w-0 flex-1 font-mono text-[12px] break-all">{token}</code>
          <CopyIcon className="size-3.5 shrink-0 text-muted-foreground" />
        </button>
      )}
      {token && <p className="mt-1 text-[11.5px] text-muted-foreground">只显示这一次，复制下来交给持有人</p>}
      {card.state === 'ask' && (
        <div className="mt-2.5 flex justify-end gap-2">
          <Button variant="ghost" size="sm" className="rounded-lg" onClick={() => onChange({ ...card, state: 'declined' })}>不要</Button>
          <Button size="sm" className="rounded-lg font-bold" onClick={run}>确认执行</Button>
        </div>
      )}
    </div>
  );
}

function TurnView({ t }: { t: Turn }) {
  const patch = useAgent((s) => s.patch);
  return (
    <div className="grid gap-3">
      <div className="ml-auto max-w-[85%] rounded-2xl rounded-br-md bg-primary px-3.5 py-2 text-[13.5px] whitespace-pre-wrap text-primary-foreground">{t.q}</div>
      <div className="grid gap-2.5">
        {t.steps.length > 0 && (
          <ol className="grid gap-0.5">
            {t.steps.map((s, i) => (
              <li key={i} className="flex items-center gap-1.5 text-[12px] text-muted-foreground">
                {!s.done ? <LoaderCircleIcon className="size-3 animate-spin text-primary" /> : s.failed ? <CircleAlertIcon className="size-3 text-destructive" /> : <CheckIcon className="size-3 text-success" />}
                {stepText(s)}
              </li>
            ))}
          </ol>
        )}
        {t.status === 'running' && !t.text && !t.steps.some((s) => !s.done) && <LoaderCircleIcon className="size-4 animate-spin text-muted-foreground" />}
        {t.text && (
          <div className="prose-sm max-w-none text-[13.5px] leading-relaxed [&_li]:my-0.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-1.5 [&_strong]:font-extrabold [&_table]:my-2 [&_table]:text-[12.5px] [&_td]:border [&_td]:px-1.5 [&_th]:border [&_th]:px-1.5 [&_ul]:list-disc [&_ul]:pl-5">
            <Markdown remarkPlugins={[remarkGfm]}>{t.text}</Markdown>
          </div>
        )}
        {t.cards.map((c, i) => (
          <ActionCard key={i} card={c} onChange={(nc) => patch(t.id, (x) => ({ ...x, cards: x.cards.map((y, j) => (j === i ? nc : y)) }))} />
        ))}
        {t.status === 'error' && <p className="rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive">{t.error}</p>}
        {t.status === 'stopped' && <p className="text-[12px] text-muted-foreground">停下了</p>}
      </div>
    </div>
  );
}

function usePhone() {
  const [phone, setPhone] = useState(() => window.matchMedia('(max-width: 767px)').matches);
  useEffect(() => {
    const m = window.matchMedia('(max-width: 767px)');
    const on = () => setPhone(m.matches);
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, []);
  return phone;
}

export function AgentPanel() {
  const { ov } = useConsole();
  const { open, setOpen, turns, ctrl, send, reset } = useAgent();
  const [text, setText] = useState('');
  const phone = usePhone();
  const end = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === 'j' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        useAgent.getState().setOpen(!useAgent.getState().open);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  useEffect(() => { end.current?.scrollIntoView({ block: 'end' }); }, [turns]);
  const go = (q: string) => { send(q); setText(''); };
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetContent side={phone ? 'bottom' : 'right'} onOpenAutoFocus={(e) => { e.preventDefault(); box.current?.focus(); }}
        className={cn('gap-0 bg-card p-0', phone ? 'h-[92dvh] rounded-t-[20px] pb-[env(safe-area-inset-bottom)]' : 'data-[side=right]:w-full data-[side=right]:sm:max-w-[500px]')}>
        <div className="flex items-center gap-2.5 border-b px-5 py-3.5 pr-12">
          <span className="grid size-8 place-items-center rounded-[10px] bg-foreground text-background"><SparklesIcon className="size-4" /></span>
          <div className="grid leading-tight">
            <SheetTitle className="text-[15px] font-extrabold">工作台助手</SheetTitle>
            <SheetDescription className="text-[12px]">改动都要你确认后才执行</SheetDescription>
          </div>
          {turns.length > 0 && (
            <Button variant="ghost" size="icon-sm" className="ml-auto rounded-lg text-muted-foreground" title="新对话" aria-label="新对话" onClick={reset}><SquarePenIcon className="size-4" /></Button>
          )}
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {!turns.length ? (
            <div className="grid gap-4 pt-6">
              <p className="text-[13.5px] text-muted-foreground">{ov.me.name}，想查什么、改什么，直接说。</p>
              <div className="grid gap-2">
                {SUGGEST.map((s) => (
                  <button key={s} type="button" onClick={() => go(s)} className="rounded-xl border px-3 py-2.5 text-left text-[13px] hover:border-primary/40 hover:bg-brand-soft/40">{s}</button>
                ))}
              </div>
            </div>
          ) : (
            <div className="grid gap-6">{turns.map((t) => <TurnView key={t.id} t={t} />)}</div>
          )}
          <div ref={end} />
        </div>
        <form className="border-t p-3" onSubmit={(e) => { e.preventDefault(); go(text); }}>
          <div className="relative">
            <Textarea ref={box} value={text} onChange={(e) => setText(e.target.value)} rows={2} placeholder="比如：二厂再加一条曲轴线"
              onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); go(text); } }}
              className="max-h-40 min-h-[64px] resize-none rounded-xl pr-12 text-[13.5px]" aria-label="对助手说" />
            {ctrl
              ? <Button type="button" size="icon-sm" variant="outline" className="absolute right-2 bottom-2 size-8 rounded-lg" aria-label="停下" onClick={() => ctrl.abort()}><CircleStopIcon /></Button>
              : <Button type="submit" size="icon-sm" className="absolute right-2 bottom-2 size-8 rounded-lg" aria-label="发送" disabled={!text.trim()}><ArrowUpIcon /></Button>}
          </div>
        </form>
      </SheetContent>
    </Sheet>
  );
}
