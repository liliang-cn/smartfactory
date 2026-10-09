// The company's audit log (GET /api/audit/{company}): who changed what, by which way in.
// Operators and administrators open it from their name in the top bar.
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { z } from 'zod';
import { BotIcon, LoaderCircleIcon, MousePointerClickIcon, PlugIcon, RadioTowerIcon, RefreshCwIcon, type LucideIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { cn } from '@/lib/utils';
import { useAgents } from '@/agents/store';
import { useBiz } from '@/advisor/store';
import { CMD_LABEL, operatorToken } from '@/live/client';
import type * as P from '@/protocol/sfp';
import type { Command } from '@/sim/types';
import { runtime, useUI } from '@/state/store';

type Entry = z.infer<typeof P.AuditEntry>;
type Via = Entry['via'];

const PAGE = 100;

const VIA: Record<Via, { label: string; icon: LucideIcon }> = {
  page: { label: '页面', icon: MousePointerClickIcon },
  live: { label: '实时', icon: RadioTowerIcon },
  assistant: { label: '助手', icon: BotIcon },
  mcp: { label: 'MCP', icon: PlugIcon },
};
const ROLE: Record<Entry['role'], string> = { visitor: '访客', member: '成员', operator: '操作员', admin: '管理员' };
const STATUS_ADVISOR: Record<string, string> = {
  adopt: '采纳计划', reject: '否决计划', measure: '开始验收', accept: '验收通过', review: '运行例行诊断',
};

async function fetchPage(company: string, before?: number): Promise<Entry[]> {
  const q = new URLSearchParams({ limit: String(PAGE) });
  if (before) q.set('before', String(before));
  const token = operatorToken();
  const res = await fetch(`/api/audit/${encodeURIComponent(company)}?${q}`, { headers: token ? { Authorization: `Bearer ${token}` } : {} });
  const body = await res.json().catch(() => null);
  if (!res.ok) throw new Error((body as { error?: string } | null)?.error ?? `请求失败（${res.status}）`);
  return body as Entry[];
}

/** "id title" as the server keeps a target: the title, or the id when there is none. */
const titleOf = (target: string) => {
  const i = target.indexOf(' ');
  return i < 0 ? target : target.slice(i + 1);
};
const idOf = (target: string) => target.split(' ')[0];

function commandText(detail: string) {
  const [cmd, arg] = detail.split(' ') as [Command, string | undefined];
  const label = CMD_LABEL[cmd] ?? cmd;
  if (arg == null) return label;
  const pct = `${Math.round(Number(arg) * 100)}%`;
  return cmd === 'speed' || cmd === 'toollimit' ? `${label} ${pct}` : `${label} ${arg}`;
}

/** What an entry did, as a sentence, and the machine it touched if any. */
function describe(e: Entry): { text: string; asset?: string; plant?: string } {
  switch (e.action) {
    case 'command': {
      const [plant, asset] = e.target.split('/');
      return { text: commandText(e.detail ?? ''), plant, asset };
    }
    case 'clock': return { text: `全厂倍速改为 ${e.detail}` };
    case 'save_note': return { text: `记笔记「${titleOf(e.target)}」` };
    case 'update_note': return { text: `更新笔记「${titleOf(e.target)}」` };
    case 'create_ticket': return { text: `登记工单 ${idOf(e.target)}「${titleOf(e.target)}」` };
    case 'update_ticket': return { text: `更新工单 ${idOf(e.target)}` };
    case 'ticket.status': return { text: `工单 ${idOf(e.target)} 改为「${e.detail}」` };
    case 'doc.add': return { text: `上传资料「${titleOf(e.target)}」` };
    case 'doc.delete': return { text: `删除资料 ${e.target}` };
    case 'note.delete': return { text: `删除笔记 ${e.target}` };
    case 'assistant.create': return { text: `新建助手「${titleOf(e.target)}」` };
    case 'assistant.update': return { text: `修改助手「${titleOf(e.target)}」` };
    case 'assistant.delete': return { text: `删除助手 ${e.target}` };
    case 'company.create': return { text: `建立或替换公司 ${e.target}` };
    case 'company.delete': return { text: `删除公司 ${e.target}` };
    case 'industry.create': return { text: `建立或替换行业包 ${e.target}` };
  }
  if (e.action.startsWith('advisor.')) {
    const verb = e.action.slice('advisor.'.length);
    return { text: `${STATUS_ADVISOR[verb] ?? verb}${e.target ? ` ${e.target}` : ''}` };
  }
  return { text: [e.action, e.target, e.detail].filter(Boolean).join(' ') };
}

function dayLabel(d: Date) {
  const today = new Date();
  const y = new Date(today);
  y.setDate(today.getDate() - 1);
  const same = (a: Date, b: Date) => a.toDateString() === b.toDateString();
  if (same(d, today)) return '今天';
  if (same(d, y)) return '昨天';
  return d.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'short', ...(d.getFullYear() !== today.getFullYear() && { year: 'numeric' }) });
}

function locate(plant: string, asset: string) {
  const rt = runtime();
  const p = rt.byId[plant] ?? rt.plants.find((x) => x.sim.assets.has(asset));
  if (!p) return;
  useAgents.getState().setOpen(false);
  useBiz.getState().setOpen(false);
  const ui = useUI.getState();
  const switching = ui.plantId !== p.layout.id;
  if (switching) ui.setPlant(p.layout.id);
  setTimeout(() => useUI.getState().select(asset, true), switching ? 400 : 0);
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

export default function AuditSheet({ open, onClose }: { open: boolean; onClose(): void }) {
  const rt = runtime();
  const company = rt.bundle.company.id;
  const phone = usePhone();
  const [entries, setEntries] = useState<Entry[]>([]);
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [via, setVia] = useState<Via | 'all'>('all');
  const [failed, setFailed] = useState(false);

  const load = useCallback(async (before?: number) => {
    setBusy(true);
    setError(null);
    try {
      const page = await fetchPage(company, before);
      setEntries((old) => (before ? [...old, ...page] : page));
      setMore(page.length === PAGE);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }, [company]);

  useEffect(() => {
    if (open) load();
  }, [open, load]);

  const shown = useMemo(() => entries.filter((e) => (via === 'all' || e.via === via) && (!failed || !e.ok)), [entries, via, failed]);
  const days = useMemo(() => {
    const out: { day: string; items: Entry[] }[] = [];
    for (const e of shown) {
      const day = dayLabel(new Date(e.at));
      if (out.at(-1)?.day !== day) out.push({ day, items: [] });
      out.at(-1)!.items.push(e);
    }
    return out;
  }, [shown]);
  const failures = entries.filter((e) => !e.ok).length;

  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      {/* focus the panel itself: no ring on the first filter, and nothing focused stays behind it */}
      <SheetContent side={phone ? 'bottom' : 'right'} onOpenAutoFocus={(ev) => { ev.preventDefault(); (ev.currentTarget as HTMLElement).focus(); }}
        className={cn('gap-0 bg-card p-0', phone ? 'h-[88dvh] rounded-t-[20px] pb-[env(safe-area-inset-bottom)]' : 'data-[side=right]:w-full data-[side=right]:sm:max-w-[560px]')}>
        <SheetHeader className="gap-3 border-b px-5 pt-5 pb-3">
          <div className="flex items-center gap-2 pr-8">
            <SheetTitle className="text-[16px] font-extrabold">操作记录</SheetTitle>
            <span className="text-[12.5px] text-muted-foreground">{rt.bundle.company.name}</span>
            <Button variant="ghost" size="icon-sm" className="ml-auto rounded-lg text-muted-foreground" aria-label="刷新" title="刷新" disabled={busy} onClick={() => load()}>
              <RefreshCwIcon className={cn('size-4', busy && 'animate-spin')} />
            </Button>
          </div>
          <SheetDescription className="sr-only">这家公司的设备指令、倍速、笔记和工单、资料和助手的改动，以及是谁、从哪里做的</SheetDescription>
          <div className="flex flex-wrap items-center gap-2">
            <ToggleGroup type="single" value={via} onValueChange={(v) => v && setVia(v as Via | 'all')} className="rounded-xl bg-surface p-0.5">
              <ToggleGroupItem value="all" className="h-7 rounded-lg px-2.5 text-[12.5px] font-bold data-[state=on]:bg-card data-[state=on]:shadow-sm">全部</ToggleGroupItem>
              {(Object.keys(VIA) as Via[]).map((k) => (
                <ToggleGroupItem key={k} value={k} className="h-7 rounded-lg px-2.5 text-[12.5px] font-bold data-[state=on]:bg-card data-[state=on]:shadow-sm">{VIA[k].label}</ToggleGroupItem>
              ))}
            </ToggleGroup>
            <button type="button" onClick={() => setFailed((f) => !f)} disabled={!failures && !failed}
              className={cn('h-8 rounded-xl px-2.5 text-[12.5px] font-bold disabled:opacity-40', failed ? 'bg-danger-soft text-destructive' : 'text-muted-foreground hover:bg-surface')}>
              失败 {failures}
            </button>
          </div>
        </SheetHeader>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-6">
          {error && <p className="mt-5 rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
          {!error && !busy && !shown.length && (
            <p className="mt-10 text-center text-[13px] text-muted-foreground">{entries.length ? '没有符合条件的记录' : '还没有操作记录'}</p>
          )}
          {days.map(({ day, items }) => (
            <section key={day}>
              <h4 className="sticky top-0 z-10 -mx-5 bg-card/95 px-5 pt-4 pb-1.5 text-[12px] font-extrabold text-muted-foreground backdrop-blur">{day}</h4>
              <ol className="relative ml-[54px] border-l border-border">
                {items.map((e) => <Row key={e.id} e={e} onLocate={onClose} />)}
              </ol>
            </section>
          ))}
          {more && (
            <div className="mt-4 flex justify-center">
              <Button variant="ghost" className="rounded-xl text-[13px] font-bold text-primary hover:bg-brand-soft" disabled={busy} onClick={() => load(entries.at(-1)?.id)}>
                {busy && <LoaderCircleIcon className="size-4 animate-spin" />}加载更早的
              </Button>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function Row({ e, onLocate }: { e: Entry; onLocate(): void }) {
  const rt = runtime();
  const d = describe(e);
  const Via = VIA[e.via];
  const at = new Date(e.at);
  const plantName = d.plant ? rt.byId[d.plant]?.layout.name ?? d.plant : null;
  return (
    <li className="relative py-2.5 pl-4">
      <time dateTime={e.at} className="absolute top-3 -left-[54px] w-[46px] text-right font-mono text-[11.5px] text-muted-foreground tabular-nums">
        {at.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })}
      </time>
      <span className={cn('absolute top-[15px] -left-[4.5px] size-2 rounded-full ring-2 ring-card', e.ok ? 'bg-primary' : 'bg-destructive')} />
      <div className="flex items-center gap-1.5 text-[12.5px]">
        <b className="font-extrabold">{e.who}</b>
        <span className="text-muted-foreground">{ROLE[e.role]}</span>
        <span className="ml-auto inline-flex items-center gap-1 rounded-md bg-surface px-1.5 py-px text-[11px] font-bold text-muted-foreground" title={`令牌 ${e.key}`}>
          <Via.icon className="size-3" />{Via.label}
        </span>
      </div>
      <p className={cn('mt-0.5 text-[13.5px] leading-snug', !e.ok && 'text-muted-foreground line-through decoration-destructive/50')}>
        {d.text}
        {d.asset && (
          <>
            {' · '}
            <button type="button" onClick={() => { onLocate(); locate(d.plant!, d.asset!); }} title="在孪生里查看"
              className="rounded-md bg-muted px-1 font-mono text-[12px] font-bold text-muted-foreground no-underline hover:bg-brand-soft hover:text-primary">
              {plantName ? `${plantName} ` : ''}{d.asset}
            </button>
          </>
        )}
        {e.via === 'assistant' && e.detail && <span className="text-muted-foreground"> · 经{e.detail}</span>}
      </p>
      {!e.ok && e.error && <p className="mt-0.5 text-[12px] text-destructive">没有成功：{e.error}</p>}
    </li>
  );
}
