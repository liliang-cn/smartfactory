// The words a plant's pack uses for the twin: a factory's by default, a shop's or a campsite's
// where its pack says so (terms in the industry pack). Every label the UI and the simulation's
// messages show comes from here, not from literals.
import type { IndustryCfg } from '@/config/schema';

export interface Words {
  plant: string;
  line: string;
  /** "条线" style counter for lines */
  lines: string;
  machine: string;
  lot: string;
  raw: string;
  goods: string;
  infeed: string;
  outfeed: string;
  conveyor: string;
  cycle: string;
  util: string;
  oee: string;
  throughput: string;
  power: string;
  vehicle: string;
  inTruck: string;
  outTruck: string;
  states: Record<string, string>;
}

/** The pack's words with the factory's in the gaps. */
export function wordsOf(industry: Pick<IndustryCfg, 'terms' | 'logistics'>): Words {
  const t = industry.terms ?? {};
  const lg = industry.logistics;
  const inV = lg?.inbound?.vehicle ?? 'truck';
  const outV = lg?.outbound?.vehicle ?? 'truck';
  const internal = lg?.internal ?? 'agv';
  return {
    plant: t.plant ?? '工厂',
    line: t.lineNoun ?? '产线',
    lines: t.line ?? '条线',
    machine: t.machine ?? '设备',
    lot: t.lot ?? '炉号',
    raw: t.raw ?? '原料',
    goods: t.goods ?? '成品',
    infeed: t.infeed ?? '上料缓存',
    outfeed: t.outfeed ?? '成品托盘',
    conveyor: t.conveyor ?? '输送线',
    cycle: t.cycle ?? '节拍',
    util: t.util ?? '稼动率',
    oee: t.oee ?? '综合设备效率 OEE',
    throughput: t.throughput ?? '每小时产量',
    power: t.power ?? '实时功率',
    vehicle: t.vehicle ?? (internal === 'forklift' ? '叉车' : 'AGV 小车'),
    inTruck: lg?.inbound?.label ?? (inV === 'van' ? '送货车' : `${t.raw ?? '原料'}进厂车`),
    outTruck: lg?.outbound?.label ?? (outV === 'rider' ? '外卖骑手' : outV === 'van' ? '配送车' : `${t.goods ?? '成品'}发运车`),
    states: t.states ?? {},
  };
}
