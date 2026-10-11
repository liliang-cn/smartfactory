// A plant drawn from above as a floor plan, from the same layout the twin builds in 3D: the hall,
// the raw-material store and finished goods, every line's belt and its machines in order.
import { useId } from 'react';
import type { PlantLayout } from '@/core/layout';
import { cn } from '@/lib/utils';

const ARCH_FILL: Record<string, string> = {
  press: '#2f5bea', robot: '#f29a2e', enclosure: '#5b7cf0', tunnel: '#e2574c', gantry: '#7a8ba8', packer: '#32a37c',
  manual: '#c58b4b', safety: '#9aa7bd', chamber: '#d06b3d', filler: '#3aa0c8', booth: '#8f6ae0', tank: '#46a6a0',
  roller: '#6475a0', frame: '#9aa7bd',
};

/** across is about how many label heights fit across the drawing as it is shown (wider shows smaller text) */
export function Blueprint({ plant, labels = true, across = 80, className }: { plant: PlantLayout; labels?: boolean; across?: number; className?: string }) {
  const id = useId();
  const { box } = plant;
  const pad = labels ? 3 : 1.5;
  // a plan capped in height (max-h) shrinks with its height, not its width
  const font = Math.max((plant.box.x1 - plant.box.x0) / across, (plant.box.z1 - plant.box.z0) / 34);
  const left = labels ? font * 8 : 0;
  const x0 = box.x0 - pad - left;
  const w = box.x1 - box.x0 + pad * 2 + left;
  const z0 = box.z0 - pad;
  const h = box.z1 - box.z0 + pad * 2;
  const outdoor = plant.site === 'outdoor';
  return (
    <svg viewBox={`${x0} ${z0} ${w} ${h}`} className={cn('block w-full', className)} role="img"
      aria-label={`${plant.name}平面图：${plant.lines.length} 条线，${plant.lines.reduce((n, l) => n + l.stations.length, 0)} 台设备`}>
      <defs>
        <pattern id={`${id}g`} width="2" height="2" patternUnits="userSpaceOnUse">
          <path d="M2 0H0V2" fill="none" stroke="var(--primary)" strokeOpacity="0.07" strokeWidth="0.08" />
        </pattern>
      </defs>
      <rect x={box.x0} y={box.z0} width={box.x1 - box.x0} height={box.z1 - box.z0} rx="1.2"
        fill={outdoor ? '#e7f3e4' : 'var(--card)'} stroke={outdoor ? '#7fb174' : 'var(--primary)'} strokeOpacity={outdoor ? 0.8 : 0.45}
        strokeWidth="0.25" strokeDasharray={outdoor ? '0.8 0.5' : undefined} />
      <rect x={box.x0} y={box.z0} width={box.x1 - box.x0} height={box.z1 - box.z0} rx="1.2" fill={`url(#${id}g)`} />
      <rect x={plant.warehouse.x0} y={plant.warehouse.z0} width={plant.warehouse.x1 - plant.warehouse.x0} height={plant.warehouse.z1 - plant.warehouse.z0}
        rx="0.6" fill="#e9dcc4" stroke="#b99a6a" strokeWidth="0.15" />
      <rect x={plant.fg.x0} y={plant.fg.z0} width={plant.fg.x1 - plant.fg.x0} height={plant.fg.z1 - plant.fg.z0}
        rx="0.6" fill="#d8efe5" stroke="#5fae8c" strokeWidth="0.15" />
      {plant.lines.map((l) => (
        <g key={l.id}>
          <line x1={l.x0} x2={l.x1} y1={l.lane} y2={l.lane} stroke="var(--muted-foreground)" strokeOpacity="0.35" strokeWidth="0.12" strokeDasharray="0.6 0.6" />
          <rect x={l.x0} y={l.z - l.beltW / 2} width={l.x1 - l.x0} height={l.beltW} rx={l.beltW / 2}
            fill={plant.flow === 'walk' ? '#efe6d4' : '#cfd6e4'} />
          {l.stations.map((s) => {
            const d = Math.min(s.size[1], l.beltW + 2.4);
            return (
              <rect key={s.id} x={s.x - s.len / 2} y={l.z - d / 2} width={s.len} height={d} rx="0.35"
                fill={ARCH_FILL[s.machine.look.archetype] ?? '#7a8ba8'} fillOpacity="0.9">
                <title>{`${s.id} ${s.name}`}</title>
              </rect>
            );
          })}
          {labels && (
            <text x={box.x0 - 1.2} y={l.z} textAnchor="end" dominantBaseline="middle" fontSize={font} fontWeight="700" fill="var(--foreground)">
              {l.name.length > 9 ? `${l.name.slice(0, 9)}…` : l.name}
            </text>
          )}
        </g>
      ))}
    </svg>
  );
}

/** The legend: each colour with the machines drawn in it. */
export function BlueprintLegend({ plants }: { plants: PlantLayout[] }) {
  const seen = new Map<string, Set<string>>();
  for (const p of plants) for (const l of p.lines) for (const s of l.stations) {
    const a = s.machine.look.archetype;
    if (!seen.has(a)) seen.set(a, new Set());
    seen.get(a)!.add(s.machine.label);
  }
  return (
    <ul className="flex flex-wrap gap-x-3.5 gap-y-1 text-[11.5px] text-muted-foreground">
      <li className="flex items-center gap-1.5"><span className="size-2.5 rounded-[3px] bg-[#e9dcc4] ring-1 ring-[#b99a6a]" />原料</li>
      <li className="flex items-center gap-1.5"><span className="size-2.5 rounded-[3px] bg-[#d8efe5] ring-1 ring-[#5fae8c]" />成品</li>
      {[...seen].map(([a, labels]) => {
        const list = [...labels];
        return (
          <li key={a} className="flex items-center gap-1.5" title={list.join('、')}>
            <span className="size-2.5 shrink-0 rounded-[3px]" style={{ background: ARCH_FILL[a] ?? '#7a8ba8' }} />
            {list.slice(0, 2).join('、')}{list.length > 2 ? ' 等' : ''}
          </li>
        );
      })}
    </ul>
  );
}
