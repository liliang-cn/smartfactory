// Link to the SmartFactory server (when the app is served by it): values read from equipment
// over MQTT / Modbus TCP / OPC UA flow into the simulation, operator commands flow back out,
// and a snapshot of the twin goes up so MCP clients can see KPIs and alarms.
// Served as plain static files there is no server, and the app stays a pure simulation.
import { create } from 'zustand';
import { toast } from 'sonner';
import { runtime, useUI } from '@/state/store';
import type { Command } from '@/sim/types';

export interface SourceInfo {
  name: string;
  type: 'mqtt' | 'modbus' | 'opcua';
  url: string;
  connected: boolean;
  error?: string;
  points: number;
  received: number;
}

interface LiveState {
  status: 'off' | 'connecting' | 'on';
  /** opened with the operator token: commands reach the equipment */
  operator: boolean;
  sources: SourceInfo[];
  /** distinct asset fields received */
  fields: number;
}

export const useLive = create<LiveState>(() => ({ status: 'off', operator: false, sources: [], fields: 0 }));

interface Tag { c: string; p: string; a: string; f: string; v: number | string | boolean; src: string; t: number }
interface Cmd { c: string; p: string; a: string; cmd: Command; arg?: number; by: string; o?: string }
type ServerMsg =
  | { type: 'hello'; operator: boolean; sources: SourceInfo[]; tags: Tag[] }
  | { type: 'sources'; sources: SourceInfo[] }
  | { type: 'tag'; tag: Tag }
  | { type: 'cmd'; cmd: Cmd };

const me = Math.random().toString(36).slice(2, 10);
const seen = new Set<string>();
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

export function startLive() {
  connect(token(), 0);
}

function connect(tok: string | null, attempt: number) {
  const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/live${tok ? `?token=${encodeURIComponent(tok)}` : ''}`;
  useLive.setState({ status: 'connecting' });
  const s = new WebSocket(url);
  let opened = false;
  let timer = 0;
  s.onopen = () => {
    opened = true;
    ws = s;
    useLive.setState({ status: 'on' });
    sendSnapshot();
    timer = window.setInterval(sendSnapshot, 3000);
  };
  s.onmessage = (e) => handle(JSON.parse(e.data) as ServerMsg);
  s.onclose = () => {
    clearInterval(timer);
    if (ws === s) ws = null;
    useLive.setState({ status: 'off', sources: [] });
    // No server answered on the first tries: static hosting or dev without the server. Stay quiet.
    if (!opened && attempt >= 2) return;
    setTimeout(() => connect(tok, opened ? 0 : attempt + 1), Math.min(30_000, 1500 * 2 ** attempt));
  };
}

function handle(m: ServerMsg) {
  switch (m.type) {
    case 'hello':
      useLive.setState({ operator: m.operator, sources: m.sources });
      m.tags.forEach(apply);
      break;
    case 'sources':
      useLive.setState({ sources: m.sources });
      break;
    case 'tag':
      apply(m.tag);
      break;
    case 'cmd': {
      const c = m.cmd;
      if (c.o === me || c.c !== runtime().bundle.company.id) return;
      const p = runtime().byId[c.p];
      if (!p) return;
      const r = p.sim.command(c.a, c.cmd, c.arg);
      const who = c.by === 'mcp' ? 'AI 助手' : '操作员';
      (r.ok ? toast.info : toast.error)(`${who}：${r.msg}`);
      useUI.getState().bump();
      break;
    }
  }
}

function apply(t: Tag) {
  const rt = runtime();
  if (t.c !== rt.bundle.company.id) return;
  if (rt.byId[t.p]?.sim.ingest(t.a, t.f, t.v, t.src)) {
    const k = `${t.p}/${t.a}/${t.f}`;
    if (!seen.has(k)) {
      seen.add(k);
      useLive.setState({ fields: seen.size });
    }
  }
}

function sendSnapshot() {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  const rt = runtime();
  const c = rt.bundle.company;
  ws.send(JSON.stringify({ type: 'snapshot', company: c.id, data: { company: { id: c.id, name: c.name }, plants: rt.plants.map((p) => p.sim.snapshot()) } }));
}

/** Forwards an operator command to the server, which hands it to the protocol adapters. */
export function sendCommand(plant: string, asset: string, cmd: Command, arg?: number) {
  if (!ws || ws.readyState !== WebSocket.OPEN || !useLive.getState().operator) return;
  ws.send(JSON.stringify({ type: 'cmd', cmd: { c: runtime().bundle.company.id, p: plant, a: asset, cmd, arg, o: me } }));
}
