// Readings unusual for their machine: hints, not alarms. The server learns what each measured
// machine normally reads in each state and says when a reading stays off it; the top bar lists
// the company's hints, a machine's details show its fields against their normals.
import { useEffect, useState } from 'react';
import { ActivityIcon } from 'lucide-react';
import type { z } from 'zod';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';
import { operatorToken, useLive, type AnomalyInfo } from '@/live/client';
import type * as P from '@/protocol/sfp';
import { goToAsset, runtime } from '@/state/store';

type Machine = z.infer<typeof P.AnomalyMachine>;

export const FIELD_LABEL: Record<string, string> = { temp: '温度', power: '功率', vib: '振动', lastMeasure: '实测值', measure: '实测值' };
export const HINT_STATE: Record<string, string> = { running: '运行', idle: '待料', blocked: '阻塞', fault: '故障', stopped: '停机', maint: '维护', changeover: '换模', '': '不分状态' };
const UNIT: Record<string, string> = { temp: '°C', power: 'kW', vib: 'mm/s' };
export const fieldLabel = (f: string) => FIELD_LABEL[f] ?? f;
export const stateLabel = (s: string) => HINT_STATE[s] ?? s;

/** A number as people read it: three significant decimals at most. */
export const short = (x: number) => (Math.abs(x) >= 100 ? x.toFixed(1) : Math.abs(x) >= 10 ? x.toFixed(2) : x.toFixed(3)).replace(/\.?0+$/, '');
/** Wall-clock time of day (hints happen on the equipment's clock, not the plant's). */
const at = (iso: string) => new Date(iso).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
/** What a hint says of its field: 振动缓慢偏高. Which way is the way it went furthest. */
export const what = (h: { field: string; kind: string; peak: number }) => `${fieldLabel(h.field)}${h.kind === 'drift' ? '缓慢' : ''}${h.peak >= 0 ? '偏高' : '偏低'}`;
export const sigma = (z: number) => `${z >= 0 ? '+' : '−'}${Math.abs(z).toFixed(1)}σ`;

/** The company's hints now, in the top bar while there are any. */
export function HintChip() {
  const hints = useLive((s) => s.anomalies);
  if (!hints.length) return null;
  const place = (p: string) => runtime().byId[p]?.layout.name ?? p;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" className="h-[34px] gap-1.5 rounded-[10px] bg-warning-soft px-2.5 text-[13px] font-bold text-warning hover:bg-warning-soft/80 max-md:hidden"
          aria-label={`读数异常提示 ${hints.length} 条`} title="接了真实设备的机器，有读数和它自己平常不一样">
          <ActivityIcon className="size-4" />
          <span className="max-lg:hidden">读数异常</span>
          <span className="tabular-nums">{hints.length}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-[min(70vh,560px)] w-[340px] overflow-y-auto rounded-2xl p-2 shadow-float">
        <DropdownMenuLabel className="grid gap-0.5 leading-snug">
          <span className="text-[13px] font-extrabold">读数异常提示</span>
          <span className="text-xs font-normal text-muted-foreground">和设备自己同一状态下的平常读数比。只是提示，设备照常运行。</span>
        </DropdownMenuLabel>
        {hints.map((h) => (
          <DropdownMenuItem key={h.id} onSelect={() => goToAsset(h.plant, h.asset)} className="mt-0.5 grid gap-0.5 rounded-xl px-2 py-1.5">
            <span className="flex items-center gap-2 text-[13px]">
              <b>{h.asset}</b><span>{what(h)}</span>
              <b className="ml-auto text-warning tabular-nums" title="最远偏离">{sigma(h.peak)}</b>
            </span>
            <small className="text-[11.5px] text-muted-foreground tabular-nums">
              {place(h.plant)} · {stateLabel(h.state)}时平常 {short(h.mean)}±{short(h.sigma)} · {at(h.since)} 起
            </small>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

async function loadMachine(company: string, plant: string, asset: string): Promise<Machine | null> {
  const t = operatorToken();
  const res = await fetch(`/api/anomalies/${encodeURIComponent(company)}/${encodeURIComponent(plant)}/${encodeURIComponent(asset)}`, {
    headers: t ? { Authorization: `Bearer ${t}` } : {},
  });
  if (!res.ok) return null;
  return res.json();
}

/**
 * A measured machine's readings against what it normally reads in the state it is in: how far
 * each is now (in σ), the normal (mean ± σ), the hints on it. Nothing for a simulated machine.
 */
export function MachineHints({ plant, asset, measured }: { plant: string; asset: string; measured: boolean }) {
  const company = runtime().bundle.company.id;
  const live = useLive((s) => s.status === 'on') && measured;
  const hints = useLive((s) => s.anomalies).filter((h) => h.plant === plant && h.asset === asset);
  const [m, setM] = useState<Machine | null>(null);
  useEffect(() => {
    if (!live) return;
    let on = true;
    const get = () => loadMachine(company, plant, asset).then((x) => on && setM(x), () => on && setM(null));
    setM(null);
    get();
    const t = setInterval(() => { if (!document.hidden) get(); }, 10_000);
    return () => { on = false; clearInterval(t); };
  }, [live, company, plant, asset]);
  if (!m && !hints.length) return null;
  return (
    <div className="mt-3">
      {hints.map((h) => <Hint key={h.id} h={h} />)}
      {m && m.fields.length > 0 && (
        <>
          <h3 className="mb-1 flex items-baseline gap-2 text-[13px] font-extrabold">
            和平常比<small className="text-[11.5px] font-semibold text-muted-foreground">{stateLabel(m.state)}时，按这台设备自己的读数学</small>
          </h3>
          <table className="w-full text-[12px] tabular-nums">
            <tbody>
              {m.fields.map((f) => {
                const warm = f.now?.warm;
                const off = !!f.anomaly;
                return (
                  <tr key={f.field} className="border-t first:border-t-0">
                    <td className="py-1 pr-2 font-bold">{fieldLabel(f.field)}</td>
                    <td className="py-1 pr-2 text-right">{short(f.value)}<span className="ml-0.5 text-muted-foreground">{UNIT[f.field] ?? ''}</span></td>
                    <td className={cn('py-1 pr-2 text-right font-extrabold', off ? 'text-warning' : 'text-muted-foreground')}>{warm ? sigma(f.z) : ''}</td>
                    <td className="py-1 text-right text-muted-foreground">
                      {f.now && f.now.state !== m.state ? `${stateLabel(f.now.state)}时` : ''}
                      {f.now ? (warm ? `平常 ${short(f.now.mean)}±${short(f.now.sigma)}` : `学习中 ${Math.round(f.now.progress * 100)}%`) : '这个状态还没学'}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </>
      )}
    </div>
  );
}

function Hint({ h }: { h: AnomalyInfo }) {
  return (
    <div className="mb-2 rounded-xl bg-warning-soft px-3 py-2 text-[12.5px] leading-relaxed text-amber-900">
      <b className="flex items-center gap-1.5 text-[13px]"><ActivityIcon className="size-3.5" />
        {what(h)}，最远 {sigma(h.peak)}
      </b>
      {h.kind === 'drift' ? '近期平均' : '现在'} {short(h.value)}（{sigma(h.z)}），{stateLabel(h.state)}时平常 {short(h.mean)}±{short(h.sigma)}，{at(h.since)} 起。
      只是提示，设备照常运行。
    </div>
  );
}
