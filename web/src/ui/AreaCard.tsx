// The area (车间) picked by clicking its board in the twin: its name, code and lines, and for
// administrators 修改车间 — the same edit-here link a machine or a line has.
// Desktop: a card on the right. Phone: a sheet along the bottom.
import { XIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { usePlant, useUI } from '@/state/store';
import { EditHere } from './EditHere';

export function AreaCard() {
  const { layout } = usePlant();
  const code = useUI((s) => s.area);
  const a = code ? layout.areas.find((x) => x.code === code) : null;
  if (!a) return null;
  const lines = layout.lines.filter((l) => a.lines.includes(l.id));
  return (
    <aside
      aria-label={`车间 ${a.name}`}
      className="absolute right-5 top-20 z-30 w-[362px] rounded-2xl bg-card p-4 shadow-float max-md:inset-x-0 max-md:bottom-0 max-md:top-auto max-md:w-auto max-md:rounded-b-none max-md:rounded-t-[20px] max-md:px-3.5 max-md:pb-[calc(16px+env(safe-area-inset-bottom))] max-md:shadow-sheet"
    >
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1 leading-tight">
          <small className="block truncate text-[11px] font-bold text-primary">车间 · {a.code}</small>
          <h2 className="mt-0.5 truncate text-[17px] font-extrabold">{a.name}</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">{a.note || `${layout.name}，${lines.length} ${layout.words.lines}`}</p>
        </div>
        <Button variant="outline" size="icon" aria-label="关闭" title="关闭 (Esc)" onClick={() => useUI.getState().selectArea(null)} className="max-md:size-9"><XIcon /></Button>
      </div>
      {lines.length > 0 && (
        <ul className="mt-3 grid gap-1 text-[12.5px]">
          {lines.map((l) => (
            <li key={l.id} className="flex items-center gap-2 rounded-lg bg-surface px-2.5 py-1.5">
              <span className="font-mono text-[11.5px] font-bold text-muted-foreground">{l.id}</span>
              <b className="truncate">{l.name}</b>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-3 flex justify-end">
        <EditHere node={{ kind: 'area', plant: layout.id, area: a.code }} label="修改车间" className="h-9 px-3 text-[13px]" />
      </div>
    </aside>
  );
}
