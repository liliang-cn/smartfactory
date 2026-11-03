// The twin's performance numbers in a corner, for whoever wants to see what the 3D view costs here.
import { XIcon } from 'lucide-react';
import { toggleStats, usePerf, useStatsShown } from './perf';

const k = (n: number) => (n >= 10000 ? `${Math.round(n / 1000)}k` : n.toLocaleString('zh-CN'));

export function PerfOverlay() {
  const shown = useStatsShown();
  const p = usePerf();
  if (!shown) return null;
  const rows: [string, string][] = [
    ['帧率', `${Math.round(p.fps)} 帧/秒`],
    ['页面内存', p.heapMB ? `${Math.round(p.heapMB)} MB` : '浏览器不提供'],
    ['每帧绘制', `${k(p.calls)} 次`],
    ['三角形', k(p.triangles)],
    ['贴图 / 几何体', `${p.textures} / ${p.geometries}`],
    ['画布', `${p.width}×${p.height} @${p.dpr}x`],
  ];
  return (
    <div className="pointer-events-auto absolute top-[76px] right-[76px] z-40 w-[220px] rounded-2xl bg-card/95 p-3 text-[12px] shadow-float backdrop-blur max-md:top-[calc(62px+env(safe-area-inset-top))]" role="status" aria-label="性能">
      <div className="mb-1.5 flex items-center">
        <b className="text-[12.5px]">性能</b>
        <button type="button" onClick={toggleStats} className="ml-auto grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-surface" aria-label="关掉性能面板">
          <XIcon className="size-3.5" />
        </button>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
        {rows.map(([a, b]) => (
          <div key={a} className="contents">
            <dt className="text-muted-foreground">{a}</dt>
            <dd className={a === '帧率' && p.fps > 0 && p.fps < 24 ? 'text-right font-bold text-destructive tabular-nums' : 'text-right font-bold tabular-nums'}>{b}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
