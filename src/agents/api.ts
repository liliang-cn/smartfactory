// The company's own assistants, at /api/agents/{company} and /api/library/{company}.
// Shapes are the protocol's (src/protocol/sfp.ts).
import type { z } from 'zod';
import { operatorToken } from '@/live/client';
import type * as P from '@/protocol/sfp';

export type Assistants = z.infer<typeof P.Assistants>;
export type Assistant = z.infer<typeof P.Assistant>;
export type Kind = z.infer<typeof P.AssistantKind>;
export type Conversation = z.infer<typeof P.Conversation>;
export type ChatMessage = z.infer<typeof P.ChatMessage>;
export type ConversationView = z.infer<typeof P.ConversationView>;
export type AssistantEvent = z.infer<typeof P.AssistantEvent>;
export type Doc = z.infer<typeof P.Doc>;
export type DocView = z.infer<typeof P.DocView>;
export type Note = z.infer<typeof P.Note>;
export type Ticket = z.infer<typeof P.Ticket>;

export class AgentError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

/** The id this browser's conversations are kept under. */
function visitor(): string {
  try {
    let v = localStorage.getItem('sf-visitor');
    if (!v) {
      v = crypto.randomUUID();
      localStorage.setItem('sf-visitor', v);
    }
    return v;
  } catch {
    return 'anonymous';
  }
}

function headers(json: boolean): Record<string, string> {
  const token = operatorToken();
  return {
    'X-SF-Visitor': visitor(),
    ...(json ? { 'Content-Type': 'application/json' } : {}),
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

async function call<T>(method: string, path: string, json?: unknown): Promise<T> {
  const res = await fetch(path, { method, headers: headers(json !== undefined), body: json !== undefined ? JSON.stringify(json) : undefined });
  const text = await res.text();
  let body: unknown = null;
  try { body = text ? JSON.parse(text) : null; } catch { body = null; }
  if (!res.ok) throw new AgentError(res.status, (body as { error?: string } | null)?.error ?? `请求失败（${res.status}）`);
  return body as T;
}

const agents = (c: string) => `/api/agents/${encodeURIComponent(c)}`;
const library = (c: string) => `/api/library/${encodeURIComponent(c)}`;

export const getAssistants = (c: string) => call<Assistants>('GET', agents(c));
export const createAssistant = (c: string, a: { kind: Kind; name: string; instructions: string }) => call<Assistant>('POST', agents(c), a);
export const updateAssistant = (c: string, id: string, a: { name: string; instructions: string }) => call<Assistant>('PUT', `${agents(c)}/${id}`, a);
export const deleteAssistant = (c: string, id: string) => call<{ ok: true }>('DELETE', `${agents(c)}/${id}`);

export const getConversations = (c: string, id: string) => call<Conversation[]>('GET', `${agents(c)}/${id}/conversations`);
export const getConversation = (c: string, id: string, conv: string) => call<ConversationView>('GET', `${agents(c)}/${id}/conversations/${conv}`);
export const deleteConversation = (c: string, id: string, conv: string) => call<{ ok: true }>('DELETE', `${agents(c)}/${id}/conversations/${conv}`);

export const getDocs = (c: string) => call<Doc[]>('GET', `${library(c)}/docs`);
export const getDoc = (c: string, id: string) => call<DocView>('GET', `${library(c)}/docs/${id}`);
export const uploadDoc = (c: string, title: string, body: string) => call<Doc>('POST', `${library(c)}/docs`, { title, body });
export const deleteDoc = (c: string, id: string) => call<{ ok: true }>('DELETE', `${library(c)}/docs/${id}`);
export const getNotes = (c: string) => call<Note[]>('GET', `${library(c)}/notes`);
export const deleteNote = (c: string, id: string) => call<{ ok: true }>('DELETE', `${library(c)}/notes/${id}`);
export const getTickets = (c: string) => call<Ticket[]>('GET', `${library(c)}/tickets`);
export const setTicketStatus = (c: string, id: string, status: string) => call<Ticket>('PATCH', `${library(c)}/tickets/${id}`, { status });

/** Ask an assistant; on sees every event of the answer as it is written. */
export async function ask(c: string, id: string, conversation: string | undefined, text: string, signal: AbortSignal, on: (ev: AssistantEvent) => void) {
  const res = await fetch(`${agents(c)}/${id}/ask`, { method: 'POST', headers: headers(true), body: JSON.stringify({ conversation, text }), signal });
  if (!res.ok || !res.body) {
    let msg = `请求失败（${res.status}）`;
    try { msg = (await res.json()).error ?? msg; } catch { /* not JSON */ }
    throw new AgentError(res.status, msg);
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  const flush = (chunk: string) => {
    const data = chunk.split('\n').filter((l) => l.startsWith('data:')).map((l) => l.slice(5).trim()).join('\n');
    if (!data) return;
    try { on(JSON.parse(data) as AssistantEvent); } catch { /* partial line */ }
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
