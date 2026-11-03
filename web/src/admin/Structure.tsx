// The company's plant breakdown (PBS): 公司 › 工厂 › 车间 › 产线 › 设备 by tag (位号), each node
// with how many lines and machines it holds and how many of them are running or down now.
import { useEffect, useState } from 'react';
import { ChevronRightIcon, ExternalLinkIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import * as api from './api';
import { Panel } from './Panel';

const STATE: Record<string, { label: string; dot: string }> = {
  running: { label: '运行', dot: 'bg-success' },
  idle: { label: '待机', dot: 'bg-muted-foreground/40' },
  blocked: { label: '堵料', dot: 'bg-warning' },
  stopped: { label: '停机', dot: 'bg-muted-foreground' },
  fault: { label: '故障', dot: 'bg-destructive' },
  maint: { label: '保养', dot: 'bg-warning' },
  changeover: { label: '换型', dot: 'bg-primary' },
};

const LEVEL: Record<api.PBSNode['kind'], string> = { company: '公司', plant: '工厂', area: '车间', line: '产线', machine: '设备' };

/** running and down (fault) first, the rest as one count */
function Rollup({ n }: { n: api.PBSNode }) {
  const run = n.states.running ?? 0;
  const fault = n.states.fault ?? 0;
  const other = n.machines - run - fault;
  return (
    <span className="flex shrink-0 items-center gap-1.5 text-[12px] tabular-nums">
      <span className="rounded-md bg-success-soft px-1.5 py-px font-bold text-success" title="在运行">运行 {run}</span>
      {fault > 0 && <span className="rounded-md bg-danger-soft px-1.5 py-px font-bold text-destructive" title="故障停机">故障 {fault}</span>}
      {other > 0 && <span className="rounded-md bg-surface px-1.5 py-px font-semibold text-muted-foreground" title="待机、堵料、停机、保养、换型">其他 {other}</span>}
    </span>
  );
}

function Node({ n, depth, company, plant, open: start }: { n: api.PBSNode; depth: number; company: string; plant?: string; open: boolean }) {
  // a line with a machine down opens by itself, so the fault is in sight
  const [open, setOpen] = useState(start || (n.kind === 'line' && (n.states.fault ?? 0) > 0));
  if (n.kind === 'machine') {
    const st = STATE[n.state ?? ''] ?? { label: n.state || '—', dot: 'bg-muted-foreground/30' };
    return (
      <li className="flex items-center gap-2 rounded-lg py-1 pr-2 text-[13px] hover:bg-surface" style={{ paddingLeft: depth * 18 + 26 }}>
        <span className={cn('size-2 shrink-0 rounded-full', st.dot)} />
        <a href={`/?company=${encodeURIComponent(company)}&plant=${encodeURIComponent(plant ?? '')}&asset=${encodeURIComponent(n.id)}`}
          className="font-mono text-[12.5px] font-bold hover:text-primary" title="在孪生里看这台">{n.id}</a>
        <span className="min-w-0 truncate">{n.name}{n.sub && n.sub !== n.name && <span className="ml-1.5 text-muted-foreground">{n.sub}</span>}</span>
        <span className={cn('ml-auto shrink-0 text-[12px] font-semibold', n.state === 'fault' ? 'text-destructive' : n.state === 'running' ? 'text-success' : 'text-muted-foreground')}
          title={n.fault || undefined}>{st.label}{n.fault ? `：${n.fault}` : ''}</span>
      </li>
    );
  }
  const counts = n.kind === 'line' ? `${n.machines} 台` : `${n.lines} 条线，${n.machines} 台`;
  return (
    <li>
      <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open}
        className={cn('flex w-full items-center gap-2 rounded-lg py-1.5 pr-2 text-left hover:bg-surface', depth === 0 && 'py-2')}
        style={{ paddingLeft: depth * 18 + 4 }}>
        <ChevronRightIcon className={cn('size-4 shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')} />
        <span className="shrink-0 rounded-md bg-brand-soft px-1.5 py-px text-[11px] font-bold text-primary">{LEVEL[n.kind]}</span>
        {n.kind === 'area' && <span className="shrink-0 font-mono text-[12px] text-muted-foreground">{n.id}</span>}
        <b className={cn('min-w-0 truncate', depth === 0 ? 'text-[14.5px]' : 'text-[13.5px]')}>{n.name}</b>
        {n.sub && <span className="min-w-0 truncate text-[12.5px] text-muted-foreground max-sm:hidden">{n.sub}</span>}
        <span className="ml-auto shrink-0 text-[12px] text-muted-foreground tabular-nums max-sm:hidden">{counts}</span>
        <Rollup n={n} />
      </button>
      {open && !!n.children?.length && (
        <ul>
          {n.children.map((c) => (
            <Node key={`${c.kind}:${c.id}`} n={c} depth={depth + 1} company={company} plant={n.kind === 'plant' ? n.id : plant}
              open={c.kind === 'plant' || c.kind === 'area'} />
          ))}
        </ul>
      )}
      {open && n.kind === 'area' && !n.children?.length && (
        <p className="py-1 text-[12.5px] text-muted-foreground" style={{ paddingLeft: (depth + 1) * 18 + 26 }}>这个车间还没有产线</p>
      )}
    </li>
  );
}

/** The breakdown, kept up to date every few seconds while it is on screen. */
export function StructurePanel({ company }: { company: string }) {
  const [tree, setTree] = useState<api.PBSNode | null>(null);
  const [error, setError] = useState<string>();
  useEffect(() => {
    let live = true;
    const load = () => api.pbs(company).then((t) => { if (live) { setTree(t); setError(undefined); } }, (e: Error) => live && setError(e.message));
    load();
    const t = setInterval(load, 5000);
    return () => { live = false; clearInterval(t); };
  }, [company]);
  return (
    <Panel title="工厂结构"
      aside={<span className="text-[12px] text-muted-foreground max-sm:hidden">公司 › 工厂 › 车间 › 产线 › 设备（位号），状态每 5 秒刷新</span>}>
      {error && <p className="text-[13px] text-destructive">结构读不出来：{error}</p>}
      {!tree && !error && <p className="text-[13px] text-muted-foreground">正在读取…</p>}
      {tree && <ul className="grid"><Node n={tree} depth={0} company={company} open /></ul>}
      {tree && (
        <a href={`/?company=${encodeURIComponent(company)}`} className="mt-3 inline-flex items-center gap-1 text-[12.5px] font-bold text-primary hover:underline">
          在孪生里看<ExternalLinkIcon className="size-3.5" />
        </a>
      )}
    </Panel>
  );
}
