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
  /**
   * what a worn machine (low health) does to readings: mean shift in sigmas and extra spread, at
   * fully worn. Where the tool, not the machine, sets the quality (a forging die), keep these small.
   */
  wear: z.object({ shift: z.number().min(0), spread: z.number().min(0) }).default({ shift: 4, spread: 2.5 }),
  /** what an out-of-spec unit is recorded as: below `lo` / above `hi` (e.g. 充不满, 尺寸超差) */
  defects: z.object({ lo: z.string().optional(), hi: z.string().optional() }).optional(),
  /** out-of-spec units are reworked (re-heat-treated, re-machined) and stay on the line instead of being scrapped */
  rework: z.boolean().default(false),
  /** reading shift per 10 minutes the unit waited (in the queue, for stock): a guest's score falls with the wait */
  wait: z.number().optional(),
  /** reading shift in sigmas per 10 °C the machine runs above its normal running temperature (an overheated die press) */
  heat: z.number().optional(),
});
export type MeasureCfg = z.infer<typeof Measure>;

/** A product family: its look at each stage. The first stage is what enters the line. */
export const Product = z.object({
  name: z.string(),
  /** drawing / part number */
  partNo: z.string().optional(),
  /** material grade, e.g. 42CrMo */
  material: z.string().optional(),
  /** kg per finished unit */
  weight: z.number().positive().optional(),
  /** kg of material that goes in per unit (billet, ingot); weight / input is the material yield */
  input: z.number().positive().optional(),
  customer: z.string().optional(),
  /**
   * part numbers of the family a line can run (a 1.5 L and a 2.0 T crankshaft): each work order is
   * one of them, by `share`, and switching to another means changing to that part's own tools
   */
  variants: z.array(z.object({
    partNo: z.string(), name: z.string(), weight: z.number().positive().optional(), input: z.number().positive().optional(),
    share: z.number().positive().default(1),
    /** selling price per unit (元) */
    price: z.number().positive().optional(),
    /** which of the product's customers buy this part number (default: all of them) */
    customers: z.array(z.string()).optional(),
  })).optional(),
  /** who buys the family: each work order is for one of them, by `share`; `transit` is hours on the road to them */
  customers: z.array(z.object({ name: z.string(), share: z.number().positive().default(1), transit: z.number().positive().default(24) })).optional(),
  /** selling price per unit (元) when the part numbers name none */
  price: z.number().positive().optional(),
  /**
   * where the material comes from: each lot (a steel heat) is drawn from a supplier by `share`;
   * a `bad` share of a supplier's lots carries `factor` times the material-borne flaws
   */
  suppliers: z.array(z.object({
    name: z.string(), share: z.number().positive(), bad: z.number().min(0).max(1).default(0.05), factor: z.number().min(1).default(4),
    /** 元 per tonne, days from order to promised delivery, chance a delivery is late */
    price: z.number().positive().optional(), lead: z.number().positive().default(20), late: z.number().min(0).max(1).default(0.1),
  })).optional(),
  stages: z.record(z.string(), z.object({ parts: z.array(Part).min(1) })),
  /** hours a finished batch keeps (保质期): what has not left by then is thrown away, oldest goes out first */
  shelf: z.number().positive().optional(),
  /** hours one unit of every batch is kept as a retained sample (留样), e.g. 48 */
  sample: z.number().positive().optional(),
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
  /** plain: no factory fittings (manual: a counter instead of racks and tool rail; frame: a deck in the body colour, no HMI) */
  style: z.enum(['industrial', 'plain']).optional(),
  parts: z.array(Part).default([]),
  /** where the units a machine with `slots` holds are drawn, [x, z] from its centre (a table, a pitch); a grid by default */
  places: z.array(z.tuple([z.number(), z.number()])).optional(),
});
export type LookCfg = z.infer<typeof Look>;

/** Units of inventory a step lends out (or uses up): {tent: 1, sleepingBag: 3}. */
const Take = z.record(z.string(), z.number().int().positive());

/**
 * A place that holds several units at once — tables of a dining room, pitches of a campsite — for a
 * time that depends on the group, or until a time of day (check-out). A walk-flow line lets people
 * in only while its first such place has room: the rest wait at the door.
 */
const Slotted = {
  /** units held at once (tables, pitches) */
  slots: z.number().int().min(1).optional(),
  /** people a slot seats, for 上座率 (default 4) */
  seats: z.number().int().min(1).optional(),
  /** seconds a group of 1, 2, 3 … people stays, in place of `cycle` (the last one for bigger groups) */
  bySize: z.array(z.number().positive()).min(1).optional(),
  /** units stay until this hour of the day comes round (11 = check out by 11:00 the next morning), give or take an hour */
  until: z.number().min(0).max(24).optional(),
  /** `take` amounts are per person of the group (and vary with appetite), not per unit */
  perPerson: z.boolean().optional(),
  /** each slot is a place of its own the twin shows and outside systems report on: what it is called (桌, 营位) and the floor it takes [x, z] */
  place: z.object({ label: z.string().default('桌'), size: z.tuple([z.number().positive(), z.number().positive()]).default([2.4, 2.4]) }).optional(),
};

/**
 * Something the site keeps a count of: lent to units and returned (tents, tables, tableware)
 * or used up and restocked by deliveries (charcoal, meat sets, firewood).
 */
export const InventoryItem = z.object({
  label: z.string(),
  unit: z.string().default('件'),
  kind: z.enum(['lend', 'consume']).default('lend'),
  /** how many the site owns / can store; a plant can override it */
  capacity: z.number().int().positive(),
  /** alarm when the available share drops below this */
  low: z.number().min(0).max(1).default(0.1),
  /** consume: amount each supply delivery brings (default half the capacity) */
  refill: z.number().int().positive().optional(),
  /**
   * consume: bought to order instead of arriving with every supply truck — when the stock falls to
   * `low`, `refill` more are ordered and arrive this many hours later (spare dies, rolls …)
   */
  lead: z.number().positive().optional(),
  /** 元 a guest pays per unit taken (a 份 of skewers): what a group spends adds up from what it took */
  price: z.number().min(0).optional(),
});
export type InventoryItemCfg = z.infer<typeof InventoryItem>;

/**
 * Figures a pack can put on top of its twin. Factories: oee, throughput, quality, power. Venues:
 * turnover 翻台率, occupancy 上座率 / 占用, queue 等位, ticket 客单价, rating 好评率 (the share of
 * scores in spec), served 每小时接待, takeaway 外卖, checkins 今日入住 / 退房, inUse (an inventory
 * item lent out), weather. Kitchens and depots: onTime 准时配送率, coldChain 冷链达标率.
 */
export const KpiId = z.enum(['oee', 'throughput', 'quality', 'power', 'turnover', 'occupancy', 'queue', 'ticket', 'rating', 'served', 'takeaway', 'checkins', 'inUse', 'weather', 'onTime', 'coldChain']);
export type KpiIdT = z.infer<typeof KpiId>;

/** Guests arriving at a walk-flow site (a shop, a campsite) through the day. */
export const Demand = z.object({
  /** groups arriving per hour per place (table, pitch) at full intensity */
  rate: z.number().positive(),
  /** intensity by hour of the day, [hour, 0..1] points joined by straight lines (13.5 = 13:30) */
  curve: z.array(z.tuple([z.number().min(0).max(24), z.number().min(0)])).min(2),
  /** multiplier by weekday, Sunday first */
  week: z.array(z.number().min(0)).length(7).optional(),
  /** share of groups of 1, 2, 3 … people */
  sizes: z.array(z.number().min(0)).min(1).max(8).default([0.12, 0.38, 0.22, 0.16, 0.08, 0.04]),
  /** minutes a waiting group gives up after, [min, max] */
  patience: z.tuple([z.number().positive(), z.number().positive()]).default([20, 70]),
  /** groups waiting at which newcomers start to walk away (all of them at twice as many) */
  balk: z.number().int().positive().default(6),
  /** 元 per person on top of what they took (茶位、调料) */
  fee: z.number().min(0).default(0),
  /** orders for delivery (外卖), separate from the guests: per hour at full intensity, what one takes, minutes to make */
  takeaway: z.object({
    rate: z.number().positive(),
    take: Take,
    prep: z.number().positive().default(12),
    /** 元 an order brings */
    spend: z.tuple([z.number().min(0), z.number().min(0)]).default([60, 140]),
  }).optional(),
  /** weather drawn each morning: the chance of rain and what rain does to arrivals */
  weather: z.object({ rain: z.number().min(0).max(1), factor: z.number().min(0).max(2) }).optional(),
});
export type DemandCfg = z.infer<typeof Demand>;

/**
 * A wearing tool counted in units it has made: forging die, trim die, cutter, punch, mould.
 * Near the end of its life measurements drift towards the limits; at the planned change point
 * (`limit` × `life`) it is changed as soon as the machine is free, at the end of its life the machine
 * stops for the change. A change uses one spare from inventory when `spare` names an item.
 */
const Tooling = z.object({
  label: z.string(),
  life: z.number().int().positive(),
  /** seconds a change takes */
  change: z.number().positive().default(600),
  /** inventory key of the spare a change uses up */
  spare: z.string().optional(),
  /** how far wear pushes measurements at end of life, in sigmas (0 = no effect) */
  drift: z.number().min(0).default(3),
  /** planned change point as a share of the life (0.85 = change at 85 %) */
  limit: z.number().min(0.3).max(1).default(1),
  /** removed tools are reconditioned (a die re-sunk) and come back as spares after `hours`; `rate` is the share that can be */
  repair: z.object({ hours: z.number().positive(), rate: z.number().min(0).max(1) }).optional(),
  /** units scrapped while setting up after a change (first-article adjustment) */
  setup: z.number().int().min(0).default(0),
  /** units until a new tool is at working temperature; flaws marked `cold` are more likely before that */
  warm: z.number().int().min(0).default(0),
  /** 元 a change costs in tooling (the new or reconditioned tool, amortised) */
  cost: z.number().min(0).default(0),
});
export type ToolingCfg = z.infer<typeof Tooling>;
/** per-step tooling overrides: no defaults, so only what the step names replaces the machine's */
const ToolingPatch = z.object({
  label: z.string().optional(),
  life: z.number().int().positive().optional(),
  change: z.number().positive().optional(),
  spare: z.string().optional(),
  drift: z.number().min(0).optional(),
  limit: z.number().min(0.3).max(1).optional(),
  setup: z.number().int().min(0).optional(),
  warm: z.number().int().min(0).optional(),
  cost: z.number().min(0).optional(),
});

export const Machine = z.object({
  label: z.string(),
  /** id prefix, e.g. CNC -> CNC-101 */
  code: z.string().regex(/^[A-Z][A-Z0-9]{0,4}$/),
  look: Look,
  /** seconds per unit at 100 % speed */
  cycle: z.number().positive().default(8),
  /** product stage after this machine; packers default to `packed` when the product has it */
  makes: z.string().optional(),
  /**
   * body temperature rise over ambient (°C) idle / running, alarm and trip limits. A machine that
   * cools (a fridge, a cold room, a freezer tunnel) has negative rises: it never trips on
   * temperature, and while it is stopped or broken down it warms towards ambient.
   */
  thermal: z.object({
    idle: z.number(), run: z.number(), warn: z.number(), trip: z.number(), tau: z.number().optional(),
    /** °C a fully worn machine runs above a healthy one (default 18; a few degrees for refrigeration) */
    wear: z.number().min(0).optional(),
  }).default({ idle: 4, run: 20, warn: 55, trip: 66 }),
  /** kW */
  power: z.object({ idle: z.number(), run: z.number() }).default({ idle: 1, run: 5 }),
  /** mm/s at 100 % speed */
  vibration: z.number().default(1),
  faults: z.array(z.string()).min(1).default(['设备故障']),
  measure: Measure.optional(),
  /** inspection without a measured value: base probability a unit is rejected */
  rejectRate: z.number().min(0).max(1).optional(),
  /**
   * A wearing tool counted in units it has made: forging die, trim die, cutter, punch, mould.
   * Near the end of its life measurements drift towards the limits; at the end the machine stops
   * for a change, which uses one spare from inventory when `spare` names an item.
   */
  tooling: Tooling.optional(),
  /** fuel burnt per hour (natural gas for a furnace …), idle / running */
  fuel: z.object({ label: z.string(), unit: z.string(), idle: z.number().min(0), run: z.number().min(0) }).optional(),
  /** a cold-chain machine (a cold room, a freezer): the highest °C its goods may be kept at; counts towards 冷链达标率 */
  hold: z.number().optional(),
  /** a crew works it by hand: it pauses for meal breaks and slows down at shift handover */
  crew: z.boolean().optional(),
  ...Slotted,
  /** how often it breaks down and how long the crew takes; defaults suit a demo, real plants are slower */
  upkeep: z.object({
    /** running hours between breakdowns at full health (a worn machine breaks down up to 7× as often) */
    mtbf: z.number().positive().default(3.33),
    /** minutes the crew takes to repair a breakdown, [min, max] */
    repair: z.tuple([z.number().positive(), z.number().positive()]).default([1, 3]),
    /** running hours until wear calls for planned maintenance */
    pm: z.number().positive().default(3.06),
    /** minutes planned maintenance takes, [min, max] */
    pmTime: z.tuple([z.number().positive(), z.number().positive()]).default([0.67, 1.5]),
    /** short stops (a part sticking in the die, a jammed chute): per running hour, seconds [min, max], what the operator logs */
    stops: z.object({
      perHour: z.number().min(0),
      secs: z.tuple([z.number().positive(), z.number().positive()]),
      reasons: z.array(z.string()).min(1),
    }).optional(),
  }).default({ mtbf: 3.33, repair: [1, 3], pm: 3.06, pmTime: [0.67, 1.5] }),
  /** what a unit failed by `rejectRate` is recorded as */
  defect: z.string().optional(),
  /**
   * flaws this step can leave in a unit, found later by an inspection that `detects` them:
   * `rate` per unit, times `material` lot factor, up to `wear`× at the end of the tool's life,
   * `cold`× while a new tool is warming up
   */
  flaws: z.array(z.object({
    type: z.string(), rate: z.number().min(0).max(1),
    material: z.boolean().default(false), wear: z.number().min(1).default(1), cold: z.number().min(1).default(1),
    /**
     * how much likelier per sigma the unit's last measured step (a heater's exit temperature) was
     * below (`lo`) or above (`hi`) its target: a cold billet does not fill the die
     */
    process: z.object({ lo: z.number().min(0).default(0), hi: z.number().min(0).default(0) }).optional(),
  })).optional(),
  /** an inspection: catches units carrying these flaws (with probability `catch`) and rejects them under the flaw's name */
  detects: z.object({ types: z.array(z.string()).min(1), catch: z.number().min(0).max(1).default(0.95) }).optional(),
  /** default inventory this machine lends / takes back (a step can override) */
  take: Take.optional(),
  give: z.array(z.string()).optional(),
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
    /** kW idle / running for this step (an induction heater sized for a heavier billet …) */
    power: z.object({ idle: z.number(), run: z.number() }).optional(),
    /** tooling overrides for this step (a smaller die lasts longer …) */
    tooling: ToolingPatch.optional(),
    /** inventory lent out (or used up) when a unit finishes this step; the unit waits if there is not enough */
    take: Take.optional(),
    /** lent inventory the unit hands back at this step (anything still held is returned when it leaves) */
    give: z.array(z.string()).optional(),
    ...Slotted,
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
  /** belt: units ride conveyors between machines; walk: units are people (or vehicles) moving along walkways */
  flow: z.enum(['belt', 'walk']).default('belt'),
  /** dashboard wording for industries that are not factories (a campsite, a hospital, a car wash …) */
  terms: z.object({
    /** overall efficiency KPI, default 综合设备效率 OEE */
    oee: z.string().optional(),
    /** units per hour KPI, default 每小时产量 */
    throughput: z.string().optional(),
    /** power KPI, default 实时功率 */
    power: z.string().optional(),
    /** what a line is called, default 条线 (e.g. 个营区) */
    line: z.string().optional(),
    /** what the logistics vehicles are, default AGV 小车 (e.g. 送餐车) */
    vehicle: z.string().optional(),
    /** the order tracker: a factory work order by default, a booking for a venue */
    order: z.object({
      /** default 工单跟踪 */
      title: z.string().optional(),
      /** tab name, default 工单 */
      tab: z.string().optional(),
      /** order number prefix, default MO */
      prefix: z.string().optional(),
      /** issued, kitted, in progress, completed, checked, delivered — default 工单下达 物料齐套 生产中 生产完成 质检入库 发运 */
      steps: z.array(z.string()).length(6).optional(),
    }).optional(),
    /** what a plant is called, default 工厂 (门店, 营地) */
    plant: z.string().optional(),
    /** what a line is called as a noun, default 产线 (区域) */
    lineNoun: z.string().optional(),
    /** what a machine is called, default 设备 (档口, 设施) */
    machine: z.string().optional(),
    /** the material lot a line runs on, default 炉号 (批号) */
    lot: z.string().optional(),
    /** what comes in and what goes out, default 原料 / 成品 (食材 / 出品) */
    raw: z.string().optional(),
    goods: z.string().optional(),
    /** the walk-flow entrance where groups wait, default 上料缓存 (门口等位区) */
    infeed: z.string().optional(),
    /** where finished units leave a line, default 成品托盘 (离店) */
    outfeed: z.string().optional(),
    /** the conveyor / walkway, default 输送线 (过道) */
    conveyor: z.string().optional(),
    /** cycle time, default 节拍 (用时) */
    cycle: z.string().optional(),
    /** utilisation, default 稼动率 (使用率) */
    util: z.string().optional(),
    /** state words in place of the factory ones, e.g. {"idle": "空闲", "changeover": "换台"} */
    states: z.record(z.string(), z.string()).optional(),
  }).default({}),
  /**
   * how material moves: inside the site (AGVs, forklifts, nobody — guests walk, staff carry), what
   * the store looks like, and the vehicles that bring material in and take goods out
   */
  logistics: z.object({
    internal: z.enum(['agv', 'forklift', 'none']).default('agv'),
    /** racks: a high-bay store; shelves: a small storeroom; none */
    store: z.enum(['racks', 'shelves', 'none']).default('racks'),
    inbound: z.object({
      vehicle: z.enum(['truck', 'van', 'none']).default('truck'),
      /** default 原料进厂车 */
      label: z.string().optional(),
      /**
       * hours between scheduled material deliveries; each brings that long's use and can come late
       * (the supplier's `late` share): the store runs dry and the lines starve. Omitted: the truck
       * comes whenever the store has room.
       */
      every: z.number().positive().optional(),
    }).default({ vehicle: 'truck' }),
    outbound: z.object({
      /** rider: couriers on motorbikes picking orders up at the counter */
      vehicle: z.enum(['truck', 'van', 'rider', 'none']).default('truck'),
      /** default 成品发运车 */
      label: z.string().optional(),
      /** how many (riders) */
      count: z.number().int().min(1).max(4).default(1),
    }).default({ vehicle: 'truck', count: 1 }),
  }).default({ internal: 'agv', store: 'racks', inbound: { vehicle: 'truck' }, outbound: { vehicle: 'truck', count: 1 } }),
  /**
   * the figures on top of the twin, in order; the first is the plant's headline. Default OEE,
   * throughput, quality and power. An item `{"id": "inUse", "item": "tent"}` shows how many of an
   * inventory item are lent out.
   */
  kpis: z.array(z.union([KpiId, z.object({ id: KpiId, item: z.string().optional(), label: z.string().optional() })])).optional(),
  /** guests who come to a walk-flow site by the hour of the day: a queue forms when the places are full */
  demand: Demand.optional(),
  /**
   * the working week: night shift (operators tire: more short stops and flaws) and a weekly planned
   * maintenance slot per line (lines take turns through the week)
   */
  calendar: z.object({
    night: z.object({ from: z.number().min(0).max(23), to: z.number().min(0).max(24), stops: z.number().min(1).default(1), flaws: z.number().min(1).default(1) }).optional(),
    pm: z.object({ hours: z.number().positive(), start: z.number().min(0).max(23) }).optional(),
    /**
     * opening hours (a shop) or the shift a kitchen works, e.g. {from: 11, to: 1}: outside them
     * lines take in no new units and finish what they have; machines stay on (fridges keep cold)
     */
    open: z.object({ from: z.number().min(0).max(23), to: z.number().min(0).max(24) }).optional(),
    /**
     * shift changes (交接班) at these hours: for `handover` minutes crews work at `pace` of their
     * speed while the outgoing shift hands over, and the handover note goes into the log
     */
    shifts: z.object({
      at: z.array(z.number().min(0).max(24)).min(1),
      handover: z.number().positive().default(15),
      pace: z.number().min(0).max(1).default(0.5),
      /** names of the shifts in the order of `at`, default 早班 / 中班 / 夜班 or 白班 / 夜班 */
      names: z.array(z.string()).optional(),
    }).optional(),
    /** meal breaks: lines take in nothing new and crewed machines pause (planned stops, not counted against OEE) */
    breaks: z.array(z.object({ at: z.number().min(0).max(24), minutes: z.number().positive(), label: z.string().default('用餐') })).optional(),
    /**
     * the week's order mix: work-order size by weekday (Sunday first), and the part number each
     * weekday favours (the customers' call-off schedule) — more changeovers on mixed days
     */
    week: z.array(z.number().min(0)).length(7).optional(),
  }).default({}),
  /**
   * how far real cycles run over the nominal one: each unit takes cycle × (1 + |N(0, pace)|), so
   * performance sits a few percent under 100 % the way it does on a real line (0 = always nominal)
   */
  pace: z.number().min(0).max(0.5).default(0),
  /**
   * quality assurance: `incoming` is the chance incoming inspection catches a bad material lot (it is
   * returned to the supplier); `found` the chance a customer finds a flawed unit that got past final inspection
   */
  qa: z.object({ incoming: z.number().min(0).max(1).default(0.3), found: z.number().min(0).max(1).default(0.6) }).default({ incoming: 0.3, found: 0.6 }),
  products: z.record(z.string(), Product),
  machines: z.record(z.string(), Machine),
  /** what the site keeps a count of (tents, tables, charcoal …), shown as in use / available / capacity */
  inventory: z.record(z.string(), InventoryItem).default({}),
  /** reusable routings, referenced by lines as `route` */
  routes: z.record(z.string(), z.array(Step).min(1)).default({}),
  /** classes, relations, rules and individuals of the trade, bound to products and machines */
  ontology: z.unknown().optional(),
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
  /**
   * the places of the line's station with slots — its tables (A01 …) or pitches: listed one by one,
   * or as the seats of each (their number is the slots). Ids default to the line's letter and a number.
   */
  tables: z.object({
    prefix: z.string().optional(),
    /** area shown with each (大厅A区, 包间); default the line's name */
    area: z.string().optional(),
    seats: z.array(z.number().int().min(1)).optional(),
    list: z.array(z.object({ id: z.string().regex(/^[A-Za-z0-9-]{1,12}$/), seats: z.number().int().min(1), area: z.string().optional() })).optional(),
  }).optional(),
}).refine((l) => l.route || l.steps, { message: '产线需要 route 或 steps 其中之一' });
export type LineCfgT = z.infer<typeof LineCfg>;

export const PlantCfg = z.object({
  id: z.string(),
  name: z.string(),
  /** two or three characters on the site badge */
  badge: z.string().max(3).optional(),
  hall: z.string().default('生产车间'),
  /** hall: an indoor workshop with walls; outdoor: grass, a fence and walkways (venues, yards, farms); shop: a shopfront onto the street */
  site: z.enum(['hall', 'outdoor', 'shop']).default('hall'),
  sub: z.string().optional(),
  /** defaults to the company's industry */
  industry: z.string().optional(),
  lines: z.array(LineCfg).min(1),
  warehouse: z.string().default('原料立体库'),
  /** this site's inventory: only these items, with these capacities ({tent: 20}); default every item of the industry */
  inventory: z.record(z.string(), z.number().int().nonnegative()).optional(),
  finishedGoods: z.string().default('成品库'),
  carriers: z.tuple([z.string(), z.string()]).default(['顺达物流', '远航货运']),
  /** who carries finished goods to customers: share of shipments, chance one is delayed on the road, tonnes a truck takes */
  freight: z.array(z.object({ carrier: z.string(), share: z.number().positive().default(1), late: z.number().min(0).max(1).default(0.1), truck: z.number().positive().default(25) })).optional(),
  /**
   * another plant of the company keeps this one stocked (a central kitchen its shops): at each
   * `depart` hour its van loads what this site is short of, oldest batches first, and arrives
   * `transit` hours later. `items` maps this site's inventory to the products there that refill it;
   * other consumables come on the same van from what the company buys in.
   */
  /** this site's own guests' day and figures, in place of its pack's (a barbecue ground next to the campsite) */
  demand: Demand.optional(),
  kpis: z.array(z.union([KpiId, z.object({ id: KpiId, item: z.string().optional(), label: z.string().optional() })])).optional(),
  supply: z.object({
    from: z.string(),
    depart: z.array(z.number().min(0).max(24)).min(1),
    transit: z.number().positive().default(1),
    items: z.record(z.string(), z.array(z.string()).min(1)),
  }).optional(),
});
export type PlantCfgT = z.infer<typeof PlantCfg>;

export const Company = z.object({
  $schema: z.string().optional(),
  id: z.string(),
  name: z.string(),
  /** short name for the badge in the top bar */
  short: z.string().max(4),
  industry: z.string(),
  /** energy prices: electricity 元/kWh by hour of day (peak / flat / valley), natural gas 元/Nm³ */
  tariff: z.object({
    power: z.array(z.object({ from: z.number().min(0).max(23), to: z.number().min(1).max(24), price: z.number().min(0) })).min(1),
    gas: z.number().min(0).default(0),
  }).optional(),
  plants: z.array(PlantCfg).min(1),
  /** the company's own additions to its industry packs' ontology */
  ontology: z.unknown().optional(),
});
export type CompanyCfg = z.infer<typeof Company>;
