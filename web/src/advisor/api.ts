// The company's advisor (DataIntelligence), reached through the twin's server at /api/advisor/{company}.
// Request and response shapes are the protocol's (src/protocol/sfp.ts).
import type { z } from 'zod';
import { operatorToken } from '@/live/client';
import type * as P from '@/protocol/sfp';

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

export type MetricInfo = z.infer<typeof P.MetricInfo>;
export type DimensionInfo = z.infer<typeof P.DimensionInfo>;

export const getMetrics = (c: string) => call<{ metrics: MetricInfo[] }>(`${base(c)}/metrics`);
export const getDimensions = (c: string) => call<{ dimensions: DimensionInfo[] }>(`${base(c)}/dimensions`);

export type Where = z.infer<typeof P.Where>;
export type Query = z.infer<typeof P.Query>;
export type Answer = z.infer<typeof P.Answer>;

export const query = (c: string, q: Query) => call<Answer>(`${base(c)}/query`, { method: 'POST', json: q });

// ---- the consulting record -----------------------------------------------------

export type Filter = P.Filter;
export type Guard = z.infer<typeof P.Guard>;
export type Measurement = z.infer<typeof P.Measurement>;
export type Goal = z.infer<typeof P.Goal>;
export type Evidence = z.infer<typeof P.Evidence>;
export type Finding = z.infer<typeof P.Finding>;
export type Action = z.infer<typeof P.Action>;
export type Plan = z.infer<typeof P.Plan>;
export type Decision = z.infer<typeof P.Decision>;
export type ActionRun = z.infer<typeof P.ActionRun>;
export type GuardResult = z.infer<typeof P.GuardResult>;
export type Acceptance = z.infer<typeof P.Acceptance>;
export type PlanView = z.infer<typeof P.PlanView>;
export type GoalView = z.infer<typeof P.GoalView>;
export type Board = z.infer<typeof P.Board>;

export const getBoard = (c: string) => call<Board>(`${base(c)}/consult`);

export type Verb = z.infer<typeof P.Verb>;

export function decide(c: string, plan: string, verb: Verb, why?: string) {
  const token = operatorToken();
  return call<Record<string, unknown>>(`${base(c)}/plans/${encodeURIComponent(plan)}/${verb}`, {
    method: 'POST',
    json: why ? { why } : {},
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
}

// ---- the advisor agent ---------------------------------------------------------

export type AskEvent = z.infer<typeof P.AskEvent>;

// ---- standing reviews (the group's diagnosis) ---------------------------------------

export type Review = z.infer<typeof P.Review>;
export type ReviewStep = z.infer<typeof P.ReviewStep>;
export type ReviewRun = z.infer<typeof P.ReviewRun>;
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
