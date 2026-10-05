// Building blocks shared by the machine archetypes and the logistics models.
import * as THREE from 'three';
import type { PlantSim } from '@/sim/plant';
import type { Asset, AssetState } from '@/sim/types';
import { M, P, blk, canvasTex, cyl, mesh, rbox } from './kit';

export interface ModelHandle {
  root: THREE.Group;
  /** Body materials recoloured by the view overlays. */
  tint: THREE.MeshStandardMaterial[];
  /** Height where the label and pins sit. */
  top: number;
  labelAt?: THREE.Vector3;
  update(a: Asset, dt: number, t: number, sim: PlantSim): void;
}

export const follow = (cur: number, want: number, dt: number, k: number) => cur + (want - cur) * (1 - Math.exp(-dt * k));

/** Three-lamp stack light: green running, amber waiting/blocked, red fault (blinking). */
export function andon(parent: THREE.Object3D, x: number, y: number, z: number) {
  const g = new THREE.Group();
  g.position.set(x, y, z);
  parent.add(g);
  cyl(g, M.greyDark, 0.025, 0.16, 0, 0, 0, 8);
  const lamps = ['#22b573', '#f5a524', '#ef4444'].map((c, i) => {
    const m = new THREE.MeshStandardMaterial({ color: new THREE.Color(c).multiplyScalar(0.45), emissive: c, emissiveIntensity: 0, roughness: 0.3 });
    cyl(g, m, 0.075, 0.11, 0, 0.16 + i * 0.115, 0, 16);
    return m;
  });
  cyl(g, M.greyDark, 0.06, 0.04, 0, 0.16 + 3 * 0.115, 0, 16);
  return {
    set(state: AssetState, t: number) {
      const blink = Math.sin(t * 7) > 0 ? 1 : 0.1;
      const slow = Math.sin(t * 3) > 0 ? 1 : 0.15;
      const on = [0, 0, 0];
      if (state === 'running' || state === 'moving' || state === 'ok') on[0] = 1;
      else if (state === 'idle' || state === 'waiting' || state === 'low' || state === 'handling') on[1] = 0.8;
      else if (state === 'blocked' || state === 'empty' || state === 'full') on[1] = blink;
      else if (state === 'maint') { on[1] = slow; on[0] = 1 - slow; }
      else if (state === 'fault') on[2] = blink;
      lamps.forEach((m, i) => { m.emissiveIntensity = on[i] * 2.4; });
    },
  };
}

export function pallet(parent: THREE.Object3D, x: number, y: number, z: number, w = 1.2, d = 1.0) {
  const g = new THREE.Group();
  g.position.set(x, y, z);
  parent.add(g);
  blk(g, M.wood, w, 0.04, d, 0, 0.1, 0, 0.01);
  for (const dz of [-d / 2 + 0.08, 0, d / 2 - 0.08]) blk(g, M.wood, w, 0.1, 0.12, 0, 0, dz, 0.01);
  return g;
}

/** A grid stack of identical items drawn with one InstancedMesh; trim it with `.count`. */
export function stack(parent: THREE.Object3D, geo: THREE.BufferGeometry, mat: THREE.Material, nx: number, nz: number, layers: number, sx: number, sy: number, sz: number, y0: number) {
  const im = new THREE.InstancedMesh(geo, mat, nx * nz * layers);
  im.castShadow = true;
  im.receiveShadow = true;
  const m = new THREE.Matrix4();
  let i = 0;
  for (let l = 0; l < layers; l++) for (let a = 0; a < nx; a++) for (let b = 0; b < nz; b++) {
    m.makeTranslation((a - (nx - 1) / 2) * sx, y0 + sy / 2 + l * sy, (b - (nz - 1) / 2) * sz);
    im.setMatrixAt(i++, m);
  }
  im.count = 0;
  parent.add(im);
  return im;
}

export const screenMat = () => new THREE.MeshStandardMaterial({ color: '#0e1a3a', emissive: '#4f7cff', emissiveIntensity: 0.6, roughness: 0.3 });

export function setScreen(mat: THREE.MeshStandardMaterial, state: AssetState) {
  const c = state === 'fault' ? '#ef4444' : state === 'maint' ? '#f5a524' : state === 'stopped' ? '#33415f' : '#4f7cff';
  mat.emissive.set(c);
  mat.emissiveIntensity = state === 'stopped' ? 0.2 : 0.7;
}

/** A small panel with a live screen, facing +z. */
export function panel(parent: THREE.Object3D, x: number, y: number, z: number, w = 0.5, h = 0.34) {
  const scr = screenMat();
  blk(parent, M.ink, w, h, 0.05, x, y, z, 0.02);
  mesh(parent, new THREE.PlaneGeometry(w - 0.08, h - 0.08), scr, x, y + h / 2, z + 0.03, false);
  return scr;
}

const hazardTex = canvasTex(64, 64, (g, _w, h) => {
  g.fillStyle = P.yellow;
  g.fillRect(0, 0, 64, 64);
  g.fillStyle = P.ink;
  for (let i = -2; i < 4; i++) {
    g.beginPath();
    g.moveTo(i * 22, 0); g.lineTo(i * 22 + 11, 0); g.lineTo(i * 22 + 11 + h, h); g.lineTo(i * 22 + h, h);
    g.closePath();
    g.fill();
  }
});
hazardTex.wrapS = hazardTex.wrapT = THREE.RepeatWrapping;
hazardTex.repeat.set(1, 6);
export const hazardMat = new THREE.MeshStandardMaterial({ map: hazardTex, roughness: 0.55 });

/**
 * Box-shaped machine body the belt passes through: plinth, walls with a slot for the belt,
 * a front with a sliding glass door and a control screen, and an accent roof.
 */
export function enclosure(g: THREE.Object3D, body: THREE.Material, accent: THREE.Material, W: number, D: number, H: number, slot: { oz: number; oy0: number; oy1: number }, light = true) {
  const t = 0.1;
  const { oz, oy0, oy1 } = slot;
  blk(g, M.blueDeep, W + 0.12, 0.22, D + 0.12, 0, 0, 0, 0.05);
  blk(g, body, W, H - 0.22, t, 0, 0.22, -D / 2 + t / 2, 0.03);
  blk(g, accent, W + 0.1, 0.18, D + 0.1, 0, H, 0, 0.08);
  for (const sx of [-1, 1]) {
    const x = sx * (W / 2 - t / 2);
    if (oy0 > 0.24) blk(g, body, t, oy0 - 0.22, D, x, 0.22, 0, 0.02);
    blk(g, body, t, H - oy1, D, x, oy1, 0, 0.02);
    const jw = D / 2 - oz;
    if (jw > 0.05) {
      blk(g, body, t, oy1 - oy0, jw, x, oy0, -(oz + jw / 2), 0.02);
      blk(g, body, t, oy1 - oy0, jw, x, oy0, oz + jw / 2, 0.02);
    }
  }
  const zf = D / 2 - t / 2;
  const sill = Math.max(0.5, oy0 - 0.1);
  blk(g, body, W, sill - 0.22, t, 0, 0.22, zf, 0.03);
  blk(g, accent, W, 0.3, t + 0.02, 0, H - 0.3, zf, 0.03);
  blk(g, body, 0.45, H - 0.3 - sill, t, -W / 2 + 0.225, sill, zf, 0.03);
  blk(g, body, 0.6, H - 0.3 - sill, t, W / 2 - 0.3, sill, zf, 0.03);

  const door = new THREE.Group();
  const dw = W - 1.05;
  const dh = H - 0.3 - sill;
  const doorX = -W / 2 + 0.45 + dw / 2;
  door.position.set(doorX, sill, D / 2 + 0.03);
  g.add(door);
  mesh(door, rbox(dw - 0.06, dh - 0.06, 0.03, 0.01), M.glass, 0, dh / 2, 0, false);
  blk(door, accent, dw, 0.06, 0.06, 0, 0, 0, 0.02);
  blk(door, accent, dw, 0.06, 0.06, 0, dh - 0.06, 0, 0.02);
  blk(door, accent, 0.06, dh, 0.06, -dw / 2 + 0.03, 0, 0, 0.02);
  blk(door, accent, 0.06, dh, 0.06, dw / 2 - 0.03, 0, 0, 0.02);
  const scr = panel(g, W / 2 - 0.3, Math.min(1.25, H - 0.8), D / 2 + 0.03, 0.44, 0.34);
  if (light) {
    const lm = new THREE.MeshStandardMaterial({ color: '#ffffff', emissive: '#ffffff', emissiveIntensity: 1.2 });
    mesh(g, new THREE.PlaneGeometry(W - 0.4, D - 0.6), lm, 0, H - 0.02, 0, false).rotation.x = Math.PI / 2;
  }
  return { door, doorX, scr };
}

/** Low-poly operator built facing +z; arms hang from shoulder pivots. */
export function operator(parent: THREE.Object3D, x: number, z: number, rotY: number, shirt: THREE.ColorRepresentation) {
  const g = new THREE.Group();
  g.position.set(x, 0, z);
  g.rotation.y = rotY;
  parent.add(g);
  const navy = new THREE.MeshStandardMaterial({ color: '#2b3557', roughness: 0.8 });
  const skin = new THREE.MeshStandardMaterial({ color: '#f1c7a5', roughness: 0.7 });
  const top = new THREE.MeshStandardMaterial({ color: shirt, roughness: 0.7 });
  for (const sx of [-0.08, 0.08]) mesh(g, new THREE.CapsuleGeometry(0.065, 0.5, 4, 10), navy, sx, 0.33, 0);
  const torso = new THREE.Group();
  torso.position.y = 0.72;
  g.add(torso);
  mesh(torso, new THREE.CapsuleGeometry(0.16, 0.34, 4, 12), top, 0, 0.27, 0);
  mesh(torso, new THREE.SphereGeometry(0.12, 16, 12), skin, 0, 0.66, 0);
  const cap = mesh(torso, new THREE.SphereGeometry(0.128, 16, 8, 0, Math.PI * 2, 0, Math.PI / 2), M.blue, 0, 0.69, 0);
  cap.scale.y = 0.8;
  blk(torso, M.blue, 0.14, 0.02, 0.1, 0, 0.68, 0.1, 0.01);
  const arms = [-1, 1].map((sx) => {
    const piv = new THREE.Group();
    piv.position.set(sx * 0.21, 0.5, 0);
    torso.add(piv);
    mesh(piv, new THREE.CapsuleGeometry(0.05, 0.36, 4, 8), top, 0, -0.22, 0);
    mesh(piv, new THREE.SphereGeometry(0.045, 10, 8), skin, 0, -0.45, 0);
    return piv;
  });
  return { g, torso, arms };
}

/** Particle burst at a moving point: weld sparks, paint mist. */
export function emitter(parent: THREE.Object3D, color: string, size: number, gravity: number, n = 70) {
  const geo = new THREE.BufferGeometry();
  const pos = new Float32Array(n * 3).fill(-100);
  geo.setAttribute('position', new THREE.BufferAttribute(pos, 3));
  const pts = new THREE.Points(geo, new THREE.PointsMaterial({ color, size, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending }));
  pts.frustumCulled = false;
  parent.add(pts);
  const vel = new Float32Array(n * 3);
  const life = new Float32Array(n);
  return {
    step(dt: number, on: boolean, at: THREE.Vector3, spread: [number, number, number], floor: number) {
      for (let i = 0; i < n; i++) {
        if (life[i] <= 0 && on && Math.random() < 0.5) {
          life[i] = 0.25 + Math.random() * 0.35;
          pos.set([at.x, at.y, at.z], i * 3);
          vel.set([(Math.random() - 0.5) * spread[0], (Math.random() - 0.3) * spread[1], (Math.random() - 0.5) * spread[2]], i * 3);
        }
        if (life[i] > 0) {
          life[i] -= dt;
          vel[i * 3 + 1] -= gravity * dt;
          for (let k = 0; k < 3; k++) pos[i * 3 + k] += vel[i * 3 + k] * dt;
          if (pos[i * 3 + 1] < floor) vel[i * 3 + 1] *= -0.3;
          if (life[i] <= 0) pos[i * 3 + 1] = -100;
        }
      }
      geo.attributes.position.needsUpdate = true;
    },
  };
}
