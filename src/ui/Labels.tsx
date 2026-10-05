// Name tags over assets. Rendered once as DOM; <LabelProjector> moves them every frame.
import { memo } from 'react';
import { cn } from '@/lib/utils';
import type { Asset } from '@/sim/types';
import { labelEls, usePlant, useTick, useUI } from '@/state/store';
import { ALERT_STATES, DOT, STATE_LABEL } from './status';

const Tag = memo(function Tag({ asset, crowded }: { asset: Asset; crowded: boolean }) {
  useTick();
  const selected = useUI((s) => s.selected === asset.id);
  const hovered = useUI((s) => s.hovered === asset.id);
  const showAll = useUI((s) => s.labels);
  const fault = asset.state === 'fault';
  const vehicle = asset.type === 'agv' || asset.type === 'truck';
  const away = asset.type === 'truck' && asset.state === 'away';
  // With many AGVs their tags would bury the plant, so then only trucks keep a permanent tag.
  const always = vehicle && (asset.type === 'truck' || !crowded);
  const show = !away && (showAll || ALERT_STATES.has(asset.state) || always || selected || hovered);
  const text = vehicle ? asset.task || STATE_LABEL[asset.state] : STATE_LABEL[asset.state];
  return (
    <button
      type="button"
      ref={(el) => {
        if (el) labelEls.set(asset.id, el);
        else labelEls.delete(asset.id);
      }}
      onClick={() => useUI.getState().select(asset.id)}
      className={cn(
        'pointer-events-auto absolute left-0 top-0 flex items-center gap-1.5 whitespace-nowrap rounded-full bg-white/95 py-1 pl-1.5 pr-2.5 text-[11.5px] shadow-[0_4px_14px_rgba(41,58,122,0.18)] will-change-transform max-md:text-[10.5px]',
        fault && 'bg-destructive text-white',
        selected && 'bg-primary text-primary-foreground',
        !show && 'hidden',
      )}
    >
      <i className={cn('size-[7px] shrink-0 rounded-full', fault || selected ? 'bg-white' : DOT[asset.state])} />
      <b className="font-bold">{asset.id}</b>
      <span className={cn('text-muted-foreground', (fault || selected) && 'text-white/80')}>{text}</span>
    </button>
  );
});

export function Labels() {
  const { sim } = usePlant();
  const assets = [...sim.assets.values()];
  return (
    <div className="pointer-events-none absolute inset-0 z-10 overflow-hidden">
      {assets.map((a) => <Tag key={a.id} asset={a} crowded={sim.agvs.length > 4} />)}
    </div>
  );
}
