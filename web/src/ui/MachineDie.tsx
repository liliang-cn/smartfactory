// The die booked on a press in the company's die ledger: its number, the strokes it has struck of
// its design life, and what last happened to it. Nothing when the ledger has none on it.
import { useEffect, useState } from 'react';
import { HammerIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { operatorToken } from '@/live/client';

interface DieOn {
  die?: { no: string; kind: string; life: number; strokes: number; left: number; resinks?: number };
  history: { at: string; text: string; who: string }[];
}

export function MachineDie({ company, plant, asset }: { company: string; plant: string; asset: string }) {
  const [data, setData] = useState<DieOn | null>(null);
  useEffect(() => {
    let live = true;
    const t = operatorToken();
    if (!t) { setData(null); return; }
    const get = () => fetch(`/api/dies/${encodeURIComponent(company)}/on/${encodeURIComponent(plant)}/${encodeURIComponent(asset)}`, { headers: { Authorization: `Bearer ${t}` } })
      .then((r) => (r.ok ? r.json() : null)).then((d) => live && setData(d), () => live && setData(null));
    get();
    const every = setInterval(get, 15_000);
    return () => { live = false; clearInterval(every); };
  }, [company, plant, asset]);
  const d = data?.die;
  if (!d) return null;
  const share = d.strokes / Math.max(1, d.life);
  return (
    <div className="mt-3.5 border-t pt-3">
      <h3 className="mb-1.5 flex items-center gap-1.5 text-[13px] font-extrabold">
        <HammerIcon className="size-4 text-primary" />模具 {d.no}
        <small className="ml-auto text-[11.5px] font-semibold text-muted-foreground">{d.kind}{d.resinks ? `，翻新 ${d.resinks} 次` : ''}</small>
      </h3>
      <div className="flex items-baseline gap-2 text-xs tabular-nums">
        <b className={cn('text-base font-extrabold', share >= 1 ? 'text-destructive' : share >= 0.9 ? 'text-amber-600' : '')}>{d.left.toLocaleString()}</b>
        <span className="text-muted-foreground">次可打，已打 {d.strokes.toLocaleString()} / {d.life.toLocaleString()}</span>
      </div>
      <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-[#e2e7f4]">
        <i className={cn('block h-full rounded-full', share >= 1 ? 'bg-destructive' : share >= 0.9 ? 'bg-amber-500' : 'bg-success')} style={{ width: `${Math.max(0, Math.min(1, 1 - share)) * 100}%` }} />
      </div>
      {data!.history[0] && <p className="mt-1.5 truncate text-[11.5px] text-muted-foreground">{new Date(data!.history[0].at).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })} {data!.history[0].text}</p>}
    </div>
  );
}
