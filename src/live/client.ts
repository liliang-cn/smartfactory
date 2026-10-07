// Link to the SmartFactory server when the app is served by it. The server runs the
// simulation for everyone (and feeds it values read from equipment over MQTT / Modbus TCP /
// OPC UA); this browser follows it: each frame replaces the local state, and the local copy
// only keeps parts and vehicles moving smoothly in between. Commands and the plant clock go
// to the server. Served as plain static files there is no server, and the app simulates locally.
import { create } from 'zustand';
import { toast } from 'sonner';
import { runtime, useUI } from '@/state/store';
import type { PlantState } from '@/sim/plant';
import type { Asset, Command } from '@/sim/types';

export interface SourceInfo {
  name: string;
  type: 'mqtt' | 'modbus' | 'opcua';
  url: string;
  connected: boolean;
  error?: string;
  points: number;
  received: number;
  /** assets this source feeds, as company/plant/asset */
  targets?: string[];
  /** the same, named for people */
  labels?: string[];
}

interface LiveState {
  status: 'off' | 'connecting' | 'on';
  /** the server runs the simulation and this browser follows it */
  following: boolean;
  /** opened with the operator token: commands reach equipment, the clock can change */
  operator: boolean;
  sources: SourceInfo[];
}

export const useLive = create<LiveState>(() => ({ status: 'off', following: false, operator: false, sources: [] }));

interface Cmd { c: string; p: string; a: string; cmd: Command; arg?: number; by: string; o?: string }
type ServerMsg =
  | { type: 'hello'; operator: boolean; sim: boolean; sources: SourceInfo[] }
  | { type: 'sources'; sources: SourceInfo[] }
  | { type: 'frame'; data: { company: string; timeScale: number; plants: Record<string, PlantState> } }
  | { type: 'result'; ok: boolean; msg: string }
  | { type: 'hist'; plant: string; asset: string; data: Asset['hist'] | null }
  | { type: 'cmd'; cmd: Cmd };

const me = Math.random().toString(36).slice(2, 10);
let ws: WebSocket | null = null;

function token(): string | null {
  const params = new URLSearchParams(location.search);
  const t = params.get('token');
  if (t) {
    try { sessionStorage.setItem('sf-token', t); } catch { /* storage unavailable */ }
    params.delete('token');
    history.replaceState(null, '', `${location.pathname}${params.size ? `?${params}` : ''}${location.hash}`);
    return t;
  }
  try { return sessionStorage.getItem('sf-token'); } catch { return null; }
}

/** The operator token this tab was opened with, if any. */
export function operatorToken(): string | null {
  try { return sessionStorage.getItem('sf-token'); } catch { return null; }
}

const send = (m: object) => {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(m));
};

export function startLive() {
  connect(token(), 0);
  // Frames for the plant on screen come twice a second; the others less often.
  useUI.subscribe((s, prev) => {
    if (s.plantId !== prev.plantId && useLive.getState().following) send({ type: 'plant', plant: s.plantId });
    if (s.selected && s.selected !== prev.selected && useLive.getState().following) send({ type: 'hist', plant: s.plantId, asset: s.selected });
  });
}

function connect(tok: string | null, attempt: number) {
  const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/live${tok ? `?token=${encodeURIComponent(tok)}` : ''}`;
  useLive.setState({ status: 'connecting' });
  const s = new WebSocket(url);
  let opened = false;
  s.onopen = () => {
    opened = true;
    ws = s;
    useLive.setState({ status: 'on' });
  };
  s.onmessage = (e) => handle(JSON.parse(e.data) as ServerMsg);
  s.onclose = () => {
    if (ws === s) ws = null;
    // Keep showing the last state and simulate locally until the server is back.
    for (const p of runtime().plants) p.sim.follower = false;
    useLive.setState({ status: 'off', following: false, sources: [] });
    // No server answered on the first tries: static hosting or dev without the server. Stay quiet.
    if (!opened && attempt >= 2) return;
    setTimeout(() => connect(tok, opened ? 0 : attempt + 1), Math.min(30_000, 1500 * 2 ** attempt));
  };
}

function handle(m: ServerMsg) {
  const rt = runtime();
  switch (m.type) {
    case 'hello':
      useLive.setState({ operator: m.operator, sources: m.sources, following: m.sim });
      if (m.sim) {
        for (const p of rt.plants) {
          p.sim.follower = true;
          p.sim.synced = false;
        }
        const ui = useUI.getState();
        send({ type: 'watch', company: rt.bundle.company.id, plant: ui.plantId });
        if (ui.selected) send({ type: 'hist', plant: ui.plantId, asset: ui.selected });
      }
      break;
    case 'sources':
      useLive.setState({ sources: m.sources });
      break;
    case 'frame': {
      if (m.data.company !== rt.bundle.company.id) return;
      for (const [id, st] of Object.entries(m.data.plants)) rt.byId[id]?.sim.importState(st);
      if (useUI.getState().timeScale !== m.data.timeScale) useUI.setState({ timeScale: m.data.timeScale });
      useUI.getState().bump();
      break;
    }
    case 'result':
      (m.ok ? toast.success : toast.error)(m.msg);
      break;
    case 'hist': {
      const a = rt.byId[m.plant]?.sim.assets.get(m.asset);
      if (a && m.data) a.hist = m.data;
      break;
    }
    case 'cmd': {
      const c = m.cmd;
      if (c.o === me || c.c !== rt.bundle.company.id) return;
      toast.info(`${c.by === 'mcp' ? 'AI 助手' : '操作员'}：${c.a} ${CMD_LABEL[c.cmd] ?? c.cmd}`);
      break;
    }
  }
}

const CMD_LABEL: Partial<Record<Command, string>> = {
  start: '启动', stop: '停止', reset: '复位', maint: '切换维护', speed: '调速', fault: '故障演练', restock: '原料入库', ship: '成品发运',
};

/** Sends a command to the server when following it; false means apply it locally. */
export function sendCommand(plant: string, asset: string, cmd: Command, arg?: number): boolean {
  if (!useLive.getState().following) return false;
  send({ type: 'cmd', plant, asset, cmd, arg, o: me });
  return true;
}

/** Asks the server to change the shared plant clock; false means change it locally. */
export function requestTimeScale(scale: number): boolean {
  if (!useLive.getState().following) return false;
  send({ type: 'time', scale });
  return true;
}
