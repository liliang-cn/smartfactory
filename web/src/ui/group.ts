// Figures of each plant and of the whole company, for the campus view's cards and panels.
import { kpiValues, type KpiValue } from '@/sim/kpis';
import type { PlantRuntime } from '@/state/store';

export interface PlantFigures {
  oee: number; tp: number; alarms: number; faults: number; power: number; running: number; machines: number;
  /** the plant's own headline figure (OEE for a factory, 翻台率 for a shop …) */
  head: KpiValue;
}

export function plantFigures(p: PlantRuntime): PlantFigures {
  const k = p.sim.kpis();
  let faults = 0;
  let running = 0;
  let machines = 0;
  for (const a of p.sim.assets.values()) {
    if (a.state === 'fault') faults++;
    if (a.spec?.runRise === undefined) continue;
    machines++;
    if (a.state === 'running') running++;
  }
  const head = kpiValues(p.sim)[0];
  return { oee: k.oee, tp: k.tp, alarms: p.sim.alarms.filter((a) => a.active).length, faults, power: k.power, running, machines, head };
}

/** A headline's short name for a narrow column. */
export const shortLabel = (k: KpiValue) => (k.id === 'oee' ? 'OEE' : k.label.replace(/^每小时/, '').slice(0, 4));

/** What the company's plants are called together, and their lines: the pack's words when they all share them. */
export function companyWords(plants: PlantRuntime[]) {
  const same = <T,>(f: (p: PlantRuntime) => T, other: T) => (new Set(plants.map(f)).size === 1 ? f(plants[0]) : other);
  return { plant: same((p) => p.layout.words.plant, '场所'), lines: same((p) => p.layout.words.lines, '个单元'), machine: same((p) => p.layout.words.machine, '设备设施') };
}

/** OEE is the plain mean of the plants; counts and output are sums. */
export function groupFigures(plants: PlantRuntime[]) {
  const per = plants.map((p) => ({ p, f: plantFigures(p) }));
  const sum = (k: Exclude<keyof PlantFigures, 'head'>) => per.reduce((s, x) => s + x.f[k], 0);
  return {
    per,
    oee: sum('oee') / Math.max(1, per.length),
    tp: sum('tp'),
    alarms: sum('alarms'),
    faults: sum('faults'),
    power: sum('power'),
    running: sum('running'),
    machines: sum('machines'),
    /** the unit output is counted in, when every plant counts in the same one */
    unit: new Set(plants.map((p) => p.layout.unit)).size === 1 ? plants[0].layout.unit : '',
    /** every plant heads with OEE: a company OEE means something */
    allOee: per.every((x) => x.f.head?.id === 'oee'),
  };
}
