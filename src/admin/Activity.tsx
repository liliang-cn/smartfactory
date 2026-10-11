// 操作记录: who changed what, in which company, by which way in.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { LoaderCircleIcon, RefreshCwIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { cn } from '@/lib/utils';
import { dayLabel, describe, ROLE, VIA, type Entry, type Via } from '@/audit/describe';
import * as api from './api';
import { PageHead, useConsole } from './Console';

const PAGE = 100;

export function ActivityList({ company: fixed }: { company?: string }) {
  const { ov } = useConsole();
  const [company, setCompany] = useState(fixed ?? (ov.me.everywhere ? '' : ov.companies[0]?.id ?? ''));
  const [entries, setEntries] = useState<Entry[]>([]);
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [via, setVia] = useState<Via | 'all'>('all');
  const [failed, setFailed] = useState(false);
  const names = useMemo(() => new Map(ov.companies.map((c) => [c.id, c.name])), [ov.companies]);

  const load = useCallback(async (before?: number) => {
    setBusy(true);
    setError(undefined);
    try {
      const page = await api.activity(company, before);
      setEntries((old) => (before ? [...old, ...page] : page));
      setMore(page.length === PAGE);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }, [company]);
  useEffect(() => { load(); }, [load]);

  const shown = entries.filter((e) => (via === 'all' || e.via === via) && (!failed || !e.ok));
  const days: { day: string; items: Entry[] }[] = [];
  for (const e of shown) {
    const day = dayLabel(new Date(e.at));
    if (days.at(-1)?.day !== day) days.push({ day, items: [] });
    days.at(-1)!.items.push(e);
  }
  const failures = entries.filter((e) => !e.ok).length;
  const where = (c: string) => (c === '*' ? '所有公司' : names.get(c) ?? c);

  return (
    <section className="rounded-[18px] bg-card shadow-float">
      <div className="flex flex-wrap items-center gap-2 border-b p-4 px-5 max-sm:px-4">
        {!fixed && (
          <Select value={company || 'all'} onValueChange={(v) => setCompany(v === 'all' ? '' : v)}>
            <SelectTrigger className="h-9 w-44 rounded-xl font-bold" aria-label="公司"><SelectValue /></SelectTrigger>
            <SelectContent>
              {ov.me.everywhere && <SelectItem value="all">所有公司</SelectItem>}
              {ov.me.everywhere && <SelectItem value="*">共用的（行业包、全局令牌）</SelectItem>}
              {ov.companies.map((c) => <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>)}
            </SelectContent>
          </Select>
        )}
        <ToggleGroup type="single" value={via} onValueChange={(v) => v && setVia(v as Via | 'all')} className="rounded-xl bg-surface p-0.5">
          <ToggleGroupItem value="all" className="h-8 rounded-lg px-2.5 text-[12.5px] font-bold data-[state=on]:bg-card data-[state=on]:shadow-sm">全部</ToggleGroupItem>
          {(Object.keys(VIA) as Via[]).map((k) => (
            <ToggleGroupItem key={k} value={k} className="h-8 rounded-lg px-2.5 text-[12.5px] font-bold data-[state=on]:bg-card data-[state=on]:shadow-sm">{VIA[k].label}</ToggleGroupItem>
          ))}
        </ToggleGroup>
        <button type="button" onClick={() => setFailed((f) => !f)} disabled={!failures && !failed}
          className={cn('h-9 rounded-xl px-2.5 text-[12.5px] font-bold disabled:opacity-40', failed ? 'bg-danger-soft text-destructive' : 'text-muted-foreground hover:bg-surface')}>
          失败 {failures}
        </button>
        <Button variant="ghost" size="icon-sm" className="ml-auto rounded-lg text-muted-foreground" aria-label="刷新" title="刷新" disabled={busy} onClick={() => load()}>
          <RefreshCwIcon className={cn('size-4', busy && 'animate-spin')} />
        </Button>
      </div>
      <div className="px-5 pb-6 max-sm:px-4">
        {error && <p className="mt-5 rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
        {!error && !busy && !shown.length && <p className="py-12 text-center text-[13px] text-muted-foreground">{entries.length ? '没有符合条件的记录' : '还没有操作记录'}</p>}
        {days.map(({ day, items }) => (
          <section key={day}>
            <h3 className="pt-5 pb-1.5 text-[12px] font-extrabold text-muted-foreground">{day}</h3>
            <ol className="relative ml-[54px] border-l border-border">
              {items.map((e) => {
                const d = describe(e);
                const V = VIA[e.via];
                return (
                  <li key={e.id} className="relative py-2.5 pl-4">
                    <time dateTime={e.at} className="absolute top-3 -left-[54px] w-[46px] text-right font-mono text-[11.5px] text-muted-foreground tabular-nums">
                      {new Date(e.at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })}
                    </time>
                    <span className={cn('absolute top-[15px] -left-[4.5px] size-2 rounded-full ring-2 ring-card', e.ok ? 'bg-primary' : 'bg-destructive')} />
                    <div className="flex flex-wrap items-center gap-x-1.5 text-[12.5px]">
                      <b className="font-extrabold">{e.who}</b>
                      <span className="text-muted-foreground">{ROLE[e.role]}</span>
                      {!fixed && <span className="rounded-md bg-brand-soft px-1.5 py-px text-[11px] font-bold text-primary">{where(e.company)}</span>}
                      <span className="ml-auto inline-flex items-center gap-1 rounded-md bg-surface px-1.5 py-px text-[11px] font-bold text-muted-foreground" title={`令牌 ${e.key}`}>
                        <V.icon className="size-3" />{V.label}
                      </span>
                    </div>
                    <p className={cn('mt-0.5 text-[13.5px] leading-snug', !e.ok && 'text-muted-foreground line-through decoration-destructive/50')}>
                      {d.text}{d.asset && <span className="text-muted-foreground"> · {d.plant}/{d.asset}</span>}
                      {e.via === 'assistant' && e.detail && <span className="text-muted-foreground"> · 经{e.detail}</span>}
                    </p>
                    {!e.ok && e.error && <p className="mt-0.5 text-[12px] text-destructive">没有成功：{e.error}</p>}
                  </li>
                );
              })}
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
    </section>
  );
}

export default function Activity() {
  return (
    <>
      <PageHead title="操作记录" sub="设备指令、倍速、场景、资料、助手、令牌的每一次改动，谁做的、从哪里做的" />
      <ActivityList />
    </>
  );
}
