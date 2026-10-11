// 校准: each measured machine's simulation parameters held against what it measured — the
// template's, the ones fitted from its stored readings, the ones in force — and how far its
// model has been from the measurements. Fit, apply, take back.
import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { ChevronDownIcon, LoaderCircleIcon, RadioTowerIcon, RotateCcwIcon, SlidersHorizontalIcon, WandSparklesIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import * as api from './api';
import { ago } from './Accounts';
import { useConsole } from './Console';
import { Panel } from './Panel';
import { go } from './router';

const FIELD: Record<string, { label: string; unit: string }> = {
  temp: { label: '温度', unit: '°C' }, power: { label: '功率', unit: 'kW' }, vib: { label: '振动', unit: 'mm/s' },
};
const QUALITY: Record<string, { label: string; tone: string }> = {
  good: { label: '可信', tone: 'text-success' }, fair: { label: '参考', tone: 'text-warning' }, poor: { label: '粗略', tone: 'text-muted-foreground' },
};
const fmt = (v: number) => (Math.abs(v) >= 100 ? v.toFixed(0) : Math.abs(v) >= 10 ? v.toFixed(1) : v.toFixed(2).replace(/\.?0+$/, ''));

/** A day of a machine's model error in one field, minute by minute (hourly buckets). */
function Trend({ company, machine, field }: { company: string; machine: string; field: string }) {
  const [pts, setPts] = useState<api.ResidualPoint[] | null>(null);
  useEffect(() => {
    api.residuals(company, machine, field, Date.now() - 24 * 3600_000, 900).then(setPts, () => setPts([]));
  }, [company, machine, field]);
  if (!pts || pts.length < 2) return <span className="text-[12px] text-muted-foreground">{pts ? '数据还不够画趋势' : ''}</span>;
  const max = Math.max(...pts.map((p) => p.mae), 1e-6);
  const t0 = pts[0].t, t1 = pts[pts.length - 1].t;
  const d = pts.map((p, i) => `${i ? 'L' : 'M'}${((p.t - t0) / Math.max(1, t1 - t0)) * 100},${30 - (p.mae / max) * 28}`).join(' ');
  return (
    <svg viewBox="0 0 100 32" preserveAspectRatio="none" className="h-8 w-full" role="img" aria-label={`${FIELD[field]?.label}误差趋势`}>
      <path d={d} fill="none" stroke="var(--primary)" strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

function MachineCard({ company, m, busy, onApply, onRevert, onFit }: {
  company: string; m: api.CalibMachine; busy: boolean;
  onApply(): void; onRevert(): void; onFit(): void;
}) {
  const [open, setOpen] = useState(false);
  const fitted = m.params.filter((p) => p.fitted !== undefined);
  const key = `${m.plant}/${m.asset}`;
  return (
    <li className="rounded-xl border bg-card">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 p-3.5">
        <button type="button" onClick={() => setOpen((o) => !o)} className="flex min-w-0 flex-1 items-center gap-2.5 text-left" aria-expanded={open}>
          <ChevronDownIcon className={cn('size-4 shrink-0 text-muted-foreground transition-transform', open && 'rotate-180')} />
          <span className="grid min-w-0 leading-tight">
            <b className="font-mono text-[14px]">{m.asset}<span className="ml-2 font-sans text-[13px] font-bold">{m.label}</span></b>
            <small className="truncate text-[12px] text-muted-foreground">{m.plantName} {m.line}</small>
          </span>
        </button>
        <div className="flex flex-wrap items-center gap-1.5 text-[12px]">
          {m.live && <span className="flex items-center gap-1 rounded-md bg-brand-soft px-1.5 py-0.5 font-bold text-primary"><RadioTowerIcon className="size-3" />实测中</span>}
          {m.appliedAt && <span className="rounded-md bg-success-soft px-1.5 py-0.5 font-bold text-success" title={`${m.appliedBy ?? ''} ${new Date(m.appliedAt).toLocaleString('zh-CN')}`}>在用校准</span>}
          {m.fittedAt ? <span className="text-muted-foreground">拟合 {fitted.length} 项，{ago(m.fittedAt)}</span> : <span className="text-muted-foreground">还没拟合</span>}
        </div>
      </div>
      {(m.replay.length > 0 || m.residual.length > 0) && (
        <div className="grid gap-x-6 gap-y-2 border-t px-3.5 py-2.5 text-[12.5px] sm:grid-cols-2">
          {m.replay.length > 0 && (
            <div className="grid gap-0.5">
              <span className="text-[11.5px] font-bold text-muted-foreground">拟合期回放：模板 → 拟合的平均误差</span>
              {m.replay.map((r) => (
                <span key={r.field} className="tabular-nums">
                  {FIELD[r.field]?.label} <s className="text-muted-foreground">{fmt(r.before)}</s> → <b className={cn(r.after < r.before ? 'text-success' : 'text-destructive')}>{fmt(r.after)}</b> {FIELD[r.field]?.unit}
                </span>
              ))}
            </div>
          )}
          {m.residual.length > 0 && (
            <div className="grid gap-0.5">
              <span className="text-[11.5px] font-bold text-muted-foreground">近 24 小时：模型和实测差多少</span>
              {m.residual.map((r) => (
                <span key={r.field} className="tabular-nums">
                  {FIELD[r.field]?.label ?? r.field} 平均差 {fmt(r.mae)} {FIELD[r.field]?.unit}，{Math.round(r.within * 100)}% 的时间在容差内
                </span>
              ))}
            </div>
          )}
        </div>
      )}
      {open && (
        <div className="grid gap-3 border-t p-3.5">
          <Table>
            <TableHeader>
              <TableRow className="text-[12px]"><TableHead>参数</TableHead><TableHead className="text-right">模板</TableHead><TableHead>拟合</TableHead><TableHead className="text-right">在用</TableHead></TableRow>
            </TableHeader>
            <TableBody>
              {m.params.map((p) => (
                <TableRow key={p.name} className="text-[13px]">
                  <TableCell className="font-bold">{p.label}<span className="ml-1 font-normal text-muted-foreground">{p.unit}</span></TableCell>
                  <TableCell className="text-right tabular-nums text-muted-foreground">{fmt(p.template)}</TableCell>
                  <TableCell className="max-w-[320px]">
                    {p.fitted !== undefined ? (
                      <span className="tabular-nums"><b>{fmt(p.fitted)}</b>{p.quality && <span className={cn('ml-1.5 text-[12px]', QUALITY[p.quality]?.tone)}>{QUALITY[p.quality]?.label}</span>}<span className="ml-1 text-[12px] text-muted-foreground">{p.n}</span></span>
                    ) : <span className="text-[12px] text-muted-foreground">{p.note || '—'}</span>}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{p.applied !== undefined ? <b className="text-success">{fmt(p.applied)}</b> : ''}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {m.residual.length > 0 && (
            <div className="grid gap-2 sm:grid-cols-3">
              {m.residual.map((r) => (
                <div key={r.field} className="grid gap-1 rounded-lg bg-surface p-2">
                  <span className="text-[11.5px] font-bold text-muted-foreground">{FIELD[r.field]?.label}误差，近 24 小时</span>
                  <Trend company={company} machine={key} field={r.field} />
                </div>
              ))}
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2">
            {m.stored && <Button variant="outline" size="sm" className="rounded-lg font-bold" disabled={busy} onClick={onFit}><WandSparklesIcon />重新拟合</Button>}
            {fitted.length > 0 && <Button size="sm" className="rounded-lg font-bold" disabled={busy} onClick={onApply}>应用拟合的 {fitted.length} 项</Button>}
            {m.appliedAt && <Button variant="ghost" size="sm" className="rounded-lg text-muted-foreground hover:bg-danger-soft hover:text-destructive" disabled={busy} onClick={onRevert}><RotateCcwIcon />撤销，恢复模板</Button>}
          </div>
        </div>
      )}
    </li>
  );
}

export function Calibration({ c }: { c: api.Company }) {
  const { ov } = useConsole();
  const [list, setList] = useState<api.CalibMachine[] | null>(null);
  const [error, setError] = useState<string>();
  const [hours, setHours] = useState('24');
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(() => { api.calibration(c.id).then((l) => { setList(l); setError(undefined); }, (e: Error) => setError(e.message)); }, [c.id]);
  useEffect(load, [load]);
  const shown = (list ?? []).filter((m) => m.stored || m.appliedAt || m.residual.length > 0);
  const anyFitted = shown.some((m) => m.params.some((p) => p.fitted !== undefined));
  const anyApplied = shown.some((m) => m.appliedAt);
  const run = async (what: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(what);
    try {
      await fn();
      toast.success(done);
      load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(null);
    }
  };
  const outcome = (out: api.CalibOutcomes, verb: string) => {
    const bad = out.filter((o) => !o.ok);
    if (bad.length) toast.error(bad.map((o) => `${o.machine}：${o.error}`).join('；'));
    return `${out.filter((o) => o.ok).length} 台设备${verb}`;
  };
  const admin = ov.me.role === 'admin';
  return (
    <Panel
      title={<span className="flex items-center gap-2"><SlidersHorizontalIcon className="size-4 text-primary" />仿真校准</span>}
      aside={admin && shown.some((m) => m.stored) && (
        <>
          <Select value={hours} onValueChange={setHours}>
            <SelectTrigger className="h-9 w-[124px] rounded-xl" aria-label="用多长时间的读数"><SelectValue /></SelectTrigger>
            <SelectContent>
              {[['6', '最近 6 小时'], ['24', '最近 24 小时'], ['72', '最近 3 天'], ['168', '最近 7 天']].map(([v, l]) => <SelectItem key={v} value={v}>{l}</SelectItem>)}
            </SelectContent>
          </Select>
          <Button variant="outline" className="h-9 rounded-xl font-bold" disabled={!!busy}
            onClick={() => run('fit', async () => { const r = await api.fitCalibration(c.id, [], Number(hours)); return r; }, '已按实测拟合')}>
            {busy === 'fit' ? <LoaderCircleIcon className="animate-spin" /> : <WandSparklesIcon />}按实测拟合
          </Button>
          {anyFitted && (
            <Button className="h-9 rounded-xl font-bold" disabled={!!busy}
              onClick={async () => { setBusy('apply'); try { const out = await api.applyCalibration(c.id, []); toast.success(outcome(out, '改用拟合参数')); load(); } catch (e) { toast.error((e as Error).message); } finally { setBusy(null); } }}>
              {busy === 'apply' && <LoaderCircleIcon className="animate-spin" />}全部应用
            </Button>
          )}
          {anyApplied && (
            <Button variant="ghost" className="h-9 rounded-xl text-muted-foreground" disabled={!!busy}
              onClick={async () => { setBusy('revert'); try { const out = await api.revertCalibration(c.id, []); toast.success(outcome(out, '恢复模板参数')); load(); } catch (e) { toast.error((e as Error).message); } finally { setBusy(null); } }}>
              全部撤销
            </Button>
          )}
        </>
      )}
    >
      {error && <p className="text-[13px] text-destructive">{error}</p>}
      {list && !shown.length && (
        <div className="grid justify-items-center gap-2 py-8 text-center">
          <p className="max-w-md text-[13px] text-muted-foreground">还没有设备上报实测数据。接了数据的设备会在这里对比模型和实测，并按实测校准它的温升、功率、节拍和故障率。</p>
          <Button variant="outline" size="sm" className="rounded-lg font-bold" onClick={() => go(`${location.pathname}?tab=connect`, true)}>去接设备</Button>
        </div>
      )}
      {shown.length > 0 && (
        <ul className="grid gap-2.5">
          {shown.map((m) => {
            const k = `${m.plant}/${m.asset}`;
            return (
              <MachineCard key={k} company={c.id} m={m} busy={!!busy}
                onFit={() => run(k, () => api.fitCalibration(c.id, [k], Number(hours)), `${m.asset} 已重新拟合`)}
                onApply={async () => { setBusy(k); try { const out = await api.applyCalibration(c.id, [k]); toast.success(outcome(out, '改用拟合参数')); load(); } catch (e) { toast.error((e as Error).message); } finally { setBusy(null); } }}
                onRevert={async () => { setBusy(k); try { const out = await api.revertCalibration(c.id, [k]); toast.success(outcome(out, '恢复模板参数')); load(); } catch (e) { toast.error((e as Error).message); } finally { setBusy(null); } }} />
            );
          })}
        </ul>
      )}
    </Panel>
  );
}
