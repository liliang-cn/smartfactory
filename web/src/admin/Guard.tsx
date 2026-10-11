// 指令: what the company lets through to its machines — the rules every command passes, how far
// AI may act on its own — and the commands an AI sent that wait for an operator.
import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { CheckIcon, LoaderCircleIcon, PlusIcon, RotateCcwIcon, ShieldCheckIcon, Trash2Icon, XIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { CMD_LABEL } from '@/live/client';
import type { Command } from '@/sim/types';
import * as api from './api';
import { ago } from './Accounts';
import { Panel } from './Panel';

type Level = api.GuardSettings['level'];
const LEVELS: { id: Level; title: string; sub: string }[] = [
  { id: 'advisory', title: '只提议', sub: 'AI 发给真实设备的指令都要操作员批准' },
  { id: 'guarded', title: '有护栏', sub: '选中的指令要批准，其余过了规则就执行' },
  { id: 'autonomous', title: '自主', sub: 'AI 的指令过了规则就执行' },
];
const STATUS: Record<api.CommandRequest['status'], { label: string; tone: string }> = {
  waiting: { label: '等批准', tone: 'bg-warning-soft text-warning' },
  deciding: { label: '处理中', tone: 'bg-surface text-muted-foreground' },
  sent: { label: '已执行', tone: 'bg-success-soft text-success' },
  refused: { label: '规则拒绝', tone: 'bg-danger-soft text-destructive' },
  denied: { label: '已拒绝', tone: 'bg-surface text-muted-foreground' },
  expired: { label: '过期', tone: 'bg-surface text-muted-foreground' },
  failed: { label: '没执行成', tone: 'bg-danger-soft text-destructive' },
};
const cmdText = (cmd: string, arg?: number) => `${CMD_LABEL[cmd as Command] ?? cmd}${arg !== undefined ? ` ${cmd === 'speed' ? `${Math.round(arg * 100)}%` : arg}` : ''}`;
const hhmm = (iso?: string) => (iso ? new Date(iso).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) : '');

/** One command waiting for a person, with approve and deny. */
export function Waiting({ company, r, onDone }: { company: string; r: api.CommandRequest; onDone(): void }) {
  const [busy, setBusy] = useState<'approve' | 'deny' | null>(null);
  const [denying, setDenying] = useState(false);
  const [reason, setReason] = useState('');
  const act = async (verb: 'approve' | 'deny') => {
    setBusy(verb);
    try {
      const done = verb === 'approve' ? await api.approveCommand(company, r.id) : await api.denyCommand(company, r.id, reason);
      if (done.status === 'sent') toast.success(`已执行：${r.asset} ${cmdText(r.cmd, r.arg)}`);
      else if (verb === 'approve') toast.error(`没有执行：${done.result ?? ''}`);
      else toast.success(`已拒绝 ${r.id}`);
      onDone();
    } catch (e) {
      toast.error((e as Error).message);
      onDone();
    } finally {
      setBusy(null);
    }
  };
  return (
    <li className="grid gap-2 rounded-xl border border-warning/40 bg-warning-soft/40 p-3.5">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <b className="text-[14px]">{r.who}</b>
        <span className="text-[13.5px]">要对 <b>{r.asset}</b> 执行「{cmdText(r.cmd, r.arg)}」</span>
        <small className="ml-auto text-[12px] text-muted-foreground tabular-nums">{r.id}，{hhmm(r.expires)} 前有效</small>
      </div>
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">发出时：{r.context}</p>
      {denying ? (
        <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); act('deny'); }}>
          <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="为什么不批（会告诉发指令的一方，可不填）" className="h-9 rounded-lg text-[13px]" autoFocus />
          <Button type="submit" variant="outline" className="h-9 rounded-lg font-bold" disabled={!!busy}>{busy === 'deny' && <LoaderCircleIcon className="animate-spin" />}拒绝</Button>
          <Button type="button" variant="ghost" className="h-9 rounded-lg" onClick={() => setDenying(false)}>取消</Button>
        </form>
      ) : (
        <div className="flex gap-2">
          <Button className="h-9 rounded-lg font-bold" disabled={!!busy} onClick={() => act('approve')}>{busy === 'approve' ? <LoaderCircleIcon className="animate-spin" /> : <CheckIcon />}批准并下发</Button>
          <Button variant="outline" className="h-9 rounded-lg font-bold" disabled={!!busy} onClick={() => setDenying(true)}><XIcon />拒绝</Button>
        </div>
      )}
    </li>
  );
}

export function GuardPanel({ c }: { c: api.Company }) {
  const [ov, setOv] = useState<api.GuardOverview | null>(null);
  const [list, setList] = useState<api.CommandRequest[]>([]);
  const [draft, setDraft] = useState<api.GuardSettings | null>(null);
  const [error, setError] = useState<string>();
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => {
    api.guard(c.id).then((o) => { setOv(o); setDraft((d) => d ?? o.settings); }, (e: Error) => setError(e.message));
    api.commandRequests(c.id).then(setList, () => {});
  }, [c.id]);
  useEffect(() => {
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, 4000);
    return () => clearInterval(t);
  }, [load]);
  if (error) return <Panel title="指令把关"><p className="text-[13px] text-destructive">{error}</p></Panel>;
  if (!ov || !draft) return <Panel title="指令把关"><div className="h-24" /></Panel>;
  const waiting = list.filter((r) => r.status === 'waiting');
  const decided = list.filter((r) => r.status !== 'waiting' && r.status !== 'deciding').slice(0, 12);
  const admin = ov.admin;
  const set = (patch: Partial<api.GuardSettings>) => setDraft((d) => d && { ...d, ...patch });
  const rule = (i: number, patch: Partial<api.GuardSettings['rules'][number]>) => set({ rules: draft.rules.map((r, j) => (j === i ? { ...r, ...patch } : r)) });
  const dirty = JSON.stringify(draft) !== JSON.stringify(ov.settings);
  const save = async () => {
    setBusy(true);
    setProblems([]);
    try {
      const o = await api.setGuard(c.id, draft);
      setOv(o);
      setDraft(o.settings);
      toast.success('指令把关已保存');
    } catch (e) {
      const err = e as api.ApiError;
      setProblems(err.problems?.length ? err.problems : [err.message]);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="grid gap-5">
      {waiting.length > 0 && (
        <Panel title={<span className="flex items-center gap-2">等待批准<span className="rounded-md bg-warning-soft px-1.5 py-0.5 text-[12px] font-bold text-warning tabular-nums">{waiting.length}</span></span>}>
          <ul className="grid gap-2.5">{waiting.map((r) => <Waiting key={r.id} company={c.id} r={r} onDone={load} />)}</ul>
        </Panel>
      )}

      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
        <Panel title="AI 能做到哪一步" aside={!admin && <small className="text-[12px] text-muted-foreground">管理员可以改</small>}>
          <div className="grid gap-2 sm:grid-cols-3">
            {LEVELS.map((l) => (
              <button key={l.id} type="button" disabled={!admin} onClick={() => set({ level: l.id })}
                className={cn('grid gap-0.5 rounded-xl border p-3 text-left transition-colors disabled:cursor-default', draft.level === l.id ? 'border-primary bg-brand-soft/50' : 'enabled:hover:bg-surface')}>
                <b className="text-[13.5px]">{l.title}</b><small className="text-[12px] leading-snug text-muted-foreground">{l.sub}</small>
              </button>
            ))}
          </div>
          {draft.level === 'guarded' && (
            <div className="mt-4 grid gap-2">
              <span className="text-[12.5px] font-bold">要批准的指令</span>
              <div className="flex flex-wrap gap-1.5">
                {ov.commands.map((cmd) => {
                  const on = draft.approve.includes(cmd);
                  return (
                    <button key={cmd} type="button" disabled={!admin} aria-pressed={on}
                      onClick={() => set({ approve: on ? draft.approve.filter((x) => x !== cmd) : [...draft.approve, cmd] })}
                      className={cn('h-8 rounded-lg border px-2.5 text-[12.5px] font-bold', on ? 'border-primary bg-brand-soft text-primary' : 'text-muted-foreground enabled:hover:bg-surface')}>
                      {CMD_LABEL[cmd as Command] ?? cmd}
                    </button>
                  );
                })}
              </div>
            </div>
          )}
          <label className="mt-4 flex items-center gap-2 text-[12.5px]">
            <span className="font-bold">等待批准</span>
            <Input type="number" min={1} max={1440} value={draft.expire} disabled={!admin} onChange={(e) => set({ expire: Number(e.target.value) || 0 })} className="h-8 w-20 rounded-lg text-center tabular-nums" aria-label="等待批准的分钟数" />
            <span className="text-muted-foreground">分钟后作废</span>
          </label>

          <div className="mt-6 flex items-center gap-2">
            <h3 className="text-[13.5px] font-extrabold">规则</h3>
            <small className="text-[12px] text-muted-foreground">人和 AI 的指令都要过；条件成立就不执行</small>
            {admin && <Button variant="ghost" size="sm" className="ml-auto h-7 rounded-lg text-[12.5px]" onClick={() => set({ rules: ov.defaults.rules })}><RotateCcwIcon />恢复默认</Button>}
          </div>
          <ul className="mt-2 grid gap-2">
            {draft.rules.map((r, i) => (
              <li key={i} className="grid grid-cols-[110px_minmax(0,1fr)_32px] gap-2 rounded-xl border p-2 max-md:grid-cols-[minmax(0,1fr)_32px]">
                <Select value={r.cmd} onValueChange={(v) => rule(i, { cmd: v as typeof r.cmd })} disabled={!admin}>
                  <SelectTrigger className="h-9 w-full rounded-lg text-[12.5px]" aria-label="指令"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="*">所有指令</SelectItem>
                    {ov.commands.map((cmd) => <SelectItem key={cmd} value={cmd}>{CMD_LABEL[cmd as Command] ?? cmd}</SelectItem>)}
                  </SelectContent>
                </Select>
                <div className="grid gap-1.5 max-md:col-span-2 max-md:row-start-2">
                  <Input value={r.when} disabled={!admin} onChange={(e) => rule(i, { when: e.target.value })} placeholder="temp > warn" className="h-9 rounded-lg font-mono text-[12.5px]" aria-label="条件" />
                  <Input value={r.say} disabled={!admin} onChange={(e) => rule(i, { say: e.target.value })} placeholder="拒绝的理由，可以写 {temp} {warn}" className="h-9 rounded-lg text-[12.5px]" aria-label="理由" />
                </div>
                {admin && <Button variant="ghost" size="icon" className="size-9 rounded-lg text-muted-foreground max-md:col-start-2 max-md:row-start-1" aria-label="删掉这条规则" onClick={() => set({ rules: draft.rules.filter((_, j) => j !== i) })}><Trash2Icon /></Button>}
              </li>
            ))}
          </ul>
          {admin && <Button variant="ghost" size="sm" className="mt-2 w-fit rounded-lg text-[12.5px]" onClick={() => set({ rules: [...draft.rules, { cmd: 'start', when: '', say: '' }] })}><PlusIcon />加一条规则</Button>}
          <p className="mt-3 text-[12px] leading-relaxed text-muted-foreground">
            条件写成 字段 比较 值，多个用 and 连接。字段：<code className="font-mono">state</code>（running、idle、fault、stopped…）、<code className="font-mono">temp</code>、<code className="font-mono">warn</code>、<code className="font-mono">trip</code>、<code className="font-mono">speed</code>、<code className="font-mono">health</code>、<code className="font-mono">alarms</code>（在报警的条数）、<code className="font-mono">arg</code>（指令的参数）、<code className="font-mono">maint</code>、<code className="font-mono">changing</code>。
          </p>
          {problems.length > 0 && <ul className="mt-3 grid gap-1 rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive">{problems.map((p, i) => <li key={i}>{p}</li>)}</ul>}
          {admin && (
            <div className="mt-4 flex items-center gap-2 border-t pt-4">
              {dirty && <Button variant="ghost" className="rounded-xl" onClick={() => { setDraft(ov.settings); setProblems([]); }}>不改了</Button>}
              <Button className="ml-auto h-10 rounded-xl font-bold" disabled={!dirty || busy} onClick={save}>{busy && <LoaderCircleIcon className="animate-spin" />}保存</Button>
            </div>
          )}
        </Panel>

        <div className="grid content-start gap-5">
          <Panel title="接了真实设备的机器">
            {ov.machines.length ? (
              <>
                <p className="mb-2 text-[12.5px] text-muted-foreground">这些机器的指令会经过把关，其余的只到孪生。</p>
                <div className="flex flex-wrap gap-1.5">{ov.machines.map((m) => <span key={m} className="rounded-lg bg-surface px-2 py-1 font-mono text-[12px]">{m}</span>)}</div>
              </>
            ) : (
              <p className="flex items-start gap-2 text-[12.5px] leading-relaxed text-muted-foreground"><ShieldCheckIcon className="mt-0.5 size-4 shrink-0" />还没有真实设备接进来。设备接上之后，发给它们的指令都会经过这里的规则。</p>
            )}
          </Panel>
          <Panel title="最近处理的">
            {decided.length ? (
              <ul className="grid gap-2">
                {decided.map((r) => (
                  <li key={r.id} className="grid gap-0.5 text-[12.5px]">
                    <div className="flex items-center gap-1.5">
                      <span className={cn('rounded-md px-1.5 py-px text-[11.5px] font-bold', STATUS[r.status].tone)}>{STATUS[r.status].label}</span>
                      <b>{r.asset}</b><span>{cmdText(r.cmd, r.arg)}</span>
                      <small className="ml-auto text-muted-foreground">{ago(r.decidedAt ?? r.created)}</small>
                    </div>
                    <span className="text-muted-foreground">{r.who} 发起{r.decidedBy ? `，${r.decidedBy} 处理` : ''}{r.result ? `：${r.result}` : ''}</span>
                  </li>
                ))}
              </ul>
            ) : <p className="text-[12.5px] text-muted-foreground">还没有 AI 发来要批准的指令。</p>}
          </Panel>
        </div>
      </div>
    </div>
  );
}
