// Shared palette, materials and geometry helpers for the toy-like "strategy game" look.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';

// Industrial palette, matched to the NVIDIA SimReady models the twin also draws: concrete floor,
// enamelled light-grey machines, steel-blue frames, safety yellow and orange.
export const P = {
  ground: '#e3e4e1',
  slab: '#c8c9c4',
  road: '#c7c9c6',
  white: '#e9ebec',
  shell: '#dadde0',
  blue: '#2c5799',
  blueDeep: '#1e3a66',
  blueSoft: '#9cb0cf',
  yellow: '#f0b400',
  orange: '#e0752d',
  kraft: '#c99a63',
  kraftDark: '#ad7f4b',
  wood: '#b98d5c',
  grey: '#a6abb2',
  greyDark: '#4b5058',
  ink: '#26292e',
  glass: '#b8c8de',
  mint: '#cfe3d6',
  tree: '#5fae78',
  treeDark: '#468f5e',
  trunk: '#9c7552',
  metal: '#b5bac0',
  green: '#22b573',
  amber: '#f5a524',
  red: '#ef4444',
  violet: '#8b5cf6',
};

/** Resolve a palette token (see config/schema Color) or pass a hex through. */
// Packs written for the earlier toy palette name its cobalt and yellow by hex; draw them in the
// industrial ones.
const LEGACY: Record<string, string> = { '#2f5bea': P.blue, '#1f3fb0': P.blueDeep, '#f6c445': P.yellow, '#f29b38': P.orange, '#b7c1d8': P.metal };
export const colorOf = (c: string | undefined, fallback: string = P.white): string => {
  if (!c) return fallback;
  const v = (P as Record<string, string>)[c] ?? c;
  return LEGACY[v.toLowerCase()] ?? v;
};
/** Default trim of a machine: graphite, like painted steel guarding. */
export const TRIM = '#454b54';

const std = (color: THREE.ColorRepresentation, o: THREE.MeshStandardMaterialParameters = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.58, metalness: 0.12, ...o });

export const M = {
  white: std(P.white, { roughness: 0.5 }),
  shell: std(P.shell),
  blue: std(P.blue, { roughness: 0.45 }),
  blueDeep: std(P.blueDeep, { roughness: 0.45 }),
  blueSoft: std(P.blueSoft),
  yellow: std(P.yellow, { roughness: 0.45 }),
  orange: std(P.orange, { roughness: 0.45 }),
  kraft: std(P.kraft, { roughness: 0.8 }),
  kraftDark: std(P.kraftDark, { roughness: 0.8 }),
  wood: std(P.wood, { roughness: 0.85 }),
  grey: std(P.grey),
  greyDark: std(P.greyDark, { roughness: 0.5 }),
  ink: std(P.ink, { roughness: 0.4 }),
  metal: std(P.metal, { roughness: 0.32, metalness: 0.85 }),
  rubber: std('#2d3348', { roughness: 0.9 }),
  glass: std(P.glass, { roughness: 0.08, metalness: 0.1, transparent: true, opacity: 0.38, depthWrite: false }),
  tree: std(P.tree, { roughness: 0.75 }),
  treeDark: std(P.treeDark, { roughness: 0.75 }),
  trunk: std(P.trunk, { roughness: 0.9 }),
  mint: std(P.mint, { roughness: 0.95 }),
};

export const bodyMat = (color: THREE.ColorRepresentation = P.white) => std(color, { roughness: 0.42, metalness: 0.22 });

const geoCache = new Map<string, THREE.BufferGeometry>();
export function rbox(w: number, h: number, d: number, r = 0.06): THREE.BufferGeometry {
  const rr = Math.min(r * 0.3, w / 2 - 0.001, h / 2 - 0.001, d / 2 - 0.001);
  const key = `rb:${w}:${h}:${d}:${rr}`;
  if (!geoCache.has(key)) {
    const geo = rr > 0.005 ? new RoundedBoxGeometry(w, h, d, 2, rr) : new THREE.BoxGeometry(w, h, d);
    geo.userData.h = h;
    geoCache.set(key, geo);
  }
  return geoCache.get(key)!;
}
export function cylGeo(rt: number, rb: number, h: number, seg = 20): THREE.BufferGeometry {
  const key = `cy:${rt}:${rb}:${h}:${seg}`;
  if (!geoCache.has(key)) geoCache.set(key, new THREE.CylinderGeometry(rt, rb, h, seg));
  return geoCache.get(key)!;
}

export function mesh(parent: THREE.Object3D | null, geo: THREE.BufferGeometry, mat: THREE.Material, x = 0, y = 0, z = 0, shadow = true): THREE.Mesh {
  const m = new THREE.Mesh(geo, mat);
  m.position.set(x, y, z);
  m.castShadow = shadow;
  m.receiveShadow = true;
  parent?.add(m);
  return m;
}

// Rounded block whose `y` is its bottom face, which is how one thinks when stacking parts.
export const blk = (parent: THREE.Object3D | null, mat: THREE.Material, w: number, h: number, d: number, x = 0, y = 0, z = 0, r = 0.06) => mesh(parent, rbox(w, h, d, r), mat, x, y + h / 2, z);
export const cyl = (parent: THREE.Object3D | null, mat: THREE.Material, r: number, h: number, x = 0, y = 0, z = 0, seg = 20) => mesh(parent, cylGeo(r, r, h, seg), mat, x, y + h / 2, z);

export function canvasTex(w: number, h: number, draw: (g: CanvasRenderingContext2D, w: number, h: number) => void): THREE.CanvasTexture {
  const c = document.createElement('canvas');
  c.width = w;
  c.height = h;
  draw(c.getContext('2d')!, w, h);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 8;
  return t;
}

// Cool blue -> yellow -> red, for the temperature / utilisation overlays.
const RAMP = ['#4f7cff', '#69c8ff', '#7fe0a8', '#ffd24a', '#ff8a3d', '#ef4444'].map((c) => new THREE.Color(c));
export function ramp(t: number, out = new THREE.Color()): THREE.Color {
  const x = Math.max(0, Math.min(0.9999, t)) * (RAMP.length - 1);
  const i = Math.floor(x);
  return out.copy(RAMP[i]).lerp(RAMP[i + 1], x - i);
}
export const RAMP_CSS = 'linear-gradient(90deg,#4f7cff,#69c8ff,#7fe0a8,#ffd24a,#ff8a3d,#ef4444)';

export const smooth = (t: number) => t * t * (3 - 2 * t);
export const clamp01 = (t: number) => Math.max(0, Math.min(1, t));
