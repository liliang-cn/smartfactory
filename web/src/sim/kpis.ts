// The figures a plant's pack puts on top of its twin (`kpis` in the industry pack), worked out
// from the simulation (and what equipment and outside systems reported into it). The UI, the
// server's snapshot and the agents all read them from here.
import type { KpiIdT } from '@/config/schema';
import type { PlantSim } from './plant';

export interface KpiValue {
  id: KpiIdT;
  label: string;
  /** the number (a share 0..1, a count, 元 …) and how it reads */
  value: number;
  text: string;
  sub: string;
  /** share of what it is worked out from that was measured or reported, 0..1 */
  measured: number;
  /** a reading that needs attention */
  bad?: boolean;
}

const pct = (v: number, d = 0) => `${(v * 100).toFixed(d)}%`;
const rate = (v: number) => `${v >= 10 ? Math.round(v) : Math.round(v * 10) / 10}`;
// (the server's JS engine has no locale formatting)
const yuan = (v: number) => `¥${String(Math.round(v)).replace(/\B(?=(\d{3})+$)/g, ',')}`;

export const DEFAULT_KPIS: KpiIdT[] = ['oee', 'throughput', 'quality', 'power'];

/** The pack's figures for the plant, in its order (the first is the headline). */
export function kpiValues(sim: PlantSim): KpiValue[] {
  const L = sim.layout;
  const w = L.words;
  const k = sim.kpis();
  const v = sim.venue?.figures();
  const dep = sim.depot?.figures();
  const list = L.kpis?.length ? L.kpis : DEFAULT_KPIS;
  const out: KpiValue[] = [];
  const placeWord = sim.places[0]?.place?.label ?? '桌';
  for (const ref of list) {
    const id = typeof ref === 'string' ? ref : ref.id;
    const item = typeof ref === 'string' ? undefined : ref.item;
    const label = typeof ref === 'string' ? undefined : ref.label;
    const add = (x: Omit<KpiValue, 'id' | 'label' | 'measured'> & { label: string; measured?: number }) => out.push({ id, ...x, label: label ?? x.label, measured: x.measured ?? 0 });
    switch (id) {
      case 'oee':
        add({ label: w.oee, value: k.oee, text: pct(k.oee, 1), sub: `可用 ${pct(k.avail)} 性能 ${pct(k.perf)} 质量 ${pct(k.quality)}`, measured: k.measured });
        break;
      case 'throughput':
        add({ label: w.throughput, value: k.tp, text: rate(k.tp), sub: `${L.unit}，${k.span > 3600 ? `近 ${Math.round(k.span / 3600)} 小时折算` : '近 60 分钟'} ${L.lines.length} ${w.lines}合计`, measured: k.measured });
        break;
      case 'quality':
        add({ label: L.qualityLabel, value: k.quality, text: pct(k.quality, 1), sub: `合格 ${k.good}，不合格 ${k.scrap}`, measured: k.measured });
        break;
      case 'power':
        add({
          label: w.power, value: k.power, text: `${k.power.toFixed(0)} kW`,
          sub: L.tariff ? `今日电费 ${yuan(k.cost)}，现价 ${k.price.toFixed(2)} 元/kWh` : `本班能耗 ${k.energy.toFixed(1)} kWh`,
        });
        break;
      case 'turnover':
        if (v) add({ label: '翻台率', value: v.turnover, text: v.turnover.toFixed(2), sub: `今日 ${v.day.sessions} ${placeWord}次 / ${v.places} ${placeWord}，平均用餐 ${v.dining.toFixed(0)} 分钟`, measured: v.reported });
        break;
      case 'occupancy':
        if (v) add({ label: placeWord === '桌' ? '上座率' : `${placeWord}占用`, value: v.occupancy, text: pct(v.occupancy), sub: `${v.occupied}/${v.places} ${placeWord}有人，座位利用 ${pct(v.seatUse)}`, measured: v.reported });
        break;
      case 'queue':
        if (v) add({ label: '等位', value: v.queue, text: `${v.queue} 组`, sub: `${v.waiting} 人，今日平均等 ${v.wait.toFixed(0)} 分钟，走掉 ${v.day.balked + v.day.reneged} 组`, bad: v.queue >= (sim.venue!.demand.balk ?? 6) });
        break;
      case 'ticket':
        if (v) add({ label: '客单价', value: v.ticket, text: v.day.persons ? yuan(v.ticket) : '—', sub: `今日营收 ${yuan(v.day.revenue + v.day.takeawayRevenue)}，堂食 ${v.day.persons} 人` });
        break;
      case 'rating':
        if (v) add({ label: L.qualityLabel, value: v.rating, text: v.day.rated ? pct(v.rating, 1) : '—', sub: `今日 ${v.day.rated} 条评价，差评 ${v.day.rated - v.day.liked}`, bad: v.day.rated > 10 && v.rating < 0.9 });
        break;
      case 'served':
        add({ label: w.throughput, value: k.tp, text: rate(k.tp), sub: v ? `${L.unit}/时，今日 ${v.day.groups} ${L.unit} ${v.day.persons} 人` : `${L.unit}，近 60 分钟` });
        break;
      case 'takeaway':
        if (v) add({ label: '外卖', value: v.day.takeaway, text: `${v.day.takeaway} 单`, sub: `在做 ${sim.venue!.orders.length} 单，待取 ${sim.venue!.counter.length} 单，${yuan(v.day.takeawayRevenue)}` });
        break;
      case 'checkins':
        if (v) add({ label: '今日入住 / 退房', value: v.day.checkins, text: `${v.day.checkins} / ${v.day.checkouts}`, sub: `在住 ${v.occupied} 组，${placeWord} ${v.occupied}/${v.places}` });
        break;
      case 'inUse': {
        const it = sim.inventory.find((x) => x.key === item) ?? sim.inventory.find((x) => x.kind === 'lend');
        if (!it) break;
        const used = it.kind === 'lend' ? it.cap - it.avail : it.avail;
        add({ label: it.kind === 'lend' ? `${it.label}在用` : `${it.label}库存`, value: used / Math.max(1, it.cap), text: `${used} / ${it.cap}`, sub: it.kind === 'lend' ? `${it.unit}，空着 ${it.avail} ${it.unit}` : `${it.unit}，剩 ${pct(it.avail / Math.max(1, it.cap))}`, bad: it.avail <= Math.floor(it.cap * it.low) });
        break;
      }
      case 'weather':
        if (v) add({ label: '今日天气', value: v.rain ? 1 : 0, text: v.rain ? '有雨' : '晴', sub: v.rain ? `客流约 ×${sim.venue!.demand.weather?.factor ?? 1}` : '客流照常' });
        break;
      case 'onTime':
        if (dep) add({ label: '准时配送率', value: dep.otif, text: dep.deliveries ? pct(dep.otif, 1) : '—', sub: `近 ${dep.deliveries} 车次按时足量；在库 ${dep.stock} ${L.unit}，留样 ${dep.samples} 批，今日过期 ${dep.wasted}`, bad: dep.deliveries > 3 && dep.otif < 0.9 });
        break;
      case 'coldChain': {
        const [ok, all] = sim.cold;
        const n = sim.lines.reduce((s, l) => s + l.stations.filter((a) => a.machine?.hold !== undefined).length, 0);
        add({ label: '冷链达标率', value: all ? ok / all : 1, text: all ? pct(ok / all, 1) : '—', sub: `今日 ${n} 台冷藏冷冻设备，超温 ${Math.round((all - ok) / 60)} 台·分钟`, bad: all > 600 && ok / all < 0.98 });
        break;
      }
    }
  }
  return out;
}
