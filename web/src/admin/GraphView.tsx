// The knowledge graph drawn as a network: plants, lines and their stations in order, the machines
// the stations are, products, suppliers, customers and the production orders. Laid out once by a
// force simulation, each plant's lines, stations and orders gathered around it; wheel to zoom,
// drag to pan, click a thing to see what it connects to, and show or hide each kind.
import { useEffect, useMemo, useRef, useState } from 'react';
import { forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY, type SimulationLinkDatum, type SimulationNodeDatum } from 'd3-force';
import { MinusIcon, PlusIcon, ScanIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import type * as api from './api';

export const KIND: Record<api.EntityKind, { label: string; color: string; r: number }> = {
  company: { label: '公司', color: '#1f2a44', r: 13 },
  plant: { label: '工厂', color: '#2f5bea', r: 10 },
  line: { label: '产线', color: '#6f8ff5', r: 6.5 },
  station: { label: '工位', color: '#a9b4c8', r: 3.6 },
  machine: { label: '设备', color: '#f29a2e', r: 6 },
  product: { label: '产品', color: '#32a37c', r: 8 },
  supplier: { label: '供应商', color: '#b99a6a', r: 5.5 },
  customer: { label: '客户', color: '#8f6ae0', r: 5.5 },
  order: { label: '工单', color: '#e0526b', r: 4.4 },
};

const PRIORITY: Record<api.EntityKind, number> = { company: 0, plant: 1, product: 2, line: 3, machine: 4, customer: 5, supplier: 6, order: 7, station: 8 };

const DIST: Record<string, number> = {
  has_plant: 70, has_line: 46, makes: 46, has_station: 12, feeds: 9, is_a: 60, supplies: 40, sold_to: 40, runs_on: 18, for_customer: 40, of_product: 50,
};

interface N extends SimulationNodeDatum { id: string; kind: api.EntityKind; mentions: number; r: number; plant?: string }
interface L extends SimulationLinkDatum<N> { type: string }

/** The plant each line, station and order belongs to, following has_line, has_station and runs_on. */
function plantsOf(g: api.KnowledgeGraph) {
  const plant = new Map<string, string>();
  for (const e of g.edges) if (e.type === 'has_line') plant.set(e.to, e.from);
  for (const e of g.edges) {
    if (e.type === 'has_station' && plant.has(e.from)) plant.set(e.to, plant.get(e.from)!);
    if (e.type === 'runs_on' && plant.has(e.to)) plant.set(e.from, plant.get(e.to)!);
  }
  for (const n of g.nodes) if (n.kind === 'plant') plant.set(n.name, n.name);
  return plant;
}

function layout(g: api.KnowledgeGraph) {
  const plantOf = plantsOf(g);
  const nodes: N[] = g.nodes.map((n) => ({
    id: n.name, kind: n.kind, mentions: n.mentions, plant: plantOf.get(n.name),
    r: KIND[n.kind].r + (n.kind === 'machine' ? Math.min(6, Math.sqrt(n.mentions)) : 0),
  }));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const links: L[] = g.edges.filter((e) => byId.has(e.from) && byId.has(e.to)).map((e) => ({ source: e.from, target: e.to, type: e.type }));
  // each plant's things around a point of their own, the plants side by side; what plants share
  // (machines, products, customers, suppliers) in between
  const plants = g.nodes.filter((n) => n.kind === 'plant').map((n) => n.name);
  const spread = 70 + 9 * Math.sqrt(nodes.length);
  const anchor = new Map(plants.map((p, i) => {
    const a = (i / Math.max(1, plants.length)) * Math.PI * 2 - Math.PI / 2;
    return [p, plants.length > 1 ? { x: Math.cos(a) * spread * 1.5, y: Math.sin(a) * spread * 0.7 } : { x: 0, y: 0 }];
  }));
  const pull = (n: N) => (n.plant ? 0.03 : 0.012);
  const sim = forceSimulation(nodes)
    .force('link', forceLink<N, L>(links).id((d) => d.id).distance((l) => DIST[l.type] ?? 30)
      .strength((l) => (l.type === 'is_a' || l.type === 'of_product' || l.type === 'for_customer' ? 0.06 : l.type === 'feeds' || l.type === 'has_station' || l.type === 'runs_on' ? 0.9 : 0.4)))
    .force('charge', forceManyBody<N>().strength((d) => (d.kind === 'station' || d.kind === 'order' ? -12 : d.kind === 'machine' ? -60 : -140)))
    .force('collide', forceCollide<N>((d) => d.r + 2))
    // wider than tall, like the box it is drawn in
    .force('x', forceX<N>((d) => anchor.get(d.plant ?? '')?.x ?? 0).strength(pull))
    .force('y', forceY<N>((d) => anchor.get(d.plant ?? '')?.y ?? 0).strength((d) => pull(d) * 2))
    .stop();
  for (let i = 0; i < 360; i++) sim.tick();
  return { nodes, links, byId };
}

interface View { x: number; y: number; w: number; h: number }

export function GraphView({ graph, selected, onSelect, className }: { graph: api.KnowledgeGraph; selected: string | null; onSelect(name: string | null): void; className?: string }) {
  const all = useMemo(() => layout(graph), [graph]);
  const byId = all.byId;
  // kinds shown; the layout stays put when one is hidden
  const [hidden, setHidden] = useState<Set<api.EntityKind>>(() => new Set());
  const nodes = useMemo(() => all.nodes.filter((n) => !hidden.has(n.kind) || n.id === selected), [all, hidden, selected]);
  const links = useMemo(() => {
    const shown = new Set(nodes.map((n) => n.id));
    return all.links.filter((l) => shown.has((l.source as N).id) && shown.has((l.target as N).id));
  }, [all, nodes]);
  const counts = useMemo(() => {
    const c = new Map<api.EntityKind, number>();
    for (const n of all.nodes) c.set(n.kind, (c.get(n.kind) ?? 0) + 1);
    return c;
  }, [all]);
  const toggle = (k: api.EntityKind) => setHidden((h) => {
    const next = new Set(h);
    if (next.has(k)) next.delete(k);
    else next.add(k);
    return next;
  });
  const fit = useMemo<View>(() => {
    if (!all.nodes.length) return { x: -100, y: -100, w: 200, h: 200 };
    const xs = all.nodes.map((n) => n.x!), ys = all.nodes.map((n) => n.y!);
    const pad = 30;
    const x0 = Math.min(...xs) - pad, x1 = Math.max(...xs) + pad, y0 = Math.min(...ys) - pad, y1 = Math.max(...ys) + pad;
    return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
  }, [all]);
  const [view, setView] = useState<View>(fit);
  useEffect(() => setView(fit), [fit]);
  const svg = useRef<SVGSVGElement>(null);
  // the drawing's size on screen, so text stays the same size in pixels at any zoom
  const [px, setPx] = useState({ w: 800, h: 560 });
  useEffect(() => {
    const el = svg.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setPx({ w: e.contentRect.width || 800, h: e.contentRect.height || 560 }));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const drag = useRef<{ x: number; y: number; v: View; moved: boolean } | null>(null);
  const [hover, setHover] = useState<string | null>(null);

  const near = useMemo(() => {
    if (!selected) return null;
    const s = new Set([selected]);
    for (const l of links) {
      const a = (l.source as N).id, b = (l.target as N).id;
      if (a === selected) s.add(b);
      if (b === selected) s.add(a);
    }
    return s;
  }, [selected, links]);

  // centre on what was selected from outside the drawing (a search, a passage)
  useEffect(() => {
    const n = selected ? byId.get(selected) : null;
    if (!n) return;
    setView((v) => {
      const inside = n.x! > v.x + v.w * 0.1 && n.x! < v.x + v.w * 0.9 && n.y! > v.y + v.h * 0.1 && n.y! < v.y + v.h * 0.9;
      return inside ? v : { ...v, x: n.x! - v.w / 2, y: n.y! - v.h / 2 };
    });
  }, [selected, byId]);

  const zoom = (k: number, cx?: number, cy?: number) => setView((v) => {
    const w = Math.min(fit.w * 2, Math.max(fit.w / 14, v.w * k));
    const h = (w / v.w) * v.h;
    const px = cx ?? v.x + v.w / 2, py = cy ?? v.y + v.h / 2;
    return { x: px - ((px - v.x) * w) / v.w, y: py - ((py - v.y) * h) / v.h, w, h };
  });
  const toWorld = (e: { clientX: number; clientY: number }) => {
    const r = svg.current!.getBoundingClientRect();
    const s = Math.max(view.w / r.width, view.h / r.height);
    const ox = (r.width * s - view.w) / 2, oy = (r.height * s - view.h) / 2;
    return { x: view.x - ox + (e.clientX - r.left) * s, y: view.y - oy + (e.clientY - r.top) * s, s };
  };
  useEffect(() => {
    const el = svg.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const p = toWorld(e);
      zoom(Math.exp(e.deltaY * 0.0015), p.x, p.y);
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  });

  // labels the most telling things first, each where it does not cover one already placed
  const labelled = useMemo(() => {
    const budget = Math.max(12, Math.round((px.w * px.h) / 7000));
    const rank = (n: N) => PRIORITY[n.kind] * 1000 - n.mentions;
    const inView = nodes.filter((n) => n.x! >= view.x && n.x! <= view.x + view.w && n.y! >= view.y && n.y! <= view.y + view.h);
    const u = Math.max(view.w / px.w, view.h / px.h);
    const placed: { x0: number; x1: number; y0: number; y1: number }[] = [];
    const out = new Set<string>();
    for (const n of inView.sort((a, b) => rank(a) - rank(b))) {
      if (out.size >= budget) break;
      const fs = n.kind === 'station' ? 11.5 * 0.85 : 11.5;
      // a CJK character is about as wide as the font is tall, a Latin one a little over half
      const w = [...n.id].reduce((s, ch) => s + (ch.charCodeAt(0) > 0x2e80 ? fs : fs * 0.6), 0);
      const cx = (n.x! - view.x) / u, top = (n.y! - n.r - view.y) / u - fs * 1.35;
      const box = { x0: cx - w / 2 - 2, x1: cx + w / 2 + 2, y0: top, y1: top + fs * 1.3 };
      if (placed.some((b) => box.x0 < b.x1 && box.x1 > b.x0 && box.y0 < b.y1 && box.y1 > b.y0)) continue;
      placed.push(box);
      out.add(n.id);
    }
    return out;
  }, [nodes, view, px]);
  const label = (n: N) => labelled.has(n.id) || n.id === selected || n.id === hover || !!near?.has(n.id);
  // world units per screen pixel
  const unit = Math.max(view.w / px.w, view.h / px.h);
  const font = 11.5 * unit;

  return (
    <div className={cn('relative overflow-hidden rounded-xl bg-[color-mix(in_oklab,var(--primary)_3%,var(--card))]', className)}>
      <svg ref={svg} viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`} className="block size-full touch-none select-none" role="img" aria-label={`知识图谱：${nodes.length} 个节点，${links.length} 条关系`}
        onPointerDown={(e) => { (e.target as Element).setPointerCapture?.(e.pointerId); drag.current = { x: e.clientX, y: e.clientY, v: view, moved: false }; }}
        onPointerMove={(e) => {
          const d = drag.current;
          if (!d) return;
          const r = svg.current!.getBoundingClientRect();
          const s = Math.max(d.v.w / r.width, d.v.h / r.height);
          if (Math.abs(e.clientX - d.x) + Math.abs(e.clientY - d.y) > 3) d.moved = true;
          setView({ ...d.v, x: d.v.x - (e.clientX - d.x) * s, y: d.v.y - (e.clientY - d.y) * s });
        }}
        onPointerUp={(e) => {
          const d = drag.current;
          drag.current = null;
          if (d && !d.moved) {
            const p = toWorld(e);
            let best: N | null = null, bd = Infinity;
            for (const n of nodes) {
              const dd = Math.hypot(n.x! - p.x, n.y! - p.y);
              if (dd < n.r + 6 * p.s && dd < bd) { best = n; bd = dd; }
            }
            onSelect(best ? best.id : null);
          }
        }}
        onPointerLeave={() => setHover(null)}>
        <g>
          {links.map((l, i) => {
            const a = l.source as N, b = l.target as N;
            const on = near ? near.has(a.id) && near.has(b.id) && (a.id === selected || b.id === selected) : false;
            const flow = l.type === 'feeds';
            const order = l.type === 'runs_on' || l.type === 'for_customer' || l.type === 'of_product';
            return (
              <line key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y}
                stroke={on ? 'var(--primary)' : flow ? '#7f93c8' : l.type === 'is_a' ? '#f29a2e' : order ? KIND.order.color : '#9aa7bd'}
                strokeOpacity={near ? (on ? 0.9 : 0.06) : l.type === 'is_a' ? 0.18 : flow ? 0.7 : order ? (l.type === 'runs_on' ? 0.5 : 0.16) : 0.35}
                strokeWidth={(on ? 1.8 : flow ? 1.2 : 0.8) * unit} />
            );
          })}
        </g>
        <g>
          {nodes.map((n) => {
            const dimmed = near && !near.has(n.id);
            return (
              <g key={n.id} opacity={dimmed ? 0.18 : 1} onPointerEnter={() => setHover(n.id)} onPointerLeave={() => setHover((h) => (h === n.id ? null : h))}>
                <circle cx={n.x} cy={n.y} r={n.r} fill={KIND[n.kind].color} stroke={n.id === selected ? 'var(--foreground)' : 'var(--card)'} strokeWidth={(n.id === selected ? 2.5 : 1) * unit} />
                {label(n) && (
                  <text x={n.x} y={n.y! - n.r - font * 0.45} textAnchor="middle" fontSize={n.kind === 'station' ? font * 0.85 : font} fontWeight={n.kind === 'station' ? 600 : 700}
                    fill="var(--foreground)" paintOrder="stroke" stroke="var(--card)" strokeWidth={font * 0.3} strokeLinejoin="round">
                    {n.id}
                  </text>
                )}
              </g>
            );
          })}
        </g>
      </svg>
      <div className="absolute right-3 bottom-3 flex flex-col overflow-hidden rounded-xl border bg-card shadow-sm">
        <button type="button" aria-label="放大" className="grid size-8 place-items-center hover:bg-surface" onClick={() => zoom(0.7)}><PlusIcon className="size-4" /></button>
        <button type="button" aria-label="缩小" className="grid size-8 place-items-center border-t hover:bg-surface" onClick={() => zoom(1.4)}><MinusIcon className="size-4" /></button>
        <button type="button" aria-label="全图" className="grid size-8 place-items-center border-t hover:bg-surface" onClick={() => setView(fit)}><ScanIcon className="size-4" /></button>
      </div>
      <ul className="absolute bottom-3 left-3 flex max-w-[calc(100%-4.5rem)] flex-wrap gap-1 rounded-xl bg-card/85 p-1 text-[11.5px] backdrop-blur max-sm:flex-nowrap max-sm:overflow-x-auto max-sm:[scrollbar-width:none]" aria-label="显示哪些">
        {(Object.keys(KIND) as api.EntityKind[]).filter((k) => counts.has(k)).map((k) => {
          const off = hidden.has(k);
          return (
            <li key={k}>
              <button type="button" aria-pressed={!off} onClick={() => toggle(k)}
                className={cn('flex h-6 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2 font-bold transition-colors', off ? 'text-muted-foreground/60 hover:bg-surface' : 'text-foreground hover:bg-surface')}>
                <span className={cn('size-2.5 rounded-full', off && 'opacity-30')} style={{ background: KIND[k].color }} />
                {KIND[k].label}<span className="font-normal tabular-nums text-muted-foreground max-sm:hidden">{counts.get(k)}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
