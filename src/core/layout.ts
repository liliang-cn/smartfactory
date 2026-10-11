// Conventions that turn a plant's config into a concrete hall: ids, coordinates, belt sizes,
// AGV loops, truck roads, orders and the default camera. Nothing here is plant-specific.
//
//   * Lines run along +x, stacked along z; each line's aisle is on its +z side.
//   * Raw-material store on the left, finished goods on the right, one AGV pair per line:
//     a feeder (store -> line start) and a collector (line end -> finished goods).
//   * Ids: line L{n}; machine {code}-{n}{seq:02}; belt CV-{n}; buffers BF-{n} / OF-{n};
//     AGVs AGV-{nn}A / AGV-{nn}B; store WH, finished goods FG, trucks TRK-IN / TRK-OUT.
//   * Belt width and part spacing follow the product size; tall products ride a low belt.
import type { CompanyCfg, IndustryCfg, LineCfgT, MachineCfg, MeasureCfg, PartCfg, PlantCfgT, ProductCfg, StepCfg } from '@/config/schema';

export interface Dims { l: number; h: number; d: number }

export interface ProductInfo {
  key: string;
  name: string;
  stages: Record<string, PartCfg[]>;
  order: string[];
  dims: Record<string, Dims>;
  max: Dims;
  partNo?: string;
  material?: string;
  /** kg per finished unit / kg of material per unit */
  weight?: number;
  input?: number;
  customer?: string;
  suppliers?: ProductCfg['suppliers'];
  variants?: ProductCfg['variants'];
  customers?: ProductCfg['customers'];
  price?: number;
}

export interface StationLayout {
  id: string;
  key: string;
  name: string;
  machine: MachineCfg;
  /** centre along x, footprint length, size [l, d, h] after fitting the product */
  x: number;
  len: number;
  size: [number, number, number];
  cycle: number;
  makes?: string;
  measure?: MeasureCfg;
  take?: Record<string, number>;
  give?: string[];
}

export interface InventoryLayout { key: string; label: string; unit: string; kind: 'lend' | 'consume'; cap: number; low: number; refill: number; lead?: number }

export interface Waypoint { x: number; z: number; act?: 'load' | 'drop' | 'pick' | 'deliver' | 'dock'; line?: string }
export interface Zone { id: string; name: string; x0: number; x1: number; z0: number; z1: number; cap: number }
export interface OrderInfo { no: string; part: string; qty: number; issued: string; kitted: string }

export interface LineLayout {
  id: string;
  no: number;
  name: string;
  note: string;
  z: number;
  x0: number;
  x1: number;
  beltY: number;
  beltW: number;
  speed: number;
  gap: number;
  spawn: number;
  product: ProductInfo;
  stations: StationLayout[];
  /** product stage units arrive in */
  input: string;
  conveyor: { id: string; name: string };
  infeed: { id: string; name: string; cap: number; x: number; z: number };
  outfeed: { id: string; name: string; cap: number; x: number; z: number };
  lane: number;
  order: OrderInfo;
}

export interface PlantLayout {
  company: { id: string; name: string; short: string };
  id: string;
  name: string;
  badge: string;
  hall: string;
  sub: string;
  industry: IndustryCfg;
  unit: string;
  qualityLabel: string;
  site: 'hall' | 'outdoor';
  flow: 'belt' | 'walk';
  terms: IndustryCfg['terms'];
  calendar: IndustryCfg['calendar'];
  pace: number;
  qa: IndustryCfg['qa'];
  tariff?: CompanyCfg['tariff'];
  /** who carries finished goods to customers */
  freight: { carrier: string; share: number; late: number; truck: number }[];
  inventory: InventoryLayout[];
  lines: LineLayout[];
  box: { x0: number; x1: number; z0: number; z1: number };
  warehouse: Zone;
  fg: Zone;
  agvs: { id: string; name: string; route: Waypoint[] }[];
  trucks: { id: string; name: string; kind: 'in' | 'out'; carrier: string; route: Waypoint[] }[];
  roads: { front: number; left: number; right: number };
  home: { target: [number, number, number]; pos: [number, number, number] };
}

// ---------- defaults per archetype ----------

/** [length, depth, height] when the config gives no size */
export const DEFAULT_SIZE: Record<string, [number, number, number]> = {
  enclosure: [2.6, 2.3, 2.45], press: [2.4, 1.8, 4.4], robot: [3.8, 3.6, 2.8], tunnel: [4.5, 1.9, 2.0],
  gantry: [1.6, 1.6, 2.3], packer: [2.3, 2.1, 2.2], manual: [2.9, 3.2, 2.6], safety: [1.9, 1.7, 2.3],
  chamber: [3, 1.8, 2.1], filler: [2.6, 2.6, 2.4], booth: [3.4, 2.0, 2.4], tank: [2.6, 3.4, 3.2],
  roller: [2.4, 2.2, 2.6], frame: [2, 2, 2],
};
/** archetypes the product passes through, so they must be longer than the product */
const ENCLOSING = new Set(['enclosure', 'tunnel', 'packer', 'chamber', 'booth']);
/** how far behind the belt edge a machine reaches */
const BACK: Record<string, number> = { robot: 2.9, tank: 3.1, filler: 1.6, manual: 1.5 };

// ---------- products ----------

function partDims(p: PartCfg): Dims {
  const s = p.size;
  let [x, y, z] = p.shape === 'box' ? [s[0], s[1], s[2] ?? s[0]]
    : p.shape === 'cylinder' || p.shape === 'cone' ? [s[0] * 2, s[1] ?? s[0], s[0] * 2]
      : p.shape === 'torus' ? [(s[0] + (s[1] ?? 0.05)) * 2, (s[0] + (s[1] ?? 0.05)) * 2, (s[1] ?? 0.05) * 2]
        : p.shape === 'capsule' ? [s[0] * 2, (s[1] ?? s[0]) + s[0] * 2, s[0] * 2]
          : [s[0] * 2, s[0] * 2, s[0] * 2];
  const quarter = (deg: number) => Math.abs(Math.round(deg / 90)) % 2 === 1;
  if (quarter(p.rotate[0])) [y, z] = [z, y];
  if (quarter(p.rotate[2])) [x, y] = [y, x];
  if (quarter(p.rotate[1])) [x, z] = [z, x];
  return { l: Math.abs(p.at[0]) * 2 + x, h: p.at[1] + y, d: Math.abs(p.at[2]) * 2 + z };
  // (torus lies in its own XY plane: rotate [90,0,0] to lay a ring flat)
}

function stageDims(parts: PartCfg[]): Dims {
  return parts.map(partDims).reduce((m, d) => ({ l: Math.max(m.l, d.l), h: Math.max(m.h, d.h), d: Math.max(m.d, d.d) }), { l: 0, h: 0, d: 0 });
}

/** A kraft carton around the bulkiest stage, with a printed band: the default packed look. */
function cartonFor(max: Dims): PartCfg[] {
  const l = +(max.l + 0.06).toFixed(2);
  const h = +(max.h + 0.06).toFixed(2);
  const d = +(max.d + 0.06).toFixed(2);
  const base = { at: [0, 0, 0] as [number, number, number], rotate: [0, 0, 0] as [number, number, number] };
  return [
    { ...base, shape: 'box', size: [l, h, d], color: 'kraft', round: 0.03 },
    { ...base, shape: 'box', size: [l + 0.004, Math.min(0.08, h * 0.2), d * 0.5], at: [0, h * 0.55, d * 0.255], color: 'blue', round: 0.01 },
  ];
}

/**
 * A product's stages in process order: each route says which stage comes after which (the stages
 * its steps make, in turn); stages no step makes (what enters a line) come first, and ties keep
 * the order written. So a pack whose keys were sorted on the way (through the server's JSON, an
 * agent's tool call) still enters with the right stage.
 */
export function stageOrder(industry: IndustryCfg, key: string): string[] {
  const keys = Object.keys(industry.products[key]?.stages ?? {});
  const made = new Set<string>();
  const next = new Map(keys.map((k) => [k, new Set<string>()]));
  const before = new Map(keys.map((k) => [k, 0]));
  for (const route of Object.values(industry.routes)) {
    const seq: string[] = [];
    for (const st of route) {
      const mk = typeof st === 'string' ? industry.machines[st]?.makes : (st.makes ?? industry.machines[st.machine]?.makes);
      if (!mk || !keys.includes(mk)) continue;
      made.add(mk);
      if (seq[seq.length - 1] !== mk) seq.push(mk);
    }
    for (let i = 1; i < seq.length; i++) {
      if (next.get(seq[i - 1])!.has(seq[i])) continue;
      next.get(seq[i - 1])!.add(seq[i]);
      before.set(seq[i], before.get(seq[i])! + 1);
    }
  }
  const out: string[] = [];
  const left = new Set(keys);
  while (left.size) {
    const ready = keys.filter((k) => left.has(k) && before.get(k) === 0);
    // routes that disagree: the rest as written
    if (!ready.length) return [...out, ...keys.filter((k) => left.has(k))];
    const k = ready.find((x) => !made.has(x)) ?? ready[0];
    out.push(k);
    left.delete(k);
    for (const b of next.get(k)!) before.set(b, before.get(b)! - 1);
  }
  return out;
}

function productInfo(industry: IndustryCfg, key: string, needsPacked: boolean): ProductInfo {
  const p = industry.products[key];
  if (!p) throw new Error(`行业包 ${industry.id} 里没有产品 ${key}`);
  const stages: Record<string, PartCfg[]> = {};
  const order = stageOrder(industry, key);
  for (const k of order) stages[k] = p.stages[k].parts;
  const dims: Record<string, Dims> = {};
  for (const k of order) dims[k] = stageDims(stages[k]);
  let max = Object.values(dims).reduce((m, d) => ({ l: Math.max(m.l, d.l), h: Math.max(m.h, d.h), d: Math.max(m.d, d.d) }));
  if (needsPacked && !stages.packed) {
    stages.packed = cartonFor(max);
    order.push('packed');
    dims.packed = stageDims(stages.packed);
    max = { l: Math.max(max.l, dims.packed.l), h: Math.max(max.h, dims.packed.h), d: Math.max(max.d, dims.packed.d) };
  }
  return { key, name: p.name, stages, order, dims, max, partNo: p.partNo, material: p.material, weight: p.weight, input: p.input, customer: p.customer, suppliers: p.suppliers, variants: p.variants, customers: p.customers, price: p.price };
}

// ---------- plant ----------

const pad2 = (n: number) => String(n).padStart(2, '0');
const r1 = (v: number) => Math.round(v * 100) / 100;

interface ExpandedLine { cfg: LineCfgT; name: string; steps: StepCfg[] }

function expandLines(plant: PlantCfgT, industry: IndustryCfg): ExpandedLine[] {
  const out: ExpandedLine[] = [];
  for (const cfg of plant.lines) {
    const steps = cfg.steps ?? industry.routes[cfg.route!];
    if (!steps) throw new Error(`行业包 ${industry.id} 里没有工艺路线 ${cfg.route}`);
    for (let i = 1; i <= cfg.repeat; i++) {
      const n = out.length + 1;
      const name = cfg.name.includes('{n}') ? cfg.name.replace('{n}', String(cfg.repeat > 1 ? i : n))
        : cfg.repeat > 1 ? `${cfg.name} ${i}` : cfg.name;
      out.push({ cfg, name, steps });
    }
  }
  return out;
}

/** Default camera for a viewport: portrait screens need to stand further back to see the hall. */
export function homeView(L: PlantLayout, aspect: number) {
  const k = Math.min(2.2, Math.max(1, 1.35 / aspect));
  const [tx, ty, tz] = L.home.target;
  const [px, py, pz] = L.home.pos;
  return { target: L.home.target, pos: [tx + (px - tx) * k, ty + (py - ty) * k, tz + (pz - tz) * k] as [number, number, number] };
}

export function layoutPlant(company: CompanyCfg, plant: PlantCfgT, industry: IndustryCfg, plantIndex: number): PlantLayout {
  const lines: LineLayout[] = [];
  const WH_W = 11;
  const X_START = 19; // first station area starts here; store occupies 2..13
  let zCursor = 0;

  expandLines(plant, industry).forEach(({ cfg, name, steps }, li) => {
    const n = li + 1;
    const resolved = steps.map((s) => {
      const st = typeof s === 'string' ? { machine: s } : s;
      const machine = industry.machines[st.machine];
      if (!machine) throw new Error(`${name}：行业包 ${industry.id} 里没有设备 ${st.machine}`);
      return { st, machine };
    });
    const needsPacked = resolved.some((r) => r.machine.look.archetype === 'packer' && !r.st.makes && !r.machine.makes);
    const product = productInfo(industry, cfg.product, needsPacked);
    const input = cfg.input ?? product.order[0];
    if (!product.stages[input]) throw new Error(`${name}：产品 ${product.name} 没有工序状态 ${input}`);
    const max = product.max;
    const walk = industry.flow === 'walk';
    // a walkway is a strip of ground wide enough for the group walking along it
    const beltW = r1(Math.max(0.9, max.d + (walk ? 0.8 : 0.3)));
    const beltY = cfg.beltHeight ?? (walk ? 0.05 : max.h > 1.2 ? 0.5 : 0.9);
    const gap = r1(max.l + 0.4);
    const speed = cfg.speed ?? (gap > 2 ? 0.9 : 0.55);

    // Walk the routing, sizing every machine around the product.
    const seq: Record<string, number> = {};
    let cursor = X_START + 1.2;
    let back = 1.6;
    const stations: StationLayout[] = resolved.map(({ st, machine }) => {
      const arch = machine.look.archetype;
      const base = machine.look.size ?? DEFAULT_SIZE[arch];
      const len = r1(ENCLOSING.has(arch) ? Math.max(base[0], max.l + 0.9) : Math.max(base[0], arch === 'robot' || arch === 'manual' ? max.l + 1.6 : 0));
      const size: [number, number, number] = [len, Math.max(base[1], beltW + 1.0), Math.max(base[2], beltY + max.h + 0.9)];
      back = Math.max(back, beltW / 2 + (BACK[arch] ?? size[1] / 2));
      seq[machine.code] = (seq[machine.code] ?? 0) + 1;
      const x = cursor + len / 2;
      cursor += len + 1.4;
      const makes = st.makes ?? machine.makes ?? (arch === 'packer' && product.stages.packed ? 'packed' : undefined);
      for (const k of [...Object.keys(('take' in st ? st.take : undefined) ?? machine.take ?? {}), ...(('give' in st ? st.give : undefined) ?? machine.give ?? []), ...(machine.tooling?.spare ? [machine.tooling.spare] : []), ...('tooling' in st && st.tooling?.spare ? [st.tooling.spare] : [])]) {
        if (!industry.inventory[k]) throw new Error(`${name}：${st.name ?? machine.label} 用到的库存 ${k} 没有在行业包 inventory 里定义`);
      }
      if (makes && !product.stages[makes]) throw new Error(`${name}：产品 ${product.name} 没有工序状态 ${makes}`);
      // per-step overrides make this station's own machine (a heavier billet needs a bigger heater …)
      const power = 'power' in st ? st.power : undefined;
      const tooling = 'tooling' in st ? st.tooling : undefined;
      const own: MachineCfg = power || tooling
        ? { ...machine, power: power ?? machine.power, tooling: tooling && machine.tooling ? { ...machine.tooling, ...tooling } : machine.tooling }
        : machine;
      return {
        id: `${machine.code}-${n}${pad2(seq[machine.code])}`,
        key: st.machine,
        name: st.name ?? machine.label,
        machine: own,
        x: r1(x),
        len,
        size,
        cycle: st.cycle ?? machine.cycle,
        take: ('take' in st ? st.take : undefined) ?? machine.take,
        give: ('give' in st ? st.give : undefined) ?? machine.give,
        makes,
        measure: st.measure ?? machine.measure,
      };
    });
    const x0 = X_START;
    const x1 = r1(cursor + 0.4);
    const front = beltW / 2 + 4.6;
    const z = zCursor + back;
    zCursor = z + front;
    const bottleneck = Math.max(...stations.map((s) => s.cycle));
    // a work order is about a day's output at 85 % (two wind-turbine shafts, 1 450 crankshafts)
    const day = (3600 / bottleneck) * 24 * 0.85;
    const qty = day >= 100 ? Math.round(day / 50) * 50 : Math.max(1, Math.round(day));
    lines.push({
      id: `L${n}`, no: n, name, note: cfg.note, z, x0, x1, beltY, beltW, speed, gap,
      spawn: r1(bottleneck * 0.96),
      product, stations, input,
      conveyor: { id: `CV-${n}`, name: `${name}输送线` },
      infeed: { id: `BF-${n}`, name: `${name}上料缓存`, cap: 40, x: x0 + 0.6, z: z + beltW / 2 + 1.1 },
      outfeed: { id: `OF-${n}`, name: `${name}成品托盘`, cap: max.l > 1.5 ? 6 : 18, x: x1 + 1.6, z },
      lane: z + beltW / 2 + 3.2,
      order: {
        no: cfg.order?.no ?? `${industry.terms.order?.prefix ?? 'MO'}-261005-${plantIndex + 1}${pad2(n)}`,
        part: cfg.order?.part ?? product.name,
        qty: cfg.order?.qty ?? qty,
        issued: `05:${pad2(30 + (n % 10))}`,
        kitted: `05:${pad2(48 + (n % 10))}`,
      },
    });
  });

  // Hall bounds, then centre everything on the origin.
  const maxX1 = Math.max(...lines.map((l) => l.outfeed.x + 1.5));
  const fgX0 = maxX1 + 4.5;
  const raw = { x0: 0, x1: fgX0 + 9.5, z0: -2.5, z1: zCursor + 1.5 };
  const dx = -(raw.x0 + raw.x1) / 2;
  const dz = -(raw.z0 + raw.z1) / 2;
  const X = (x: number) => r1(x + dx);
  const Z = (z: number) => r1(z + dz);
  for (const l of lines) {
    l.z = Z(l.z); l.x0 = X(l.x0); l.x1 = X(l.x1); l.lane = Z(l.lane);
    l.infeed.x = X(l.infeed.x); l.infeed.z = Z(l.infeed.z);
    l.outfeed.x = X(l.outfeed.x); l.outfeed.z = Z(l.outfeed.z);
    for (const s of l.stations) s.x = X(s.x);
  }
  const box = { x0: X(raw.x0), x1: X(raw.x1), z0: Z(raw.z0), z1: Z(raw.z1) };
  const warehouse: Zone = { id: 'WH', name: plant.warehouse, x0: X(2), x1: X(2 + WH_W), z0: box.z0 + 2.5, z1: box.z1 - 2.5, cap: 1600 * lines.length };
  const fg: Zone = { id: 'FG', name: plant.finishedGoods, x0: X(fgX0), x1: X(fgX0 + 8.5), z0: box.z0 + 2.5, z1: box.z1 - 2.5, cap: 0 };
  const whDock = X(2 + WH_W + 2);
  const fgDock = X(fgX0 - 1.6);

  const agvs = lines.flatMap((l) => [
    {
      id: `AGV-${pad2(l.no)}A`, name: `${l.name}送料车`,
      route: [{ x: whDock, z: l.lane, act: 'load', line: l.id }, { x: l.infeed.x, z: l.lane, act: 'drop', line: l.id }] as Waypoint[],
    },
    {
      id: `AGV-${pad2(l.no)}B`, name: `${l.name}成品车`,
      route: [{ x: fgDock, z: l.lane, act: 'deliver' }, { x: l.outfeed.x, z: l.lane, act: 'pick', line: l.id }] as Waypoint[],
    },
  ]);

  const roads = { front: r1(box.z1 + 8), left: r1(box.x0 - 12), right: r1(box.x1 + 7) };
  const midZ = r1((box.z0 + box.z1) / 2);
  const trucks = [
    {
      id: 'TRK-IN', name: '原料进厂车', kind: 'in' as const, carrier: plant.carriers[0],
      route: [{ x: roads.left - 90, z: roads.front }, { x: roads.left, z: roads.front }, { x: roads.left, z: midZ, act: 'dock' }, { x: roads.left, z: box.z0 - 70 }] as Waypoint[],
    },
    {
      id: 'TRK-OUT', name: '成品发运车', kind: 'out' as const, carrier: plant.carriers[1],
      route: [{ x: roads.right + 100, z: roads.front }, { x: roads.right, z: roads.front }, { x: roads.right, z: midZ, act: 'dock' }, { x: roads.right, z: box.z0 - 70 }] as Waypoint[],
    },
  ];

  const W = box.x1 - box.x0;
  const D = box.z1 - box.z0;
  const dist = Math.max(W * 0.88, D * 1.45, 55);
  const dir = [40, 46, 58].map((v) => v / Math.hypot(40, 46, 58));
  const target: [number, number, number] = [0, 0, 0];
  const home = { target, pos: [target[0] + dir[0] * dist, dir[1] * dist, dir[2] * dist] as [number, number, number] };

  return {
    company: { id: company.id, name: company.name, short: company.short },
    id: plant.id,
    name: plant.name,
    badge: plant.badge ?? plant.name.slice(0, 2),
    hall: plant.hall,
    sub: plant.sub ?? `${industry.name}，${lines.length} 条产线`,
    industry,
    unit: industry.unit,
    qualityLabel: industry.qualityLabel,
    site: plant.site,
    flow: industry.flow,
    terms: industry.terms,
    calendar: industry.calendar,
    pace: industry.pace,
    qa: industry.qa,
    tariff: company.tariff,
    freight: plant.freight?.length ? plant.freight : [{ carrier: plant.carriers[1], share: 1, late: 0.1, truck: 25 }],
    // a plant that lists its inventory keeps only those items (a BBQ site has no tents)
    inventory: Object.entries(industry.inventory).filter(([key]) => !plant.inventory || key in plant.inventory).map(([key, it]) => {
      const cap = plant.inventory?.[key] ?? it.capacity;
      return { key, label: it.label, unit: it.unit, kind: it.kind, cap, low: it.low, refill: it.refill ?? Math.ceil(cap / 2), lead: it.lead };
    }),
    lines,
    box,
    warehouse,
    fg,
    agvs,
    trucks,
    roads,
    home,
  };
}
