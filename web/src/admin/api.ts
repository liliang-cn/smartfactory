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
export type TemplateSummary = z.infer<typeof P.TemplateSummary>;
export type Template = z.infer<typeof P.Template>;
export type SaveTemplate = z.infer<typeof P.SaveTemplate>;
export type Order = z.infer<typeof P.Order>;
export type OrderInput = z.infer<typeof P.OrderInput>;
export type OrderResults = z.infer<typeof P.OrderResults>;
export type ERPConfig = z.infer<typeof P.ERPConfig>;
export type ERPOverview = z.infer<typeof P.ERPOverview>;
export type PullResult = z.infer<typeof P.PullResult>;
export type CalibMachine = z.infer<typeof P.CalibMachine>;
export type CalibOutcomes = z.infer<typeof P.CalibOutcomes>;
export type ResidualPoint = z.infer<typeof P.Residuals>[number];

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
    headers: { 'X-SF-Via': 'page', ...(json !== undefined && { 'Content-Type': 'application/json' }), ...(t && { Authorization: `Bearer ${t}` }) },
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
export const compose = (brief: string, base: unknown, signal: AbortSignal, on: (ev: ComposeEvent) => void, kind: 'company' | 'industry' = 'company') =>
  stream('/api/admin/compose', { kind, brief, ...(base ? { base } : {}) }, signal, on);

/** The console agent: one turn of a conversation; changes come back as actions to confirm. */
export const agent = (conversation: string | null, text: string, signal: AbortSignal, on: (ev: ConsoleEvent) => void) =>
  stream('/api/admin/agent', { conversation: conversation ?? '', text }, signal, on);

/** Carry out an action (one the console agent proposed), as the signed-in person. */
export const doAction = (a: ConsoleAction) => call<unknown>('POST', '/api/admin/actions', a);

export const templates = () => call<TemplateSummary[]>('GET', '/api/admin/templates');
export const template = (source: TemplateSummary['source'], id: string) => call<Template>('GET', `/api/admin/templates/${source}/${encodeURIComponent(id)}`);
export const saveTemplate = (t: SaveTemplate) => call<TemplateSummary>('POST', '/api/admin/templates', t);
export const deleteTemplate = (id: string) => call<{ ok: true }>('DELETE', `/api/admin/templates/${encodeURIComponent(id)}`);
export const importTemplate = (t: unknown) => call<TemplateSummary>('POST', '/api/admin/templates/import', t);

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
const erpPath = (c: string) => `/api/admin/erp/${encodeURIComponent(c)}`;
const ordersPath = (c: string) => `/api/erp/${encodeURIComponent(c)}/orders`;
export const erp = (c: string) => call<ERPOverview>('GET', erpPath(c));
export const setERP = (c: string, cfg: ERPConfig) => call<{ ok: true; secret?: string }>('PUT', erpPath(c), cfg);
export const pullERP = (c: string, dry: boolean) => call<PullResult>('POST', `${erpPath(c)}/pull${dry ? '?dry=1' : ''}`);
export const rotateERPSecret = (c: string) => call<{ secret: string }>('POST', `${erpPath(c)}/secret`);
export const orders = (c: string, status: string[] = [], q = '') =>
  call<Order[]>('GET', `${ordersPath(c)}?status=${status.join(',')}&q=${encodeURIComponent(q)}`);
export const order = (c: string, no: string) => call<Order>('GET', `${ordersPath(c)}/${encodeURIComponent(no)}`);
export const sendOrders = (c: string, list: OrderInput[]) => call<OrderResults>('POST', ordersPath(c), list);
export const cancelOrder = (c: string, no: string) => call<Order>('DELETE', `${ordersPath(c)}/${encodeURIComponent(no)}`);

const calibPath = (c: string) => `/api/admin/calib/${encodeURIComponent(c)}`;
export const calibration = (c: string) => call<CalibMachine[]>('GET', calibPath(c));
export const fitCalibration = (c: string, machines: string[], hours: number) => call<CalibMachine[]>('POST', `${calibPath(c)}/fit`, { machines, hours });
export const applyCalibration = (c: string, machines: string[]) => call<CalibOutcomes>('POST', `${calibPath(c)}/apply`, { machines });
export const revertCalibration = (c: string, machines: string[]) => call<CalibOutcomes>('POST', `${calibPath(c)}/revert`, { machines });
export const residuals = (c: string, machine: string, field: string, from: number, step: number) =>
  call<ResidualPoint[]>('GET', `/api/calib/${encodeURIComponent(c)}/residuals?machine=${encodeURIComponent(machine)}&field=${field}&from=${from}&step=${step}`);

export const assistants = (c: string) => call<z.infer<typeof P.Assistants>>('GET', `/api/agents/${encodeURIComponent(c)}`);
export type Doc = z.infer<typeof P.Doc>;
export type DocVersion = z.infer<typeof P.DocVersion>;
export type UploadResult = z.infer<typeof P.UploadResults>[number];
const libPath = (c: string) => `/api/library/${encodeURIComponent(c)}`;
export const docs = (c: string) => call<Doc[]>('GET', `${libPath(c)}/docs`);
export const docVersions = (c: string, id: string) => call<DocVersion[]>('GET', `${libPath(c)}/docs/${encodeURIComponent(id)}/versions`);
export const deleteDoc = (c: string, id: string) => call<{ ok: true }>('DELETE', `${libPath(c)}/docs/${encodeURIComponent(id)}`);

/** Uploads one file to the library, reporting how much of it has gone (0–1). */
export function uploadDoc(c: string, file: File, onProgress: (f: number) => void, signal?: AbortSignal): Promise<UploadResult> {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open('POST', `${libPath(c)}/files`);
    const t = token();
    if (t) x.setRequestHeader('Authorization', `Bearer ${t}`);
    x.setRequestHeader('X-SF-Via', 'page');
    x.upload.onprogress = (e) => { if (e.lengthComputable) onProgress(e.loaded / e.total); };
    x.onload = () => {
      let body: unknown = null;
      try { body = JSON.parse(x.responseText); } catch { /* not JSON */ }
      if (x.status >= 300) {
        reject(new ApiError(x.status, (body as { error?: string } | null)?.error ?? `上传失败（${x.status}）`));
        return;
      }
      resolve((body as UploadResult[])[0]);
    };
    x.onerror = () => reject(new ApiError(0, '连不上服务器'));
    x.onabort = () => reject(new ApiError(0, '已取消'));
    signal?.addEventListener('abort', () => x.abort());
    const form = new FormData();
    form.append('file', file, file.name);
    x.send(form);
  });
}

/** Saves a document's original file (a version of it, or the current one). */
export async function downloadDoc(c: string, id: string, name: string, version?: number) {
  const t = token();
  const res = await fetch(`${libPath(c)}/docs/${encodeURIComponent(id)}/file${version ? `?version=${version}` : ''}`, { headers: t ? { Authorization: `Bearer ${t}` } : {} });
  if (!res.ok) {
    const b = await res.json().catch(() => null) as { error?: string } | null;
    throw new ApiError(res.status, b?.error ?? `下载失败（${res.status}）`);
  }
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/** The files the library reads, for pickers. */
export const DOC_ACCEPT = '.pdf,.docx,.xlsx,.xlsm,.csv,.md,.markdown,.txt';
export const health = () => call<z.infer<typeof P.Health>>('GET', '/api/health');

export const ROLE_LABEL: Record<Role, string> = { visitor: '访客', member: '成员', operator: '操作员', admin: '管理员' };
export const ROLE_NOTE: Record<Exclude<Role, 'visitor'>, string> = {
  member: '看数据，让助手记笔记、登记工单',
  operator: '再加：下发设备指令、调全厂倍速、采纳经营建议',
  admin: '再加：搭场景、管资料和助手、发令牌',
};
