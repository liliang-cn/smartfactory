// The phone page's calls to /api/checks, and the rounds it keeps on the phone while there is no
// network: each with the time it was taken, sent again once the phone is back online.
import type { z } from 'zod';
import type * as P from '@/protocol/sfp';

export type Overview = z.infer<typeof P.ChecksOverview>;
export type Form = z.infer<typeof P.CheckForm>;
export type Field = z.infer<typeof P.CheckField>;
export type Round = z.infer<typeof P.CheckRound>;
export type Machine = z.infer<typeof P.CheckMachine>;
export type MachineChecks = z.infer<typeof P.MachineChecks>;
export type Submission = z.infer<typeof P.CheckSubmission>;
export type Input = z.infer<typeof P.CheckInput>;
export type Value = z.infer<typeof P.CheckValue>;

export class ApiError extends Error {
  constructor(public status: number, message: string, public problems: string[] = []) {
    super(message);
  }
}

// An operator's phone is theirs: the token stays on it (the twin and the console keep theirs per tab).
const KEY = 'sf-check-token';

export function token(): string | null {
  const params = new URLSearchParams(location.search);
  const t = params.get('token');
  if (t) {
    signIn(t);
    params.delete('token');
    history.replaceState(null, '', `${location.pathname}${params.size ? `?${params}` : ''}`);
    return t;
  }
  try { return localStorage.getItem(KEY) ?? sessionStorage.getItem('sf-token'); } catch { return null; }
}
export function signIn(t: string) {
  try { localStorage.setItem(KEY, t.trim()); } catch { /* storage blocked */ }
}
export function signOut() {
  try { localStorage.removeItem(KEY); sessionStorage.removeItem('sf-token'); } catch { /* storage blocked */ }
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

const base = (c: string) => `/api/checks/${encodeURIComponent(c)}`;
export const overview = (c: string) => call<Overview>('GET', base(c));
export const machine = (c: string, plant: string, asset: string) =>
  call<MachineChecks>('GET', `${base(c)}/machines/${encodeURIComponent(plant)}/${encodeURIComponent(asset)}`);
export const submit = (c: string, input: Input) => call<Submission>('POST', `${base(c)}/submissions`, input);
/** The companies the caller's token covers. */
export const companies = () => call<{ companies: string[] }>('GET', '/api/health').then((h) => h.companies);

export async function photo(c: string, id: string): Promise<string> {
  const t = token();
  const res = await fetch(`${base(c)}/photos/${encodeURIComponent(id)}`, { headers: t ? { Authorization: `Bearer ${t}` } : {} });
  if (!res.ok) throw new ApiError(res.status, '照片读不出来');
  return URL.createObjectURL(await res.blob());
}

/** A name of the phone's own for a round, so sending it twice keeps it once. */
export function newID(): string {
  const b = new Uint8Array(10);
  crypto.getRandomValues(b);
  return `m-${[...b].map((x) => x.toString(16).padStart(2, '0')).join('')}`;
}

// ---------- rounds waiting on the phone ----------

export interface Queued {
  company: string;
  input: Input;
  /** what to call it on the list: the machine and the form */
  label: string;
  /** why the server would not take it (it stays until the person dismisses it) */
  refused?: string;
}

const QUEUE = 'sf-check-queue';

export function queued(): Queued[] {
  try { return JSON.parse(localStorage.getItem(QUEUE) ?? '[]') as Queued[]; } catch { return []; }
}

function keep(list: Queued[]) {
  localStorage.setItem(QUEUE, JSON.stringify(list));
  window.dispatchEvent(new Event('sf-check-queue'));
}

/** Keeps a round on the phone to send later; false when the phone has no room for it (photos). */
export function enqueue(q: Queued): boolean {
  try {
    keep([...queued().filter((x) => x.input.id !== q.input.id), q]);
    return true;
  } catch {
    return false;
  }
}

export function dismiss(id: string) {
  try { keep(queued().filter((x) => x.input.id !== id)); } catch { /* storage blocked */ }
}

/** A failure that sending again later may get past: no network, the server away or busy. */
export const transient = (e: unknown) =>
  !(e instanceof ApiError) || e.status === 408 || e.status === 429 || e.status >= 500;

let flushing: Promise<number> | null = null;

/** Sends what waits on the phone, oldest first; stops at the first that cannot go yet. Says how many went. */
export function flush(): Promise<number> {
  flushing ??= (async () => {
    let sent = 0;
    for (const q of queued()) {
      if (q.refused) continue;
      try {
        await submit(q.company, q.input);
        dismiss(q.input.id!);
        sent++;
      } catch (e) {
        if (transient(e)) break;
        // signed out or no longer allowed: wait for the person to sign in again
        if (e instanceof ApiError && (e.status === 401 || e.status === 403)) break;
        const err = e as ApiError;
        try { keep(queued().map((x) => (x.input.id === q.input.id ? { ...x, refused: [err.message, ...err.problems].filter(Boolean).join('；') } : x))); } catch { /* storage blocked */ }
      }
    }
    return sent;
  })().finally(() => { flushing = null; });
  return flushing;
}

/** Shrinks a photo to at most 1280 px on its long side, as a JPEG data URL (a few hundred kB). */
export async function shrink(file: File): Promise<string> {
  const bmp = await createImageBitmap(file);
  const k = Math.min(1, 1280 / Math.max(bmp.width, bmp.height));
  const cv = document.createElement('canvas');
  cv.width = Math.round(bmp.width * k);
  cv.height = Math.round(bmp.height * k);
  cv.getContext('2d')!.drawImage(bmp, 0, 0, cv.width, cv.height);
  bmp.close();
  return cv.toDataURL('image/jpeg', 0.72);
}
