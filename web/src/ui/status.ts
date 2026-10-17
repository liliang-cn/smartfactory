import type { PlantLayout } from '@/core/layout';
import { STATE_LABEL } from '@/sim/plant';
import type { AssetState } from '@/sim/types';

export { STATE_LABEL };

/** A state in the plant's own words (换台 for a shop's changeover …). */
export const stateLabel = (L: Pick<PlantLayout, 'words'>, s: AssetState) => L.words.states[s] ?? STATE_LABEL[s];

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
  free: 'bg-success-soft text-success',
  occupied: 'bg-brand-soft text-primary',
  reserved: 'bg-warning-soft text-warning',
  cleaning: 'bg-muted text-muted-foreground',
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
  free: 'bg-success',
  occupied: 'bg-primary',
  reserved: 'bg-amber-400',
  cleaning: 'bg-slate-400',
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
