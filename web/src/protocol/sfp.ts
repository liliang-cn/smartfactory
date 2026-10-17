// SFP, the SmartFactory protocol between the browser and the twin's server: the live channel
// (WebSocket /api/live) and the HTTP API; and SFD, how equipment reports to the twin and takes
// its commands (the "equipment" section below). This file is the standard. The browser's types are
// inferred from it, `npm run schema` writes it as protocol/sfp.schema.json, and the server's
// conformance test (server/internal/web/protocol_test.go) checks what the server sends against
// that file, as does internal/e2e/standard_test.go for what equipment puts on the wire.
// Human-readable versions: protocol/README.md and protocol/devices.md.
import { z } from 'zod';

/** Bumped on a change an older peer would misread; adding a message or an optional field is not one. */
export const PROTOCOL = 1;

const num = z.number();
const str = z.string();

// ---------- shared ----------

/**
 * What a caller may do in a company. No token: visitor. A token carries one role and the
 * companies it covers; elsewhere its holder is a visitor.
 */
export const Role = z.enum(['visitor', 'member', 'operator', 'admin', 'erp', 'reporter']).meta({
  description: 'visitor: watch, demo commands that undo themselves · member: + record notes and tickets · operator: + commands that reach equipment, the plant clock, the advisor\'s decisions · admin: + scenes, documents, assistants · erp: a business system\'s token for the order API of its companies, a visitor everywhere else · reporter: a camera box, POS or table sensor\'s token that reports the tables (and dining sessions) of its companies, a visitor everywhere else',
});

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
  /** readings kept but not given to the twin (out of range, or a jump no machine can make), and why the last one was */
  flagged: num.optional(),
  lastFlag: str.optional(),
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
  /** the caller is an operator (or above) of the company the page asked about (?company=) */
  operator: z.boolean(),
  /** the caller's role in that company */
  role: Role,
  /** the token holder's name; 访客 without one */
  name: str,
  /** the server runs the simulation; the browser follows it */
  sim: z.boolean(),
  /** server build */
  version: str,
  sources: z.array(Source),
});
export const Sources = z.object({ type: z.literal('sources'), sources: z.array(Source) });

/**
 * A hint: a measured machine's field reading unusually for that machine in its state, against the
 * normal learned from its own readings. Not an alarm: nothing stops. Simulated machines have none.
 */
export const Anomaly = z.object({
  id: num, company: str, plant: str, asset: str,
  /** temp · power · vib · lastMeasure · any other number the equipment reports */
  field: str,
  /** the machine's state whose normal it is held against; "" when it reports no state */
  state: str,
  /** level: readings stayed K σ off the normal · drift: their recent average moved off it, by less */
  kind: z.enum(['level', 'drift']),
  /** RFC 3339; ended is absent while it lasts */
  since: str, ended: str.optional(),
  /** the reading now (level) or the recent average (drift); z: how far from the normal, in σ of a reading; peak: the furthest it went */
  value: num, z: num, peak: num,
  /** the normal in that state */
  mean: num, sigma: num,
  source: str,
  /** the hint in words */
  text: str,
});
/** Every 5 s with sources, the watched company's hints now (servers that keep readings). */
export const Anomalies = z.object({ type: z.literal('anomalies'), anomalies: z.array(Anomaly) });
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
    /** who: the token holder's name */
    n: str.optional(),
    o: str.optional(),
  }),
}).meta({ description: 'An operator or an AI assistant acted on the watched company' });

export const ServerMessage = z.discriminatedUnion('type', [Hello, Sources, Anomalies, Frame, Result, Hist, CmdBroadcast]);

// ---------- HTTP ----------

export const ErrorBody = z.object({ error: str, rule: str.optional() });

/** GET /api/site — what this server runs (a customer's instance runs its own company only) */
export const Site = z.object({ companies: z.array(str) });

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
/** q: quality flags — 1 out of range, 2 a jump no machine can make, 4 the device's time was unusable (arrival time instead) */
export const Tag = z.object({ c: str, p: str, a: str, f: str, v: z.union([num, str, z.boolean()]), src: str, t: num, q: num.optional() });

// ---------- HTTP: stored readings (/api/series/{company}/{plant}/{asset}) ----------

/** raw: [t, value, flags]; minute and hour: [t, avg, min, max, readings, flagged] (avg, min, max null when every reading was flagged). t in unix ms. */
export const SeriesPoint = z.union([
  z.tuple([num, num, num]),
  z.tuple([num, num.nullable(), num.nullable(), num.nullable(), num, num]),
]);
export const SeriesAnswer = z.object({
  company: str, plant: str, asset: str,
  /** the range, unix ms */
  from: num, to: num,
  /** raw for up to 6 hours within the raw retention, minute up to 8 days, hour beyond (or as step asked) */
  resolution: z.enum(['raw', 'minute', 'hour']),
  series: z.array(z.object({ field: str, points: z.array(SeriesPoint) })),
  /** state words and texts the device sent, when they changed */
  events: z.array(z.object({ field: str, t: num, v: str, src: str, q: num.optional() })),
  /** a forced raw answer cut to its latest readings */
  truncated: z.boolean().optional(),
  /** every field stored for the asset */
  fields: z.array(str),
}).meta({ description: 'GET /api/series/{company}/{plant}/{asset}?field=temp,power&from=&to=&step= — members and above' });
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

// ---------- HTTP: the companies' own assistants (/api/agents/{company}/…, /api/library/{company}/…) ----------

/** RFC 3339 */
const time = str;
const Tools = z.record(str, z.unknown());

export const AssistantKind = z.enum(['docs', 'notes', 'service']);
export const AssistantTemplate = z.object({ kind: AssistantKind, title: str, tools: z.array(str) });
export const Assistant = z.object({
  id: str, company: str, kind: AssistantKind, name: str, instructions: str, created: time, updated: time,
});
export const Assistants = z.object({
  /** whether the server has a model to answer with */
  ready: z.boolean(),
  /** the caller is an operator (or above) of this company */
  operator: z.boolean(),
  /** the caller's role in this company: members change ticket status, admins set up assistants and documents */
  role: Role,
  /** the token holder's name; 访客 without one */
  you: str,
  templates: z.array(AssistantTemplate), agents: z.array(Assistant), severities: z.array(str), statuses: z.array(str),
}).meta({ description: "A company's assistants and the kinds it can make" });
export const AssistantRequest = z.object({ kind: AssistantKind.optional(), name: str, instructions: str.optional() });

export const Conversation = z.object({ id: str, agent: str, title: str, created: time, updated: time });
export const Conversations = z.array(Conversation).meta({ description: "The caller's own conversations with one assistant" });
export const ChatStep = z.object({ tool: str, args: Tools.optional(), result: z.unknown().optional() });
export const ChatMessage = z.object({
  id: num, role: z.enum(['user', 'assistant']), text: str, steps: z.array(ChatStep).optional(),
  status: z.enum(['done', 'running', 'stopped', 'failed']), at: time,
});
export const ConversationView = z.object({ conversation: Conversation, messages: z.array(ChatMessage) });
export const AssistantAsk = z.object({ conversation: str.optional(), text: str });
export const AssistantEvent = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('conversation'), id: str, title: str }),
  z.object({ kind: z.literal('thinking') }),
  z.object({ kind: z.literal('tool_call'), tool: str, args: Tools.optional() }),
  z.object({ kind: z.literal('tool_result'), tool: str, result: z.unknown().optional() }),
  /** the answer as it is written */
  z.object({ kind: z.literal('delta'), text: str }),
  /** what was written so far is dropped; the answer is written again */
  z.object({ kind: z.literal('reset') }),
  z.object({ kind: z.literal('complete'), text: str.optional() }),
  z.object({ kind: z.literal('error'), text: str }),
]).meta({ description: 'One server-sent event of an assistant answering: `data: <AssistantEvent JSON>`' });

/** markdown · text (typed or uploaded as text, UTF-8 or GBK) · pdf · docx · xlsx · csv */
export const DocKind = z.enum(['markdown', 'text', 'pdf', 'docx', 'xlsx', 'csv']);
export const Doc = z.object({
  id: str, title: str,
  /** characters of text, and the passages it was cut into (0 for a scan with no text) */
  size: num, parts: num, by: str, kind: DocKind,
  /** the file it was uploaded as, its size in bytes, its pages (PDF) */
  file: str.optional(), bytes: num.optional(), pages: num.optional(),
  /** uploads under this title; the knowledge base has the latest */
  version: num,
  /** what could not be read: a scan without text, pages that are pictures, the encoding it was read in */
  problems: z.array(str).optional(),
  created: time, updated: time,
});
export const Docs = z.array(Doc);
export const DocView = z.object({ doc: Doc, body: str });
export const DocUpload = z.object({ title: str, body: str });
export const DocVersion = z.object({
  version: num, file: str.optional(), kind: DocKind, bytes: num, pages: num.optional(), parts: num, problems: z.array(str).optional(),
  by: str, created: time,
  /** the version the knowledge base has; whether its file can be downloaded (GET …/file?version=) */
  current: z.boolean(), download: z.boolean(),
});
export const DocVersions = z.array(DocVersion).meta({ description: 'GET /api/library/{company}/docs/{doc}/versions — newest first' });
/**
 * POST /api/library/{company}/files (multipart/form-data, "file" parts, an optional "title" before
 * them for a single file; administrators; 50 MB a file). A file with the title or file name of a
 * document already there is its next version.
 */
export const UploadResults = z.array(z.object({
  file: str, ok: z.boolean(), outcome: z.enum(['created', 'updated']).optional(), doc: Doc.optional(), error: str.optional(),
})).meta({ description: 'POST /api/library/{company}/files — each file answered on its own' });
export const Note = z.object({
  id: str, title: str, body: str, tags: z.array(str), assets: z.array(str), by: str, created: time, updated: time,
});
export const Notes = z.array(Note);
export const Ticket = z.object({
  id: str, customer: str, product: str, batch: str, title: str, detail: str, severity: str, status: str, reply: str,
  log: z.array(z.object({ at: time, by: str, text: str })), created: time, updated: time,
});
export const Tickets = z.array(Ticket);
export const TicketPatch = z.object({ status: str });
export const Done = z.object({ ok: z.literal(true) });

// ---------- equipment (MQTT / Modbus TCP / OPC UA): protocol/devices.md ----------

const Scalar = z.union([num, str, z.boolean()]);

/** What a machine reports as its state; anything but fault / maint / stopped / changeover runs it. */
export const MachineState = z.enum(['idle', 'running', 'blocked', 'stopped', 'fault', 'maint', 'changeover']);

/**
 * The fields of one asset in one message: {prefix}/{company}/{plant}/{asset} carries a JSON
 * object of them, {prefix}/{company}/{plant}/{asset}/{field} one of them as plain text. Other
 * fields are kept and shown with the asset; inv.<key> / cap.<key> set the plant's inventory.
 */
export const DeviceFields = z.object({
  state: MachineState.optional(),
  /** fault text; "" / false / 0 clears the fault */
  fault: z.union([str, z.boolean(), num]).optional(),
  /** n-th fault of the machine type, 0 = none */
  faultCode: num.int().min(0).optional(),
  /** true / 1 / "on" runs the machine, false / 0 / "off" stops it */
  running: Scalar.optional(),
  /** °C */
  temp: num.optional(),
  /** kW */
  power: num.optional(),
  /** mm/s */
  vib: num.optional(),
  /** factor 0.5–1.5 (or percent 50–150) */
  speed: num.optional(),
  /** 0–1 */
  health: num.min(0).max(1).optional(),
  /** one message per measured unit */
  measure: num.optional(),
  /** polled protocols: the last measured value, and a counter that moves once per measured unit */
  lastMeasure: num.optional(),
  mcount: num.optional(),
  toolUsed: num.int().min(0).optional(),
  toolLife: num.int().positive().optional(),
  stock: num.optional(),
  count: num.optional(),
  /** vehicles: % */
  battery: num.optional(),
  /** vehicles: metres in plant coordinates, radians */
  x: num.optional(),
  z: num.optional(),
  heading: num.optional(),
  task: str.optional(),
}).catchall(Scalar).meta({ description: 'Fields an asset reports' });

/** Published by the server on {prefix}/{company}/{plant}/{asset}/cmd. */
export const DeviceCommand = z.object({
  cmd: Command,
  arg: num.optional(),
  /** who issued it: an operator, an AI assistant over MCP, a visitor or the automatic undo of a visitor's action */
  by: z.enum(['operator', 'mcp', 'visitor', 'auto']),
  /** wall-clock ms */
  t: num,
});

/** Where each value of a plant lives under the sf convention (GET /api/devices/{company}/{plant}). */
export const DeviceMap = z.object({
  company: str,
  plant: str,
  mqtt: z.object({ data: str, field: str, cmd: str }),
  modbus: z.object({
    points: z.array(z.object({
      asset: str, field: str, register: z.literal('holding'), address: num,
      kind: z.enum(['int16', 'uint16', 'float32']), scale: num.optional(), map: z.record(str, str).optional(),
    })),
    /** write the argument ×argScale to argAddress, then the command code to address; the device sets it back to 0 */
    commands: z.array(z.object({ asset: str, address: num, argAddress: num, argScale: num })),
    codes: z.object({ state: z.array(str), command: z.array(str) }),
  }),
  opcua: z.object({
    points: z.array(z.object({ asset: str, field: str, node: str })),
    /** write the argument to argNode, then the command name to node; the device sets it back to "" */
    commands: z.array(z.object({ asset: str, node: str, argNode: str })),
  }),
});

/** Everything the standard defines, by name: what protocol/sfp.schema.json lists under $defs. */
export const AuditEntry = z.object({
  id: num, at: time, who: str, role: Role,
  /** which token: the start of its hash ("token" for the original single token) */
  key: str,
  /** api: another system (the ERP) calling the order API with its own token · guard: the command guard itself (a request lapsing) · anomaly: the detector of unusual readings */
  via: z.enum(['page', 'live', 'assistant', 'mcp', 'cli', 'api', 'guard', 'anomaly']),
  /** the company it happened in; * for what every company shares */
  company: str,
  /** command · clock · note/ticket writes by an assistant (save_note, update_note, create_ticket, update_ticket) · ticket.status · doc.add · doc.update · doc.delete · note.delete · assistant.create/update/delete · advisor.<verb> · company.create/delete · industry.create · token.issue/revoke · knowledge.rebuild · calib.fit · calib.apply · calib.revert · erp.config · erp.secret · erp.pull · erp.orders · erp.cancel · command.refused / command.pending / command.approved / command.denied / command.expired (the guard; target plant/asset and the request id) · guard.config · device.add / device.renew / device.revoke (a device on the embedded broker; target its name) */
  /** command · clock · note/ticket writes by an assistant (save_note, update_note, create_ticket, update_ticket) · ticket.status · doc.add · doc.update · doc.delete · note.delete · assistant.create/update/delete · advisor.<verb> · company.create/delete · industry.create · token.issue/revoke · knowledge.rebuild · calib.fit · calib.apply · calib.revert · erp.config · erp.secret · erp.pull · erp.orders · erp.cancel · command.refused / command.pending / command.approved / command.denied / command.expired (the guard; target plant/asset and the request id) · guard.config · check.form / check.form.delete (target the form id) · check.submit (a form filled in: target plant/asset and the submission id; context every value) */
  /** command · clock · note/ticket writes by an assistant (save_note, update_note, create_ticket, update_ticket) · ticket.status · doc.add · doc.update · doc.delete · note.delete · assistant.create/update/delete · advisor.<verb> · company.create/delete · industry.create · token.issue/revoke · knowledge.rebuild · calib.fit · calib.apply · calib.revert · erp.config · erp.secret · erp.pull · erp.orders · erp.cancel · command.refused / command.pending / command.approved / command.denied / command.expired (the guard; target plant/asset and the request id) · guard.config · anomaly / anomaly.end (a hint raised and over; target plant/asset) · anomaly.reset */
  action: str,
  target: str, detail: str.optional(), ok: z.boolean(), error: str.optional(),
  /** what was true when it happened, for a command to a machine: its state, temperature and alarms, the rule that refused it, who asked and who approved */
  context: str.optional(),
});
export const AuditLog = z.array(AuditEntry).meta({ description: "A company's changes, newest first (GET /api/audit/{company}?before=&limit=)" });

// ---------- the console (/admin) ----------

/** The signed-in administrator. companies is empty for a token covering every company. */
export const AdminMe = z.object({ name: str, role: Role, companies: z.array(str), everywhere: z.boolean(), key: str });

export const AdminCompany = z.object({
  id: str, name: str, short: str, industry: str,
  /** shipped with the app (cannot be replaced or deleted); the others were created in the console or over MCP */
  builtin: z.boolean(),
  plants: z.array(z.object({ id: str, name: str, hall: str, site: z.enum(['hall', 'outdoor']), lines: num, machines: num })),
  /** values currently arriving from real equipment */
  liveTags: num,
  /** the company has a business advisor */
  advisor: z.boolean(),
});

export const IndustrySummary = z.object({
  id: str, name: str, flow: z.enum(['belt', 'walk']), unit: str, builtin: z.boolean(),
  products: z.array(str),
  /** machine key → "label (archetype)" */
  machines: z.record(str, str),
});

export const AdminOverview = z.object({
  me: AdminMe,
  /** the companies the token covers */
  companies: z.array(AdminCompany),
  industries: z.array(IndustrySummary),
  /** tokens can be issued here (the server keeps data) */
  issue: z.boolean(),
  /** the server runs the companies' assistants */
  assistants: z.boolean(),
  version: str,
  /** the companies this server is limited to (absent when any: the demo) */
  runs: z.array(str).optional(),
  /** a company's own installation: one company, none until its factory is built */
  single: z.boolean(),
}).meta({ description: 'GET /api/admin — administrators only (401 without a token, 403 below admin)' });

/** A company the engine accepted (POST /api/admin/companies, same id replaces). */
export const CompanyCreated = z.object({
  ok: z.literal(true), id: str, name: str,
  plants: z.array(z.object({ id: str, name: str, site: z.enum(['hall', 'outdoor']), lines: z.array(str), machines: num })),
});

/** A config the engine turned down (422): each problem, and where. */
export const ConfigProblem = z.object({ error: str, errors: z.array(str), where: str.optional() });

export const Account = z.object({
  /** the start of the token's hash */
  key: str, name: str, role: Role,
  /** empty: every company */
  companies: z.array(str),
  /** last day it works, YYYY-MM-DD */
  expires: str.optional(),
  /** legacy: the original single token · config: the server's config file (changed only there) · issued: here */
  from: z.enum(['legacy', 'config', 'issued']),
  created: time.optional(), createdBy: str.optional(),
  /** last time it was used */
  used: time.optional(),
  revoked: time.optional(), revokedBy: str.optional(),
  expired: z.boolean().optional(),
});
export const Accounts = z.array(Account).meta({ description: 'GET /api/admin/tokens — the tokens of the companies the caller administers' });
export const IssueToken = z.object({
  name: str, role: z.enum(['member', 'operator', 'admin', 'erp', 'reporter']),
  /** null: every company (only an administrator of every company) */
  companies: z.array(str).nullable(),
  expires: str.optional(),
});
/** The token itself is in this answer only; the server keeps a hash. */
export const IssuedToken = z.object({ token: str, account: Account });

/**
 * POST /api/admin/compose: a site described in words, and optionally the config to change. kind
 * company (default): a company config, with new industry packs when none fits; industry: a pack alone.
 */
export const ComposeRequest = z.object({ kind: z.enum(['company', 'industry']).optional(), brief: str, base: z.record(str, z.unknown()).optional() });
export const ComposeEvent = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('thinking') }),
  z.object({ kind: z.literal('tool_call'), tool: str, args: z.record(str, z.unknown()).optional() }),
  z.object({ kind: z.literal('tool_result'), tool: str, result: z.unknown().optional() }),
  z.object({ kind: z.literal('delta'), text: str }),
  z.object({ kind: z.literal('reset') }),
  /** a company config that passed the engine's check (nothing was created) */
  z.object({ kind: z.literal('draft'), result: z.record(str, z.unknown()) }),
  /** an industry pack it wrote that passed the check (nothing was installed); a company it drafts may use it */
  z.object({ kind: z.literal('industry'), result: z.record(str, z.unknown()) }),
  z.object({ kind: z.literal('complete'), text: str.optional() }),
  z.object({ kind: z.literal('error'), text: str }),
]).meta({ description: 'One server-sent event of the scene architect: `data: <ComposeEvent JSON>`' });

// ---------- templates (/api/admin/templates) ----------

/** A factory to start from, as lists show it. */
export const TemplateSummary = z.object({
  id: str, name: str, description: str.optional(), author: str.optional(),
  /** builtin · local (saved or imported on this installation) */
  source: z.enum(['builtin', 'local']),
  industry: str, industryName: str,
  plants: z.array(z.object({ id: str, name: str, site: z.enum(['hall', 'outdoor']), lines: z.array(str), machines: num })),
  created: time.optional(),
});
export const Templates = z.array(TemplateSummary).meta({ description: 'GET /api/admin/templates' });
/** A template whole: a company config (its id the template's) and the industry packs it needs that are not built in. */
export const Template = z.object({
  id: str, name: str, description: str.optional(), author: str.optional(), created: time,
  company: z.record(str, z.unknown()),
  industries: z.record(str, z.record(str, z.unknown())).optional(),
}).meta({ description: 'GET /api/admin/templates/{source}/{id} (the file a template is exported as); POST /api/admin/templates/import takes one' });
/** POST /api/admin/templates: a template of a running company (from) or of a config (company). */
export const SaveTemplate = z.object({
  id: str, name: str, description: str.optional(), from: str.optional(),
  company: z.record(str, z.unknown()).optional(), industries: z.record(str, z.unknown()).optional(),
});

// ---------- knowledge base (/api/knowledge/{company}) ----------

/**
 * What a thing in the graph is: the process, the production orders from the ERP, and what recently happened to a machine —
 * check (a form out of spec, or a station's latest checks) · anomaly (a hint that a reading is unusual) · calibration · command (one an AI sent)
 */
export const EntityKind = z.enum(['company', 'plant', 'line', 'station', 'machine', 'product', 'supplier', 'customer', 'order', 'check', 'anomaly', 'calibration', 'command']);
/**
 * has_plant · has_line · makes · has_station · is_a (station → machine) · feeds (station → next station) · supplies · sold_to ·
 * for_customer (order → customer) · of_product (order → product) · runs_on (order → line) ·
 * checked_on (check → station) · seen_on (anomaly → station) · calibrates (calibration → station) · targets (command → station)
 */
export const RelationType = z.enum(['has_plant', 'has_line', 'makes', 'has_station', 'is_a', 'feeds', 'supplies', 'sold_to', 'for_customer', 'of_product', 'runs_on',
  'checked_on', 'seen_on', 'calibrates', 'targets']);

export const KnowledgeOverview = z.object({
  kinds: z.record(str, num),
  relations: z.record(str, num),
  /** passages from documents, from the process, one per production order kept, and one per thing that happened to a machine */
  docs: num, process: num, orders: num, events: num,
  /** documents in the library, and how many are in the knowledge base */
  library: num, indexed: num,
}).meta({ description: 'GET /api/knowledge/{company} — members and above' });

export const Passage = z.object({
  /**
   * doc: a passage of a library document (doc, part locate it) · process: the company's process · order: a production order ·
   * check · anomaly · calibration · command: what happened to a machine
   */
  from: z.enum(['doc', 'process', 'order', 'check', 'anomaly', 'calibration', 'command']),
  title: str, doc: str.optional(), part: num, text: str, entities: z.array(str).optional(),
});
export const KnowledgeAnswer = z.object({
  passages: z.array(Passage),
  /** how it searched: lexical, or ppr (a walk over the graph from the things the question names) */
  mode: str,
  entities: z.array(str).optional(),
}).meta({ description: 'GET /api/knowledge/{company}/search?q=&limit=' });

/** title: what happened, in a line, for a check, an anomaly, a calibration or a command */
const Link = z.object({ type: str, out: z.boolean(), name: str, kind: str, title: str.optional() });
/** one line of what happened (a value on a form, a σ, a parameter, who decided); bad: out of spec, overdue, refused */
const Fact = z.object({ label: str, value: str, bad: z.boolean().optional() });
export const EntityAnswer = z.object({
  entity: z.object({ name: str, kind: EntityKind, links: z.array(Link), mentions: z.array(str), detail: z.array(Fact).optional() }).optional(),
  /** when nothing has exactly that name: things whose names contain it */
  candidates: z.array(Link),
}).meta({ description: 'GET /api/knowledge/{company}/entity?name=' });

export const KnowledgeGraph = z.object({
  nodes: z.array(z.object({ name: str, kind: EntityKind, mentions: num })),
  edges: z.array(z.object({ from: str, to: str, type: RelationType })),
}).meta({ description: 'GET /api/knowledge/{company}/graph — the process graph whole, for drawing' });

// ---------- production orders from the ERP (/api/erp/{company}/orders, /api/admin/erp/{company}) ----------

/** queued: waiting for its line · running · done · cancelled: withdrawn */
export const OrderStatus = z.enum(['queued', 'running', 'done', 'cancelled']);

/** A production order as the twin keeps it. */
export const Order = z.object({
  no: str, partNo: str.optional(), product: str.optional(), name: str.optional(), qty: num,
  due: time.optional(), customer: str.optional(), price: num.optional(),
  /** where it runs: plant and line ids, and their names */
  plant: str, plantName: str, line: str, lineName: str,
  /** why it cannot run (no line makes its part any more) */
  problem: str.optional(),
  status: OrderStatus, good: num, scrap: num,
  /** push: the ERP sent it · pull: read from the ERP · console: entered by hand */
  source: z.enum(['push', 'pull', 'console']),
  created: time, started: time.optional(), finished: time.optional(), updated: time,
});
export const Orders = z.array(Order).meta({ description: 'GET /api/erp/{company}/orders?status=&q=&limit= — running, then queued in the order they will run, then the rest; members and above' });

/**
 * An order as an ERP sends it (POST /api/erp/{company}/orders: one, a list, or {orders: [...]};
 * operators and above). Numbers may be strings; due is RFC 3339, "2026-10-20 17:00", "2026-10-20"
 * (Beijing time) or epoch seconds or milliseconds. Without plant/line it goes to a line that makes
 * its part number (or product). Sending a number again changes a queued order, a running one's
 * qty, due, customer and price, and queues a withdrawn one again.
 */
export const OrderInput = z.object({
  no: str, partNo: str.optional(), product: str.optional(), name: str.optional(), qty: z.union([num, str]),
  due: z.union([str, num]).optional(), customer: str.optional(), price: z.union([num, str]).optional(),
  plant: str.optional(), line: str.optional(),
});
export const OrderResults = z.array(z.object({
  no: str, ok: z.boolean(),
  outcome: z.enum(['created', 'updated', 'unchanged', 'requeued']).optional(),
  plant: str.optional(), line: str.optional(), lineName: str.optional(), error: str.optional(),
})).meta({ description: 'POST /api/erp/{company}/orders — each order answered on its own, in the order sent' });

/**
 * What the ERP's callback receives when a line starts or finishes an order: POST with
 * X-SF-Event and X-SF-Signature: sha256=hex(HMAC-SHA256(secret, body)).
 */
export const OrderCallback = z.object({
  event: z.enum(['order.started', 'order.done']), company: str, at: time, order: Order,
});

const ERPConfigBody = {
  enabled: z.boolean(),
  /** a line with no order from the ERP: idle (waits) or simulate (makes up its own) */
  whenEmpty: z.enum(['idle', 'simulate']),
  pull: z.object({
    url: str, method: z.enum(['GET', 'POST']).optional(), body: str.optional(),
    /** the console sees the names only; a value left empty keeps what it was */
    headers: z.record(str, str).optional(),
    /** where the list is in the answer, a dotted path ("data.rows"); empty for the answer itself */
    items: str.optional(),
    /** order field (no, partNo, product, name, qty, due, customer, price, plant, line) → dotted path in an item; a number indexes an array */
    fields: z.record(str, str),
    /** minutes between pulls; 0 or absent pulls only when asked */
    every: num.optional(),
  }).optional(),
  notify: z.object({ url: str }).optional(),
};
export const ERPConfig = z.object(ERPConfigBody).meta({ description: 'PUT /api/admin/erp/{company}; answers {ok, secret?} — the callback signing secret, shown once, when a callback is first set' });

export const ERPOverview = z.object({
  config: z.object({ ...ERPConfigBody, whenEmpty: z.enum(['idle', 'simulate']), secretSet: z.boolean() }),
  status: z.object({
    lastPull: time.optional(), pullError: str.optional(), pullFetched: num, pullProblems: z.array(str).optional(),
    lastNotify: time.optional(), notifyError: str.optional(), delivered: num, failed: num,
  }),
  counts: z.record(OrderStatus, num),
  /** the order fields a pull can map */
  fields: z.array(str),
}).meta({ description: 'GET /api/admin/erp/{company} — the connection, how it last went, orders by status' });

export const PullResult = z.object({
  fetched: num,
  items: z.array(z.object({ order: z.record(str, z.unknown()), lineName: str.optional(), outcome: str.optional(), error: str.optional() })),
  created: num, updated: num, failed: num,
}).meta({ description: 'POST /api/admin/erp/{company}/pull?dry=1 — what a pull reads and where each order would run (dry: nothing stored)' });

// ---------- calibration (/api/admin/calib/{company}, /api/calib/{company}/residuals) ----------

/** A machine's simulation parameter: the template's value, the one fitted from its readings, the one in force. */
export const CalibParam = z.object({
  /** idleRise · runRise · tau · pIdle · pRun (what running adds at full speed) · vib · cycle · mtbf · repairMin · repairMax */
  name: str, label: str, unit: str, template: num,
  /** absent when the readings do not allow it: note says why */
  fitted: num.optional(),
  applied: num.optional(),
  /** readings (or events) it was fitted from */
  n: num,
  quality: z.enum(['good', 'fair', 'poor']).optional(),
  note: str.optional(),
});
/** The fitted period played back: the model's mean absolute error with the template's parameters and with the fitted ones. */
export const CalibReplay = z.object({ field: z.enum(['temp', 'power', 'vib']), before: num, after: num, n: num });
/** How far a machine's model was from what it measured (model − measured): mean, mean absolute, share of the time within tolerance. */
export const ResidualSummary = z.object({ field: str, seconds: num, mean: num, mae: num, within: num });

export const CalibMachine = z.object({
  plant: str, plantName: str, line: str, asset: str, name: str, label: str,
  /** reporting now · has readings stored to fit from */
  live: z.boolean(), stored: z.boolean(),
  params: z.array(CalibParam),
  replay: z.array(CalibReplay),
  fittedAt: time.optional(), from: time.optional(), to: time.optional(),
  appliedAt: time.optional(), appliedBy: str.optional(),
  /** over the last 24 hours */
  residual: z.array(ResidualSummary),
});
export const Calibration = z.array(CalibMachine).meta({ description: 'GET /api/admin/calib/{company}; POST …/fit answers the machines it fitted — administrators' });
/** POST /api/admin/calib/{company}/fit | apply | revert: the machines ("plant/asset" or the asset id; none: all), and for a fit the hours of readings (default 24) */
export const CalibRequest = z.object({ machines: z.array(str).optional(), hours: num.optional() });
export const CalibOutcomes = z.array(z.object({ machine: str, ok: z.boolean(), params: num.optional(), error: str.optional() }))
  .meta({ description: 'POST /api/admin/calib/{company}/apply | revert — each machine answered on its own' });
export const Residuals = z.array(z.object({ t: num, seconds: num, mean: num, mae: num, within: num }))
  .meta({ description: 'GET /api/calib/{company}/residuals?machine=&field=temp|power|vib&from=&to=&step= — by step seconds (a minute at least); members and above' });
// ---------- the command guard (/api/guard/{company}) ----------

/** advisory: AI proposes, a person confirms every command to a machine · guarded: AI's commands pass the rules, the listed kinds wait for a person · autonomous: the rules alone */
export const GuardLevel = z.enum(['advisory', 'guarded', 'autonomous']);
/**
 * Refuses a command when its conditions hold for the machine now. when: conditions joined by
 * "and", each a field (state temp warn trip speed health alarms arg maint changing), an operator
 * (= != > >= < <=) and a number, word or field. say: the reason, with {temp} {warn} {trip} {speed}
 * {arg} {state} {alarms} filled in.
 */
export const GuardRule = z.object({ cmd: z.union([Command, z.literal('*')]), when: str, say: str });
export const GuardSettings = z.object({
  level: GuardLevel,
  /** at guarded, the commands an AI sends that wait for a person */
  approve: z.array(Command),
  rules: z.array(GuardRule),
  /** minutes a command waits for a person before it lapses */
  expire: num,
}).meta({ description: 'PUT /api/guard/{company} (administrators); answers GuardOverview' });
export const GuardOverview = z.object({
  settings: GuardSettings, defaults: GuardSettings, commands: z.array(Command),
  /** the company's machines (plant/asset) fed by real equipment: their commands are judged */
  machines: z.array(str),
  /** how many commands wait for a person */
  waiting: num,
  /** the caller may change the settings */
  admin: z.boolean(),
}).meta({ description: 'GET /api/guard/{company} — operators and above' });
/** A command an AI sent that waits for a person, and what became of it. */
export const CommandRequest = z.object({
  id: str, company: str, plant: str, asset: str, cmd: Command, arg: num.optional(),
  /** who sent it, and how (mcp, assistant) */
  who: str, via: str,
  /** why it waits, and the machine's state when it was sent */
  reason: str, context: str,
  created: time, expires: time,
  status: z.enum(['waiting', 'deciding', 'sent', 'refused', 'denied', 'expired', 'failed']),
  decidedBy: str.optional(), decidedAt: time.optional(),
  /** the twin's answer, or why it was refused, denied or lapsed */
  result: str.optional(),
}).meta({ description: 'POST /api/guard/{company}/requests/{id}/approve | deny {reason?} — operators and above; MCP command_request' });
export const CommandRequests = z.array(CommandRequest).meta({ description: 'GET /api/guard/{company}/requests?status= — waiting first, then the newest' });

// ---------- devices on the embedded broker (/api/admin/devices) ----------

/**
 * A device with an identity of its own on the embedded broker: a token (its MQTT password, user
 * name and client id being its name) or a client certificate from the server's CA (CN = its
 * name). It may publish readings of its own assets only and hear only their commands.
 */
export const BrokerDevice = z.object({
  id: str,
  /** MQTT client id and user name; the certificate's CN */
  name: str,
  company: str, plant: str,
  /** asset ids; "*" every asset of the plant, a trailing * a prefix ("RF-*") */
  assets: z.array(str),
  kind: z.enum(['token', 'cert']),
  created: time, createdBy: str,
  /** when its certificate lapses */
  expires: time.optional(),
  revoked: time.optional(), revokedBy: str.optional(),
  connected: z.boolean(),
  /** its last accepted connection or message */
  lastSeen: time.optional(),
  /** how often it was refused (a wrong credential, a topic not its own), and the last time */
  refused: num, lastRefusal: str.optional(), refusedAt: time.optional(),
});
export const BrokerDevices = z.object({
  devices: z.array(BrokerDevice),
  /** the broker's shared users (mqtt.users): whoever holds one may publish and subscribe to anything */
  shared: z.array(str),
  /** listen is empty when the server runs no broker; local: it takes connections from this machine only */
  broker: z.object({ listen: str, tls: str.optional(), prefix: str, local: z.boolean() }),
}).meta({ description: 'GET /api/admin/devices/{company} — administrators' });
export const RegisterDevice = z.object({
  name: str, plant: str, assets: z.array(str), kind: z.enum(['token', 'cert']).optional(),
}).meta({ description: 'POST /api/admin/devices/{company} — administrators; answers DeviceCredential' });
export const DeviceCredential = z.object({
  device: BrokerDevice,
  /** token devices: the token, shown this once */
  token: str.optional(),
  /** certificate devices: its certificate and private key (PEM), the CA's certificate, and the three as a zip (base64) — shown this once */
  cert: str.optional(), key: str.optional(), ca: str.optional(), bundle: str.optional(),
}).meta({ description: 'POST /api/admin/devices/{company} and /{id}/renew (a new credential; the old one stops working); DELETE /{id} revokes and answers BrokerDevice' });
// ---------- forms filled in by hand (/api/checks/{company}) ----------

export const CheckFieldKind = z.enum(['number', 'choice', 'yesno', 'text', 'photo']);
/**
 * One thing a form asks. key: a letter, then letters, digits and _. A number is stored as
 * readings (source manual) under its key, or under twin — the machine's own field it stands for,
 * stored with the machine's readings of it; a measure also reaches the twin's quality chart and,
 * without a spec of its own, is judged by the machine's.
 */
export const CheckField = z.object({
  key: str, label: str, kind: CheckFieldKind, unit: str.optional(),
  /** a number's spec */
  lo: num.optional(), hi: num.optional(), target: num.optional(),
  /** a choice's answers; a yes/no question's are 是 and 否 */
  options: z.array(str).optional(),
  /** the abnormal answers (for a choice, 异常 and 不合格 unless given) */
  bad: z.array(str).optional(),
  twin: z.enum(['measure', 'temp', 'power', 'vib']).optional(),
  required: z.boolean().optional(),
});
/** When a round is due on each machine: every shift (at: the shifts' starts, default 08:00 and 20:00), every day (at: its start, default 08:00) or every so many hours from the day's first (at, default 00:00); within: minutes from its start a round must be done in (0: before the next starts). No every: filled in when needed. */
export const CheckSchedule = z.object({
  every: z.enum(['', 'shift', 'day', 'hours']).optional(),
  hours: num.optional(), at: z.array(str).optional(), within: num.optional(),
});
/** patrol 点检 · first 首件检验 · lab 化验 · other */
export const CheckKind = z.enum(['patrol', 'first', 'lab', 'other']);
export const CheckForm = z.object({
  id: str, name: str, kind: CheckKind,
  /** the plant it is for (none: every plant); lines and assets narrow it to those of the plant */
  plant: str.optional(), lines: z.array(str).optional(), assets: z.array(str).optional(),
  fields: z.array(CheckField), schedule: CheckSchedule,
  /** what the person filling it in reads first */
  note: str.optional(), by: str.optional(), created: time, updated: time,
}).meta({ description: 'POST /api/checks/{company}/forms — administrators; without an id a new form, else it replaces that one; answers the form as saved (422 with every problem)' });
export const CheckForms = z.array(CheckForm).meta({ description: 'GET /api/checks/{company}/templates — forms to start one from' });
/** A form due on one machine. */
export const CheckRound = z.object({
  form: str, formName: str, kind: CheckKind, plant: str, plantName: str, line: str, lineName: str, asset: str, assetName: str,
  every: str,
  /** done: this round is in · due: still in time · overdue: past its time, or the last round was missed · open: no schedule */
  status: z.enum(['done', 'due', 'overdue', 'open']),
  /** this round's start, when it must be done by, when the next starts, since when it is overdue */
  start: time.optional(), due: time.optional(), next: time.optional(), since: time.optional(),
  /** the last time it was filled in, by whom, with how many abnormal values */
  last: time.optional(), lastBy: str.optional(), lastBad: num.optional(),
});
export const CheckRounds = z.array(CheckRound).meta({ description: 'GET /api/checks/{company}/rounds?status=overdue,due — overdue first (longest first), then due soonest; members and above' });
export const CheckMachine = z.object({
  plant: str, plantName: str, line: str, lineName: str, asset: str, name: str, label: str,
  /** the machine's quality measure and its spec */
  measure: z.object({ label: str, unit: str, target: num, lo: num.optional(), hi: num.optional(), digits: num }).optional(),
  rounds: z.array(CheckRound),
});
export const ChecksOverview = z.object({
  forms: z.array(CheckForm),
  /** every station some form is for, with its rounds */
  machines: z.array(CheckMachine),
  kinds: z.record(str, str), twinFields: z.record(str, str),
  /** the caller's name, whether they may fill forms in, set them up */
  who: str, operator: z.boolean(), admin: z.boolean(), now: time,
}).meta({ description: 'GET /api/checks/{company} — members and above' });
/** One answer as it was kept and judged. */
export const CheckValue = z.object({
  key: str, label: str, kind: CheckFieldKind,
  num: num.optional(),
  /** an answer, a note, or a photo's id (GET /api/checks/{company}/photos/{id}) */
  text: str.optional(), unit: str.optional(),
  /** the spec the number was judged by */
  lo: num.optional(), hi: num.optional(),
  /** out of spec or abnormal, and how */
  bad: z.boolean().optional(), why: str.optional(),
  /** the readings field a number went to, and why the store keeps it but does not believe it */
  field: str.optional(), flag: str.optional(),
});
export const CheckSubmission = z.object({
  id: str, form: str, formName: str, kind: CheckKind, plant: str, plantName: str, asset: str, assetName: str,
  /** when the values were taken, and when they arrived */
  taken: time, received: time,
  /** who filled it in, and from where (page, cli …) */
  by: str, via: str,
  values: z.array(CheckValue), note: str.optional(),
  /** how many values were out of spec or abnormal */
  bad: num,
  /** sent well after it was taken (a phone that was offline): stored at its time, kept from the twin */
  late: z.boolean().optional(),
  /** sent before; nothing was done again */
  duplicate: z.boolean().optional(),
}).meta({ description: 'POST /api/checks/{company}/submissions answers the submission kept — operators and above' });
export const CheckSubmissions = z.array(CheckSubmission).meta({ description: 'GET /api/checks/{company}/submissions?form=&plant=&asset=&from=&to=&bad=1&limit= — the latest taken first; format=csv: a spreadsheet, one value a row' });
export const CheckInput = z.object({
  /** the phone's own id for it: sent twice, kept once */
  id: str.optional(), form: str, plant: str, asset: str,
  /** when the values were taken (default: when it arrives); up to 30 days back */
  taken: time.optional(),
  /** by field key: a number, an answer, a text, or a photo as a data URL (JPEG, PNG or WebP, up to 2 MB) */
  values: z.record(str, z.union([num, str, z.boolean()])),
  note: str.optional(),
}).meta({ description: 'POST /api/checks/{company}/submissions' });
export const MachineChecks = z.object({
  machine: CheckMachine, forms: z.array(CheckForm),
  /** the last time each form was filled in on it, the latest first */
  last: z.array(CheckSubmission),
  operator: z.boolean(),
}).meta({ description: 'GET /api/checks/{company}/machines/{plant}/{asset} — members and above; 404 when no form is for it' });

// ---------- a restaurant's tables (/api/report/{company}/…, /api/tables/{company}) ----------

export const TableState = z.enum(['free', 'occupied', 'reserved', 'cleaning']).meta({ description: 'free 空闲 · occupied 就餐中 · reserved 已预订 · cleaning 清台中' });
/** ms since the epoch, or RFC 3339 */
const when = z.union([num, str]);
export const TableReport = z.object({
  table: str,
  /** the plant (shop); may be left out when only one plant has a table of that id */
  plant: str.optional(),
  state: TableState,
  /** people at an occupied table (default 2, or the seats of a smaller table) */
  people: num.optional(),
  /** when the table got into this state (default: now); a time more than 5 minutes ahead is taken as now */
  since: when.optional(),
  /** what saw it: a camera box, a sensor, a waiter (default: the token holder) */
  source: str.optional(),
});
export const TableReports = z.union([TableReport, z.array(TableReport), z.object({ reports: z.array(TableReport) })]).meta({
  description: 'POST /api/report/{company}/tables — one, a list, or {reports}; the same report twice is kept once; one older than what the table last reported is kept (its sessions are rebuilt) but the twin keeps the newer. A reporter token of the company, operators and above',
});
export const TableEvent = z.object({
  table: str, plant: str.optional(),
  event: z.enum(['seated', 'ordered', 'paid', 'left']),
  people: num.optional(),
  at: when.optional(),
  source: str.optional(),
});
export const TableEvents = z.union([TableEvent, z.array(TableEvent), z.object({ events: z.array(TableEvent) })]).meta({
  description: 'POST /api/report/{company}/sessions — a POS\'s session events: seated opens a session, paid / left close it (the table is then to clear), ordered is kept',
});
export const TableReportResult = z.object({
  accepted: num, late: num, duplicate: num, unknown: num, invalid: num,
  items: z.array(z.object({ table: str, plant: str.optional(), status: z.enum(['accepted', 'late', 'duplicate', 'unknown', 'invalid']), error: str.optional() })),
}).meta({ description: 'What became of each report or event, in the order sent' });
export const TableNow = z.object({
  plant: str, plantName: str, line: str, lineName: str, id: str, area: str, seats: num,
  state: TableState, people: num,
  /** when the state began (ms) */
  since: num,
  /** sim: the twin\'s own; else what reported it */
  source: str,
  /** reports govern the table (reported within the last 3 hours) */
  reported: z.boolean(),
  lastSource: str.optional(), lastAt: num.optional(),
  /** minutes in this state */
  minutes: num,
  /** sessions at it today */
  sessions: num,
});
export const TableKpis = z.object({
  plant: str.optional(), name: str.optional(),
  /** the business day (from 05:00) */
  day: str,
  tables: num, seats: num, occupied: num, reserved: num, cleaning: num, free: num, people: num,
  /** 上座率: occupied tables / tables, now */
  occupancy: num,
  /** 座位利用率: people / seats, now */
  seatUse: num,
  sessions: num,
  /** 翻台率: sessions per table today */
  turnover: num,
  /** 平均用餐时长 of the sessions that ended today */
  avgMinutes: num.nullable(),
  guests: num, waiting: num,
  source: z.enum(['measured', 'simulated', 'mixed']), measuredShare: num, reportedSessions: num,
});
export const TablesOverview = z.object({
  tables: z.array(TableNow), total: TableKpis, plants: z.array(TableKpis), now: num,
}).meta({ description: 'GET /api/tables/{company} — every table now and today\'s figures per plant and in all; anyone' });
export const TableSession = z.object({ plant: str, table: str, start: num, end: num.nullable(), people: num, source: str });
export const TableDay = z.object({
  day: str, sessions: z.array(TableSession),
  marks: z.array(z.object({ at: num, kind: str, people: num, source: str, received: num })),
}).meta({ description: 'GET /api/tables/{company}/{plant}/{table}/sessions?day=YYYY-MM-DD — that table\'s sessions on the business day, with its reports and events' });
export const TableCurrent = z.object({
  table: TableNow, open: TableSession.nullable(), minutes: num.nullable(), today: z.array(TableSession),
}).meta({ description: 'GET /api/tables/{company}/{plant}/{table} — the table now and its session in progress' });
export const TableHistory = z.array(z.object({
  plant: str, start: num, label: str, sessions: num, guests: num, avgMinutes: num.nullable(), occupiedHours: num, reported: num,
})).meta({ description: 'GET /api/tables/{company}/history?from=&to=&by=hour|day — sessions per plant by hour or business day' });

// ---------- the value report (/api/value/{company}) ----------

const DataSource = z.enum(['measured', 'simulated']);
/** One number of the report. value null: there was nothing to count it from (the page says 没有数据). */
export const ValueFigure = z.object({
  key: str, label: str, value: num.nullable(),
  /** % (value is a fraction), h, min, or what is counted (条, 次, 台, 张, 件) */
  unit: str.optional(),
  /** how many records it was counted from: the count itself, a rate's denominator, a plant figure's line-hours */
  n: num,
  /** what it was counted from */
  basis: str,
  /** plant figures: from equipment, or the twin's simulation (or hours partly measured) */
  source: DataSource.optional(),
});
const sectionFields = {
  figures: z.array(ValueFigure),
  /** why the part has no data */
  missing: str.optional(),
  /** what to keep in mind reading it */
  note: str.optional(),
};
export const ValueSection = z.object(sectionFields);
export const PlanResult = z.object({
  id: str, does: str, metric: str, label: str, want: str, adopted: time,
  baseline: num.optional(), measured: num.optional(), target: num.optional(),
  /** (measured − baseline) / |baseline| */
  change: num.optional(),
  /** it moved the way the plan wanted */
  better: z.boolean().optional(),
  dataDays: num,
  outcome: z.enum(['achieved', 'missed', 'guard_broken', 'no_data']).optional(),
  accepted: z.boolean(),
  unit: str.optional(),
});
export const FieldReplay = z.object({ field: str, label: str, unit: str, before: num, after: num, machines: num, n: num });
export const ValueWeek = z.object({
  /** Monday */
  start: time, days: num,
  oee: num.nullable(), output: num.nullable(), downtime: num.nullable(),
  /** output per line per day, downtime hours per machine per day, over the hours with records */
  outputPerDay: num.nullable(), downtimePerDay: num.nullable(),
  lineHours: num,
  /** share of the week's line-hours measured from equipment */
  measured: num.nullable(),
});
const ValueSide = z.object({ from: time, to: time, oee: num.nullable(), outputPerDay: num.nullable(), downtimePerDay: num.nullable(), days: num, lineHours: num });
export const ValueCompare = z.object({
  plan: str, does: str, adopted: time, days: num, source: DataSource.optional(), before: ValueSide, after: ValueSide,
  /** correlation, not proof */
  note: str,
});
export const ValueReport = z.object({
  company: str, name: str,
  period: z.object({ from: time, to: time, days: num, label: str }),
  at: time,
  suggestions: z.object({ ...sectionFields, plans: z.array(PlanResult) }),
  ai: ValueSection,
  calibration: z.object({ ...sectionFields, fields: z.array(FieldReplay) }),
  orders: ValueSection,
  plant: z.object({ ...sectionFields, source: DataSource.optional(), weeks: z.array(ValueWeek), compare: ValueCompare.optional() }),
}).meta({ description: 'GET /api/value/{company}?days=30 | ?from=YYYY-MM-DD&to=YYYY-MM-DD (&format=csv for the export) — members and above; MCP value_report' });
// ---------- readings unusual for their machine (/api/anomalies/{company}) ----------

/** A field's normal in one state; warm: learned long enough to be judged, progress (0–1) until then. */
export const AnomalyBaseline = z.object({ state: str, n: num, mean: num, sigma: num, warm: z.boolean(), progress: num });
export const AnomalyField = z.object({
  field: str, value: num, at: time,
  /** the last reading's distance, in σ, from the normal it was held against: now, that of the state the machine was in (absent until a reading in a state) */
  z: num, now: AnomalyBaseline.optional(),
  /** every state's normal, most readings first */
  states: z.array(AnomalyBaseline),
  anomaly: Anomaly.optional(),
});
export const AnomalyMachine = z.object({ plant: str, asset: str, state: str, source: str, fields: z.array(AnomalyField) })
  .meta({ description: 'GET /api/anomalies/{company}/{plant}/{asset} — a measured machine\'s fields against their normals; anyone watching the twin' });
export const AnomalyReport = z.object({
  current: z.array(Anomaly),
  /** the last 30 days', newest first, ended and current */
  recent: z.array(Anomaly),
  machines: z.array(AnomalyMachine),
  /**
   * k σ for sustain seconds, or the ten-minute average driftK σ off for driftSustain seconds, is a
   * hint (a slow field waits longer); it ends once back for clear seconds, and the same kind then
   * waits cooldown seconds; a field is learned for warmup seconds in a state first; the normal then
   * follows span readings
   */
  settings: z.object({ k: num, driftK: num, sustain: num, driftSustain: num, clear: num, cooldown: num, warmup: num, span: num }),
  /** hints open now and begun in the last hour; past maxOpen or maxPerHour new ones wait (paused), heldBack counts them */
  valve: z.object({ open: num, lastHour: num, maxOpen: num, maxPerHour: num, paused: z.boolean(), heldBack: num }),
  /** the caller may reset a machine's normal */
  admin: z.boolean(),
}).meta({ description: 'GET /api/anomalies/{company} — members and above; MCP list_anomalies' });
/** POST /api/admin/anomalies/{company}/reset: the machines ("plant/asset" or the asset id) to learn again, after maintenance */
export const AnomalyReset = z.object({ machines: z.array(str) });
export const AnomalyResetResult = z.array(z.object({ machine: str, ok: z.boolean(), error: str.optional() }))
  .meta({ description: 'POST /api/admin/anomalies/{company}/reset — administrators; each machine answered on its own' });

// ---------- the console agent (/api/admin/agent) ----------

/** A change the console agent proposes; a person confirms it (POST /api/admin/actions) and it runs as them, or turns it down (POST /api/admin/actions/dismiss). */
export const ConsoleAction = z.object({
  /** the proposal (AP-…); the activity log keeps what became of it */
  id: str.optional(),
  kind: z.enum(['create_company', 'delete_company', 'create_industry', 'issue_token', 'revoke_token', 'rebuild_knowledge', 'cancel_order', 'apply_calibration', 'approve_command', 'deny_command', 'reset_baseline']),
  title: str, detail: str.optional(), args: z.record(str, z.unknown()),
});
export const ConsoleEvent = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('conversation'), id: str }),
  z.object({ kind: z.literal('thinking') }),
  z.object({ kind: z.literal('tool_call'), tool: str, args: z.record(str, z.unknown()).optional() }),
  z.object({ kind: z.literal('tool_result'), tool: str, result: z.unknown().optional() }),
  z.object({ kind: z.literal('delta'), text: str }),
  z.object({ kind: z.literal('reset') }),
  /** a change to confirm */
  z.object({ kind: z.literal('action'), result: ConsoleAction }),
  z.object({ kind: z.literal('complete'), text: str.optional() }),
  z.object({ kind: z.literal('error'), text: str }),
]).meta({ description: 'One server-sent event of the console agent: `data: <ConsoleEvent JSON>`' });

export const DEFS = {
  ClientMessage, ServerMessage, PlantState,
  ErrorBody, Health, Site, Directory, RuntimeCompany, Tags, Advisors, DeviceMap, SeriesAnswer,
  Metrics, Dimensions, Query, Answer, Board, DecideRequest, AskRequest, AskEvent, Reviews, ReviewRun,
  DeviceFields, DeviceCommand,
  Assistants, Assistant, AssistantRequest, Conversations, ConversationView, AssistantAsk, AssistantEvent,
  Docs, Doc, DocView, DocUpload, DocVersions, UploadResults, Notes, Note, Tickets, Ticket, TicketPatch, Done,
  AuditLog,
  AdminOverview, CompanyCreated, ConfigProblem, Accounts, Account, IssueToken, IssuedToken, ComposeRequest, ComposeEvent,
  KnowledgeOverview, KnowledgeAnswer, EntityAnswer, KnowledgeGraph, ConsoleAction, ConsoleEvent,
  Templates, TemplateSummary, Template, SaveTemplate,
  Order, Orders, OrderInput, OrderResults, OrderCallback, ERPConfig, ERPOverview, PullResult,
  CalibMachine, Calibration, CalibRequest, CalibOutcomes, Residuals,
  GuardSettings, GuardOverview, CommandRequest, CommandRequests,
  BrokerDevice, BrokerDevices, RegisterDevice, DeviceCredential,
  CheckForm, CheckForms, CheckRounds, ChecksOverview, CheckSubmission, CheckSubmissions, CheckInput, MachineChecks,
  ValueReport,
  Anomaly, Anomalies, AnomalyMachine, AnomalyReport, AnomalyReset, AnomalyResetResult,
  TableReports, TableEvents, TableReportResult, TablesOverview, TableDay, TableCurrent, TableHistory,
} satisfies Record<string, z.ZodType>;

export type ClientMessage = z.infer<typeof ClientMessage>;
export type Role = z.infer<typeof Role>;
export type ServerMessage = z.infer<typeof ServerMessage>;
export type PlantState = z.infer<typeof PlantState>;
export type ProductTuple = z.infer<typeof ProductTuple>;
export type SourceInfo = z.infer<typeof Source>;
export type AnomalyInfo = z.infer<typeof Anomaly>;
