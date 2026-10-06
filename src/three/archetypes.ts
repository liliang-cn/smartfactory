// Parametric machine models. A machine type in config picks one archetype plus size, colours,
// tool and glow; every archetype fits itself around the belt and the product that arrives.
// Local frame: belt along +x at z = 0, belt top at `beltY`, aisle towards +z.
import * as THREE from 'three';
import type { LookCfg } from '@/config/schema';
import { M, P, blk, clamp01, colorOf, cyl, cylGeo, mesh, rbox, smooth } from './kit';
import {
  andon, emitter, enclosure, follow, hazardMat, operator, panel, setScreen, type ModelHandle,
} from './common';

export interface BuildCtx {
  look: LookCfg;
  /** [length, depth, height] after fitting the product */
  size: [number, number, number];
  beltY: number;
  beltW: number;
  /** top of the product as it arrives at this machine */
  top: number;
  plen: number;
  body: THREE.MeshStandardMaterial;
  accent: THREE.MeshStandardMaterial;
  /** inspection that throws units out: show a scrap bin */
  rejects: boolean;
  /** glow follows temperature (furnaces, ovens) rather than just running */
  hot: boolean;
}

type Builder = (c: BuildCtx) => ModelHandle;
const V = (x: number, y: number, z: number) => new THREE.Vector3(x, y, z);

// ---------- box machine with a sliding door (CNC, slitter, winder, sealer …) ----------

const buildEnclosure: Builder = (c) => {
  const root = new THREE.Group();
  const [W, D, H] = c.size;
  const enc = enclosure(root, c.body, c.accent, W, D, H, { oz: c.beltW / 2 + 0.15, oy0: c.beltY - 0.3, oy1: c.top + 0.3 });
  const tool = c.look.tool ?? 'spindle';
  const head = new THREE.Group();
  root.add(head);
  let spin: THREE.Mesh | null = null;
  if (tool === 'spindle') {
    blk(head, c.accent, 0.5, 0.55, 0.5, 0, 0.42, 0, 0.06);
    spin = cyl(head, M.metal, 0.08, 0.32, 0, 0.1, 0, 12);
    cyl(head, M.ink, 0.035, 0.12, 0, 0, 0, 10);
    blk(root, M.blueSoft, 0.12, 0.12, D - 0.4, 0, H - 0.3, 0, 0.03);
    blk(root, M.greyDark, 0.6, 0.5, 0.5, W / 2 + 0.4, 0, -D / 2 + 0.35, 0.05);
  } else if (tool === 'pusher') {
    blk(head, M.metal, Math.min(0.8, W - 0.8), 0.08, Math.min(0.6, c.beltW - 0.2), 0, 0, 0, 0.02);
    cyl(head, M.metal, 0.04, 0.6, 0, 0.08, 0, 10);
  }
  head.visible = tool !== 'none';
  const lamp = andon(root, W / 2 - 0.2, H + 0.18, -D / 2 + 0.2);
  const upY = Math.min(H - 0.95, c.top + 0.6);
  let doorX = 0;
  let y = upY;
  return {
    root, tint: [c.body], top: H + 0.7,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(enc.scr, a.state);
      const p = a.progress;
      const run = a.state === 'running';
      const open = !run || p < 0.06 || p > 0.94;
      doorX = follow(doorX, open ? -0.8 : 0, dt, 6);
      enc.door.position.x = enc.doorX + doorX;
      let want = upY;
      if (run && p > 0.14 && p < 0.86) want = c.top + 0.05 + (tool === 'spindle' ? Math.abs(Math.sin(p * 18)) * 0.08 : 0);
      y = follow(y, want, dt, 5);
      head.position.y = y;
      if (spin && run) spin.rotation.y += dt * 40;
    },
  };
};

// ---------- hydraulic / mechanical press ----------

const buildPress: Builder = (c) => {
  const root = new THREE.Group();
  const [L, D] = c.size;
  const baseH = Math.max(0.3, c.beltY - 0.4);
  const crownY = Math.max(c.top + 1.6, c.size[2] - 1.4);
  const colX = L / 2 - 0.25;
  blk(root, M.blueDeep, L - 0.1, baseH, D, 0, 0, 0, 0.06);
  for (const sx of [-1, 1]) blk(root, c.body, 0.4, crownY - baseH, D - 0.5, sx * colX, baseH, -0.1, 0.08);
  blk(root, c.accent, L, 0.7, D - 0.1, 0, crownY, -0.1, 0.12);
  cyl(root, M.metal, 0.28, 0.5, 0, crownY + 0.7, -0.1);
  cyl(root, M.blueDeep, 0.32, 0.12, 0, crownY + 1.2, -0.1);
  const ram = new THREE.Group();
  root.add(ram);
  blk(ram, M.metal, L - 1.1, 0.38, Math.min(1.0, D - 0.8), 0, 0, 0, 0.05);
  cyl(ram, M.metal, 0.12, 1.2, 0, 0.38, 0, 12);
  const beam = new THREE.MeshStandardMaterial({ color: '#ffd24a', emissive: '#ffb020', emissiveIntensity: 0.8 });
  for (const sx of [-1, 1]) {
    blk(root, M.yellow, 0.1, 1.7, 0.1, sx * (colX - 0.1), baseH, D / 2 - 0.15, 0.03);
    for (let i = 0; i < 6; i++) mesh(root, rbox(0.03, 0.03, 0.03, 0), beam, sx * (colX - 0.16), baseH + 0.25 + i * 0.25, D / 2 - 0.15, false);
  }
  const scr = panel(root, L / 2 + 0.15, 1.2, D / 2 - 0.4);
  const lamp = andon(root, colX, crownY + 0.7, -D / 2 + 0.25);
  const up = crownY - 0.45;
  let y = up;
  return {
    root, tint: [c.body], top: crownY + 1.4,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(scr, a.state);
      const p = a.progress;
      let want = up;
      if (a.state === 'running' && p > 0.3 && p < 0.7) want = up - (up - (c.top + 0.02)) * Math.sin((Math.PI * (p - 0.3)) / 0.4);
      y = follow(y, want, dt, 14);
      ram.position.y = y;
    },
  };
};

// ---------- six-axis robot with a selectable tool ----------

const buildRobot: Builder = (c) => {
  const root = new THREE.Group();
  const tool = c.look.tool ?? 'gripper';
  const welds = tool === 'welder' || tool === 'torch' || tool === 'spray';
  const [L] = c.size;
  const edge = c.beltW / 2;
  const BZ = -(edge + 1.05);
  const L1 = 1.05;
  const L2 = 0.95;
  const TOOL = 0.3;
  const ped = Math.max(0.5, c.top - 0.75);

  if (c.look.fence !== false) {
    const fenceMat = new THREE.MeshStandardMaterial({ color: '#7f97e8', transparent: true, opacity: 0.18, depthWrite: false, side: THREE.DoubleSide });
    const zf = -(edge + 0.3);
    const zb = -(edge + 2.35);
    const fx = L / 2;
    for (const [x0, z0, x1, z1] of [[-fx, zb, -fx, zf], [fx, zb, fx, zf], [-fx, zb, fx, zb]]) {
      const len = Math.hypot(x1 - x0, z1 - z0);
      const pn = mesh(root, new THREE.PlaneGeometry(len, 1.5), fenceMat, (x0 + x1) / 2, 0.85, (z0 + z1) / 2, false);
      pn.rotation.y = x0 === x1 ? Math.PI / 2 : 0;
      blk(root, c.accent, x0 === x1 ? 0.05 : len, 0.05, x0 === x1 ? len : 0.05, (x0 + x1) / 2, 1.6, (z0 + z1) / 2, 0.02);
    }
    for (const [x, z] of [[-fx, zb], [fx, zb], [-fx, zf], [fx, zf]]) blk(root, c.accent, 0.08, 1.65, 0.08, x, 0, z, 0.02);
  }

  const base = new THREE.Group();
  base.position.set(0, 0, BZ);
  root.add(base);
  blk(base, M.greyDark, 0.75, ped, 0.75, 0, 0, 0, 0.06);
  const yaw = new THREE.Group();
  yaw.position.y = ped;
  base.add(yaw);
  cyl(yaw, c.body, 0.34, 0.3, 0, 0, 0, 24);
  const shoulder = new THREE.Group();
  shoulder.position.y = 0.35;
  yaw.add(shoulder);
  mesh(shoulder, cylGeo(0.2, 0.2, 0.44, 20), M.greyDark, 0, 0, 0).rotation.x = Math.PI / 2;
  blk(shoulder, c.body, 0.24, L1, 0.28, 0, 0, 0, 0.1);
  const elbow = new THREE.Group();
  elbow.position.y = L1;
  shoulder.add(elbow);
  mesh(elbow, cylGeo(0.16, 0.16, 0.36, 20), M.greyDark, 0, 0, 0).rotation.x = Math.PI / 2;
  blk(elbow, c.body, 0.19, L2, 0.21, 0, 0, 0, 0.08);
  const wrist = new THREE.Group();
  wrist.position.y = L2;
  elbow.add(wrist);
  mesh(wrist, cylGeo(0.1, 0.1, 0.24, 16), M.greyDark, 0, 0, 0).rotation.x = Math.PI / 2;
  if (welds) {
    cyl(wrist, M.ink, 0.05, TOOL - 0.06, 0, 0, 0, 10);
    cyl(wrist, new THREE.MeshStandardMaterial({ color: tool === 'spray' ? '#9aa3b8' : '#c47b3d', metalness: 0.6, roughness: 0.3 }), 0.035, 0.08, 0, TOOL - 0.08, 0, 10);
    blk(elbow, M.ink, 0.22, 0.18, 0.18, 0, 0.1, 0.2, 0.04);
  } else {
    blk(wrist, M.ink, 0.2, 0.08, 0.2, 0, 0.05, 0, 0.02);
    blk(wrist, M.metal, 0.04, 0.17, 0.12, -0.07, 0.13, 0, 0.01);
    blk(wrist, M.metal, 0.04, 0.17, 0.12, 0.07, 0.13, 0, 0.01);
  }
  blk(root, M.ink, 0.5, 1.1, 0.4, L / 2 + 0.45, 0, BZ - 0.1, 0.05);
  const lamp = andon(root, L / 2 + 0.45, 1.1, BZ - 0.1);

  // Sparks for arc welding, a gas flame for brazing, a mist for painting.
  const fx = tool === 'welder' ? emitter(root, '#ffcf6b', 0.06, 9.8)
    : tool === 'torch' ? emitter(root, '#ff9a3c', 0.05, 6)
      : tool === 'spray' ? emitter(root, c.look.fluid ? colorOf(c.look.fluid) : '#9fc0ff', 0.09, 1.5, 90) : null;
  const flash = tool === 'welder' || tool === 'torch' ? new THREE.PointLight(tool === 'torch' ? '#ff9a3c' : '#ffb35c', 0, 4, 2) : null;
  if (flash) root.add(flash);
  let flame: THREE.Group | null = null;
  if (tool === 'torch') {
    const fl = new THREE.Group();
    flame = fl;
    fl.position.y = TOOL + 0.07;
    wrist.add(fl);
    const add = (r: number, h: number, color: string, o: number) => mesh(fl, new THREE.ConeGeometry(r, h, 14, 1, true),
      new THREE.MeshBasicMaterial({ color, transparent: true, opacity: o, depthWrite: false, blending: THREE.AdditiveBlending }), 0, 0, 0, false);
    add(0.045, 0.16, '#ff8a2a', 0.75);
    add(0.022, 0.1, '#7fb2ff', 0.9).position.y = -0.02;
    fl.visible = false;
  }

  const shoulderPos = V(0, ped + 0.35, BZ);
  const tmp = V(0, 0, 0);
  function solve(target: THREE.Vector3) {
    const w = tmp.set(target.x, target.y + TOOL, target.z);
    const dx = w.x - shoulderPos.x;
    const dz = w.z - shoulderPos.z;
    const d = Math.hypot(dx, dz);
    const h = w.y - shoulderPos.y;
    yaw.rotation.y = Math.atan2(-dz, dx);
    const Dd = Math.min(Math.hypot(d, h), L1 + L2 - 0.001);
    const alpha = Math.atan2(d, h);
    const beta = Math.acos(THREE.MathUtils.clamp((L1 * L1 + Dd * Dd - L2 * L2) / (2 * L1 * Dd), -1, 1));
    const phi = Math.acos(THREE.MathUtils.clamp((L1 * L1 + L2 * L2 - Dd * Dd) / (2 * L1 * L2), -1, 1));
    const th1 = alpha - beta;
    const th2 = Math.PI - phi;
    shoulder.rotation.z = -th1;
    elbow.rotation.z = -th2;
    wrist.rotation.z = -(Math.PI - th1 - th2);
  }

  const reach = Math.min(0.2, c.plen * 0.3);
  const home = V(0.25, c.top + 0.8, BZ + 0.65);
  const above = V(0, c.top + 0.5, 0);
  const down = V(0, c.top + 0.03, 0);
  const w0 = V(-reach, c.top + 0.06, -Math.min(0.16, c.beltW / 4));
  const w1 = V(reach, c.top + 0.06, -Math.min(0.16, c.beltW / 4));
  const tip = home.clone();
  const want = V(0, 0, 0);
  const lv = V(0, 0, 0);
  const lerpV = (a0: THREE.Vector3, b0: THREE.Vector3, t0: number) => lv.copy(a0).lerp(b0, smooth(clamp01(t0)));

  return {
    root, tint: [c.body], top: Math.max(2.9, c.top + 1.5),
    update(a, dt, t) {
      lamp.set(a.state, t);
      const p = a.progress;
      let working = false;
      if (a.state === 'running') {
        if (p < 0.2) want.copy(lerpV(home, above, p / 0.2));
        else if (!welds) {
          if (p < 0.32) want.copy(lerpV(above, down, (p - 0.2) / 0.12));
          else if (p < 0.66) want.copy(down).add(lv.set(Math.sin(p * 40) * 0.03, 0, 0));
          else if (p < 0.8) want.copy(lerpV(down, above, (p - 0.66) / 0.14));
          else want.copy(lerpV(above, home, (p - 0.8) / 0.2));
        } else {
          if (p < 0.3) want.copy(lerpV(above, w0, (p - 0.2) / 0.1));
          else if (p < 0.76) { want.copy(lerpV(w0, w1, (p - 0.3) / 0.46)); working = true; }
          else if (p < 0.86) want.copy(lerpV(w1, above, (p - 0.76) / 0.1));
          else want.copy(lerpV(above, home, (p - 0.86) / 0.14));
        }
      } else want.copy(home);
      tip.lerp(want, 1 - Math.exp(-dt * 14));
      solve(tip);
      fx?.step(dt, working, tip, tool === 'spray' ? [0.8, -1.2, 0.8] : [2.4, 1.8, 2.4], c.beltY);
      if (flash) {
        flash.position.copy(tip);
        flash.intensity = working ? 3 + Math.random() * 6 : 0;
      }
      if (flame) {
        flame.visible = working;
        flame.scale.set(1, 0.85 + Math.random() * 0.3, 1);
      }
    },
  };
};

// ---------- tunnels: furnace, oven, dryer, freezer, washer ----------

function shell(root: THREE.Object3D, body: THREE.Material, accent: THREE.Material, L: number, D: number, H: number, lo: number, hi: number, glassFront: boolean) {
  blk(root, M.blueDeep, L + 0.12, 0.22, D + 0.12, 0, 0, 0, 0.05);
  if (lo > 0.3) blk(root, body, L, lo - 0.22, D, 0, 0.22, 0, 0.03);
  blk(root, body, L, H - lo, 0.12, 0, lo, -D / 2 + 0.06, 0.03);
  if (glassFront) {
    blk(root, body, L, 0.3, 0.12, 0, H - 0.3, D / 2 - 0.06, 0.03);
    mesh(root, rbox(L - 0.2, H - lo - 0.36, 0.04, 0.01), M.glass, 0, (lo + H - 0.3) / 2, D / 2 - 0.06, false);
    for (let i = 0; i <= 3; i++) blk(root, accent, 0.08, H - lo, 0.14, -L / 2 + 0.05 + (i * (L - 0.1)) / 3, lo, D / 2 - 0.06, 0.02);
  } else {
    blk(root, body, L, H - lo, 0.12, 0, lo, D / 2 - 0.06, 0.03);
    for (let x = -L / 2 + 0.5; x < L / 2 - 0.2; x += 1.0) blk(root, accent, 0.06, H - lo - 0.02, 0.04, x, lo, D / 2 + 0.01, 0.02);
  }
  blk(root, accent, L + 0.12, 0.2, D + 0.12, 0, H, 0, 0.08);
  for (const sx of [-1, 1]) blk(root, body, 0.12, H - hi, D, sx * (L / 2 - 0.06), hi, 0, 0.03);
}

function innerGlow(root: THREE.Object3D, L: number, D: number, lo: number, H: number, color: string) {
  const m = new THREE.MeshStandardMaterial({ color: '#1d1410', emissive: color, emissiveIntensity: 0.3, side: THREE.BackSide });
  mesh(root, new THREE.BoxGeometry(L - 0.12, H - lo - 0.02, D - 0.3), m, 0, lo + (H - lo) / 2, 0, false);
  return m;
}

const buildTunnel: Builder = (c) => {
  const root = new THREE.Group();
  const [L, D] = c.size;
  const lo = Math.max(0.22, c.beltY - 0.15);
  const hi = c.top + 0.25;
  const H = Math.max(c.size[2], hi + 0.45);
  shell(root, c.body, c.accent, L, D, H, lo, hi, false);
  const glow = innerGlow(root, L, D, lo, H, colorOf(c.look.glow, c.hot ? '#ff6a1a' : '#e8f0ff'));
  const n = c.look.stacks ?? (c.hot ? 2 : 0);
  for (let i = 0; i < n; i++) {
    const x = n === 1 ? 0 : -L / 2 + 1 + (i * (L - 2)) / (n - 1);
    cyl(root, M.metal, 0.18, 1.3, x, H + 0.2, -0.4, 16);
    cyl(root, M.blueDeep, 0.22, 0.1, x, H + 1.5, -0.4, 16);
  }
  blk(root, c.body, 0.8, 1.9, 0.55, L / 2 - 0.5, 0, -(D / 2 + 0.35), 0.06);
  const scr = panel(root, L / 2 - 0.5, 1.2, -(D / 2 + 0.07));
  const lamp = andon(root, L / 2 - 0.5, 1.9, -(D / 2 + 0.35));
  return {
    root, tint: [c.body], top: H + (n ? 1.7 : 0.7),
    update(a, _dt, t) {
      lamp.set(a.state, t);
      setScreen(scr, a.state);
      const spec = a.spec!;
      const level = c.hot ? clamp01((a.temp - 40) / Math.max(40, (spec.trip ?? 200) - 60)) : a.state === 'running' ? 0.55 : 0.12;
      glow.emissiveIntensity = level * 2.2 * (0.92 + Math.sin(t * 3) * 0.08);
    },
  };
};

// ---------- inspection gantry: vision, metal detector, labeller, printer ----------

const buildGantry: Builder = (c) => {
  const root = new THREE.Group();
  const [L] = c.size;
  const pz = c.beltW / 2 + 0.25;
  const beamY = Math.max(c.size[2] - 0.2, c.top + 1.1);
  for (const sz of [-1, 1]) blk(root, c.accent, 0.14, beamY, 0.14, 0, 0, sz * pz, 0.04);
  blk(root, c.accent, 0.2, 0.18, pz * 2 + 0.2, 0, beamY, 0, 0.06);
  blk(root, c.body, 0.34, 0.34, 0.3, 0, beamY - 0.4, 0, 0.06);
  cyl(root, M.ink, 0.08, 0.16, 0, beamY - 0.55, 0, 16);
  const ring = new THREE.MeshStandardMaterial({ color: '#ffffff', emissive: '#dfe8ff', emissiveIntensity: 1.2 });
  mesh(root, new THREE.TorusGeometry(0.16, 0.025, 8, 28), ring, 0, beamY - 0.55, 0, false).rotation.x = Math.PI / 2;
  const coneH = beamY - 0.55 - c.top;
  const cone = mesh(root, new THREE.ConeGeometry(c.beltW * 0.45, coneH, 28, 1, true),
    new THREE.MeshBasicMaterial({ color: '#8fb0ff', transparent: true, opacity: 0.12, depthWrite: false, side: THREE.DoubleSide }),
    0, c.top + coneH / 2, 0, false);
  const beam = mesh(root, new THREE.BoxGeometry(0.03, 0.01, c.beltW * 0.7), new THREE.MeshBasicMaterial({ color: '#7ea0ff', transparent: true, opacity: 0.9 }), 0, c.top + 0.01, 0, false);
  const mx = Math.min(1.0, L / 2 + 0.2);
  blk(root, M.greyDark, 0.06, 1.25, 0.06, mx, 0, pz + 0.25, 0.02);
  const scr = panel(root, mx, 1.25, pz + 0.25, 0.6, 0.38);
  if (c.rejects) blk(root, new THREE.MeshStandardMaterial({ color: P.red, roughness: 0.6 }), 0.7, 0.5, 0.6, -mx, 0, pz + 0.35, 0.06);
  const lamp = andon(root, 0, beamY + 0.18, pz - 0.1);
  return {
    root, tint: [c.body], top: beamY + 0.9,
    update(a, _dt, t, sim) {
      lamp.set(a.state, t);
      const run = a.state === 'running';
      const p = a.progress;
      beam.visible = run && p > 0.1 && p < 0.9;
      cone.visible = beam.visible;
      beam.position.x = (smooth(clamp01((p - 0.1) / 0.8)) - 0.5) * Math.min(0.6, c.plen);
      const v = a.verdict ?? a.reading;
      if (v && sim.elapsed - v.at < 1.5) { scr.emissive.set(v.ok ? '#22b573' : '#ef4444'); scr.emissiveIntensity = 1.1; }
      else setScreen(scr, a.state);
    },
  };
};

// ---------- packer: enclosure with a carton magazine and a pusher ----------

const buildPacker: Builder = (c) => {
  const root = new THREE.Group();
  const [W, D, H] = c.size;
  const enc = enclosure(root, c.body, c.accent, W, D, H, { oz: c.beltW / 2 + 0.15, oy0: c.beltY - 0.3, oy1: c.top + 0.4 });
  const mw = Math.min(0.95, W - 1.2);
  for (let i = 0; i < 6; i++) blk(root, M.kraft, mw, 0.04, 0.7, -0.3, H + 0.18 + i * 0.045, -0.2, 0.01);
  blk(root, c.accent, mw + 0.1, 0.05, 0.8, -0.3, H + 0.18 + 6 * 0.045, -0.2, 0.02);
  const pusher = new THREE.Group();
  root.add(pusher);
  blk(pusher, M.metal, 0.5, 0.08, 0.5, 0, 0, 0, 0.02);
  cyl(pusher, M.metal, 0.04, 0.6, 0, 0.08, 0, 10);
  const lamp = andon(root, W / 2 - 0.2, H + 0.18, D / 2 - 0.25);
  const up = Math.min(H - 0.95, c.top + 0.7);
  let y = up;
  return {
    root, tint: [c.body], top: H + 0.8,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(enc.scr, a.state);
      const p = a.progress;
      let want = up;
      if (a.state === 'running' && p > 0.35 && p < 0.75) want = c.top + 0.1 + (1 - Math.sin((Math.PI * (p - 0.35)) / 0.4)) * 0.4;
      y = follow(y, want, dt, 10);
      pusher.position.y = y;
    },
  };
};

// ---------- manual station with operators ----------

const buildManual: Builder = (c) => {
  const root = new THREE.Group();
  const [L] = c.size;
  const edge = c.beltW / 2;
  const n = c.look.operators ?? 2;
  const plain = c.look.style === 'plain';
  blk(root, plain ? c.body : M.ink, L - 0.1, 0.03, 1.1, 0, 0, edge + 0.7, 0.03);
  const rz = -(edge + 0.65);
  const railZ = -(edge + 0.15);
  if (plain) {
    // a service counter behind the walkway, in the accent colour
    blk(root, c.body, L - 0.4, 0.9, 0.6, 0, 0, rz, 0.05);
    blk(root, c.accent, L - 0.36, 0.06, 0.64, 0, 0.9, rz, 0.02);
  } else {
    for (const x of [-L / 2 + 0.15, L / 2 - 0.15]) blk(root, c.body, 0.08, 1.6, 0.6, x, 0, rz, 0.02);
    for (const y of [0.45, 0.95, 1.45]) blk(root, c.body, L - 0.22, 0.05, 0.62, 0, y, rz, 0.02);
    const bins = Math.max(2, Math.floor((L - 0.4) / 0.6));
    for (const y of [0.5, 1.0]) for (let i = 0; i < bins; i++) blk(root, i % 2 ? M.yellow : c.accent, 0.5, 0.26, 0.5, -((bins - 1) * 0.6) / 2 + i * 0.6, y, rz, 0.05);
    for (const x of [-L / 2 + 0.05, L / 2 - 0.05]) blk(root, c.accent, 0.1, 2.5, 0.1, x, 0, railZ, 0.03);
    blk(root, c.accent, L, 0.12, 0.12, 0, 2.5, railZ, 0.03);
  }
  const pitch = Math.min(1.1, (L - 0.6) / n);
  const xs = Array.from({ length: n }, (_, i) => (i - (n - 1) / 2) * pitch);
  const tools = plain ? [] : xs.map((x) => {
    blk(root, c.accent, 0.1, 0.1, edge + 0.3, x, 2.42, railZ + (edge + 0.3) / 2, 0.02);
    const tg = new THREE.Group();
    tg.position.set(x, 2.0, 0.15);
    root.add(tg);
    const line = mesh(tg, cylGeo(0.006, 0.006, 1, 6), M.ink, 0, 0.2, 0, false);
    cyl(tg, M.ink, 0.04, 0.22, 0, -0.22, 0, 10);
    return { tg, line };
  });
  let scr: ReturnType<typeof panel> | null = null;
  if (!plain) {
    blk(root, M.greyDark, 0.06, 1.7, 0.06, L / 2 + 0.1, 0, edge + 1.1, 0.02);
    scr = panel(root, L / 2 + 0.1, 1.6, edge + 1.1);
  }
  const lamp = plain ? andon(root, L / 2 - 0.25, 1.05, rz) : andon(root, L / 2 - 0.05, 2.62, railZ);
  const ops = xs.map((x) => operator(root, x, edge + 0.5, Math.PI, '#5b8cff'));
  let reach = 0;
  return {
    root, tint: [c.body], top: plain ? 2.2 : 2.9,
    update(a, dt, t) {
      lamp.set(a.state, t);
      if (scr) setScreen(scr, a.state);
      const run = a.state === 'running';
      reach = follow(reach, run ? 1 : 0, dt, 4);
      ops.forEach((o, i) => {
        const ph = t * 5 + i * 1.7;
        o.arms[0].rotation.x = -reach * (1.1 + Math.sin(ph) * 0.25);
        o.arms[1].rotation.x = -reach * (1.0 + Math.cos(ph * 1.3) * 0.3);
        o.torso.rotation.x = reach * 0.18 + Math.sin(t * 1.2 + i) * 0.02;
        o.torso.rotation.y = run ? Math.sin(ph * 0.4) * 0.15 : 0;
      });
      tools.forEach((tl, i) => {
        const y = run ? c.top + 0.45 + Math.max(0, Math.sin(t * 2.5 + i * 2)) * 0.25 : 1.9;
        tl.tg.position.y = follow(tl.tg.position.y, y, dt, 6);
        const len = 2.42 - tl.tg.position.y;
        tl.line.scale.y = len;
        tl.line.position.y = len / 2;
      });
    },
  };
};

// ---------- electrical safety / function test frame ----------

const buildSafety: Builder = (c) => {
  const root = new THREE.Group();
  const [L] = c.size;
  const px = L / 2 - 0.1;
  const pz = c.beltW / 2 + 0.3;
  const topY = Math.max(2.1, c.top + 1.0);
  for (const x of [-px, px]) for (const z of [-pz, pz]) mesh(root, rbox(0.1, topY, 0.1, 0.02), hazardMat, x, topY / 2, z);
  blk(root, c.body, L, 0.18, pz * 2 + 0.2, 0, topY, 0, 0.06);
  const beacon = new THREE.MeshStandardMaterial({ color: '#7a1d1d', emissive: '#ef4444', emissiveIntensity: 0, roughness: 0.3 });
  cyl(root, beacon, 0.1, 0.18, 0, topY + 0.18, pz - 0.35, 16);
  const head = new THREE.Group();
  root.add(head);
  blk(head, M.ink, 0.5, 0.16, 0.36, 0, 0.3, 0, 0.04);
  for (const x of [-0.12, 0.12]) cyl(head, M.yellow, 0.025, 0.3, x, 0, 0, 8);
  cyl(head, M.metal, 0.03, 1.6, 0, 0.46, 0, 8);
  const curtain = new THREE.MeshStandardMaterial({ color: '#ffd24a', emissive: '#ffb020', emissiveIntensity: 0.8 });
  for (let i = 0; i < 7; i++) mesh(root, rbox(0.03, 0.03, 0.03, 0), curtain, -px + 0.06, c.beltY - 0.25 + i * 0.2, pz, false);
  blk(root, c.body, 0.75, 1.35, 0.5, px + 0.45, 0, -(pz + 0.6), 0.05);
  const scr = panel(root, px + 0.45, 0.9, -(pz + 0.35), 0.5, 0.32);
  const lamp = andon(root, px + 0.7, 1.35, -(pz + 0.75));
  let y = topY - 0.25;
  return {
    root, tint: [c.body], top: topY + 0.8,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(scr, a.state);
      const p = a.progress;
      const testing = a.state === 'running' && p > 0.2 && p < 0.8;
      beacon.emissiveIntensity = testing ? (Math.sin(t * 10) > 0 ? 2.6 : 0.3) : 0;
      y = follow(y, testing ? c.top + 0.02 : topY - 0.25, dt, 8);
      head.position.y = y - 0.3;
    },
  };
};

// ---------- closed test chamber with end doors (leak test, X-ray, formation) ----------

const buildChamber: Builder = (c) => {
  const root = new THREE.Group();
  const [L, D] = c.size;
  const lo = Math.max(0.22, c.beltY - 0.12);
  const hi = c.top + 0.2;
  const H = Math.max(c.size[2], hi + 0.5);
  shell(root, c.body, c.accent, L, D, H, lo, hi, true);
  const inner = innerGlow(root, L, D, lo, H, colorOf(c.look.glow, '#b9a8ff'));
  const doors = [-1, 1].map((sx) => mesh(root, rbox(0.08, hi - lo, D - 0.3, 0.02), c.accent, sx * (L / 2 + 0.06), 0, 0));
  const bottle = new THREE.MeshStandardMaterial({ color: colorOf(c.look.fluid, '#a7b0c4'), roughness: 0.35, metalness: 0.4 });
  for (let i = 0; i < 3; i++) {
    cyl(root, bottle, 0.13, 1.35, L / 2 - 0.9 + i * 0.32, 0, -(D / 2 + 0.4), 16);
    cyl(root, M.ink, 0.05, 0.12, L / 2 - 0.9 + i * 0.32, 1.35, -(D / 2 + 0.4), 10);
  }
  blk(root, M.ink, 0.8, 0.55, 0.55, -L / 2 + 0.6, 0, -(D / 2 + 0.45), 0.06);
  const scr = panel(root, L / 2 - 0.25, H - 0.75, D / 2 + 0.01, 0.5, 0.34);
  const lamp = andon(root, -L / 2 + 0.25, H + 0.16, -D / 2 + 0.25);
  let shut = 0;
  return {
    root, tint: [c.body], top: H + 0.8,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(scr, a.state);
      const p = a.progress;
      shut = follow(shut, a.state === 'running' && p > 0.06 && p < 0.94 ? 1 : 0, dt, 7);
      const h = hi - lo;
      for (const d of doors) d.position.y = lo + h / 2 + (1 - shut) * h;
      inner.emissiveIntensity = shut * (0.5 + 0.15 * Math.sin(t * 4));
    },
  };
};

// ---------- filler / dispenser: cabinet, bottle and a boom head (fill, glue, charge) ----------

const buildFiller: Builder = (c) => {
  const root = new THREE.Group();
  const [L] = c.size;
  const cz = -(c.beltW / 2 + 0.75);
  blk(root, c.body, 1.3, 1.9, 0.7, 0.2, 0, cz, 0.08);
  blk(root, c.accent, 1.34, 0.12, 0.74, 0.2, 1.9, cz, 0.05);
  const scr = panel(root, 0.05, 1.25, cz + 0.36, 0.6, 0.42);
  const gauge = new THREE.MeshStandardMaterial({ color: '#ffffff', roughness: 0.3 });
  for (const x of [0.55, 0.75]) mesh(root, cylGeo(0.08, 0.08, 0.03, 20), gauge, x, 1.45, cz + 0.36, false).rotation.x = Math.PI / 2;
  blk(root, M.greyDark, 0.5, 0.08, 0.5, Math.min(1.25, L / 2), 0, cz, 0.03);
  cyl(root, new THREE.MeshStandardMaterial({ color: colorOf(c.look.fluid, '#8fb4ff'), roughness: 0.35, metalness: 0.3 }), 0.19, 0.95, Math.min(1.25, L / 2), 0.08, cz, 20);
  const boomY = Math.max(2.25, c.top + 1.0);
  const postZ = -(c.beltW / 2 + 0.25);
  blk(root, c.accent, 0.12, boomY + 0.1, 0.12, -0.75, 0, postZ, 0.03);
  blk(root, c.accent, 0.12, 0.12, -postZ + 0.1, -0.75, boomY, postZ / 2, 0.03);
  const head = new THREE.Group();
  head.position.set(-0.75, boomY - 0.35, 0.05);
  root.add(head);
  blk(head, M.ink, 0.16, 0.2, 0.16, 0, 0, 0, 0.03);
  cyl(head, M.yellow, 0.03, 0.12, 0, -0.12, 0, 10);
  const hose = mesh(root, cylGeo(0.025, 0.025, 1, 8), new THREE.MeshStandardMaterial({ color: '#2d3348', roughness: 0.8 }), -0.75, boomY, 0.05, false);
  const lamp = andon(root, 0.7, 2.02, cz - 0.15);
  let y = boomY - 0.35;
  return {
    root, tint: [c.body], top: boomY + 0.6,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(scr, a.state);
      const p = a.progress;
      y = follow(y, a.state === 'running' && p > 0.12 && p < 0.88 ? c.top + 0.12 : boomY - 0.35, dt, 6);
      head.position.y = y;
      const len = boomY - (y + 0.2);
      hose.scale.y = Math.max(0.05, len);
      hose.position.y = y + 0.2 + len / 2;
    },
  };
};

// ---------- booth: test room, paint booth, clean room ----------

const buildBooth: Builder = (c) => {
  const root = new THREE.Group();
  const [L, D] = c.size;
  const lo = Math.max(0.22, c.beltY - 0.12);
  const hi = c.top + 0.25;
  const H = Math.max(c.size[2], hi + 0.6);
  shell(root, c.body, c.accent, L, D, H, lo, hi, true);
  const light = new THREE.MeshStandardMaterial({ color: '#ffffff', emissive: colorOf(c.look.glow, '#e8f0ff'), emissiveIntensity: 0.3 });
  mesh(root, new THREE.PlaneGeometry(L - 0.5, D - 0.6), light, 0, H - 0.02, 0, false).rotation.x = Math.PI / 2;
  for (const z of [-0.45, 0.45]) mesh(root, cylGeo(0.16, 0.16, L - 0.3, 18), M.metal, 0, H + 0.36, z).rotation.z = Math.PI / 2;
  cyl(root, M.white, 0.38, 0.32, -L / 4, H + 0.16, 0, 24);
  const fan = new THREE.Group();
  fan.position.set(-L / 4, H + 0.5, 0);
  root.add(fan);
  for (let i = 0; i < 4; i++) {
    const piv = new THREE.Group();
    piv.rotation.y = (i * Math.PI) / 2;
    fan.add(piv);
    mesh(piv, rbox(0.3, 0.02, 0.08, 0.01), M.ink, 0.15, 0, 0, false);
  }
  const scr = panel(root, L / 2 - 0.5, H - 0.75, D / 2 + 0.01, 0.62, 0.36);
  const lamp = andon(root, L / 2 - 0.25, H + 0.16, -D / 2 + 0.25);
  let spin = 0;
  return {
    root, tint: [c.body], top: H + 1.0,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(scr, a.state);
      const run = a.state === 'running';
      spin = follow(spin, run ? 18 : 2, dt, 2);
      fan.rotation.y += spin * dt;
      light.emissiveIntensity = run ? 1.1 : 0.3;
    },
  };
};

// ---------- tank: mixer, reactor, slurry tank ----------

const buildTank: Builder = (c) => {
  const root = new THREE.Group();
  const [L, , Hs] = c.size;
  const tz = -(c.beltW / 2 + 1.55);
  const r = Math.min(0.85, L / 2 - 0.3);
  const th = Math.max(1.6, Hs - 1.3);
  const y0 = 0.92;
  for (const [x, z] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) blk(root, M.greyDark, 0.1, y0, 0.1, x * r * 0.7, 0, tz + z * r * 0.7, 0.02);
  mesh(root, new THREE.ConeGeometry(r, 0.45, 32), c.body, 0, y0 - 0.225, tz).rotation.x = Math.PI;
  cyl(root, c.body, r, th, 0, y0, tz, 32);
  const dome = mesh(root, new THREE.SphereGeometry(r, 32, 12, 0, Math.PI * 2, 0, Math.PI / 2), c.accent, 0, y0 + th, tz);
  dome.scale.y = 0.35;
  for (const y of [y0 + th * 0.33, y0 + th * 0.66]) mesh(root, new THREE.TorusGeometry(r + 0.01, 0.03, 8, 40), c.accent, 0, y, tz, false).rotation.x = Math.PI / 2;
  // sight glass with the fill level
  const sg = th * 0.8;
  mesh(root, rbox(0.14, sg, 0.03, 0.01), M.glass, 0, y0 + th * 0.1 + sg / 2, tz + r + 0.01, false);
  const fluid = new THREE.MeshStandardMaterial({ color: colorOf(c.look.fluid, '#f2d39b'), roughness: 0.4, emissive: colorOf(c.look.fluid, '#f2d39b'), emissiveIntensity: 0.15 });
  const level = mesh(root, new THREE.BoxGeometry(0.1, 1, 0.02), fluid, 0, 0, tz + r + 0.02, false);
  // agitator drive on top
  const motorY = y0 + th + r * 0.35;
  blk(root, c.accent, 0.42, 0.36, 0.42, 0, motorY, tz, 0.06);
  const coupling = new THREE.Group();
  coupling.position.set(0, motorY + 0.36, tz);
  root.add(coupling);
  cyl(coupling, M.metal, 0.08, 0.16, 0, 0, 0, 12);
  blk(coupling, M.yellow, 0.3, 0.04, 0.05, 0, 0.16, 0, 0.01);
  // discharge pipe over the belt
  const pipeY = c.top + 0.65;
  const pz0 = tz + r;
  const pipe = mesh(root, cylGeo(0.06, 0.06, -pz0, 12), M.metal, 0, pipeY, pz0 / 2, false);
  pipe.rotation.x = Math.PI / 2;
  cyl(root, M.metal, 0.06, pipeY - (c.top + 0.15), 0, c.top + 0.15, 0, 12);
  blk(root, M.ink, 0.16, 0.16, 0.16, 0, pipeY - 0.08, pz0 + 0.1, 0.03);
  const scr = panel(root, L / 2 - 0.2, 1.1, tz + r + 0.15, 0.44, 0.3);
  const lamp = andon(root, 0.3, motorY + 0.36, tz);
  let fill = 0.8;
  return {
    root, tint: [c.body], top: motorY + 1.0,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(scr, a.state);
      const run = a.state === 'running';
      if (run) coupling.rotation.y += dt * 6 * a.speed;
      fill = follow(fill, run ? 0.85 - 0.45 * a.progress : 0.85, dt, run ? 6 : 0.5);
      const h = Math.max(0.02, sg * fill);
      level.scale.y = h;
      level.position.y = y0 + th * 0.1 + h / 2;
    },
  };
};

// ---------- roller: rolling mill, calender, ring roller, coater ----------

const buildRoller: Builder = (c) => {
  const root = new THREE.Group();
  const [L] = c.size;
  const pz = c.beltW / 2 + 0.25;
  const rr = 0.26;
  const frameH = Math.max(c.top + 2 * rr + 0.5, 1.8);
  const fl = Math.min(L * 0.6, 1.6);
  for (const sz of [-1, 1]) blk(root, c.body, fl, frameH, 0.25, 0, 0, sz * pz, 0.05);
  blk(root, c.accent, fl, 0.25, pz * 2 + 0.25, 0, frameH, 0, 0.06);
  const roll = (y: number) => {
    const g = new THREE.Group();
    g.position.set(0, y, 0);
    root.add(g);
    const m = mesh(g, cylGeo(rr, rr, pz * 2 - 0.1, 28), M.metal, 0, 0, 0);
    m.rotation.x = Math.PI / 2;
    blk(g, M.ink, 0.06, 0.02, pz * 2 - 0.12, 0, rr - 0.01, 0, 0.005);
    return g;
  };
  const upper = roll(c.top + rr + 0.02);
  const lower = c.beltY - rr - 0.08 > 0.2 ? roll(c.beltY - rr - 0.08) : null;
  blk(root, c.accent, 0.7, 0.6, 0.6, 0, 0.2, -(pz + 0.5), 0.06);
  mesh(root, cylGeo(0.08, 0.08, 0.5, 12), M.metal, 0, c.top + rr, -(pz + 0.2)).rotation.x = Math.PI / 2;
  const scr = panel(root, fl / 2 + 0.35, 1.0, pz + 0.05, 0.44, 0.3);
  const lamp = andon(root, fl / 2 - 0.15, frameH + 0.25, -pz);
  const uy = upper.position.y;
  return {
    root, tint: [c.body], top: frameH + 0.9,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(scr, a.state);
      const run = a.state === 'running';
      if (run) {
        upper.rotation.z -= dt * 5 * a.speed;
        if (lower) lower.rotation.z += dt * 5 * a.speed;
      }
      upper.position.y = uy - (run && a.progress > 0.2 && a.progress < 0.8 ? 0.02 : 0);
    },
  };
};

// ---------- frame: an empty base for machines described entirely by parts ----------

const buildFrame: Builder = (c) => {
  const root = new THREE.Group();
  const [L, D, H] = c.size;
  const plain = c.look.style === 'plain';
  // plain: a low deck in the body colour (a wooden platform, a pitch), no operator panel
  blk(root, plain ? c.body : M.greyDark, L, plain ? 0.04 : 0.12, D, 0, 0, 0, plain ? 0.02 : 0.04);
  const scr = plain ? null : panel(root, L / 2 - 0.3, 1.0, D / 2 - 0.05, 0.44, 0.3);
  const lamp = andon(root, L / 2 - 0.2, Math.max(1.4, H * 0.6), -D / 2 + 0.2);
  return {
    root, tint: [c.body], top: H + 0.4,
    update(a, _dt, t) {
      lamp.set(a.state, t);
      if (scr) setScreen(scr, a.state);
    },
  };
};

export const ARCHETYPE_BUILDERS: Record<LookCfg['archetype'], Builder> = {
  enclosure: buildEnclosure, press: buildPress, robot: buildRobot, tunnel: buildTunnel, gantry: buildGantry,
  packer: buildPacker, manual: buildManual, safety: buildSafety, chamber: buildChamber, filler: buildFiller,
  booth: buildBooth, tank: buildTank, roller: buildRoller, frame: buildFrame,
};
