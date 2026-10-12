// What people last wrote down by hand on a machine (patrol rounds, first-article checks, lab
// results) and when its next round is due, for the Inspector. Shown to members and above of the
// company; nothing when no form is for the machine.
import { useEffect, useState } from 'react';
import type { z } from 'zod';
import { ClipboardCheckIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { operatorToken } from '@/live/client';
import type * as P from '@/protocol/sfp';

type MachineChecks = z.infer<typeof P.MachineChecks>;
type Round = MachineChecks['machine']['rounds'][number];

const hm = (iso?: string) => {
  if (!iso) return '';
  const d = new Date(iso);
  const t = d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
  return d.toDateString() === new Date().toDateString() ? t : `${d.getMonth() + 1}-${d.getDate()} ${t}`;
};
const span = (iso?: string) => {
  const m = Math.max(1, Math.round((Date.now() - Date.parse(iso ?? '')) / 60000));
  return m < 60 ? `${m} 分钟` : m < 2880 ? `${Math.floor(m / 60)} 小时` : `${Math.floor(m / 1440)} 天`;
};

function due(r: Round): { text: string; tone: string } {
  switch (r.status) {
    case 'overdue': return { text: `逾期 ${span(r.since)}`, tone: 'text-destructive font-bold' };
    case 'due': return { text: `${hm(r.due)} 前做`, tone: 'text-warning font-bold' };
    case 'done': return { text: `本轮已做，下一轮 ${hm(r.next)}`, tone: 'text-success' };
    default: return { text: '需要时填', tone: 'text-muted-foreground' };
  }
}

export function ManualRecords({ company, plant, asset }: { company: string; plant: string; asset: string }) {
  const [data, setData] = useState<MachineChecks | null>(null);
  useEffect(() => {
    let live = true;
    const t = operatorToken();
    if (!t) { setData(null); return; }
    const get = () => fetch(`/api/checks/${encodeURIComponent(company)}/machines/${encodeURIComponent(plant)}/${encodeURIComponent(asset)}`, { headers: { Authorization: `Bearer ${t}` } })
      .then((r) => (r.ok ? r.json() : null)).then((d) => live && setData(d), () => live && setData(null));
    get();
    const every = setInterval(get, 60_000);
    return () => { live = false; clearInterval(every); };
  }, [company, plant, asset]);
  if (!data) return null;
  const link = `/check?${new URLSearchParams({ company, plant, asset })}`;
  return (
    <div className="mt-3.5 border-t pt-3">
      <h3 className="mb-1.5 flex items-center gap-1.5 text-[13px] font-extrabold">
        <ClipboardCheckIcon className="size-4 text-primary" />人工记录
        {data.operator && <a href={link} target="_blank" rel="noreferrer" className="ml-auto text-[12px] font-bold text-primary hover:underline">去填写</a>}
      </h3>
      <ul className="grid gap-2 text-xs">
        {data.machine.rounds.map((r) => {
          const d = due(r);
          const last = data.last.find((s) => s.form === r.form);
          return (
            <li key={r.form} className="grid gap-0.5">
              <div className="flex items-baseline gap-2">
                <b className="text-[12.5px] text-foreground">{r.formName}</b>
                <span className={cn('ml-auto tabular-nums', d.tone)}>{d.text}</span>
              </div>
              {last ? (
                <p className="leading-relaxed text-muted-foreground">
                  {last.values.filter((v) => v.kind !== 'photo' && v.kind !== 'text').map((v, i) => (
                    <span key={v.key} className={cn(v.bad && 'font-bold text-destructive')}>
                      {i > 0 && '，'}{v.label} {v.num !== undefined ? `${v.num}${v.unit ? ` ${v.unit}` : ''}` : v.text}
                    </span>
                  ))}
                  <span className="text-muted-foreground/70">（{last.by} {hm(last.taken)}）</span>
                </p>
              ) : <p className="text-muted-foreground">还没填过</p>}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
