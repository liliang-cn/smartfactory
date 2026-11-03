// How the twin draws on this browser: frames per second, the page's memory, and what the 3D view
// draws each frame. PerfProbe (inside the canvas) measures it every second; the overlay shows it
// when asked (?stats=1 or the menu), and once a minute the page tells the server how it is doing
// (the console's 运行状况) — the numbers only, nothing about who is watching.
import { useEffect, useRef, useSyncExternalStore } from 'react';
import { useFrame, useThree } from '@react-three/fiber';
import { runtime } from '@/state/store';

export interface Perf {
  fps: number;
  heapMB: number;
  calls: number;
  triangles: number;
  textures: number;
  geometries: number;
  width: number;
  height: number;
  dpr: number;
  /** frames drawn since the page opened */
  frames: number;
}

let perf: Perf = { fps: 0, heapMB: 0, calls: 0, triangles: 0, textures: 0, geometries: 0, width: 0, height: 0, dpr: 1, frames: 0 };
/** the frame rate of each of the last 60 seconds the page was visible */
const seconds: number[] = [];
const subs = new Set<() => void>();
const emit = () => subs.forEach((f) => f());
const subscribe = (f: () => void) => { subs.add(f); return () => subs.delete(f); };

export const usePerf = () => useSyncExternalStore(subscribe, () => perf);

// the overlay: ?stats=1 opens it, the menu toggles it, the browser remembers
const KEY = 'sf.stats';
let shown = (() => {
  try {
    const q = new URLSearchParams(location.search).get('stats');
    if (q !== null) return q !== '0';
    return localStorage.getItem(KEY) === '1';
  } catch {
    return false;
  }
})();
const shownSubs = new Set<() => void>();
export const useStatsShown = () => useSyncExternalStore((f) => { shownSubs.add(f); return () => shownSubs.delete(f); }, () => shown);
export function toggleStats() {
  shown = !shown;
  try { localStorage.setItem(KEY, shown ? '1' : '0'); } catch { /* private window */ }
  shownSubs.forEach((f) => f());
}

const heapMB = () => {
  const m = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory;
  return m ? m.usedJSHeapSize / 1048576 : 0;
};

/** Counts frames and reads the renderer once a second. Goes inside the <Canvas>. */
export function PerfProbe() {
  const gl = useThree((s) => s.gl);
  const n = useRef(0);
  const t0 = useRef(performance.now());
  useEffect(() => startReporting(), []);
  useFrame(() => {
    n.current++;
    perf.frames++;
    const now = performance.now();
    const dt = now - t0.current;
    if (dt < 1000) return;
    const fps = (n.current * 1000) / dt;
    n.current = 0;
    t0.current = now;
    // a second that took far longer was the page coming back from the background
    if (dt < 2500) {
      seconds.push(fps);
      if (seconds.length > 60) seconds.shift();
    }
    const c = gl.domElement;
    perf = {
      fps, heapMB: heapMB(), calls: gl.info.render.calls, triangles: gl.info.render.triangles,
      textures: gl.info.memory.textures, geometries: gl.info.memory.geometries,
      width: c.width, height: c.height, dpr: gl.getPixelRatio(), frames: perf.frames,
    };
    emit();
  });
  return null;
}

let reporting = false;
/** Once a minute, while the page is visible, the median frame rate of the last minute and the rest. */
function startReporting() {
  if (reporting) return;
  reporting = true;
  let id = '';
  try {
    id = sessionStorage.getItem('sf.tab') ?? '';
    if (!id) sessionStorage.setItem('sf.tab', (id = crypto.randomUUID()));
  } catch {
    id = crypto.randomUUID();
  }
  setInterval(() => {
    if (document.hidden || seconds.length < 10) return;
    const sorted = [...seconds].sort((a, b) => a - b);
    let company = '';
    try { company = runtime().bundle.company.id; } catch { /* not loaded yet */ }
    const body = {
      id, fps: Math.round(sorted[Math.floor(sorted.length / 2)] * 10) / 10, heapMB: Math.round(perf.heapMB),
      width: perf.width, height: perf.height, dpr: perf.dpr, calls: perf.calls, triangles: perf.triangles,
      mobile: matchMedia('(pointer: coarse)').matches, company,
    };
    fetch('/api/health/browser', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), keepalive: true }).catch(() => {});
  }, 60_000);
}
