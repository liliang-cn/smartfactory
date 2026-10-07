import type { AssetState } from '@/sim/types';

export { STATE_LABEL } from '@/sim/plant';

/** Chip colours: green only for "working", amber for waiting, red for fault, violet for maintenance. */
export const CHIP: Record<AssetState, string> = {
  running: 'bg-success-soft text-success',
  ok: 'bg-success-soft text-success',
  idle: 'bg-warning-soft text-warning',
  waiting: 'bg-warning-soft text-warning',
  low: 'bg-warning-soft text-warning',
  blocked: 'bg-orange-100 text-orange-700',
  empty: 'bg-orange-100 text-orange-700',
  full: 'bg-orange-100 text-orange-700',
  fault: 'bg-danger-soft text-destructive',
  maint: 'bg-maint-soft text-maint',
  changeover: 'bg-maint-soft text-maint',
  stopped: 'bg-muted text-muted-foreground',
  away: 'bg-muted text-muted-foreground',
  moving: 'bg-brand-soft text-primary',
  handling: 'bg-brand-soft text-primary',
};

export const DOT: Record<AssetState, string> = {
  running: 'bg-success',
  ok: 'bg-success',
  idle: 'bg-amber-400',
  waiting: 'bg-amber-400',
  low: 'bg-amber-400',
  blocked: 'bg-warning',
  empty: 'bg-warning',
  full: 'bg-warning',
  fault: 'bg-destructive',
  maint: 'bg-maint',
  changeover: 'bg-maint',
  stopped: 'bg-slate-400',
  away: 'bg-slate-400',
  moving: 'bg-primary',
  handling: 'bg-primary',
};

export const ALERT_STATES = new Set<AssetState>(['fault', 'blocked', 'empty', 'full', 'maint', 'changeover']);

export const pct = (v: number, d = 0) => `${(v * 100).toFixed(d)}%`;
/** parts per hour: whole above ten, one decimal below (a slow line makes under one an hour) */
export const rate = (v: number) => `${v >= 10 ? Math.round(v) : Math.round(v * 10) / 10}`;

export const hhmm = (sec: number, withSeconds = false) => {
  const t = ((Math.floor(sec) % 86400) + 86400) % 86400;
  const h = String(Math.floor(t / 3600)).padStart(2, '0');
  const m = String(Math.floor((t % 3600) / 60)).padStart(2, '0');
  return withSeconds ? `${h}:${m}:${String(t % 60).padStart(2, '0')}` : `${h}:${m}`;
};
