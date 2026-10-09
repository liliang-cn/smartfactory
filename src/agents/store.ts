// Whether the assistants view is open, which assistant and conversation it shows, and the answer
// being written.
import { create } from 'zustand';
import { useBiz } from '@/advisor/store';
import { ask, getConversation, type ChatMessage } from './api';

export interface Step { tool: string; args?: Record<string, unknown>; result?: unknown; done: boolean }
export interface Turn {
  id: string; q: string; status: 'running' | 'done' | 'error' | 'stopped';
  steps: Step[]; text: string; error?: string; start: number; end?: number;
}

interface AgentsState {
  open: boolean;
  agent: string | null;
  conv: string | null;
  turns: Turn[];
  ctrl: AbortController | null;
  /** bumped when an answer ends: what the assistant works on may have changed */
  rev: number;
  setOpen(open: boolean): void;
  select(company: string, agent: string): void;
  newChat(): void;
  openConv(company: string, agent: string, conv: string): Promise<void>;
  send(company: string, text: string): void;
  stop(): void;
  refresh(): void;
}

const remembered = (company: string) => {
  try { return localStorage.getItem(`sf-agent-${company}`); } catch { return null; }
};

/** A kept conversation as turns: each question with the answer that followed it. */
function toTurns(msgs: ChatMessage[]): Turn[] {
  const out: Turn[] = [];
  for (const m of msgs) {
    const at = Date.parse(m.at);
    if (m.role === 'user') {
      out.push({ id: String(m.id), q: m.text, status: 'done', steps: [], text: '', start: at, end: at });
      continue;
    }
    const t = out.at(-1);
    if (!t) continue;
    t.steps = (m.steps ?? []).map((s) => ({ ...s, done: true }));
    t.text = m.status === 'failed' ? '' : m.text;
    t.error = m.status === 'failed' ? m.text : undefined;
    t.status = m.status === 'failed' ? 'error' : m.status === 'running' || m.status === 'stopped' ? 'stopped' : 'done';
    t.end = at;
  }
  return out;
}

export const useAgents = create<AgentsState>()((set, get) => {
  const patch = (id: string, f: (t: Turn) => Turn) => set((s) => ({ turns: s.turns.map((t) => (t.id === id ? f(t) : t)) }));
  return {
    open: new URLSearchParams(location.search).get('view') === 'agents',
    agent: null,
    conv: null,
    turns: [],
    ctrl: null,
    rev: 0,
    setOpen(open) {
      set({ open });
      if (open) useBiz.getState().setOpen(false);
      const url = new URL(location.href);
      if (open) url.searchParams.set('view', 'agents');
      else if (url.searchParams.get('view') === 'agents') url.searchParams.delete('view');
      history.replaceState(null, '', url);
    },
    select(company, agent) {
      if (get().agent === agent) return;
      get().ctrl?.abort();
      set({ agent, conv: null, turns: [], ctrl: null });
      try { localStorage.setItem(`sf-agent-${company}`, agent); } catch { /* storage blocked */ }
    },
    newChat() {
      if (get().ctrl) return;
      set({ conv: null, turns: [] });
    },
    async openConv(company, agent, conv) {
      if (get().ctrl) return;
      const v = await getConversation(company, agent, conv);
      if (get().agent !== agent) return;
      set({ conv, turns: toTurns(v.messages) });
    },
    send(company, text) {
      const { agent, conv } = get();
      if (!agent || get().ctrl) return;
      const id = `t${Date.now()}`;
      const ctrl = new AbortController();
      set((s) => ({ ctrl, turns: [...s.turns, { id, q: text, status: 'running', steps: [], text: '', start: Date.now() }] }));
      const finish = (f: (t: Turn) => Turn) => {
        patch(id, (t) => (t.status === 'running' ? { ...f(t), end: Date.now() } : t));
        set((s) => ({ ctrl: s.ctrl === ctrl ? null : s.ctrl, rev: s.rev + 1 }));
      };
      ask(company, agent, conv ?? undefined, text, ctrl.signal, (ev) => {
        if (get().agent !== agent) return;
        switch (ev.kind) {
          case 'conversation':
            set({ conv: ev.id });
            break;
          case 'tool_call':
            patch(id, (t) => ({ ...t, steps: [...t.steps, { tool: ev.tool, args: ev.args, done: false }] }));
            break;
          case 'tool_result':
            patch(id, (t) => {
              const steps = [...t.steps];
              const i = steps.findLastIndex((s) => s.tool === ev.tool && !s.done);
              if (i >= 0) steps[i] = { ...steps[i], result: ev.result, done: true };
              return { ...t, steps };
            });
            break;
          case 'delta':
            patch(id, (t) => ({ ...t, text: t.text + ev.text }));
            break;
          case 'reset':
            patch(id, (t) => ({ ...t, text: '' }));
            break;
          case 'complete':
            finish((t) => ({ ...t, status: 'done', text: ev.text || t.text }));
            break;
          case 'error':
            finish((t) => ({ ...t, status: 'error', error: ev.text }));
            break;
        }
      })
        .then(() => finish((t) => ({ ...t, status: t.text ? 'done' : 'stopped' })))
        .catch((e: Error) => finish((t) => (ctrl.signal.aborted ? { ...t, status: 'stopped' } : { ...t, status: 'error', error: e.message })));
    },
    stop() {
      get().ctrl?.abort();
    },
    refresh: () => set((s) => ({ rev: s.rev + 1 })),
  };
});

export { remembered };

// the business view and this one share the space under the top bar
useBiz.subscribe((s) => {
  if (s.open && useAgents.getState().open) useAgents.getState().setOpen(false);
});
