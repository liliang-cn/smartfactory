// 读数异常: what each measured machine normally reads, learned per field and state from its own
// readings, and the hints when a reading stays off it — now and over the last 30 days. Hints, not
// alarms: nothing stops. After maintenance a machine reads differently: an administrator has it
// learned again.
import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { ActivityIcon, ChevronDownIcon, RotateCcwIcon } from 'lucide-react';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { fieldLabel, short, sigma, stateLabel, what } from '@/ui/Hints';
import * as api from './api';
import { ago } from './Accounts';
import { Panel } from './Panel';

const span = (s: number) => (s >= 86400 ? `${+(s / 86400).toFixed(1)} 天` : s >= 3600 ? `${+(s / 3600).toFixed(1)} 小时` : s >= 60 ? `${Math.round(s / 60)} 分钟` : `${Math.round(s)} 秒`);
/** a field read since the server started (its last reading is not the zero time) */
const read = (f: { at: string }) => Date.parse(f.at) > 0;
const when = (iso: string) => new Date(iso).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' });

function HintRow({ a }: { a: api.Anomaly }) {
  const ended = a.ended ? (Date.parse(a.ended) - Date.parse(a.since)) / 1000 : null;
  return (
    <li className={cn('grid gap-0.5 rounded-xl px-3 py-2', ended === null ? 'bg-warning-soft' : 'border')}>
      <span className="flex flex-wrap items-baseline gap-x-2 text-[13px]">
        <b className="font-mono">{a.asset}</b>
        <b>{what(a)}</b>
        <b className={cn('tabular-nums', ended === null ? 'text-warning' : 'text-muted-foreground')} title="最远偏离">{sigma(a.peak)}</b>
        <span className="ml-auto text-[12px] text-muted-foreground tabular-nums">{when(a.since)}{ended !== null ? `，持续 ${span(ended)}` : ' 起'}</span>
      </span>
      <small className="text-[12px] text-muted-foreground tabular-nums">
        {a.kind === 'drift' ? '近期平均' : '读数'} {short(a.value)}，{stateLabel(a.state)}时平常 {short(a.mean)}±{short(a.sigma)} · {a.plant} · 来自 {a.source}
      </small>
    </li>
  );
}

function MachineRow({ m, admin, onReset }: { m: api.AnomalyReport['machines'][number]; admin: boolean; onReset(): void }) {
  const [open, setOpen] = useState(false);
  const off = m.fields.filter((f) => f.anomaly).length;
  const learning = m.fields.filter((f) => f.now && !f.now.warm).length;
  const last = m.fields.reduce((t, f) => Math.max(t, Date.parse(f.at)), 0);
  return (
    <li className="rounded-xl border">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2.5">
        <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="flex min-w-0 flex-1 items-center gap-2.5 text-left">
          <ChevronDownIcon className={cn('size-4 shrink-0 text-muted-foreground transition-transform', open && 'rotate-180')} />
          <span className="grid min-w-0 leading-tight">
            <b className="font-mono text-[14px]">{m.asset}<span className="ml-2 font-sans text-[12.5px] font-semibold text-muted-foreground">{m.plant} · {stateLabel(m.state)}</span></b>
            <small className="truncate text-[12px] text-muted-foreground">
              {m.fields.map((f) => fieldLabel(f.field)).join('、')}{m.source ? ` · 来自 ${m.source}` : ''}{last > 0 ? ` · ${ago(new Date(last).toISOString())}` : ' · 重启后还没收到读数'}
            </small>
          </span>
        </button>
        <span className="flex items-center gap-1.5 text-[12px]">
          {off > 0 && <span className="rounded-md bg-warning-soft px-1.5 py-0.5 font-bold text-warning">{off} 项异常</span>}
          {learning > 0 && <span className="rounded-md bg-surface px-1.5 py-0.5 font-bold text-muted-foreground">{learning} 项学习中</span>}
        </span>
      </div>
      {open && (
        <div className="grid gap-3 border-t p-3">
          <Table>
            <TableHeader>
              <TableRow className="text-[12px]">
                <TableHead>读数</TableHead><TableHead>状态</TableHead><TableHead className="text-right">平常</TableHead><TableHead className="text-right">学过的读数</TableHead><TableHead className="text-right">现在</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {m.fields.flatMap((f) => f.states.map((b, i) => (
                <TableRow key={`${f.field}/${b.state}`} className="text-[13px]">
                  <TableCell className="font-bold">{i === 0 ? fieldLabel(f.field) : ''}</TableCell>
                  <TableCell className={cn(b.state === m.state && 'font-bold')}>{stateLabel(b.state)}</TableCell>
                  <TableCell className="text-right tabular-nums">{short(b.mean)}±{short(b.sigma)}{!b.warm && <span className="ml-1.5 text-[12px] text-muted-foreground">学了 {Math.round(b.progress * 100)}%</span>}</TableCell>
                  <TableCell className="text-right tabular-nums text-muted-foreground">{b.n.toLocaleString()}</TableCell>
                  <TableCell className={cn('text-right font-bold tabular-nums', f.anomaly ? 'text-warning' : 'text-muted-foreground')}>
                    {i === 0 && read(f) ? `${short(f.value)}${f.now?.warm ? `  ${sigma(f.z)}` : ''}` : i === 0 ? '—' : ''}
                  </TableCell>
                </TableRow>
              )))}
            </TableBody>
          </Table>
          {admin && (
            <Button variant="outline" size="sm" className="w-fit rounded-lg font-bold" onClick={onReset}><RotateCcwIcon />重新学习</Button>
          )}
        </div>
      )}
    </li>
  );
}

export function AnomalyPanel({ c }: { c: api.Company }) {
  const [rep, setRep] = useState<api.AnomalyReport | null>(null);
  const [error, setError] = useState<string>();
  const [resetting, setResetting] = useState<string | null>(null);
  const load = useCallback(() => { api.anomalies(c.id).then((r) => { setRep(r); setError(undefined); }, (e: Error) => setError(e.message)); }, [c.id]);
  useEffect(() => {
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, 15_000);
    return () => clearInterval(t);
  }, [load]);
  const ended = (rep?.recent ?? []).filter((a) => a.ended).slice(0, 20);
  const s = rep?.settings;
  return (
    <Panel title={<span className="flex items-center gap-2"><ActivityIcon className="size-4 text-warning" />读数异常</span>} className="xl:col-span-2">
      {error && <p className="text-[13px] text-destructive">{error}</p>}
      {s && (
        <p className="mb-3 text-[12.5px] leading-relaxed text-muted-foreground">
          每台接了真实数据的设备，按它自己在各个状态下的读数学平常值（每个状态先学 {span(s.warmup)}）。读数偏离平常 {s.k} 个标准差持续 {span(s.sustain)}，或近期平均慢慢偏离，就提示一次。只是提示，设备照常运行；仿真的设备不会有。
        </p>
      )}
      {rep && !rep.machines.length && <p className="text-[13px] text-muted-foreground">还没有设备上报实测数据。</p>}
      {rep && rep.machines.length > 0 && (
        <div className="grid gap-4">
          <section className="grid gap-1.5">
            <h3 className="text-[13px] font-extrabold">现在</h3>
            {rep.current.length ? <ul className="grid gap-1.5">{rep.current.map((a) => <HintRow key={a.id} a={a} />)}</ul> : <p className="text-[13px] text-muted-foreground">没有读数异常。</p>}
          </section>
          {ended.length > 0 && (
            <section className="grid gap-1.5">
              <h3 className="text-[13px] font-extrabold">最近 30 天结束的</h3>
              <ul className="grid gap-1.5">{ended.map((a) => <HintRow key={a.id} a={a} />)}</ul>
            </section>
          )}
          <section className="grid gap-1.5">
            <h3 className="text-[13px] font-extrabold">各设备的平常值</h3>
            <ul className="grid gap-1.5">
              {rep.machines.map((m) => <MachineRow key={`${m.plant}/${m.asset}`} m={m} admin={rep.admin} onReset={() => setResetting(`${m.plant}/${m.asset}`)} />)}
            </ul>
          </section>
        </div>
      )}
      <AlertDialog open={!!resetting} onOpenChange={(o) => !o && setResetting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>让 {resetting} 重新学习？</AlertDialogTitle>
            <AlertDialogDescription>
              检修、换件之后读数本来就会变。忘掉它学过的平常值，结束它现在的提示，从现在起重新学，学满 {s ? span(s.warmup) : ''}之前不提示。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>不了</AlertDialogCancel>
            <AlertDialogAction onClick={async () => {
              const k = resetting!;
              try {
                const out = await api.resetBaseline(c.id, [k]);
                const bad = out.filter((o) => !o.ok);
                if (bad.length) toast.error(bad.map((o) => `${o.machine}：${o.error}`).join('；'));
                else toast.success(`${k} 重新学习`);
                load();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}>重新学习</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Panel>
  );
}
