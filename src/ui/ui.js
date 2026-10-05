// DOM side of the twin: KPI cards, inspector, order tracker, fleet table, search, toasts.
// Structure is built once per selection; values are patched in place a few times a second
// so sliders and buttons keep focus while the plant keeps moving.
import { STATE_LABEL } from '../sim/plant.js';
import { GROUPS } from '../sim/layout.js';
import { RAMP_CSS } from '../view/kit.js';

const $ = (s, el = document) => el.querySelector(s);
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
const pct = (v, d = 0) => `${(v * 100).toFixed(d)}%`;
export const hhmm = (sec, s = false) => {
  const t = ((Math.floor(sec) % 86400) + 86400) % 86400;
  const h = String(Math.floor(t / 3600)).padStart(2, '0');
  const m = String(Math.floor((t % 3600) / 60)).padStart(2, '0');
  return s ? `${h}:${m}:${String(t % 60).padStart(2, '0')}` : `${h}:${m}`;
};

const ICON = {
  cnc: '<svg viewBox="0 0 26 26"><rect x="3" y="5" width="20" height="16" rx="3" fill="#2f5bea"/><rect x="6" y="8" width="9" height="8" rx="1.5" fill="#bcd0ff"/><rect x="17" y="8" width="3" height="3" rx="1" fill="#fff"/><rect x="3" y="19" width="20" height="3" rx="1.2" fill="#1f3fb0"/></svg>',
  press: '<svg viewBox="0 0 26 26"><rect x="4" y="3" width="18" height="5" rx="2" fill="#2f5bea"/><rect x="5" y="8" width="3" height="12" fill="#9db4ff"/><rect x="18" y="8" width="3" height="12" fill="#9db4ff"/><rect x="9" y="10" width="8" height="4" rx="1" fill="#1f3fb0"/><rect x="3" y="20" width="20" height="3" rx="1.2" fill="#1f3fb0"/></svg>',
  robot: '<svg viewBox="0 0 26 26"><rect x="6" y="19" width="10" height="4" rx="1.5" fill="#1f3fb0"/><path d="M11 19V13l6-6 3 3" stroke="#f6c445" stroke-width="3.4" fill="none" stroke-linecap="round" stroke-linejoin="round"/><circle cx="11" cy="13" r="2.2" fill="#2f5bea"/><circle cx="17" cy="7" r="2" fill="#2f5bea"/></svg>',
  oven: '<svg viewBox="0 0 26 26"><rect x="3" y="7" width="20" height="13" rx="3" fill="#2f5bea"/><rect x="6" y="11" width="14" height="5" rx="1.5" fill="#ff8a3d"/><rect x="7" y="3" width="3" height="4" fill="#9db4ff"/><rect x="16" y="3" width="3" height="4" fill="#9db4ff"/></svg>',
  qc: '<svg viewBox="0 0 26 26"><path d="M5 22V6h16v16" stroke="#2f5bea" stroke-width="2.6" fill="none" stroke-linecap="round"/><rect x="10" y="8" width="6" height="5" rx="1.5" fill="#1f3fb0"/><path d="M9 20l4-6 4 6z" fill="#bcd0ff"/></svg>',
  pack: '<svg viewBox="0 0 26 26"><path d="M4 9l9-5 9 5v10l-9 5-9-5z" fill="#d9a46b"/><path d="M4 9l9 5 9-5M13 14v10" stroke="#bf8a52" stroke-width="1.6" fill="none"/></svg>',
  conveyor: '<svg viewBox="0 0 26 26"><rect x="3" y="13" width="20" height="5" rx="2.5" fill="#2f5bea"/><circle cx="7" cy="15.5" r="1.4" fill="#fff"/><circle cx="13" cy="15.5" r="1.4" fill="#fff"/><circle cx="19" cy="15.5" r="1.4" fill="#fff"/><rect x="6" y="7" width="5" height="5" rx="1" fill="#d9a46b"/><rect x="14" y="7" width="5" height="5" rx="1" fill="#d9a46b"/></svg>',
  agv: '<svg viewBox="0 0 26 26"><rect x="3" y="12" width="20" height="7" rx="3" fill="#f6c445"/><rect x="5" y="7" width="12" height="5" rx="1" fill="#d9a46b"/><circle cx="8" cy="20" r="2" fill="#28314b"/><circle cx="18" cy="20" r="2" fill="#28314b"/><rect x="19" y="10" width="3" height="2" rx="1" fill="#28314b"/></svg>',
  truck: '<svg viewBox="0 0 26 26"><rect x="2" y="6" width="14" height="12" rx="2" fill="#fff" stroke="#2f5bea" stroke-width="1.6"/><path d="M16 10h4l3 4v4h-7z" fill="#2f5bea"/><circle cx="7" cy="19" r="2.2" fill="#28314b"/><circle cx="19" cy="19" r="2.2" fill="#28314b"/></svg>',
  buffer: '<svg viewBox="0 0 26 26"><rect x="4" y="19" width="18" height="3" rx="1" fill="#c79a68"/><rect x="5" y="14" width="7" height="5" rx="1" fill="#aab6d3"/><rect x="13" y="14" width="7" height="5" rx="1" fill="#aab6d3"/><rect x="9" y="9" width="7" height="5" rx="1" fill="#aab6d3"/></svg>',
  outfeed: '<svg viewBox="0 0 26 26"><rect x="4" y="19" width="18" height="3" rx="1" fill="#c79a68"/><rect x="5" y="13" width="7" height="6" rx="1" fill="#d9a46b"/><rect x="13" y="13" width="7" height="6" rx="1" fill="#d9a46b"/><rect x="9" y="7" width="7" height="6" rx="1" fill="#d9a46b"/></svg>',
  warehouse: '<svg viewBox="0 0 26 26"><path d="M3 10l10-6 10 6v12H3z" fill="#2f5bea"/><rect x="7" y="13" width="12" height="9" fill="#e8eefe"/><rect x="9" y="15" width="3" height="3" fill="#d9a46b"/><rect x="14" y="15" width="3" height="3" fill="#d9a46b"/></svg>',
  fgzone: '<svg viewBox="0 0 26 26"><rect x="3" y="4" width="20" height="18" rx="3" fill="#fff3d0" stroke="#f6c445" stroke-width="1.6" stroke-dasharray="3 2"/><rect x="7" y="9" width="5" height="5" rx="1" fill="#d9a46b"/><rect x="14" y="9" width="5" height="5" rx="1" fill="#d9a46b"/><rect x="10" y="15" width="5" height="5" rx="1" fill="#d9a46b"/></svg>',
};
ICON.welder = ICON.robot;

const KPI_ICON = {
  oee: '<svg viewBox="0 0 24 24"><path d="M4 16a8 8 0 1 1 16 0" fill="none" stroke="#2f5bea" stroke-width="2.4" stroke-linecap="round"/><path d="M12 16l4-5" stroke="#1f3fb0" stroke-width="2.4" stroke-linecap="round"/><circle cx="12" cy="16" r="2" fill="#1f3fb0"/></svg>',
  tp: '<svg viewBox="0 0 24 24"><path d="M3 8l9-5 9 5v8l-9 5-9-5z" fill="#2f5bea"/><path d="M3 8l9 5 9-5" fill="#6f8ff5"/><path d="M12 13v8" stroke="#1f3fb0" stroke-width="1.4"/></svg>',
  q: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="#2f5bea"/><path d="M8 12.5l3 3 5-6" stroke="#fff" stroke-width="2.2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  power: '<svg viewBox="0 0 24 24"><path d="M13 2 5 13h6l-1 9 8-11h-6z" fill="#2f5bea"/></svg>',
};

const STEP_ICON = {
  doc: '<svg viewBox="0 0 16 16"><rect x="3" y="2" width="10" height="12" rx="2" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M6 6h4M6 9h4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
  box: '<svg viewBox="0 0 16 16"><path d="M2.5 5 8 2l5.5 3v6L8 14l-5.5-3z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><path d="M2.5 5 8 8l5.5-3M8 8v6" stroke="currentColor" stroke-width="1.5"/></svg>',
  gear: '<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="2.3" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M8 1.8v2M8 12.2v2M1.8 8h2M12.2 8h2M3.6 3.6 5 5M11 11l1.4 1.4M3.6 12.4 5 11M11 5l1.4-1.4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
  check: '<svg viewBox="0 0 16 16"><path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  truck: '<svg viewBox="0 0 16 16"><rect x="1.5" y="4" width="8" height="7" rx="1" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M9.5 6.5h3l2 2.5v2h-5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><circle cx="4.5" cy="12" r="1.3" fill="currentColor"/><circle cx="12" cy="12" r="1.3" fill="currentColor"/></svg>',
};

const ORDERS = {
  L1: { no: 'MO-261005-01', part: '电机端盖', qty: 1200, issued: '05:40', kitted: '05:58' },
  L2: { no: 'MO-261005-02', part: '支架焊合件', qty: 1000, issued: '05:42', kitted: '06:03' },
};

const SVG_FOCUS = '<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="4.5" fill="none" stroke="currentColor" stroke-width="1.5"/><circle cx="8" cy="8" r="1.4" fill="currentColor"/><path d="M8 1v2.2M8 12.8V15M1 8h2.2M12.8 8H15" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>';
const SVG_FOLLOW = '<svg viewBox="0 0 16 16"><path d="M8 1.8 13 13.5 8 11 3 13.5z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/></svg>';
const SVG_CLOSE = '<svg viewBox="0 0 16 16"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/></svg>';
const CHEV = '<svg viewBox="0 0 16 16" width="14" height="14"><path d="M6 3.5 10.5 8 6 12.5" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>';

const isMachine = (a) => a.spec?.runRise !== undefined;
const isVehicle = (a) => a.type === 'agv' || a.type === 'truck';
const groupName = (a) => GROUPS.find((g) => g.id === a.group)?.name ?? '';

export class UI {
  constructor(sim, act, state) {
    this.sim = sim;
    this.act = act;
    this.state = state;
    this.tab = 'machines';
    this.trackerLine = 'L1';
    this.chartMetric = 'temp';
    this._lastHtml = {};
    this._prevKpi = null;
    this._kpiMark = -999;
    this._seenAlarm = 0;
    this._bind();
    this._buildKpis();
  }

  // ---------- wiring ----------

  _bind() {
    const act = this.act;
    $('#speed').addEventListener('click', (e) => {
      const b = e.target.closest('button');
      if (b) act.setSpeed(Number(b.dataset.speed));
    });
    $('#modes').addEventListener('click', (e) => {
      const b = e.target.closest('button');
      if (b) act.setMode(b.dataset.mode);
    });
    $('#labels-btn').addEventListener('click', () => act.toggleLabels());
    document.querySelector('.mapctl').addEventListener('click', (e) => {
      const b = e.target.closest('button');
      if (b) act.cam(b.dataset.cam);
    });
    $('#bell').addEventListener('click', () => { this.tab = 'alarms'; this.renderFleet(true); });
    $('#fleet-tabs').addEventListener('click', (e) => {
      const b = e.target.closest('button');
      if (!b) return;
      this.tab = b.dataset.tab;
      this.renderFleet(true);
    });
    $('#ack-all').addEventListener('click', () => { this.sim.ackAll(); this.renderFleet(true); });

    // Rows are re-rendered while the plant runs, so act on pointerdown (mouse/touch) and on
    // keyboard-originated clicks (detail === 0) instead of relying on a full click cycle.
    const rowHandler = (e) => {
      if (e.type === 'click' && e.detail !== 0) return;
      const ack = e.target.closest('[data-ack]');
      if (ack) { this.sim.ack(Number(ack.dataset.ack)); this.renderFleet(true); return; }
      const row = e.target.closest('[data-id]');
      if (row) act.select(row.dataset.id, true);
    };
    $('#fleet-body').addEventListener('pointerdown', rowHandler);
    $('#fleet-body').addEventListener('click', rowHandler);
    $('#tracker').addEventListener('pointerdown', (e) => this._trackerClick(e));
    $('#tracker').addEventListener('click', (e) => { if (e.detail === 0) this._trackerClick(e); });

    // Search
    const input = $('#search');
    const box = $('#search-results');
    let idx = 0;
    const results = () => {
      const q = input.value.trim().toLowerCase();
      if (!q) return [];
      return [...this.sim.assets.values()].filter((a) =>
        `${a.id} ${a.name} ${a.spec?.label ?? ''} ${groupName(a)}`.toLowerCase().includes(q)).slice(0, 8);
    };
    const draw = () => {
      const r = results();
      if (!input.value.trim()) { box.classList.remove('open'); return; }
      box.classList.add('open');
      box.innerHTML = r.length
        ? r.map((a, i) => `<button type="button" role="option" data-id="${a.id}" aria-selected="${i === idx}">
            <span class="chip ${a.state}">${STATE_LABEL[a.state] ?? ''}</span><b>${a.id}</b> ${esc(a.name)}<small>${esc(groupName(a))}</small></button>`).join('')
        : '<div class="empty">没有匹配的设备，试试编号如 CNC、AGV、TRK</div>';
    };
    input.addEventListener('input', () => { idx = 0; draw(); });
    input.addEventListener('keydown', (e) => {
      const r = results();
      if (e.key === 'ArrowDown') { idx = Math.min(r.length - 1, idx + 1); draw(); e.preventDefault(); }
      if (e.key === 'ArrowUp') { idx = Math.max(0, idx - 1); draw(); e.preventDefault(); }
      if (e.key === 'Enter' && r[idx]) { act.select(r[idx].id, true); input.value = ''; draw(); input.blur(); }
      if (e.key === 'Escape') { input.value = ''; draw(); input.blur(); }
    });
    input.addEventListener('blur', () => setTimeout(() => box.classList.remove('open'), 150));
    input.addEventListener('focus', draw);
    box.addEventListener('pointerdown', (e) => {
      const b = e.target.closest('[data-id]');
      if (b) { act.select(b.dataset.id, true); input.value = ''; draw(); }
    });
  }

  _trackerClick(e) {
    const t = e.target.closest('[data-line]');
    if (t) { this.trackerLine = t.dataset.line; this.renderTracker(true); return; }
    const s = e.target.closest('[data-id]');
    if (s) this.act.select(s.dataset.id, true);
  }

  // ---------- top bar & KPIs ----------

  _buildKpis() {
    const card = (k, label) => `<div class="kpi" data-k="${k}"><span class="kpi-icon">${KPI_ICON[k]}</span>
      <span class="kpi-text"><small>${label}</small><span class="kpi-val"><b></b><em></em></span><span class="kpi-sub"></span></span></div>`;
    $('#kpis').innerHTML = card('oee', '综合设备效率 OEE') + card('tp', '每小时产量') + card('q', '良品率') + card('power', '实时功率');
    this.kpiEls = {};
    for (const el of document.querySelectorAll('.kpi')) {
      this.kpiEls[el.dataset.k] = { b: $('b', el), em: $('em', el), sub: $('.kpi-sub', el) };
    }
  }

  renderTop() {
    const s = this.state;
    const sim = this.sim;
    $('#clock').textContent = hhmm(sim.clock, true);
    $('#live').classList.toggle('paused', s.timeScale === 0);
    $('#live-text').textContent = s.timeScale === 0 ? '已暂停' : s.timeScale === 1 ? '实时' : `${s.timeScale} 倍速`;
    for (const b of document.querySelectorAll('#speed button')) b.classList.toggle('on', Number(b.dataset.speed) === s.timeScale);
    for (const b of document.querySelectorAll('#modes button')) b.classList.toggle('on', b.dataset.mode === s.mode);
    $('#labels-btn').setAttribute('aria-pressed', String(s.labels));
    const unacked = sim.alarms.filter((a) => !a.acked).length;
    const bc = $('#bell-count');
    bc.hidden = unacked === 0;
    bc.textContent = unacked > 99 ? '99+' : unacked;

    const legend = $('#legend');
    legend.hidden = s.mode === 'live';
    $('i', legend).style.background = RAMP_CSS;
    $('#legend-lo').textContent = s.mode === 'heat' ? '常温' : '0%';
    $('#legend-hi').textContent = s.mode === 'heat' ? '停机温度' : '100%';

    const k = sim.kpis();
    if (sim.elapsed - this._kpiMark >= 60 || !this._prevKpi) { this._prevKpi = this._snap ?? k; this._snap = k; this._kpiMark = sim.elapsed; }
    const prev = this._prevKpi;
    const delta = (el, d, fmt) => {
      el.textContent = Math.abs(d) < 1e-6 ? '' : `${d > 0 ? '↑' : '↓'} ${fmt(Math.abs(d))}`;
      el.classList.toggle('down', d < 0);
    };
    const E = this.kpiEls;
    E.oee.b.textContent = pct(k.oee, 1);
    delta(E.oee.em, k.oee - prev.oee, (v) => `${(v * 100).toFixed(1)}`);
    E.oee.sub.textContent = `可用 ${pct(k.avail)}\u2002性能 ${pct(k.perf)}\u2002质量 ${pct(k.quality)}`;
    E.tp.b.textContent = `${k.tp}`;
    delta(E.tp.em, k.tp - prev.tp, (v) => `${v.toFixed(0)}`);
    E.tp.sub.textContent = '件，近 60 分钟两线合计';
    E.q.b.textContent = pct(k.quality, 1);
    delta(E.q.em, k.quality - prev.quality, (v) => `${(v * 100).toFixed(2)}`);
    E.q.sub.textContent = `良品 ${k.good}，不良 ${k.scrap}`;
    E.power.b.textContent = `${k.power.toFixed(0)} kW`;
    E.power.em.textContent = '';
    E.power.sub.textContent = `本班能耗 ${k.energy.toFixed(1)} kWh`;

    // Pop a toast for each new fault
    for (const al of sim.alarms) {
      if (al.id <= this._seenAlarm) break;
      if (al.prio === 1) this.toast(`${al.asset}：${al.text}`, 'alarm', al.asset);
    }
    this._seenAlarm = sim.alarms[0]?.id ?? this._seenAlarm;
  }

  // ---------- inspector ----------

  openInspector(id) {
    const el = $('#inspector');
    document.getElementById('app').classList.toggle('has-inspector', !!id);
    if (!id) { el.hidden = true; this.insp = null; return; }
    const a = this.sim.assets.get(id);
    el.hidden = false;
    this.chartMetric = isMachine(a) ? 'temp' : 'level';
    el.innerHTML = this._inspectorHtml(a);
    this.insp = { id, el, a };
    this._bindInspector(a);
    this.renderInspector();
  }

  _inspectorHtml(a) {
    const where = a.line ? `${a.line.name} ${a.line.note}` : groupName(a);
    const kind = a.spec?.label ?? '';
    const tiles = [];
    const tile = (key, label, wide = false, bar = false) =>
      tiles.push(`<div class="tile${wide ? ' wide' : ''}"><small>${label}</small><b data-v="${key}"></b>${bar ? `<div class="bar"><i data-bar="${key}"></i></div>` : ''}</div>`);
    if (isMachine(a)) {
      tile('temp', '温度', false, true);
      tile('power', '功率');
      tile('health', '健康度', false, true);
      tile('util', '稼动率', false, true);
      tile('cycle', '节拍');
      tile('cycles', '累计循环');
    } else if (a.type === 'agv') {
      tile('battery', '电量', false, true);
      tile('vel', '速度');
      tile('task', '当前任务', true);
      tile('cargo', '载货');
      tile('power', '功率');
    } else if (a.type === 'truck') {
      tile('carrier', '承运商');
      tile('load', '装载');
      tile('task', '当前任务', true);
    } else if (a.type === 'buffer' || a.type === 'warehouse') {
      tile('stock', '库存', true, true);
      tile('feeder', a.type === 'buffer' ? '送料车' : '进厂车', true);
    } else {
      tile('count', a.type === 'fgzone' ? '暂存成品' : '托盘成品', true, true);
      tile('feeder', a.type === 'fgzone' ? '发运车' : '取货车', true);
    }

    const chartTabs = isMachine(a)
      ? `<div class="seg" data-chart-tabs><button type="button" data-metric="temp" class="on">温度</button><button type="button" data-metric="power">功率</button><button type="button" data-metric="vib">振动</button></div>`
      : '';
    const chartTitle = isMachine(a) ? '' : a.type === 'agv' ? '电量趋势' : '库存趋势';

    const ctl = [];
    const controllable = isMachine(a) || isVehicle(a);
    if (controllable) {
      ctl.push(`<div class="btn-row">
        <button type="button" class="btn primary" data-cmd="toggle"></button>
        ${a.type !== 'truck' ? '<button type="button" class="btn" data-cmd="maint"></button>' : ''}
        <button type="button" class="btn" data-cmd="reset">复位</button>
        <button type="button" class="btn danger" data-cmd="fault">模拟故障</button>
      </div>`);
      if (a.type !== 'truck') {
        ctl.push(`<label class="slider">速度<input type="range" min="50" max="150" step="5" data-speed /><output data-speed-out></output></label>`);
        ctl.push(`<p class="hint">${a.type === 'agv' ? '提速会更快耗电。' : '提速缩短节拍，但会升温并加快磨损，故障率随之上升。'}</p>`);
      }
    } else if (a.type === 'warehouse') {
      ctl.push('<div class="btn-row"><button type="button" class="btn primary" data-cmd="restock">原料入库 400 件</button></div>');
    } else if (a.type === 'fgzone') {
      ctl.push('<div class="btn-row"><button type="button" class="btn primary" data-cmd="ship">立即发运</button></div>');
    }

    return `
      <div class="insp-head">
        <span class="insp-icon">${ICON[a.type] ?? ''}</span>
        <div class="insp-title"><small>${esc(where)}${kind ? ` ${esc(kind)}` : ''}</small><h2>${esc(a.name)}</h2><p>${a.id}</p></div>
        <div class="insp-actions">
          <button type="button" class="icon-btn" data-act="focus" title="聚焦 (F)" aria-label="聚焦">${SVG_FOCUS}</button>
          ${isVehicle(a) ? `<button type="button" class="icon-btn" data-act="follow" title="跟随" aria-label="跟随" aria-pressed="false">${SVG_FOLLOW}</button>` : ''}
          <button type="button" class="icon-btn" data-act="close" title="关闭 (Esc)" aria-label="关闭">${SVG_CLOSE}</button>
        </div>
      </div>
      <div class="status-row"><span class="chip" data-chip></span><span data-status-note></span></div>
      <div class="fault-box" data-fault hidden></div>
      <div class="tiles">${tiles.join('')}</div>
      <div class="chart"><div class="chart-head"><b>${chartTitle}</b>${chartTabs}<span data-chart-note></span></div><canvas data-chart></canvas></div>
      ${ctl.length ? `<div class="controls">${ctl.join('')}</div>` : ''}
      <div class="events"><h3>最近记录</h3><ul data-events></ul></div>`;
  }

  _bindInspector(a) {
    const el = this.insp.el;
    el.addEventListener('click', (e) => {
      const b = e.target.closest('button');
      if (!b) return;
      if (b.dataset.act === 'close') this.act.select(null);
      if (b.dataset.act === 'focus') this.act.focus(a.id);
      if (b.dataset.act === 'follow') this.act.toggleFollow();
      if (b.dataset.metric) {
        this.chartMetric = b.dataset.metric;
        for (const x of el.querySelectorAll('[data-metric]')) x.classList.toggle('on', x === b);
        this._drawChart();
      }
      const cmd = b.dataset.cmd;
      if (cmd) {
        const real = cmd === 'toggle' ? (a.enabled ? 'stop' : 'start') : cmd;
        this.act.command(a.id, real);
        this.renderInspector();
      }
    });
    const range = $('[data-speed]', el);
    if (range) {
      range.value = Math.round(a.speed * 100);
      const show = () => {
        $('[data-speed-out]', el).textContent = `${range.value}%`;
        range.style.setProperty('--p', `${(range.value - 50)}%`);
      };
      show();
      range.addEventListener('input', show);
      range.addEventListener('change', () => this.act.command(a.id, 'speed', range.value / 100));
    }
  }

  renderInspector() {
    if (!this.insp) return;
    const { el, a } = this.insp;
    const set = (k, html) => { const n = $(`[data-v="${k}"]`, el); if (n && n.innerHTML !== html) n.innerHTML = html; };
    const bar = (k, frac, cls = '') => {
      const n = $(`[data-bar="${k}"]`, el);
      if (!n) return;
      n.style.width = `${Math.max(0, Math.min(1, frac)) * 100}%`;
      n.className = cls;
    };
    const chip = $('[data-chip]', el);
    chip.className = `chip ${a.state}`;
    chip.textContent = STATE_LABEL[a.state] ?? a.state;
    const note = $('[data-status-note]', el);
    const fb = $('[data-fault]', el);
    fb.hidden = !a.fault;
    if (a.fault) fb.innerHTML = `<b>${esc(a.fault.text)}</b>${hhmm(a.fault.at, true)} 触发。排除原因后点“复位”恢复${isMachine(a) ? `，温度需低于 ${a.spec.trip - 4}°C` : ''}。`;

    if (isMachine(a)) {
      const s = a.spec;
      set('temp', `${a.temp.toFixed(1)}<u>°C</u>`);
      bar('temp', (a.temp - 20) / (s.trip - 20), a.temp > s.warn ? 'bad' : a.temp > s.warn - 8 ? 'warn' : 'ok');
      set('power', `${a.power.toFixed(1)}<u>kW</u>`);
      set('health', `${(a.health * 100).toFixed(0)}<u>%</u>`);
      bar('health', a.health, a.health < 0.35 ? 'bad' : a.health < 0.6 ? 'warn' : 'ok');
      set('util', `${(a.util * 100).toFixed(0)}<u>%</u>`);
      bar('util', a.util);
      set('cycle', a.cycle ? `${(a.cycle / a.speed).toFixed(1)}<u>秒</u>` : `${(0.55 * a.speed).toFixed(2)}<u>m/s</u>`);
      set('cycles', `${a.cycles.toLocaleString()}<u>次</u>`);
      note.textContent = a.maint > 0 ? `维护剩余 ${Math.ceil(a.maint)} 秒`
        : a.state === 'running' && a.cycle ? `本件进度 ${(a.progress * 100).toFixed(0)}%`
        : a.state === 'idle' ? '等待上游来料' : a.state === 'blocked' ? '下游未放行' : '';
    } else if (a.type === 'agv') {
      const g = a.agv;
      set('battery', `${a.battery.toFixed(0)}<u>%</u>`);
      bar('battery', a.battery / 100, a.battery < 18 ? 'bad' : a.battery < 35 ? 'warn' : 'ok');
      set('vel', `${(a.vel ?? 0).toFixed(2)}<u>m/s</u>`);
      set('task', esc(g.task || '空闲'));
      set('cargo', g.cargo ? `${g.cargo.n}<u>${g.cargo.kind === 'raw' ? '件原料' : '箱成品'}</u>` : '空车');
      set('power', `${a.power.toFixed(2)}<u>kW</u>`);
      note.textContent = `位置 x ${g.x.toFixed(1)}  z ${g.z.toFixed(1)}`;
    } else if (a.type === 'truck') {
      const t = a.truck;
      set('carrier', esc(a.carrier));
      set('load', a.kind === 'in' ? `${t.phase === 'away' ? 0 : t.load}<u>件原料</u>` : `${t.load}<u>箱成品</u>`);
      set('task', esc(a.task || STATE_LABEL[a.state]));
      note.textContent = a.kind === 'in' ? '原料进厂' : '成品出厂';
    } else if (a.type === 'buffer' || a.type === 'warehouse') {
      set('stock', `${a.stock}<u>/ ${a.cap} 件</u>`);
      bar('stock', a.stock / a.cap, a.stock / a.cap < 0.15 ? 'bad' : a.stock / a.cap < 0.3 ? 'warn' : 'ok');
      const feeder = a.type === 'buffer'
        ? this.sim.agvs.find((g) => g.route.some((w) => w.line === a.line.id && w.act === 'drop'))?.asset
        : this.sim.trucks.find((t) => t.kind === 'in')?.asset;
      set('feeder', feeder ? `${feeder.id} <u>${esc(feeder.task || STATE_LABEL[feeder.state])}</u>` : '');
      note.textContent = '';
    } else {
      const cap = a.type === 'fgzone' ? 288 : a.cap;
      set('count', `${a.count}<u>/ ${cap} 箱</u>`);
      bar('count', a.count / cap, a.count >= cap ? 'bad' : 'ok');
      const feeder = a.type === 'fgzone'
        ? this.sim.trucks.find((t) => t.kind === 'out')?.asset
        : this.sim.agvs.find((g) => g.route.some((w) => w.line === a.line.id && w.act === 'pick'))?.asset;
      set('feeder', feeder ? `${feeder.id} <u>${esc(feeder.task || STATE_LABEL[feeder.state])}</u>` : '');
      note.textContent = '';
    }

    const tgl = $('[data-cmd="toggle"]', el);
    if (tgl) tgl.textContent = a.enabled ? '停止' : '启动';
    const mt = $('[data-cmd="maint"]', el);
    if (mt) mt.textContent = a.maint > 0 ? '结束维护' : '维护';
    const rs = $('[data-cmd="reset"]', el);
    if (rs) rs.disabled = !a.fault;
    const fl = $('[data-cmd="fault"]', el);
    if (fl) fl.disabled = !!a.fault;
    const fol = $('[data-act="follow"]', el);
    if (fol) fol.setAttribute('aria-pressed', String(this.state.follow));

    const ev = this.sim.log.filter((x) => x.asset === a.id).slice(0, 4);
    const evHtml = ev.length
      ? ev.map((x) => `<li class="${x.kind}"><time>${hhmm(x.at)}</time><span>${esc(x.text)}</span></li>`).join('')
      : '<li><time></time><span>本班暂无记录</span></li>';
    const ul = $('[data-events]', el);
    if (ul.innerHTML !== evHtml) ul.innerHTML = evHtml;
    this._drawChart();
  }

  _drawChart() {
    if (!this.insp) return;
    const { el, a } = this.insp;
    const cv = $('[data-chart]', el);
    const data = a.hist[this.chartMetric] ?? [];
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    const W = cv.clientWidth;
    const H = cv.clientHeight;
    if (!W) return;
    if (cv.width !== W * dpr) { cv.width = W * dpr; cv.height = H * dpr; }
    const g = cv.getContext('2d');
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, W, H);
    const noteEl = $('[data-chart-note]', el);
    if (data.length < 2) {
      g.fillStyle = '#8f97b2';
      g.font = '12px "Plus Jakarta Sans", sans-serif';
      g.fillText('正在采集数据', 4, H / 2);
      return;
    }
    const lines = [];
    if (this.chartMetric === 'temp' && a.spec?.warn) lines.push([a.spec.warn, '#f2a516', '报警'], [a.spec.trip, '#e5383b', '停机']);
    let lo = Math.min(...data);
    let hi = Math.max(...data);
    for (const [v] of lines) { lo = Math.min(lo, v); hi = Math.max(hi, v); }
    if (this.chartMetric === 'level') { lo = 0; hi = Math.max(hi, a.cap ?? 100); }
    const pad = (hi - lo) * 0.12 || 1;
    lo -= pad;
    hi += pad;
    const X = (i) => (i / (240 - 1)) * W;
    const off = 240 - data.length;
    const Y = (v) => H - 4 - ((v - lo) / (hi - lo)) * (H - 8);
    for (const [v, c] of lines) {
      g.strokeStyle = c;
      g.setLineDash([4, 4]);
      g.lineWidth = 1;
      g.beginPath();
      g.moveTo(0, Y(v));
      g.lineTo(W, Y(v));
      g.stroke();
    }
    g.setLineDash([]);
    const grad = g.createLinearGradient(0, 0, 0, H);
    grad.addColorStop(0, 'rgba(47,91,234,0.28)');
    grad.addColorStop(1, 'rgba(47,91,234,0)');
    g.beginPath();
    data.forEach((v, i) => (i ? g.lineTo(X(i + off), Y(v)) : g.moveTo(X(i + off), Y(v))));
    g.lineTo(X(data.length - 1 + off), H);
    g.lineTo(X(off), H);
    g.closePath();
    g.fillStyle = grad;
    g.fill();
    g.beginPath();
    data.forEach((v, i) => (i ? g.lineTo(X(i + off), Y(v)) : g.moveTo(X(i + off), Y(v))));
    g.strokeStyle = '#2f5bea';
    g.lineWidth = 2;
    g.lineJoin = 'round';
    g.stroke();
    const last = data[data.length - 1];
    g.fillStyle = '#2f5bea';
    g.beginPath();
    g.arc(X(data.length - 1 + off), Y(last), 3.2, 0, Math.PI * 2);
    g.fill();
    const unit = { temp: '°C', power: 'kW', vib: 'mm/s', level: a.type === 'agv' ? '%' : '' }[this.chartMetric];
    noteEl.textContent = `近 4 分钟  ${last.toFixed(this.chartMetric === 'vib' ? 2 : 0)} ${unit}`;
  }

  // ---------- tracker ----------

  renderTracker(force = false) {
    const sim = this.sim;
    const line = sim.lineById[this.trackerLine];
    const o = ORDERS[line.id];
    const k = sim.lineKpi(line);
    const done = Math.min(o.qty, line.good);
    const remain = o.qty - done;
    const eta = k.tp > 0 ? sim.clock + (remain / k.tp) * 3600 : null;
    const finished = done >= o.qty;
    const steps = [
      ['doc', '工单下达', o.issued, 'done'],
      ['box', '物料齐套', o.kitted, 'done'],
      ['gear', finished ? '生产完成' : '生产中', `${done} / ${o.qty}`, finished ? 'done' : 'now'],
      ['check', '质检入库', finished ? '已完成' : eta ? `预计 ${hhmm(eta)}` : '待定', finished ? 'now' : ''],
      ['truck', '发运', `良率 ${pct(k.quality, 1)}`, ''],
    ];
    const t = sim.trucks.find((x) => x.kind === 'out');
    const ta = t.asset;
    const html = `
      <div>
        <div class="tr-head">
          <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="3" fill="#2f5bea"/><path d="M7 9h10M7 13h6" stroke="#fff" stroke-width="1.8" stroke-linecap="round"/></svg>
          <h3>工单跟踪</h3>
          <span class="kpi-sub">${o.no}  ${o.part}</span>
          <div class="seg">${sim.lines.map((l) => `<button type="button" data-line="${l.id}" class="${l.id === line.id ? 'on' : ''}">${l.name}</button>`).join('')}</div>
        </div>
        <div class="steps">${steps.map(([ic, label, sub, st]) => `<div class="step ${st}"><span class="dot">${STEP_ICON[ic]}</span><b>${label}</b><small>${sub}</small></div>`).join('')}</div>
      </div>
      <button type="button" class="ship" data-id="${ta.id}">
        <h4>${ta.id}</h4><span class="arrow">${CHEV}</span>
        <p>${esc(ta.carrier)}，${ta.state === 'away' ? '成品出厂车' : `装载 ${t.load} 箱`}</p>
        <span class="chip ${ta.state}">${STATE_LABEL[ta.state]}</span>
        <p>${esc(ta.task || '')}</p>
      </button>`;
    if (force || html !== this._lastHtml.tracker) {
      this._lastHtml.tracker = html;
      $('#tracker').innerHTML = html;
    }
  }

  // ---------- fleet table ----------

  renderFleet(force = false) {
    const sim = this.sim;
    const sel = this.state.selected;
    const all = [...sim.assets.values()];
    const machines = all.filter(isMachine);
    const vehicles = all.filter(isVehicle);
    const alarms = sim.activeAlarms();
    const running = machines.filter((a) => a.state === 'running').length;
    $('#t-machines').textContent = `${running}/${machines.length}`;
    $('#t-vehicles').textContent = `${vehicles.filter((a) => a.state === 'moving' || a.state === 'handling').length}/${vehicles.length}`;
    $('#t-alarms').textContent = alarms.length;
    $('#fleet-tabs [data-tab="alarms"]').classList.toggle('has-alarm', alarms.some((x) => !x.acked));
    for (const b of document.querySelectorAll('#fleet-tabs button')) b.classList.toggle('on', b.dataset.tab === this.tab);
    $('#ack-all').hidden = !(this.tab === 'alarms' && alarms.some((x) => !x.acked));

    let html = '';
    if (this.tab === 'machines') {
      html = machines.map((a) => `
        <button type="button" class="row${a.id === sel ? ' sel' : ''}" data-id="${a.id}">
          <span class="id"><b>${a.id}</b><small>${esc(groupName(a))}</small></span>
          <span class="what"><span class="chip ${a.state}">${STATE_LABEL[a.state]}</span><span>${a.temp.toFixed(0)}°C</span></span>
          <span class="mini"><span>稼动 ${(a.util * 100).toFixed(0)}%</span><span class="bar"><i style="width:${(a.util * 100).toFixed(0)}%"></i></span></span>
          <span class="chev">${CHEV}</span>
        </button>`).join('');
    } else if (this.tab === 'vehicles') {
      html = vehicles.map((a) => {
        const right = a.type === 'agv'
          ? `<span class="mini"><span>${a.battery.toFixed(0)}%</span><span class="bar"><i style="width:${a.battery.toFixed(0)}%;${a.battery < 20 ? 'background:#e5383b' : ''}"></i></span></span>`
          : `<span class="chip ${a.state}">${STATE_LABEL[a.state]}</span>`;
        return `
        <button type="button" class="row${a.id === sel ? ' sel' : ''}" data-id="${a.id}">
          <span class="id"><b>${a.id}</b><small>${esc(a.type === 'agv' ? a.name : a.carrier)}</small></span>
          <span class="what"><i style="background:${a.state === 'fault' ? '#e5383b' : a.state === 'moving' ? '#2f5bea' : '#f2b33d'}"></i><span>${esc(a.task || STATE_LABEL[a.state])}</span></span>
          ${right}
          <span class="chev">${CHEV}</span>
        </button>`;
      }).join('');
    } else if (this.tab === 'alarms') {
      html = alarms.length ? alarms.map((al) => `
        <div class="row alarm${al.acked ? '' : ' unacked'}${al.active ? '' : ' cleared'}" data-id="${al.asset}" role="button" tabindex="0">
          <span class="prio p${al.prio}"></span>
          <span class="txt"><b>${esc(al.text)}</b><small>${al.asset}  ${hhmm(al.at, true)}${al.active ? '' : '  已恢复'}</small></span>
          ${al.acked ? '<small class="kpi-sub">已确认</small>' : `<button type="button" class="ack" data-ack="${al.id}">确认</button>`}
        </div>`).join('') : '<div class="empty-note">没有未处理的报警。在设备详情里点“模拟故障”可以演练处置流程。</div>';
    } else {
      html = sim.log.slice(0, 40).map((x) => `
        <div class="logrow ${x.kind}" data-id="${x.asset}"><time>${hhmm(x.at, true)}</time><b>${x.asset}</b><span>${esc(x.text)}</span></div>`).join('')
        || '<div class="empty-note">本班还没有事件。</div>';
    }
    if (force || html !== this._lastHtml.fleet) {
      this._lastHtml.fleet = html;
      $('#fleet-body').innerHTML = html;
    }
  }

  // ---------- transient ----------

  tooltip(a, x, y) {
    const tip = $('#tip');
    if (!a) { tip.hidden = true; return; }
    tip.hidden = false;
    tip.style.left = `${x}px`;
    tip.style.top = `${y}px`;
    const extra = isMachine(a) ? `${a.temp.toFixed(0)}°C  ${a.power.toFixed(1)} kW`
      : a.type === 'agv' ? `电量 ${a.battery.toFixed(0)}%`
      : a.type === 'buffer' || a.type === 'warehouse' ? `库存 ${a.stock}`
      : a.count !== undefined ? `${a.count} 箱` : a.task ?? '';
    tip.innerHTML = `<b>${a.id}</b> ${esc(a.name)}<br><small>${STATE_LABEL[a.state] ?? ''}  ${esc(extra)}</small>`;
  }

  toast(text, kind = 'ok', id = null) {
    const box = $('#toasts');
    const el = document.createElement('div');
    el.className = `toast ${kind}`;
    el.innerHTML = `<i></i>${esc(text)}`;
    if (id) { el.style.pointerEvents = 'auto'; el.style.cursor = 'pointer'; el.onclick = () => this.act.select(id, true); }
    box.prepend(el);
    while (box.children.length > 3) box.lastChild.remove();
    setTimeout(() => el.remove(), kind === 'alarm' ? 5000 : 2600);
  }
}
