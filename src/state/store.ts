// Runtime of the loaded company (one simulation per plant, all running) and the UI state
// shared by the React panels and the R3F scene. React re-reads the mutable sims when `tick`
// bumps a few times a second.
import { create } from 'zustand';
import { toast } from 'sonner';
import { requestTimeScale, sendCommand } from '@/live/client';
import type { CompanyBundle } from '@/config/loader';
import type { PlantLayout } from '@/core/layout';
import { PlantSim } from '@/sim/plant';
import type { Command } from '@/sim/types';
import type { ModelHandle } from '@/three/common';

export interface PlantRuntime { layout: PlantLayout; sim: PlantSim }
export interface Runtime { bundle: CompanyBundle; plants: PlantRuntime[]; byId: Record<string, PlantRuntime> }

let rt: Runtime | null = null;

export function initRuntime(bundle: CompanyBundle): Runtime {
  const plants = bundle.plants.map((layout) => ({ layout, sim: new PlantSim(layout) }));
  rt = { bundle, plants, byId: Object.fromEntries(plants.map((p) => [p.layout.id, p])) };
  Object.assign(window, { twin: { rt, ui: useUI, models } });
  return rt;
}

export const runtime = (): Runtime => rt!;
/** The plant on screen (non-hook; safe inside event handlers and frame loops). */
export const currentPlant = (): PlantRuntime => rt!.byId[useUI.getState().plantId] ?? rt!.plants[0];
export const currentSim = () => currentPlant().sim;

/** Model handles by asset id, filled by the scene so the camera can frame things. */
export const models = new Map<string, ModelHandle>();
/** DOM name tags by asset id, positioned each frame by the scene. */
export const labelEls = new Map<string, HTMLElement>();

export type ViewMode = 'live' | 'heat' | 'util';
export type CamOp = 'in' | 'out' | 'left' | 'right' | 'home' | 'focus';

interface UIState {
  plantId: string;
  selected: string | null;
  hovered: string | null;
  tip: { id: string; x: number; y: number } | null;
  follow: boolean;
  timeScale: number;
  mode: ViewMode;
  labels: boolean;
  tick: number;
  cam: { op: CamOp; id?: string; n: number } | null;
  fleetTab: string;
  sheetOpen: boolean;
  trackerLine: string;

  setPlant(id: string): void;
  select(id: string | null, focus?: boolean): void;
  hover(id: string | null, x?: number, y?: number): void;
  setTimeScale(s: number): void;
  setMode(m: ViewMode): void;
  toggleLabels(): void;
  toggleFollow(): void;
  camera(op: CamOp, id?: string): void;
  command(id: string, cmd: Command, arg?: number): void;
  setFleetTab(tab: string, open?: boolean): void;
  setSheetOpen(open: boolean): void;
  setTrackerLine(id: string): void;
  bump(): void;
}

export const useUI = create<UIState>()((set, get) => ({
  plantId: '',
  selected: null,
  hovered: null,
  tip: null,
  follow: false,
  timeScale: 1,
  mode: 'live',
  labels: false,
  tick: 0,
  cam: null,
  fleetTab: 'machines',
  sheetOpen: false,
  trackerLine: '',

  setPlant(id) {
    const p = rt?.byId[id];
    if (!p) return;
    document.body.style.cursor = '';
    set({ plantId: id, selected: null, hovered: null, tip: null, follow: false, trackerLine: p.layout.lines[0].id, sheetOpen: false });
    const url = new URL(location.href);
    url.searchParams.set('company', rt!.bundle.company.id);
    url.searchParams.set('plant', id);
    history.replaceState(null, '', url);
    document.title = `SmartFactory ${p.layout.name}`;
  },
  select(id, focus = false) {
    const sim = currentSim();
    if (id && !sim.assets.has(id)) return;
    const a = id ? sim.assets.get(id)! : null;
    set({
      selected: id,
      follow: a && (a.agv || a.truck) ? get().follow : false,
      sheetOpen: false,
      ...(a?.line ? { trackerLine: a.line.id } : {}),
    });
    if (id && focus) get().camera('focus', id);
  },
  hover(id, x = 0, y = 0) {
    set({ hovered: id, tip: id ? { id, x, y } : null });
  },
  setTimeScale: (timeScale) => {
    if (!requestTimeScale(timeScale)) set({ timeScale });
  },
  setMode: (mode) => set({ mode }),
  toggleLabels: () => set((s) => ({ labels: !s.labels })),
  toggleFollow() {
    const follow = !get().follow;
    set({ follow });
    const id = get().selected;
    if (follow && id) get().camera('focus', id);
  },
  camera(op, id) {
    if (op === 'home') set({ follow: false });
    set((s) => ({ cam: { op, id, n: (s.cam?.n ?? 0) + 1 } }));
  },
  command(id, cmd, arg) {
    // Following the server: it applies the command and answers with the result.
    if (sendCommand(get().plantId, id, cmd, arg)) return;
    const r = currentSim().command(id, cmd, arg);
    if (r.ok) toast.success(r.msg);
    else toast.error(r.msg);
    get().bump();
  },
  setFleetTab(fleetTab, open) {
    set((s) => ({ fleetTab, sheetOpen: open ?? s.sheetOpen }));
  },
  setSheetOpen: (sheetOpen) => set({ sheetOpen }),
  setTrackerLine: (trackerLine) => set({ trackerLine }),
  bump: () => set((s) => ({ tick: s.tick + 1 })),
}));

/** Re-render the calling component whenever the sims publish a new snapshot. */
export const useTick = () => useUI((s) => s.tick);

/** The plant on screen; re-renders when it changes. */
export function usePlant(): PlantRuntime {
  const id = useUI((s) => s.plantId);
  return rt!.byId[id] ?? rt!.plants[0];
}

/** Sidebar groups for a plant: its lines, then logistics and storage. */
export function groupsOf(layout: PlantLayout) {
  return [
    ...layout.lines.map((l) => ({ id: l.id, name: l.name, note: l.note })),
    { id: 'LOG', name: '物流', note: 'AGV 与货车' },
    { id: 'WH', name: '仓储', note: '' },
  ];
}
