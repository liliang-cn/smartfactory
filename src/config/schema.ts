// Configuration format for SmartFactory. Everything that differs between factories lives in
// JSON validated by these schemas; everything else is convention (see core/layout.ts).
//
//   src/config/industries/<id>.json   an industry pack: products, machine types, routings
//   src/config/companies/<id>.json    a company: its plants and their production lines
//
// Dropping a new file into either folder registers it; no code changes needed.
import { z } from 'zod';

/** Hex colour or a palette token: white, shell, blue, blueDeep, blueSoft, yellow, orange, kraft, wood, grey, greyDark, ink, metal, glass, red, green, mint. */
export const Color = z.string();
const Vec3 = z.tuple([z.number(), z.number(), z.number()]);

/** Motion bound to the machine (or, for product parts, to the unit being processed). */
export const Anim = z.object({
  /** spin: rotate about `axis`; stroke: move along `axis` by `to` inside `window`; blink: flash `glow`; heat: glow follows temperature. */
  type: z.enum(['spin', 'stroke', 'blink', 'heat']),
  axis: z.enum(['x', 'y', 'z']).default('y'),
  /** spin speed in rad/s at 100 % machine speed */
  speed: z.number().default(6),
  /** stroke distance in metres (negative = down/back) */
  to: z.number().default(-0.5),
  /** part of the cycle (0..1) during which a stroke happens */
  window: z.tuple([z.number(), z.number()]).default([0.2, 0.8]),
  /** only while the machine is working (default) or all the time */
  when: z.enum(['running', 'always']).default('running'),
});

/**
 * A primitive used to compose products and to decorate machines.
 * `at` is the centre of the part's bottom face; `size` depends on `shape`:
 * box [length, height, depth] · cylinder [radius, height] · sphere [radius] · cone [radius, height] ·
 * torus [radius, tube] · capsule [radius, length]
 */
export const Part = z.object({
  shape: z.enum(['box', 'cylinder', 'sphere', 'cone', 'torus', 'capsule']),
  size: z.array(z.number().positive()).min(1).max(3),
  at: Vec3.default([0, 0, 0]),
  /** degrees about x, y, z */
  rotate: Vec3.default([0, 0, 0]),
  color: Color.default('white'),
  metal: z.number().min(0).max(1).optional(),
  rough: z.number().min(0).max(1).optional(),
  /** emissive colour */
  glow: Color.optional(),
  opacity: z.number().min(0).max(1).optional(),
  /** corner radius for boxes */
  round: z.number().min(0).optional(),
  anim: Anim.optional(),
});
export type PartCfg = z.infer<typeof Part>;

/** Per-unit measurement with tolerance. One-sided limits: give only `lo` or only `hi`. */
export const Measure = z.object({
  label: z.string(),
  unit: z.string(),
  target: z.number(),
  lo: z.number().optional(),
  hi: z.number().optional(),
  /** process spread at a healthy machine */
  sigma: z.number().positive(),
  /** which way wear pushes the reading */
  drift: z.union([z.literal(1), z.literal(-1)]).default(1),
  /** reading is a magnitude (leak rate, defect size): never below target */
  abs: z.boolean().default(false),
  digits: z.number().int().min(0).default(1),
  /** reading shift per +100 % speed */
  speedBias: z.number().default(0),
});
export type MeasureCfg = z.infer<typeof Measure>;

/** A product family: its look at each stage. The first stage is what enters the line. */
export const Product = z.object({
  name: z.string(),
  stages: z.record(z.string(), z.object({ parts: z.array(Part).min(1) })),
});
export type ProductCfg = z.infer<typeof Product>;

export const ARCHETYPES = [
  'enclosure', 'press', 'robot', 'tunnel', 'gantry', 'packer', 'manual', 'safety', 'chamber', 'filler', 'booth', 'tank', 'roller', 'frame',
] as const;

/** How a machine looks. Archetypes are parametric models; `parts` adds anything else. */
export const Look = z.object({
  archetype: z.enum(ARCHETYPES),
  /** [length along the line, depth, height] in metres; defaults per archetype, grown to fit the product */
  size: Vec3.optional(),
  color: Color.optional(),
  accent: Color.optional(),
  /** robot: gripper | welder | torch | spray; enclosure: spindle | pusher | none */
  tool: z.enum(['gripper', 'welder', 'torch', 'spray', 'spindle', 'pusher', 'none']).optional(),
  /** robot: safety fence around the cell */
  fence: z.boolean().optional(),
  /** tunnel / chamber / booth: interior light colour, e.g. #ff6a1a for a furnace, #9fe8ff for a freezer */
  glow: Color.optional(),
  /** tunnel: chimneys on the roof */
  stacks: z.number().int().min(0).optional(),
  /** manual: number of operators */
  operators: z.number().int().min(1).max(4).optional(),
  /** filler / tank: colour of the bottle or the liquid */
  fluid: Color.optional(),
  parts: z.array(Part).default([]),
});
export type LookCfg = z.infer<typeof Look>;

export const Machine = z.object({
  label: z.string(),
  /** id prefix, e.g. CNC -> CNC-101 */
  code: z.string().regex(/^[A-Z][A-Z0-9]{0,4}$/),
  look: Look,
  /** seconds per unit at 100 % speed */
  cycle: z.number().positive().default(8),
  /** product stage after this machine; packers default to `packed` when the product has it */
  makes: z.string().optional(),
  /** body temperature rise over ambient (°C) idle / running, alarm and trip limits */
  thermal: z.object({
    idle: z.number(), run: z.number(), warn: z.number(), trip: z.number(), tau: z.number().optional(),
  }).default({ idle: 4, run: 20, warn: 55, trip: 66 }),
  /** kW */
  power: z.object({ idle: z.number(), run: z.number() }).default({ idle: 1, run: 5 }),
  /** mm/s at 100 % speed */
  vibration: z.number().default(1),
  faults: z.array(z.string()).min(1).default(['设备故障']),
  measure: Measure.optional(),
  /** inspection without a measured value: base probability a unit is rejected */
  rejectRate: z.number().min(0).max(1).optional(),
}).refine((m) => m.thermal.warn >= 22 + m.thermal.run + 6 && m.thermal.trip > m.thermal.warn, {
  // `idle`/`run` are rises over 22 °C ambient; `warn`/`trip` are absolute temperatures.
  message: 'thermal.warn 应至少比运行温度（22°C + run）高 6°C，trip 应高于 warn',
  path: ['thermal'],
});
export type MachineCfg = z.infer<typeof Machine>;

/** One step of a routing: a machine key, or a machine with per-line overrides. */
export const Step = z.union([
  z.string(),
  z.object({
    machine: z.string(),
    name: z.string().optional(),
    cycle: z.number().positive().optional(),
    makes: z.string().optional(),
    measure: Measure.optional(),
  }),
]);
export type StepCfg = z.infer<typeof Step>;

export const Industry = z.object({
  $schema: z.string().optional(),
  id: z.string(),
  name: z.string(),
  /** what one unit is called: 件, 台, 箱, 辆 … */
  unit: z.string().default('件'),
  qualityLabel: z.string().default('一次合格率'),
  products: z.record(z.string(), Product),
  machines: z.record(z.string(), Machine),
  /** reusable routings, referenced by lines as `route` */
  routes: z.record(z.string(), z.array(Step).min(1)).default({}),
});
export type IndustryCfg = z.infer<typeof Industry>;

export const LineCfg = z.object({
  /** "{n}" is replaced by the line number within the plant when `repeat` > 1 */
  name: z.string(),
  note: z.string().default(''),
  /** product family key from the industry pack */
  product: z.string(),
  /** product stage units arrive in; defaults to the first stage (a paint shop takes body-in-white) */
  input: z.string().optional(),
  /** routing key from the industry pack … */
  route: z.string().optional(),
  /** … or the steps inline */
  steps: z.array(Step).optional(),
  /** build this many identical lines */
  repeat: z.number().int().min(1).default(1),
  /** belt speed m/s at 100 % */
  speed: z.number().positive().optional(),
  /** belt top height in metres */
  beltHeight: z.number().positive().optional(),
  order: z.object({ no: z.string().optional(), part: z.string().optional(), qty: z.number().int().positive().optional() }).optional(),
}).refine((l) => l.route || l.steps, { message: '产线需要 route 或 steps 其中之一' });
export type LineCfgT = z.infer<typeof LineCfg>;

export const PlantCfg = z.object({
  id: z.string(),
  name: z.string(),
  /** two or three characters on the site badge */
  badge: z.string().max(3).optional(),
  hall: z.string().default('生产车间'),
  sub: z.string().optional(),
  /** defaults to the company's industry */
  industry: z.string().optional(),
  lines: z.array(LineCfg).min(1),
  warehouse: z.string().default('原料立体库'),
  finishedGoods: z.string().default('成品库'),
  carriers: z.tuple([z.string(), z.string()]).default(['顺达物流', '远航货运']),
});
export type PlantCfgT = z.infer<typeof PlantCfg>;

export const Company = z.object({
  $schema: z.string().optional(),
  id: z.string(),
  name: z.string(),
  /** short name for the badge in the top bar */
  short: z.string().max(4),
  industry: z.string(),
  plants: z.array(PlantCfg).min(1),
});
export type CompanyCfg = z.infer<typeof Company>;
