// The console's calls to /api/admin. Shapes are the protocol's (src/protocol/sfp.ts).
import type { z } from 'zod';
import type * as P from '@/protocol/sfp';
import type { CompanyCfg, IndustryCfg } from '@/config/schema';

export type Overview = z.infer<typeof P.AdminOverview>;
export type Me = z.infer<typeof P.AdminMe>;
export type Company = z.infer<typeof P.AdminCompany>;
export type IndustrySummary = z.infer<typeof P.IndustrySummary>;
export type Account = z.infer<typeof P.Account>;
export type IssueToken = z.infer<typeof P.IssueToken>;
export type IssuedToken = z.infer<typeof P.IssuedToken>;
export type CompanyCreated = z.infer<typeof P.CompanyCreated>;
export type AuditEntry = z.infer<typeof P.AuditEntry>;
export type Role = z.infer<typeof P.Role>;
export type ComposeEvent = z.infer<typeof P.ComposeEvent>;
export type ConsoleEvent = z.infer<typeof P.ConsoleEvent>;
export type ConsoleAction = z.infer<typeof P.ConsoleAction>;
export type KnowledgeOverview = z.infer<typeof P.KnowledgeOverview>;
export type KnowledgeAnswer = z.infer<typeof P.KnowledgeAnswer>;
export type Passage = z.infer<typeof P.Passage>;
export type EntityAnswer = z.infer<typeof P.EntityAnswer>;
export type KnowledgeGraph = z.infer<typeof P.KnowledgeGraph>;
export type EntityKind = z.infer<typeof P.EntityKind>;

export class ApiError extends Error {
  constructor(public status: number, message: string, public problems: string[] = []) {
    super(message);
  }
}

const KEY = 'sf-token';

/** The token this tab signed in with: ?token= once, then kept for the tab (as the twin does). */
export function token(): string | null {
  const params = new URLSearchParams(location.search);
  const t = params.get('token');
  if (t) {
    signIn(t);
    params.delete('token');
    history.replaceState(null, '', `${location.pathname}${params.size ? `?${params}` : ''}`);
    return t;
  }
  try { return sessionStorage.getItem(KEY); } catch { return null; }
}
export function signIn(t: string) {
  try { sessionStorage.setItem(KEY, t.trim()); } catch { /* storage blocked */ }
}
export function signOut() {
  try { sessionStorage.removeItem(KEY); } catch { /* storage blocked */ }
}

async function call<T>(method: string, path: string, json?: unknown): Promise<T> {
  const t = token();
  const res = await fetch(path, {
    method,
    headers: { ...(json !== undefined && { 'Content-Type': 'application/json' }), ...(t && { Authorization: `Bearer ${t}` }) },
    body: json !== undefined ? JSON.stringify(json) : undefined,
  });
  const text = await res.text();
  let body: unknown = null;
  try { body = text ? JSON.parse(text) : null; } catch { body = null; }
  if (!res.ok) {
    const b = body as { error?: string; errors?: string[] } | null;
    throw new ApiError(res.status, b?.error ?? `请求失败（${res.status}）`, b?.errors ?? []);
  }
  return body as T;
}

export const overview = () => call<Overview>('GET', '/api/admin');
export const createCompany = (cfg: CompanyCfg | unknown) => call<CompanyCreated>('POST', '/api/admin/companies', cfg);
export const deleteCompany = (id: string) => call<{ ok: true }>('DELETE', `/api/admin/companies/${encodeURIComponent(id)}`);
export const industry = (id: string) => call<IndustryCfg>('GET', `/api/admin/industries/${encodeURIComponent(id)}`);
export const createIndustry = (cfg: unknown) => call<unknown>('POST', '/api/admin/industries', cfg);
export const accounts = () => call<Account[]>('GET', '/api/admin/tokens');
export const issue = (t: IssueToken) => call<IssuedToken>('POST', '/api/admin/tokens', t);
export const revoke = (key: string) => call<{ ok: true }>('DELETE', `/api/admin/tokens/${encodeURIComponent(key)}`);
export const activity = (company: string, before?: number) => {
  const q = new URLSearchParams({ limit: '100' });
  if (company) q.set('company', company);
  if (before) q.set('before', String(before));
  return call<AuditEntry[]>('GET', `/api/admin/audit?${q}`);
};

/** The scene architect: brief in, events out (server-sent), until it ends or signal aborts. */
export const compose = (brief: string, base: unknown, signal: AbortSignal, on: (ev: ComposeEvent) => void) =>
  stream('/api/admin/compose', { brief, ...(base ? { base } : {}) }, signal, on);

/** The console agent: one turn of a conversation; changes come back as actions to confirm. */
export const agent = (conversation: string | null, text: string, signal: AbortSignal, on: (ev: ConsoleEvent) => void) =>
  stream('/api/admin/agent', { conversation: conversation ?? '', text }, signal, on);

/** Carry out an action (one the console agent proposed), as the signed-in person. */
export const doAction = (a: ConsoleAction) => call<unknown>('POST', '/api/admin/actions', a);

const kbPath = (c: string) => `/api/knowledge/${encodeURIComponent(c)}`;
export const knowledge = (c: string) => call<KnowledgeOverview>('GET', kbPath(c));
export const knowledgeSearch = (c: string, q: string) => call<KnowledgeAnswer>('GET', `${kbPath(c)}/search?q=${encodeURIComponent(q)}&limit=8`);
export const knowledgeEntity = (c: string, name: string) => call<EntityAnswer>('GET', `${kbPath(c)}/entity?name=${encodeURIComponent(name)}`);
export const knowledgeGraph = (c: string) => call<KnowledgeGraph>('GET', `${kbPath(c)}/graph`);
export const rebuildKnowledge = (c: string) => call<{ docs: number }>('POST', `${kbPath(c)}/rebuild`);

async function stream<E>(path: string, body: unknown, signal: AbortSignal, on: (ev: E) => void) {
  const t = token();
  const res = await fetch(path, {
    method: 'POST', signal,
    headers: { 'Content-Type': 'application/json', ...(t && { Authorization: `Bearer ${t}` }) },
    body: JSON.stringify(body),
  });
  if (!res.ok || !res.body) {
    let msg = `请求失败（${res.status}）`;
    try { msg = (await res.json()).error ?? msg; } catch { /* not JSON */ }
    throw new ApiError(res.status, msg);
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  const flush = (chunk: string) => {
    const data = chunk.split('\n').filter((l) => l.startsWith('data:')).map((l) => l.slice(5).trim()).join('\n');
    if (data) try { on(JSON.parse(data) as E); } catch { /* partial */ }
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

/** A company's assistants and documents (the twin's own API), for the customer page. */
export const assistants = (c: string) => call<z.infer<typeof P.Assistants>>('GET', `/api/agents/${encodeURIComponent(c)}`);
export const docs = (c: string) => call<z.infer<typeof P.Docs>>('GET', `/api/library/${encodeURIComponent(c)}/docs`);
export const health = () => call<z.infer<typeof P.Health>>('GET', '/api/health');

export const ROLE_LABEL: Record<Role, string> = { visitor: '访客', member: '成员', operator: '操作员', admin: '管理员' };
export const ROLE_NOTE: Record<Exclude<Role, 'visitor'>, string> = {
  member: '看数据，让助手记笔记、登记工单',
  operator: '再加：下发设备指令、调全厂倍速、采纳经营建议',
  admin: '再加：搭场景、管资料和助手、发令牌',
};
