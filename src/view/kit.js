// Shared palette, materials and geometry helpers for the toy-like "strategy game" look.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';

export const P = {
  ground: '#e9ecf7',
  slab: '#f6f7fc',
  road: '#d9deee',
  white: '#ffffff',
  shell: '#f1f3fa',
  blue: '#2f5bea',
  blueDeep: '#1f3fb0',
  blueSoft: '#a9bcff',
  yellow: '#f6c445',
  orange: '#f29b38',
  kraft: '#d9a46b',
  kraftDark: '#bf8a52',
  wood: '#c79a68',
  grey: '#cfd5e4',
  greyDark: '#58627a',
  ink: '#28314b',
  glass: '#b9cdfa',
  mint: '#d5f1e1',
  tree: '#5fcb8c',
  treeDark: '#43b277',
  trunk: '#b98b60',
  metal: '#b7c1d8',
  green: '#22b573',
  amber: '#f5a524',
  red: '#ef4444',
  violet: '#8b5cf6',
};

const std = (color, o = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.62, metalness: 0, ...o });

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
  metal: std(P.metal, { roughness: 0.3, metalness: 0.55 }),
  rubber: std('#2d3348', { roughness: 0.9 }),
  glass: std(P.glass, { roughness: 0.08, metalness: 0.1, transparent: true, opacity: 0.38, depthWrite: false }),
  tree: std(P.tree, { roughness: 0.75 }),
  treeDark: std(P.treeDark, { roughness: 0.75 }),
  trunk: std(P.trunk, { roughness: 0.9 }),
  mint: std(P.mint, { roughness: 0.95 }),
};

export const bodyMat = (color = P.white) => std(color, { roughness: 0.5 });

const geoCache = new Map();
export function rbox(w, h, d, r = 0.06) {
  const rr = Math.min(r, w / 2 - 0.001, h / 2 - 0.001, d / 2 - 0.001);
  const key = `rb:${w}:${h}:${d}:${rr}`;
  if (!geoCache.has(key)) {
    const geo = rr > 0.005 ? new RoundedBoxGeometry(w, h, d, 2, rr) : new THREE.BoxGeometry(w, h, d);
    geo.userData.h = h;
    geoCache.set(key, geo);
  }
  return geoCache.get(key);
}
export function cylGeo(rt, rb, h, seg = 20) {
  const key = `cy:${rt}:${rb}:${h}:${seg}`;
  if (!geoCache.has(key)) geoCache.set(key, new THREE.CylinderGeometry(rt, rb, h, seg));
  return geoCache.get(key);
}

export function mesh(parent, geo, mat, x = 0, y = 0, z = 0, shadow = true) {
  const m = new THREE.Mesh(geo, mat);
  m.position.set(x, y, z);
  m.castShadow = shadow;
  m.receiveShadow = true;
  parent?.add(m);
  return m;
}

// Rounded block whose `y` is its bottom face, which is how one thinks when stacking parts.
export const blk = (parent, mat, w, h, d, x = 0, y = 0, z = 0, r = 0.06) => mesh(parent, rbox(w, h, d, r), mat, x, y + h / 2, z);
export const cyl = (parent, mat, r, h, x = 0, y = 0, z = 0, seg = 20) => mesh(parent, cylGeo(r, r, h, seg), mat, x, y + h / 2, z);

export function canvasTex(w, h, draw) {
  const c = document.createElement('canvas');
  c.width = w;
  c.height = h;
  draw(c.getContext('2d'), w, h);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 8;
  return t;
}

// Cool blue -> yellow -> red, for the temperature / utilisation overlays.
const RAMP = ['#4f7cff', '#69c8ff', '#7fe0a8', '#ffd24a', '#ff8a3d', '#ef4444'].map((c) => new THREE.Color(c));
export function ramp(t, out = new THREE.Color()) {
  const x = Math.max(0, Math.min(0.9999, t)) * (RAMP.length - 1);
  const i = Math.floor(x);
  return out.copy(RAMP[i]).lerp(RAMP[i + 1], x - i);
}
export const RAMP_CSS = 'linear-gradient(90deg,#4f7cff,#69c8ff,#7fe0a8,#ffd24a,#ff8a3d,#ef4444)';

export const smooth = (t) => t * t * (3 - 2 * t);
export const clamp01 = (t) => Math.max(0, Math.min(1, t));
