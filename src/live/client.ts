// Link to the SmartFactory server when the app is served by it. The server runs the
// simulation for everyone (and feeds it values read from equipment over MQTT / Modbus TCP /
// OPC UA); this browser follows it: each frame replaces the local state, and the local copy
// only keeps parts and vehicles moving smoothly in between. Commands and the plant clock go
// to the server. Served as plain static files there is no server, and the app simulates locally.
import { create } from 'zustand';
import { toast } from 'sonner';
import { runtime, useUI } from '@/state/store';
import { PROTOCOL, type ClientMessage, type Role, type ServerMessage, type SourceInfo } from '@/protocol/sfp';
import type { Command } from '@/sim/types';

export type { SourceInfo };

interface LiveState {
  status: 'off' | 'connecting' | 'on';
  /** the server runs the simulation and this browser follows it */
  following: boolean;
  /** an operator (or above) of this company: commands reach equipment, the clock can change */
  operator: boolean;
  /** the caller's role in this company */
  role: Role;
  /** the token holder's name; 访客 without one */
  name: string;
  sources: SourceInfo[];
}

export const useLive = create<LiveState>(() => ({ status: 'off', following: false, operator: false, role: 'visitor', name: '访客', sources: [] }));

const me = Math.random().toString(36).slice(2, 10);
let ws: WebSocket | null = null;
/** the server speaks a newer protocol than this page: stop reconnecting until it is reloaded */
let stale = false;

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

const send = (m: ClientMessage) => {
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
  // the company on screen, so the greeting says what this caller may do in it
  const q = new URLSearchParams({ company: runtime().bundle.company.id });
  if (tok) q.set('token', tok);
  const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/live?${q}`;
  useLive.setState({ status: 'connecting' });
  const s = new WebSocket(url);
  let opened = false;
  s.onopen = () => {
    opened = true;
    ws = s;
    useLive.setState({ status: 'on' });
  };
  s.onmessage = (e) => handle(JSON.parse(e.data) as ServerMessage);
  s.onclose = () => {
    if (ws === s) ws = null;
    // Keep showing the last state and simulate locally until the server is back.
    for (const p of runtime().plants) p.sim.follower = false;
    useLive.setState({ status: 'off', following: false, sources: [] });
    // No server answered on the first tries: static hosting or dev without the server. Stay quiet.
    if (stale || (!opened && attempt >= 2)) return;
    setTimeout(() => connect(tok, opened ? 0 : attempt + 1), Math.min(30_000, 1500 * 2 ** attempt));
  };
}

function handle(m: ServerMessage) {
  const rt = runtime();
  switch (m.type) {
    case 'hello':
      // a page cached from before the server changed its protocol: frames would be misread
      if (m.protocol !== PROTOCOL) {
        stale = true;
        toast.error('服务器已更新，请刷新页面', { duration: Infinity });
        ws?.close();
        return;
      }
      useLive.setState({ operator: m.operator, role: m.role, name: m.name, sources: m.sources, following: m.sim });
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
      toast.info(`${c.n ?? (c.by === 'mcp' ? 'AI 助手' : '操作员')}${c.by === 'mcp' ? '（经 MCP）' : ''}：${c.a} ${CMD_LABEL[c.cmd] ?? c.cmd}`);
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
