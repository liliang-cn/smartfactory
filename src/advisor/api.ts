// The company's advisor (DataIntelligence), reached through the twin's server at /api/advisor/{company}.
import { operatorToken } from '@/live/client';

export class AdvisorError extends Error {
  constructor(public status: number, message: string, public rule?: string) {
    super(message);
  }
}

async function call<T>(path: string, init?: RequestInit & { json?: unknown }): Promise<T> {
  const { json, ...rest } = init ?? {};
  const res = await fetch(path, {
    ...rest,
    headers: { ...(json !== undefined ? { 'Content-Type': 'application/json' } : {}), ...rest.headers },
    body: json !== undefined ? JSON.stringify(json) : rest.body,
  });
  const text = await res.text();
  let body: unknown = null;
  try { body = text ? JSON.parse(text) : null; } catch { body = null; }
  if (!res.ok) {
    const b = body as { error?: string; rule?: string } | null;
    throw new AdvisorError(res.status, b?.error ?? (res.status >= 500 ? '顾问服务暂不可用' : `请求失败（${res.status}）`), b?.rule);
  }
  return body as T;
}

const base = (company: string) => `/api/advisor/${encodeURIComponent(company)}`;

// ---- the semantic model ------------------------------------------------------

export type MetricInfo = { name: string; description: string; synonyms?: string[]; additivity?: string };
export type DimensionInfo = { name: string; type?: string; synonyms?: string[] };

export const getMetrics = (c: string) => call<{ metrics: MetricInfo[] }>(`${base(c)}/metrics`);
export const getDimensions = (c: string) => call<{ dimensions: DimensionInfo[] }>(`${base(c)}/dimensions`);

export type Where = { dimension: string; op: string; values: unknown[] };
export type Query = { metrics: string[]; group_by?: string[]; where?: Where[]; grain?: 'day' | 'week' | 'month'; limit?: number };
export type Answer = { columns: string[]; rows: unknown[][]; sql: string };

export const query = (c: string, q: Query) => call<Answer>(`${base(c)}/query`, { method: 'POST', json: q });

// ---- the consulting record -----------------------------------------------------

export type Filter = { dimension?: string; metric?: string; op: string; values?: unknown[]; and?: Filter[]; or?: Filter[] };
export type Guard = { metric: string; worse_is: 'up' | 'down'; tolerance?: number | null; limit?: number | null };
export type Measurement = { metric: string; scope?: Filter[]; from: string; to: string; value: number | null; data_days: number; sql: string; at: string };
export type Goal = {
  id: string; what: string; metric: string; direction: 'up' | 'down'; by: number; within_days: number;
  scope?: Filter[]; guards?: Guard[]; baseline?: Measurement; author: string; via?: string; at: string;
};
export type Evidence = { asked: string; sql: string; columns: string[]; rows: unknown[][]; row_count: number; truncated?: boolean; at: string };
export type Finding = { id: string; goal?: string; says: string; evidence: Evidence[]; by: string; via?: string; at: string };
export type Action = { server: string; tool: string; args?: Record<string, unknown>; why?: string };
export type Plan = {
  id: string; goal?: string; findings: string[]; does: string;
  expect: { metric: string; direction: 'up' | 'down'; by: number; within_days: number; scope?: Filter[] };
  guards?: Guard[]; actions?: Action[]; cost?: string; by: string; via?: string; at: string; note?: string;
};
export type Decision = {
  plan: string; verdict: 'adopted' | 'rejected'; who: string; role?: string; why?: string; at: string;
  baseline?: Measurement; guard_baselines?: Measurement[];
};
export type ActionRun = { plan: string; index: number; server: string; tool: string; result?: string; error?: string; skipped?: boolean; ms: number; at: string };
export type GuardResult = { guard: Guard; baseline: number | null; measured: Measurement | null; broken: boolean };
export type Acceptance = {
  plan: string; final: boolean; metric: string; baseline: number | null; target: number | null; measured: Measurement | null;
  guards?: GuardResult[]; outcome: 'achieved' | 'missed' | 'guard_broken' | 'no_data'; at: string;
};
export type PlanView = {
  plan: Plan; state: 'proposed' | 'adopted' | 'rejected' | 'accepted'; decision?: Decision; due?: string;
  actions?: ActionRun[]; progress?: Acceptance; acceptance?: Acceptance;
};
export type GoalView = { goal: Goal; target?: number; findings: Finding[]; plans: PlanView[] };
export type Board = { goals: GoalView[]; loose_findings?: Finding[]; loose_plans?: PlanView[]; at: string };

export const getBoard = (c: string) => call<Board>(`${base(c)}/consult`);

export type Verb = 'adopt' | 'reject' | 'measure' | 'accept';

export function decide(c: string, plan: string, verb: Verb, why?: string) {
  const token = operatorToken();
  return call<Record<string, unknown>>(`${base(c)}/plans/${encodeURIComponent(plan)}/${verb}`, {
    method: 'POST',
    json: why ? { why } : {},
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
}

// ---- the advisor agent ---------------------------------------------------------

export type AskEvent =
  | { kind: 'thinking'; text?: string }
  | { kind: 'tool_call'; tool: string; args?: Record<string, unknown> }
  | { kind: 'tool_result'; tool: string; result?: unknown }
  | { kind: 'verify'; text?: string }
  | { kind: 'verified'; text?: string }
  | { kind: 'verify_skipped'; text?: string }
  | { kind: 'complete'; text?: string }
  | { kind: 'error'; text?: string };

// ---- standing reviews (the group's diagnosis) ---------------------------------------

export type Review = { name: string; title: string; schedule?: string; can_run: boolean; running: boolean };
export type ReviewStep = { tool: string; args?: Record<string, unknown>; result?: unknown };
export type ReviewRun = {
  id: number; name: string; title: string; by: string; trigger: 'schedule' | 'manual'; started_at: string; finished_at: string;
  answer: string; status: 'running' | 'done' | 'failed'; steps?: ReviewStep[]; corrected?: string;
};
export const getReviews = (c: string) => call<{ reviews: Review[]; runs: ReviewRun[] }>(`${base(c)}/reviews`);
export const getReviewRun = (c: string, id: number) => call<ReviewRun>(`${base(c)}/reviews/runs/${id}`);

/** One run of the agent as server-sent events over a POST: a question, or a standing review. */
export async function ask(c: string, question: string, signal: AbortSignal, on: (ev: AskEvent) => void, review?: string) {
  const token = review ? operatorToken() : null;
  const res = await fetch(review ? `${base(c)}/reviews/${encodeURIComponent(review)}/run` : `${base(c)}/ask`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify({ question }),
    signal,
  });
  if (!res.ok || !res.body) {
    let msg = `请求失败（${res.status}）`;
    try { msg = (await res.json()).error ?? msg; } catch { /* not JSON */ }
    throw new AdvisorError(res.status, msg);
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  const flush = (chunk: string) => {
    const data = chunk.split('\n').filter((l) => l.startsWith('data:')).map((l) => l.slice(5).trim()).join('\n');
    if (!data) return;
    try { on(JSON.parse(data) as AskEvent); } catch { /* partial or foreign line */ }
  };
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true }).replace(/\r\n/g, '\n');
    let i: number;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      flush(buf.slice(0, i));
      buf = buf.slice(i + 2);
    }
  }
  if (buf.trim()) flush(buf);
}
