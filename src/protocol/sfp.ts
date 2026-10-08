// SFP, the SmartFactory protocol between the browser and the twin's server: the live channel
// (WebSocket /api/live) and the HTTP API. This file is the standard. The browser's types are
// inferred from it, `npm run schema` writes it as protocol/sfp.schema.json, and the server's
// conformance test (server/internal/web/protocol_test.go) checks what the server sends against
// that file. Human-readable version: protocol/README.md.
import { z } from 'zod';

/** Bumped on a change an older peer would misread; adding a message or an optional field is not one. */
export const PROTOCOL = 1;

const num = z.number();
const str = z.string();

// ---------- shared ----------

export const Command = z.enum(['start', 'stop', 'speed', 'reset', 'maint', 'fault', 'restock', 'ship', 'retool', 'toollimit']).meta({
  description: 'speed: arg 0.5–1.5 · toollimit: arg 0.3–1 of the die life · fault: a drill',
});

export const AssetState = z.enum([
  'running', 'idle', 'blocked', 'stopped', 'fault', 'maint', 'changeover',
  'moving', 'waiting', 'handling', 'away',
  'ok', 'empty', 'low', 'full',
]);

export const Source = z.object({
  name: str,
  type: z.enum(['mqtt', 'modbus', 'opcua']),
  url: str,
  connected: z.boolean(),
  error: str.optional(),
  points: num,
  received: num,
  /** RFC 3339; the zero time when nothing arrived yet */
  lastAt: str,
  /** company/plant/asset of each asset this source feeds */
  targets: z.array(str),
  /** the same, named for people */
  labels: z.array(str).optional(),
}).meta({ description: 'A protocol adapter reading equipment values' });

export const LiveValues = z.object({
  /** adapter name */
  src: str,
  /** wall-clock ms of the last value */
  at: num,
  f: z.record(str, z.union([num, str, z.boolean()])),
}).meta({ description: 'Values read from equipment; while fresh they replace the simulated ones' });

export const Alarm = z.object({
  id: num,
  asset: str,
  code: str,
  prio: z.union([z.literal(1), z.literal(2), z.literal(3)]),
  text: str,
  /** plant seconds since the simulation started */
  at: num,
  active: z.boolean(),
  acked: z.boolean(),
  clearedAt: num.optional(),
});

export const LogEntry = z.object({ at: num, asset: str, text: str, kind: z.enum(['info', 'op', 'alarm']), prio: num });

export const SimEvent = z.discriminatedUnion('type', [
  z.object({ type: z.literal('reject'), line: str, asset: str, x: num, z: num, kind: str }),
  z.object({ type: z.literal('fault'), asset: str }),
]);

/** [id, position along the line, steps done, steps in process, seconds at this step, stage, seconds stalled, inventory held] */
export const ProductTuple = z.tuple([str, num, num, num, num, str, num, z.record(str, num).nullable()]);

export const AssetFrame = z.object({
  id: str,
  enabled: z.boolean(),
  speed: num,
  fault: z.object({ text: str, at: num }).nullable(),
  maint: num,
  state: AssetState,
  temp: num,
  vib: num,
  power: num,
  health: num,
  util: num,
  avail: num,
  cycles: num,
  progress: num,
  /** id of the unit being processed */
  busy: str.nullable(),
  reading: z.object({ v: num, ok: z.boolean(), at: num }).nullable(),
  rejects: num,
  verdict: z.object({ ok: z.boolean(), at: num }).nullable(),
  stock: num,
  count: num,
  battery: num,
  vel: num,
  task: str,
  live: LiveValues.nullable(),
  /** units made with the current tool, and its planned change point */
  toolUsed: num.optional(),
  toolLimit: num.optional(),
  /** seconds left of a tool change */
  changing: num.optional(),
});

export const LineFrame = z.object({
  id: str,
  spawnT: num,
  pid: num,
  good: num,
  scrap: num,
  /** units finished within the counting window */
  tp: num,
  products: z.array(ProductTuple),
  /** [heat no, supplier] */
  lot: z.tuple([str, str]).optional(),
  /** [order no, part name, quantity, good, issued at] */
  job: z.tuple([str, str, num, num, num]).optional(),
});

export const PlantState = z.object({
  /** shift clock, seconds since midnight */
  clock: num,
  /** seconds simulated since start */
  elapsed: num,
  /** kWh this shift */
  energy: num,
  /** kW now */
  power: num,
  alarmSeq: num,
  assets: z.array(AssetFrame),
  lines: z.array(LineFrame),
  agvs: z.array(z.object({
    id: str, idx: num, x: num, z: num, heading: num,
    cargo: z.object({ kind: z.enum(['raw', 'fg']), n: num }).nullable(),
    mode: z.enum(['move', 'dwell']), dwell: num, waiting: z.boolean(), waitedT: num, task: str,
  })),
  trucks: z.array(z.object({
    id: str, idx: num, x: num, z: num, heading: num,
    phase: z.enum(['away', 'drive', 'dock', 'leave']), wait: num, dock: num, load: num,
  })),
  /** newest first, at most 80 */
  alarms: z.array(Alarm),
  /** newest first, at most 60 */
  log: z.array(LogEntry),
  /** [key, available, capacity] */
  inventory: z.array(z.tuple([str, num, num])).optional(),
  /** what happened since the previous frame of this plant */
  events: z.array(SimEvent),
}).meta({ description: "A plant's replicated state" });

// ---------- live channel: browser → server ----------

export const Watch = z.object({
  type: z.literal('watch'),
  company: str,
  /** the plant on screen, or "all" for every plant at full rate */
  plant: str,
});
export const PlantSwitch = z.object({ type: z.literal('plant'), plant: str });
export const Cmd = z.object({
  type: z.literal('cmd'),
  plant: str,
  asset: str,
  cmd: Command,
  arg: num.optional(),
  /** the sending tab, echoed back on the broadcast so it can skip its own */
  o: str.optional(),
});
export const Time = z.object({ type: z.literal('time'), scale: num.min(0).max(20) });
export const HistRequest = z.object({ type: z.literal('hist'), plant: str, asset: str });

export const ClientMessage = z.discriminatedUnion('type', [Watch, PlantSwitch, Cmd, Time, HistRequest]);

// ---------- live channel: server → browser ----------

export const Hello = z.object({
  type: z.literal('hello'),
  protocol: z.literal(PROTOCOL),
  /** opened with the operator token */
  operator: z.boolean(),
  /** the server runs the simulation; the browser follows it */
  sim: z.boolean(),
  /** server build */
  version: str,
  sources: z.array(Source),
});
export const Sources = z.object({ type: z.literal('sources'), sources: z.array(Source) });
export const Frame = z.object({
  type: z.literal('frame'),
  data: z.object({
    company: str,
    timeScale: num,
    /** by plant id: the watched plant(s) every frame, the others every 4 s */
    plants: z.record(str, PlantState),
  }),
});
export const Result = z.object({ type: z.literal('result'), ok: z.boolean(), msg: str });
export const History = z.object({
  temp: z.array(num), power: z.array(num), vib: z.array(num), level: z.array(num), meas: z.array(num),
});
export const Hist = z.object({ type: z.literal('hist'), plant: str, asset: str, data: History.nullable() });
export const CmdBroadcast = z.object({
  type: z.literal('cmd'),
  cmd: z.object({
    c: str, p: str, a: str, cmd: Command, arg: num.optional(),
    by: z.enum(['operator', 'mcp']),
    o: str.optional(),
  }),
}).meta({ description: 'An operator or an AI assistant acted on the watched company' });

export const ServerMessage = z.discriminatedUnion('type', [Hello, Sources, Frame, Result, Hist, CmdBroadcast]);

// ---------- HTTP ----------

export const ErrorBody = z.object({ error: str, rule: str.optional() });

export const Health = z.object({
  ok: z.boolean(),
  version: str,
  companies: z.array(str),
  /** milliseconds one simulation step of every company takes */
  stepMs: num,
  /** RFC 3339 of the last state save */
  savedAt: str,
  sources: z.array(Source),
});

export const Directory = z.array(z.object({ id: str, name: str, short: str }));
export const RuntimeCompany = z.object({
  /** see src/config/schema/company.json */
  company: z.record(str, z.unknown()),
  /** industry packs it brings, by id; see src/config/schema/industry.json */
  industries: z.record(str, z.record(str, z.unknown())),
});
export const Tag = z.object({ c: str, p: str, a: str, f: str, v: z.union([num, str, z.boolean()]), src: str, t: num });
export const Tags = z.array(Tag);
export const Advisors = z.record(str, str).meta({ description: "Each company's advisor console URL" });

// ---------- HTTP: the advisor (/api/advisor/{company}/…, answered by DataIntelligence) ----------
// Its objects may carry more fields than listed here.

const open = z.looseObject;

export const MetricInfo = open({ name: str, description: str, synonyms: z.array(str).optional(), additivity: str.optional() });
export const DimensionInfo = open({ name: str, type: str.optional(), synonyms: z.array(str).optional() });
export const Metrics = open({ metrics: z.array(MetricInfo) });
export const Dimensions = open({ dimensions: z.array(DimensionInfo) });

export const Where = z.object({ dimension: str, op: str, values: z.array(z.unknown()) });
export const Query = z.object({
  metrics: z.array(str),
  group_by: z.array(str).optional(),
  where: z.array(Where).optional(),
  grain: z.enum(['day', 'week', 'month']).optional(),
  limit: num.optional(),
});
export const Answer = open({ columns: z.array(str), rows: z.array(z.array(z.unknown())), sql: str });

export type Filter = { dimension?: string; metric?: string; op: string; values?: unknown[]; and?: Filter[]; or?: Filter[] };
export const Filter: z.ZodType<Filter> = z.lazy(() => open({
  dimension: str.optional(), metric: str.optional(), op: str, values: z.array(z.unknown()).optional(),
  and: z.array(Filter).optional(), or: z.array(Filter).optional(),
}));
const Direction = z.enum(['up', 'down']);
export const Guard = open({ metric: str, worse_is: Direction, tolerance: num.nullable().optional(), limit: num.nullable().optional() });
export const Measurement = open({
  metric: str, scope: z.array(Filter).optional(), from: str, to: str, value: num.nullable(), data_days: num, sql: str, at: str,
});
export const Goal = open({
  id: str, what: str, metric: str, direction: Direction, by: num, within_days: num,
  scope: z.array(Filter).optional(), guards: z.array(Guard).optional(), baseline: Measurement.optional(),
  author: str, via: str.optional(), at: str,
});
export const Evidence = open({
  asked: str, sql: str, columns: z.array(str), rows: z.array(z.array(z.unknown())), row_count: num, truncated: z.boolean().optional(), at: str,
});
export const Finding = open({ id: str, goal: str.optional(), says: str, evidence: z.array(Evidence), by: str, via: str.optional(), at: str });
export const Action = open({ server: str, tool: str, args: z.record(str, z.unknown()).optional(), why: str.optional() });
export const Plan = open({
  id: str, goal: str.optional(), findings: z.array(str), does: str,
  expect: open({ metric: str, direction: Direction, by: num, within_days: num, scope: z.array(Filter).optional() }),
  guards: z.array(Guard).optional(), actions: z.array(Action).optional(), cost: str.optional(),
  by: str, via: str.optional(), at: str, note: str.optional(),
});
export const Decision = open({
  plan: str, verdict: z.enum(['adopted', 'rejected']), who: str, role: str.optional(), why: str.optional(), at: str,
  baseline: Measurement.optional(), guard_baselines: z.array(Measurement).optional(),
});
export const ActionRun = open({
  plan: str, index: num, server: str, tool: str, result: str.optional(), error: str.optional(), skipped: z.boolean().optional(), ms: num, at: str,
});
export const GuardResult = open({ guard: Guard, baseline: num.nullable(), measured: Measurement.nullable(), broken: z.boolean() });
export const Acceptance = open({
  plan: str, final: z.boolean(), metric: str, baseline: num.nullable(), target: num.nullable(), measured: Measurement.nullable(),
  guards: z.array(GuardResult).optional(), outcome: z.enum(['achieved', 'missed', 'guard_broken', 'no_data']), at: str,
});
export const PlanView = open({
  plan: Plan, state: z.enum(['proposed', 'adopted', 'rejected', 'accepted']), decision: Decision.optional(), due: str.optional(),
  actions: z.array(ActionRun).optional(), progress: Acceptance.optional(), acceptance: Acceptance.optional(),
});
export const GoalView = open({ goal: Goal, target: num.optional(), findings: z.array(Finding), plans: z.array(PlanView) });
export const Board = open({
  goals: z.array(GoalView), loose_findings: z.array(Finding).optional(), loose_plans: z.array(PlanView).optional(), at: str,
}).meta({ description: 'The consulting record: goals, findings, plans and their decisions' });

export const Verb = z.enum(['adopt', 'reject', 'measure', 'accept']);
export const DecideRequest = z.object({ why: str.optional() });

export const AskRequest = z.object({ question: str });
export const AskEvent = z.discriminatedUnion('kind', [
  open({ kind: z.literal('thinking'), text: str.optional() }),
  open({ kind: z.literal('tool_call'), tool: str, args: z.record(str, z.unknown()).optional() }),
  open({ kind: z.literal('tool_result'), tool: str, result: z.unknown().optional() }),
  open({ kind: z.literal('verify'), text: str.optional() }),
  open({ kind: z.literal('verified'), text: str.optional() }),
  open({ kind: z.literal('verify_skipped'), text: str.optional() }),
  open({ kind: z.literal('complete'), text: str.optional() }),
  open({ kind: z.literal('error'), text: str.optional() }),
]).meta({ description: 'One server-sent event of an agent run: `data: <AskEvent JSON>`' });

export const Review = open({ name: str, title: str, schedule: str.optional(), can_run: z.boolean(), running: z.boolean() });
export const ReviewStep = open({ tool: str, args: z.record(str, z.unknown()).optional(), result: z.unknown().optional() });
export const ReviewRun = open({
  id: num, name: str, title: str, by: str, trigger: z.enum(['schedule', 'manual']), started_at: str, finished_at: str,
  answer: str, status: z.enum(['running', 'done', 'failed']), steps: z.array(ReviewStep).optional(), corrected: str.optional(),
});
export const Reviews = open({ reviews: z.array(Review), runs: z.array(ReviewRun) });

/** Everything the standard defines, by name: what protocol/sfp.schema.json lists under $defs. */
export const DEFS = {
  ClientMessage, ServerMessage, PlantState,
  ErrorBody, Health, Directory, RuntimeCompany, Tags, Advisors,
  Metrics, Dimensions, Query, Answer, Board, DecideRequest, AskRequest, AskEvent, Reviews, ReviewRun,
} satisfies Record<string, z.ZodType>;

export type ClientMessage = z.infer<typeof ClientMessage>;
export type ServerMessage = z.infer<typeof ServerMessage>;
export type PlantState = z.infer<typeof PlantState>;
export type ProductTuple = z.infer<typeof ProductTuple>;
export type SourceInfo = z.infer<typeof Source>;
