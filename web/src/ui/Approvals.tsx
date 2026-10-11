// Commands an AI sent to the company's real machines that wait for an operator: a chip in the top
// bar while there are any, and approve or deny right there.
import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { CheckIcon, LoaderCircleIcon, ShieldAlertIcon, XIcon } from 'lucide-react';
import type { z } from 'zod';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { CMD_LABEL, operatorToken, useLive } from '@/live/client';
import type * as P from '@/protocol/sfp';
import type { Command } from '@/sim/types';
import { runtime } from '@/state/store';

type Request = z.infer<typeof P.CommandRequest>;

async function call<T>(method: string, path: string, json?: unknown): Promise<T> {
  const token = operatorToken();
  const res = await fetch(path, {
    method,
    headers: { ...(json !== undefined && { 'Content-Type': 'application/json' }), ...(token && { Authorization: `Bearer ${token}` }) },
    body: json !== undefined ? JSON.stringify(json) : undefined,
  });
  const body = await res.json().catch(() => null);
  if (!res.ok) throw new Error((body as { error?: string } | null)?.error ?? `请求失败（${res.status}）`);
  return body as T;
}

const what = (r: Request) => `${CMD_LABEL[r.cmd as Command] ?? r.cmd}${r.arg !== undefined ? ` ${r.cmd === 'speed' ? `${Math.round(r.arg * 100)}%` : r.arg}` : ''}`;

export function Approvals() {
  const role = useLive((s) => s.role);
  const operator = role === 'operator' || role === 'admin';
  const company = runtime().bundle.company.id;
  const base = `/api/guard/${encodeURIComponent(company)}/requests`;
  const [list, setList] = useState<Request[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(() => { call<Request[]>('GET', `${base}?status=waiting`).then(setList, () => setList([])); }, [base]);
  useEffect(() => {
    if (!operator) return;
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, 5000);
    return () => clearInterval(t);
  }, [operator, load]);
  if (!operator || list.length === 0) return null;
  const decide = async (r: Request, verb: 'approve' | 'deny') => {
    setBusy(r.id + verb);
    try {
      const done = await call<Request>('POST', `${base}/${encodeURIComponent(r.id)}/${verb}`, {});
      if (verb === 'deny') toast.success(`已拒绝 ${r.asset} ${what(r)}`);
      else if (done.status === 'sent') toast.success(`已执行：${r.asset} ${what(r)}`);
      else toast.error(`没有执行：${done.result ?? ''}`);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(null);
      load();
    }
  };
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost" className="h-10 gap-1.5 rounded-xl bg-warning-soft px-3 font-bold text-warning hover:bg-warning-soft/80 max-md:h-9 max-md:px-2">
          <ShieldAlertIcon className="size-4" />待批准 {list.length}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[380px] max-w-[calc(100vw-24px)] rounded-2xl p-3">
        <p className="mb-2 px-1 text-[12.5px] text-muted-foreground">AI 发给真实设备的指令，批准后才会下发。</p>
        <ul className="grid max-h-[60dvh] gap-2 overflow-y-auto">
          {list.map((r) => (
            <li key={r.id} className="grid gap-1.5 rounded-xl border p-3">
              <p className="text-[13px] leading-snug"><b>{r.who}</b> 要对 <b>{r.asset}</b> 执行「{what(r)}」</p>
              <p className="text-[12px] leading-snug text-muted-foreground">{r.context}</p>
              <div className="flex gap-2">
                <Button size="sm" className="h-8 rounded-lg font-bold" disabled={!!busy} onClick={() => decide(r, 'approve')}>
                  {busy === r.id + 'approve' ? <LoaderCircleIcon className="animate-spin" /> : <CheckIcon />}批准
                </Button>
                <Button size="sm" variant="outline" className="h-8 rounded-lg font-bold" disabled={!!busy} onClick={() => decide(r, 'deny')}>
                  {busy === r.id + 'deny' ? <LoaderCircleIcon className="animate-spin" /> : <XIcon />}拒绝
                </Button>
                <small className="ml-auto self-center text-[11.5px] text-muted-foreground tabular-nums">{r.id}</small>
              </div>
            </li>
          ))}
        </ul>
      </PopoverContent>
    </Popover>
  );
}
