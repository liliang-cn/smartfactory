// The console's pages live under /admin; links push history, back and forward work.
import { useSyncExternalStore } from 'react';

const listeners = new Set<() => void>();
const notify = () => listeners.forEach((f) => f());
window.addEventListener('popstate', notify);

export function go(path: string, replace = false) {
  if (replace) history.replaceState(null, '', path);
  else history.pushState(null, '', path);
  notify();
  window.scrollTo(0, 0);
}

export function usePath(): string {
  return useSyncExternalStore((f) => { listeners.add(f); return () => listeners.delete(f); }, () => location.pathname + location.search);
}

/** Matches /admin/a/:x/b against the current path; params by name, or null. */
export function match(path: string, pattern: string): Record<string, string> | null {
  const [p] = path.split('?');
  const a = p.replace(/\/+$/, '').split('/');
  const b = pattern.split('/');
  if (a.length !== b.length) return null;
  const out: Record<string, string> = {};
  for (let i = 0; i < b.length; i++) {
    if (b[i].startsWith(':')) out[b[i].slice(1)] = decodeURIComponent(a[i]);
    else if (a[i] !== b[i]) return null;
  }
  return out;
}

export const query = (path: string) => new URLSearchParams(path.split('?')[1] ?? '');
