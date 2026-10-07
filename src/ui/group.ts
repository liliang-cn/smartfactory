// Figures of each plant and of the whole company, for the campus view's cards and panels.
import type { PlantRuntime } from '@/state/store';

export interface PlantFigures { oee: number; tp: number; alarms: number; faults: number; power: number; running: number; machines: number }

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
  return { oee: k.oee, tp: k.tp, alarms: p.sim.alarms.filter((a) => a.active).length, faults, power: k.power, running, machines };
}

/** OEE is the plain mean of the plants; counts and output are sums. */
export function groupFigures(plants: PlantRuntime[]) {
  const per = plants.map((p) => ({ p, f: plantFigures(p) }));
  const sum = (k: keyof PlantFigures) => per.reduce((s, x) => s + x.f[k], 0);
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
  };
}
