// Procedural models for every asset type. Each builder returns
// { root, tint: [materials recoloured by overlays], update(asset, dt, time, sim) }.
// Station models are built in a local frame: belt along +x at z = 0, aisle side towards +z.
import * as THREE from 'three';
import { LINE_Y } from '../sim/layout.js';
import { M, P, blk, bodyMat, canvasTex, clamp01, cyl, cylGeo, mesh, rbox, smooth } from './kit.js';

const TOP = LINE_Y + 0.26; // top face of a part sitting on the belt
const V = (x, y, z) => new THREE.Vector3(x, y, z);

// ---------- shared bits ----------

export function andon(parent, x, y, z) {
  const g = new THREE.Group();
  g.position.set(x, y, z);
  parent.add(g);
  cyl(g, M.greyDark, 0.025, 0.16, 0, 0, 0, 8);
  const lamps = ['#22b573', '#f5a524', '#ef4444'].map((c, i) => {
    const m = new THREE.MeshStandardMaterial({
      color: new THREE.Color(c).multiplyScalar(0.45), emissive: c, emissiveIntensity: 0, roughness: 0.3,
    });
    cyl(g, m, 0.075, 0.11, 0, 0.16 + i * 0.115, 0, 16);
    return m;
  });
  cyl(g, M.greyDark, 0.06, 0.04, 0, 0.16 + 3 * 0.115, 0, 16);
  return {
    set(state, t) {
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

function pallet(parent, x, y, z, w = 1.2, d = 1.0) {
  const g = new THREE.Group();
  g.position.set(x, y, z);
  parent.add(g);
  blk(g, M.wood, w, 0.04, d, 0, 0.1, 0, 0.01);
  for (const dz of [-d / 2 + 0.08, 0, d / 2 - 0.08]) blk(g, M.wood, w, 0.1, 0.12, 0, 0, dz, 0.01);
  return g;
}

// Stack of identical items on a pallet, drawn with one InstancedMesh and trimmed with `.count`.
function stack(parent, geo, mat, nx, nz, layers, sx, sy, sz, y0) {
  const n = nx * nz * layers;
  const im = new THREE.InstancedMesh(geo, mat, n);
  im.castShadow = true;
  im.receiveShadow = true;
  const m = new THREE.Matrix4();
  let i = 0;
  for (let l = 0; l < layers; l++) {
    for (let a = 0; a < nx; a++) {
      for (let b = 0; b < nz; b++) {
        m.makeTranslation((a - (nx - 1) / 2) * sx, y0 + sy / 2 + l * sy, (b - (nz - 1) / 2) * sz);
        im.setMatrixAt(i++, m);
      }
    }
  }
  im.count = 0;
  parent.add(im);
  return im;
}

function enclosure(g, body, W, D, H, opts = {}) {
  const t = 0.1;
  const oy0 = 0.6;
  const oy1 = 1.4;
  const oz = 0.6;
  blk(g, M.blueDeep, W + 0.12, 0.22, D + 0.12, 0, 0, 0, 0.05);
  blk(g, body, W, H - 0.22, t, 0, 0.22, -D / 2 + t / 2, 0.03);
  blk(g, M.blue, W + 0.1, 0.18, D + 0.1, 0, H, 0, 0.08);
  for (const sx of [-1, 1]) {
    const x = sx * (W / 2 - t / 2);
    blk(g, body, t, oy0 - 0.22, D, x, 0.22, 0, 0.02);
    blk(g, body, t, H - oy1, D, x, oy1, 0, 0.02);
    const jw = D / 2 - oz;
    blk(g, body, t, oy1 - oy0, jw, x, oy0, -(oz + jw / 2), 0.02);
    blk(g, body, t, oy1 - oy0, jw, x, oy0, oz + jw / 2, 0.02);
  }
  const zf = D / 2 - t / 2;
  blk(g, body, W, 0.5, t, 0, 0.22, zf, 0.03);
  blk(g, M.blue, W, 0.3, t + 0.02, 0, H - 0.3, zf, 0.03);
  blk(g, body, 0.45, H - 1.02, t, -W / 2 + 0.225, 0.72, zf, 0.03);
  blk(g, body, 0.6, H - 1.02, t, W / 2 - 0.3, 0.72, zf, 0.03);

  const door = new THREE.Group();
  const dw = W - 1.05;
  const dh = H - 1.04;
  door.position.set(-W / 2 + 0.45 + dw / 2, 0.72, D / 2 + 0.03);
  g.add(door);
  mesh(door, rbox(dw - 0.06, dh - 0.06, 0.03, 0.01), M.glass, 0, dh / 2, 0, false);
  blk(door, M.blue, dw, 0.06, 0.06, 0, 0, 0, 0.02);
  blk(door, M.blue, dw, 0.06, 0.06, 0, dh - 0.06, 0, 0.02);
  blk(door, M.blue, 0.06, dh, 0.06, -dw / 2 + 0.03, 0, 0, 0.02);
  blk(door, M.blue, 0.06, dh, 0.06, dw / 2 - 0.03, 0, 0, 0.02);
  blk(door, M.white, 0.05, 0.3, 0.05, dw / 2 - 0.15, dh / 2 - 0.15, 0.05, 0.02);

  // Control panel with a live screen on the right column
  const screenMat = new THREE.MeshStandardMaterial({ color: '#0e1a3a', emissive: '#4f7cff', emissiveIntensity: 0.6, roughness: 0.3 });
  blk(g, M.ink, 0.44, 0.34, 0.05, W / 2 - 0.3, 1.25, D / 2 + 0.03, 0.02);
  mesh(g, new THREE.PlaneGeometry(0.36, 0.26), screenMat, W / 2 - 0.3, 1.42, D / 2 + 0.061, false);

  if (opts.light) {
    const lm = new THREE.MeshStandardMaterial({ color: '#ffffff', emissive: '#ffffff', emissiveIntensity: 1.2 });
    mesh(g, new THREE.PlaneGeometry(W - 0.4, D - 0.6), lm, 0, H - 0.02, 0, false).rotation.x = Math.PI / 2;
  }
  return { door, dw, screenMat };
}

function setScreen(mat, state) {
  const c = state === 'fault' ? '#ef4444' : state === 'maint' ? '#f5a524' : state === 'stopped' ? '#33415f' : '#4f7cff';
  mat.emissive.set(c);
  mat.emissiveIntensity = state === 'stopped' ? 0.2 : 0.7;
}

// ---------- line stations ----------

function buildCNC(a) {
  const root = new THREE.Group();
  const body = bodyMat();
  const W = 2.6;
  const D = 2.3;
  const H = 2.45;
  const enc = enclosure(root, body, W, D, H, { light: true });
  const spindle = new THREE.Group();
  root.add(spindle);
  blk(spindle, M.blue, 0.5, 0.55, 0.5, 0, 0.42, 0, 0.06);
  const spin = cyl(spindle, M.metal, 0.08, 0.32, 0, 0.1, 0, 12);
  cyl(spindle, M.ink, 0.035, 0.12, 0, 0, 0, 10);
  blk(root, M.blueSoft, 0.12, 0.12, D - 0.4, 0, H - 0.3, 0, 0.03);
  // chip bin on the back corner
  blk(root, M.greyDark, 0.6, 0.5, 0.5, W / 2 + 0.4, 0, -D / 2 + 0.35, 0.05);
  const lamp = andon(root, W / 2 - 0.2, H + 0.18, -D / 2 + 0.2);
  let doorX = 0;
  let spY = 1.4;
  return {
    root, tint: [body], top: H + 0.7,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(enc.screenMat, a.state);
      const p = a.progress;
      const run = a.state === 'running';
      const open = !run || p < 0.06 || p > 0.94;
      doorX += ((open ? -0.8 : 0) - doorX) * (1 - Math.exp(-dt * 6));
      enc.door.position.x = -W / 2 + 0.45 + enc.dw / 2 + doorX;
      let want = 1.45;
      if (run && p > 0.14 && p < 0.86) want = TOP + 0.05 + Math.abs(Math.sin(p * 18)) * 0.08;
      spY += (want - spY) * (1 - Math.exp(-dt * 5));
      spindle.position.y = spY;
      if (run) spin.rotation.y += dt * 40;
    },
  };
}

function buildPress(a) {
  const root = new THREE.Group();
  const body = bodyMat();
  blk(root, M.blueDeep, 2.3, 0.5, 1.8, 0, 0, 0, 0.06);
  for (const sx of [-1, 1]) blk(root, body, 0.4, 2.6, 1.3, sx * 0.95, 0.5, -0.1, 0.08);
  blk(root, M.blue, 2.4, 0.7, 1.7, 0, 3.0, -0.1, 0.12);
  cyl(root, M.metal, 0.28, 0.5, 0, 3.7, -0.1);
  cyl(root, M.blueDeep, 0.32, 0.12, 0, 4.2, -0.1);
  const ram = new THREE.Group();
  root.add(ram);
  blk(ram, M.metal, 1.3, 0.38, 1.0, 0, 0, 0, 0.05);
  cyl(ram, M.metal, 0.12, 1.2, 0, 0.38, 0, 12);
  // light-curtain posts guarding the front opening
  const beamMat = new THREE.MeshStandardMaterial({ color: '#ffd24a', emissive: '#ffb020', emissiveIntensity: 0.8 });
  for (const sx of [-1, 1]) {
    blk(root, M.yellow, 0.1, 1.7, 0.1, sx * 0.85, 0.5, 0.75, 0.03);
    for (let i = 0; i < 6; i++) mesh(root, rbox(0.03, 0.03, 0.03, 0), beamMat, sx * 0.79, 0.75 + i * 0.25, 0.75, false);
  }
  blk(root, M.ink, 0.5, 0.36, 0.06, 1.35, 1.2, 0.5, 0.03);
  const lamp = andon(root, 1.05, 3.7, -0.75);
  let y = 2.2;
  return {
    root, tint: [body], top: 4.5,
    update(a, dt, t) {
      lamp.set(a.state, t);
      const p = a.progress;
      let want = 2.25;
      if (a.state === 'running' && p > 0.3 && p < 0.7) want = 2.25 - (2.25 - (TOP + 0.02)) * Math.sin(Math.PI * (p - 0.3) / 0.4);
      y += (want - y) * (1 - Math.exp(-dt * 14));
      ram.position.y = y;
    },
  };
}

function buildRobot(a) {
  const welder = a.type === 'welder';
  const root = new THREE.Group();
  const body = bodyMat(P.yellow);
  const BZ = -1.5;
  const L1 = 1.05;
  const L2 = 0.95;
  const TOOL = 0.3;

  // Safety fence on three sides, open towards the belt
  const fenceMat = new THREE.MeshStandardMaterial({ color: '#7f97e8', transparent: true, opacity: 0.18, depthWrite: false, side: THREE.DoubleSide });
  const fence = [[-1.9, -2.8, -1.9, -0.75], [1.9, -2.8, 1.9, -0.75], [-1.9, -2.8, 1.9, -2.8]];
  for (const [x0, z0, x1, z1] of fence) {
    const len = Math.hypot(x1 - x0, z1 - z0);
    const pn = mesh(root, new THREE.PlaneGeometry(len, 1.5), fenceMat, (x0 + x1) / 2, 0.85, (z0 + z1) / 2, false);
    pn.rotation.y = x0 === x1 ? Math.PI / 2 : 0;
    blk(root, M.blue, x0 === x1 ? 0.05 : len, 0.05, x0 === x1 ? len : 0.05, (x0 + x1) / 2, 1.6, (z0 + z1) / 2, 0.02);
  }
  for (const [x, z] of [[-1.9, -2.8], [1.9, -2.8], [-1.9, -0.75], [1.9, -0.75]]) blk(root, M.blue, 0.08, 1.65, 0.08, x, 0, z, 0.02);

  const base = new THREE.Group();
  base.position.set(0, 0, BZ);
  root.add(base);
  blk(base, M.greyDark, 0.75, 0.5, 0.75, 0, 0, 0, 0.06);
  const yaw = new THREE.Group();
  yaw.position.y = 0.5;
  base.add(yaw);
  cyl(yaw, body, 0.34, 0.3, 0, 0, 0, 24);
  const shoulder = new THREE.Group();
  shoulder.position.y = 0.35;
  yaw.add(shoulder);
  mesh(shoulder, cylGeo(0.2, 0.2, 0.44, 20), M.greyDark, 0, 0, 0).rotation.x = Math.PI / 2;
  blk(shoulder, body, 0.24, L1, 0.28, 0, 0, 0, 0.1);
  const elbow = new THREE.Group();
  elbow.position.y = L1;
  shoulder.add(elbow);
  mesh(elbow, cylGeo(0.16, 0.16, 0.36, 20), M.greyDark, 0, 0, 0).rotation.x = Math.PI / 2;
  blk(elbow, body, 0.19, L2, 0.21, 0, 0, 0, 0.08);
  const wrist = new THREE.Group();
  wrist.position.y = L2;
  elbow.add(wrist);
  mesh(wrist, cylGeo(0.1, 0.1, 0.24, 16), M.greyDark, 0, 0, 0).rotation.x = Math.PI / 2;
  if (welder) {
    cyl(wrist, M.ink, 0.05, TOOL - 0.06, 0, 0, 0, 10);
    cyl(wrist, new THREE.MeshStandardMaterial({ color: '#c47b3d', metalness: 0.6, roughness: 0.3 }), 0.035, 0.08, 0, TOOL - 0.08, 0, 10);
    blk(elbow, M.ink, 0.22, 0.18, 0.18, 0, 0.1, 0.2, 0.04); // wire feeder
  } else {
    blk(wrist, M.ink, 0.2, 0.08, 0.2, 0, 0.05, 0, 0.02);
    blk(wrist, M.metal, 0.04, 0.17, 0.12, -0.07, 0.13, 0, 0.01);
    blk(wrist, M.metal, 0.04, 0.17, 0.12, 0.07, 0.13, 0, 0.01);
  }
  blk(root, M.ink, 0.5, 1.1, 0.4, 2.35, 0, -1.6, 0.05); // controller cabinet
  const lamp = andon(root, 2.35, 1.1, -1.6);

  // Weld sparks and a flickering point light
  let sparks = null;
  let flash = null;
  if (welder) {
    const N = 70;
    const geo = new THREE.BufferGeometry();
    const pos = new Float32Array(N * 3);
    geo.setAttribute('position', new THREE.BufferAttribute(pos, 3));
    const pts = new THREE.Points(geo, new THREE.PointsMaterial({
      color: '#ffcf6b', size: 0.06, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending,
    }));
    pts.frustumCulled = false;
    root.add(pts);
    sparks = { pts, pos, vel: new Float32Array(N * 3), life: new Float32Array(N), N };
    flash = new THREE.PointLight('#ffb35c', 0, 4, 2);
    root.add(flash);
  }

  const home = V(0.25, 1.95, -0.85);
  const tip = home.clone();
  const tmp = V(0, 0, 0);
  const shoulderPos = V(0, 0.85, BZ);

  function solve(target) {
    const w = tmp.set(target.x, target.y + TOOL, target.z);
    const dx = w.x - shoulderPos.x;
    const dz = w.z - shoulderPos.z;
    const d = Math.hypot(dx, dz);
    const h = w.y - shoulderPos.y;
    yaw.rotation.y = Math.atan2(-dz, dx);
    const D = Math.min(Math.hypot(d, h), L1 + L2 - 0.001);
    const alpha = Math.atan2(d, h);
    const beta = Math.acos(THREE.MathUtils.clamp((L1 * L1 + D * D - L2 * L2) / (2 * L1 * D), -1, 1));
    const phi = Math.acos(THREE.MathUtils.clamp((L1 * L1 + L2 * L2 - D * D) / (2 * L1 * L2), -1, 1));
    const th1 = alpha - beta;
    const th2 = Math.PI - phi;
    shoulder.rotation.z = -th1;
    elbow.rotation.z = -th2;
    wrist.rotation.z = -(Math.PI - th1 - th2);
  }

  const lerpV = (a0, b0, t0) => tmp.copy(a0).lerp(b0, smooth(clamp01(t0)));
  const above = V(0, TOP + 0.5, 0);
  const down = V(0, TOP + 0.03, 0);
  const w0 = V(-0.2, TOP + 0.06, 0.16);
  const w1 = V(0.2, TOP + 0.06, 0.16);
  const want = V(0, 0, 0);

  return {
    root, tint: [body], top: 2.9,
    update(a, dt, t) {
      lamp.set(a.state, t);
      const p = a.progress;
      let welding = false;
      if (a.state === 'running') {
        if (p < 0.2) want.copy(lerpV(home, above, p / 0.2));
        else if (!welder) {
          if (p < 0.32) want.copy(lerpV(above, down, (p - 0.2) / 0.12));
          else if (p < 0.66) want.copy(down).add(tmp.set(Math.sin(p * 40) * 0.03, 0, 0));
          else if (p < 0.8) want.copy(lerpV(down, above, (p - 0.66) / 0.14));
          else want.copy(lerpV(above, home, (p - 0.8) / 0.2));
        } else {
          if (p < 0.3) want.copy(lerpV(above, w0, (p - 0.2) / 0.1));
          else if (p < 0.76) { want.copy(lerpV(w0, w1, (p - 0.3) / 0.46)); welding = true; }
          else if (p < 0.86) want.copy(lerpV(w1, above, (p - 0.76) / 0.1));
          else want.copy(lerpV(above, home, (p - 0.86) / 0.14));
        }
      } else want.copy(home);
      tip.lerp(want, 1 - Math.exp(-dt * 14));
      solve(tip);

      if (sparks) {
        const s = sparks;
        for (let i = 0; i < s.N; i++) {
          if (s.life[i] <= 0 && welding && Math.random() < 0.5) {
            s.life[i] = 0.25 + Math.random() * 0.35;
            s.pos.set([tip.x, tip.y, tip.z], i * 3);
            s.vel.set([(Math.random() - 0.5) * 2.4, Math.random() * 1.8, (Math.random() - 0.3) * 2.4], i * 3);
          }
          if (s.life[i] > 0) {
            s.life[i] -= dt;
            s.vel[i * 3 + 1] -= 9.8 * dt;
            for (let k = 0; k < 3; k++) s.pos[i * 3 + k] += s.vel[i * 3 + k] * dt;
            if (s.pos[i * 3 + 1] < LINE_Y) s.vel[i * 3 + 1] *= -0.3;
            if (s.life[i] <= 0) s.pos[i * 3 + 1] = -100;
          }
        }
        s.pts.geometry.attributes.position.needsUpdate = true;
        flash.position.copy(tip);
        flash.intensity = welding ? 3 + Math.random() * 6 : 0;
      }
    },
  };
}

function buildOven(a) {
  const root = new THREE.Group();
  const body = bodyMat();
  const L = 5;
  const D = 1.9;
  const H = 2.0;
  blk(root, M.blueDeep, L + 0.1, 0.22, D + 0.1, 0, 0, 0, 0.05);
  blk(root, body, L, 0.38, D, 0, 0.22, 0, 0.03);
  blk(root, body, L, H - 0.6, 0.14, 0, 0.6, -D / 2 + 0.07, 0.04);
  blk(root, body, L, H - 0.6, 0.14, 0, 0.6, D / 2 - 0.07, 0.04);
  blk(root, M.blue, L + 0.12, 0.22, D + 0.12, 0, H, 0, 0.1);
  for (const sx of [-1, 1]) blk(root, body, 0.12, H - 1.35, D, sx * (L / 2 - 0.06), 1.35, 0, 0.03);
  for (let i = -2; i <= 2; i++) blk(root, M.blue, 0.06, H - 0.62, 0.04, i * 1.0, 0.6, D / 2 + 0.01, 0.02);
  const glow = new THREE.MeshStandardMaterial({ color: '#2b1408', emissive: '#ff6a1a', emissiveIntensity: 1, side: THREE.BackSide });
  mesh(root, new THREE.BoxGeometry(L - 0.1, H - 0.62, D - 0.3), glow, 0, 0.6 + (H - 0.62) / 2, 0, false);
  for (const x of [-1.5, 1.5]) {
    cyl(root, M.metal, 0.18, 1.3, x, H + 0.22, -0.4, 16);
    cyl(root, M.blueDeep, 0.22, 0.1, x, H + 1.52, -0.4, 16);
  }
  blk(root, body, 0.8, 1.9, 0.55, L / 2 + 0.7, 0, -0.75, 0.06);
  blk(root, M.ink, 0.5, 0.32, 0.04, L / 2 + 0.7, 1.3, -0.46, 0.02);
  const lamp = andon(root, L / 2 + 0.7, 1.9, -0.75);
  return {
    root, tint: [body], top: H + 1.7,
    update(a, dt, t) {
      lamp.set(a.state, t);
      glow.emissiveIntensity = clamp01((a.temp - 40) / 160) * 2.2 * (0.92 + Math.sin(t * 3) * 0.08);
    },
  };
}

function buildQC(a) {
  const root = new THREE.Group();
  const body = bodyMat();
  for (const sz of [-1, 1]) blk(root, M.blue, 0.14, 2.15, 0.14, 0, 0, sz * 0.72, 0.04);
  blk(root, M.blue, 0.2, 0.18, 1.62, 0, 2.15, 0, 0.06);
  blk(root, body, 0.34, 0.34, 0.3, 0, 1.75, 0, 0.06);
  cyl(root, M.ink, 0.08, 0.16, 0, 1.6, 0, 16);
  const ringMat = new THREE.MeshStandardMaterial({ color: '#ffffff', emissive: '#dfe8ff', emissiveIntensity: 1.2 });
  mesh(root, new THREE.TorusGeometry(0.16, 0.025, 8, 28), ringMat, 0, 1.6, 0, false).rotation.x = Math.PI / 2;
  const cone = mesh(root, new THREE.ConeGeometry(0.42, 1.25 - 0.26, 28, 1, true),
    new THREE.MeshBasicMaterial({ color: '#8fb0ff', transparent: true, opacity: 0.12, depthWrite: false, side: THREE.DoubleSide }),
    0, (1.6 + TOP) / 2, 0, false);
  const beam = mesh(root, new THREE.BoxGeometry(0.03, 0.01, 0.62),
    new THREE.MeshBasicMaterial({ color: '#7ea0ff', transparent: true, opacity: 0.9 }), 0, TOP + 0.01, 0, false);
  // operator monitor with pass / fail flash
  blk(root, M.greyDark, 0.06, 1.25, 0.06, 1.0, 0, 0.95, 0.02);
  blk(root, M.ink, 0.6, 0.38, 0.06, 1.0, 1.25, 0.95, 0.03);
  const scr = new THREE.MeshStandardMaterial({ color: '#0e1a3a', emissive: '#4f7cff', emissiveIntensity: 0.6 });
  mesh(root, new THREE.PlaneGeometry(0.52, 0.3), scr, 1.0, 1.44, 0.985, false);
  // reject bin
  blk(root, new THREE.MeshStandardMaterial({ color: '#ef4444', roughness: 0.6 }), 0.7, 0.5, 0.6, -0.9, 0, 1.0, 0.06);
  const lamp = andon(root, 0, 2.33, 0.6);
  return {
    root, tint: [body], top: 3.0,
    update(a, dt, t, sim) {
      lamp.set(a.state, t);
      const run = a.state === 'running';
      const p = a.progress;
      beam.visible = run && p > 0.1 && p < 0.9;
      cone.visible = beam.visible;
      beam.position.x = (smooth(clamp01((p - 0.1) / 0.8)) - 0.5) * 0.5;
      const v = a.verdict;
      if (v && sim.elapsed - v.at < 1.5) { scr.emissive.set(v.ok ? '#22b573' : '#ef4444'); scr.emissiveIntensity = 1.1; }
      else setScreen(scr, a.state);
    },
  };
}

function buildPack(a) {
  const root = new THREE.Group();
  const body = bodyMat();
  const W = 2.3;
  const D = 2.1;
  const H = 2.2;
  const enc = enclosure(root, body, W, D, H, { light: true });
  for (let i = 0; i < 6; i++) blk(root, M.kraft, 0.95, 0.04, 0.7, -0.3, H + 0.18 + i * 0.045, -0.2, 0.01);
  blk(root, M.blue, 1.05, 0.05, 0.8, -0.3, H + 0.18 + 6 * 0.045, -0.2, 0.02);
  const pusher = new THREE.Group();
  root.add(pusher);
  blk(pusher, M.metal, 0.5, 0.08, 0.5, 0, 0, 0, 0.02);
  cyl(pusher, M.metal, 0.04, 0.6, 0, 0.08, 0, 10);
  const lamp = andon(root, W / 2 - 0.2, H + 0.18, D / 2 - 0.25);
  let y = 1.6;
  return {
    root, tint: [body], top: H + 0.8,
    update(a, dt, t) {
      lamp.set(a.state, t);
      setScreen(enc.screenMat, a.state);
      const p = a.progress;
      const run = a.state === 'running';
      enc.door.position.x = -W / 2 + 0.45 + enc.dw / 2;
      let want = 1.6;
      if (run && p > 0.35 && p < 0.75) want = TOP + 0.36 + (1 - Math.sin(Math.PI * (p - 0.35) / 0.4)) * 0.4;
      y += (want - y) * (1 - Math.exp(-dt * 10));
      pusher.position.y = y;
    },
  };
}

// ---------- line-level things (built in world frame) ----------

export function buildConveyor(line) {
  const root = new THREE.Group();
  const len = line.len;
  root.position.set(line.x0 + len / 2, 0, line.z);
  const frame = bodyMat('#dfe5f5');
  const beltTex = canvasTex(128, 16, (g, w, h) => {
    g.fillStyle = '#3a4260';
    g.fillRect(0, 0, w, h);
    g.fillStyle = '#4b5578';
    g.fillRect(0, 0, 10, h);
  });
  beltTex.wrapS = THREE.RepeatWrapping;
  beltTex.repeat.set(len / 0.5, 1);
  const beltMat = new THREE.MeshStandardMaterial({ map: beltTex, roughness: 0.85 });
  mesh(root, new THREE.BoxGeometry(len, 0.05, 0.86), beltMat, 0, LINE_Y - 0.025, 0);
  for (const sz of [-1, 1]) {
    blk(root, frame, len + 0.1, 0.2, 0.08, 0, LINE_Y - 0.22, sz * 0.47, 0.03);
    blk(root, M.white, len, 0.05, 0.05, 0, LINE_Y + 0.08, sz * 0.5, 0.02);
  }
  for (let x = -len / 2 + 0.6; x < len / 2; x += 2.2) {
    for (const sz of [-1, 1]) blk(root, M.greyDark, 0.07, LINE_Y - 0.22, 0.07, x, 0, sz * 0.42, 0.02);
  }
  for (const sx of [-1, 1]) mesh(root, cylGeo(0.1, 0.1, 0.9, 16), M.metal, sx * len / 2, LINE_Y - 0.08, 0).rotation.x = Math.PI / 2;
  blk(root, M.blue, 0.4, 0.34, 0.36, len / 2 - 0.3, LINE_Y - 0.5, -0.7, 0.05);
  const lamp = andon(root, len / 2 - 0.3, LINE_Y - 0.16, -0.7);
  return {
    root, tint: [frame], top: 1.6, labelAt: new THREE.Vector3(-len / 2 + 1.5, 0, 0),
    update(a, dt, t) {
      lamp.set(a.state, t);
      if (a.state === 'running') beltTex.offset.x -= (0.55 * a.speed * dt) / 0.5;
    },
  };
}

const blankGeo = rbox(0.3, 0.12, 0.26, 0.02);
const blankMat = new THREE.MeshStandardMaterial({ color: '#aab6d3', roughness: 0.35, metalness: 0.5 });
const cartonGeo = rbox(0.36, 0.28, 0.42, 0.03);

export function buildBuffer(line, a) {
  const root = new THREE.Group();
  const side = Math.sign(-line.z);
  root.position.set(line.x0 + 0.6, 0, line.z + side * 1.6);
  pallet(root, 0, 0, 0);
  const im = stack(root, blankGeo, blankMat, 3, 3, 5, 0.36, 0.13, 0.31, 0.14);
  return {
    root, tint: [], top: 1.3,
    update(a) { im.count = Math.min(45, a.stock); },
  };
}

export function buildOutfeed(line, a) {
  const root = new THREE.Group();
  root.position.set(line.x1 + 1.3, 0, line.z);
  pallet(root, 0, 0, 0);
  const im = stack(root, cartonGeo, M.kraft, 3, 2, 4, 0.38, 0.29, 0.45, 0.14);
  blk(root, M.blue, 0.06, 1.4, 1.1, 0.75, 0, 0, 0.02);
  return {
    root, tint: [], top: 1.8,
    update(a) { im.count = Math.min(24, a.count); },
  };
}

// ---------- logistics ----------

export function buildAGV(a) {
  const root = new THREE.Group();
  const body = bodyMat(P.yellow);
  blk(root, M.ink, 1.24, 0.14, 0.8, 0, 0.05, 0, 0.05);
  blk(root, body, 1.3, 0.22, 0.86, 0, 0.12, 0, 0.1);
  blk(root, M.white, 1.1, 0.04, 0.7, 0, 0.34, 0, 0.03);
  cyl(root, M.ink, 0.08, 0.1, 0.52, 0.34, 0, 16);
  for (const sx of [-1, 1]) for (const sz of [-1, 1]) {
    mesh(root, cylGeo(0.09, 0.09, 0.08, 12), M.rubber, sx * 0.42, 0.09, sz * 0.38).rotation.x = Math.PI / 2;
  }
  const ledMat = new THREE.MeshStandardMaterial({ color: '#1e2a52', emissive: '#22b573', emissiveIntensity: 1.6 });
  mesh(root, rbox(0.04, 0.05, 0.6, 0.01), ledMat, 0.66, 0.24, 0, false);
  mesh(root, rbox(0.04, 0.05, 0.6, 0.01), ledMat, -0.66, 0.24, 0, false);
  const cargo = new THREE.Group();
  root.add(cargo);
  pallet(cargo, 0, 0.36, 0, 1.1, 0.8);
  const raw = stack(cargo, blankGeo, blankMat, 3, 2, 3, 0.34, 0.13, 0.32, 0.5);
  raw.count = 18;
  const fg = stack(cargo, cartonGeo, M.kraft, 3, 2, 2, 0.37, 0.29, 0.4, 0.5);
  fg.count = 12;
  const led = { running: '#22b573', moving: '#22b573', waiting: '#f5a524', handling: '#4f7cff', fault: '#ef4444', maint: '#f5a524', stopped: '#33415f' };
  return {
    root, tint: [body], top: 1.3,
    update(a, dt, t) {
      const g = a.agv;
      root.position.set(g.x, 0, g.z);
      root.rotation.y = -g.heading;
      cargo.visible = !!g.cargo;
      raw.visible = g.cargo?.kind === 'raw';
      fg.visible = g.cargo?.kind === 'fg';
      ledMat.emissive.set(led[a.state] || '#33415f');
      ledMat.emissiveIntensity = a.state === 'fault' ? (Math.sin(t * 8) > 0 ? 2 : 0.2) : 1.6;
    },
  };
}

const truckSide = canvasTex(512, 128, (g, w, h) => {
  g.fillStyle = '#ffffff';
  g.fillRect(0, 0, w, h);
  g.fillStyle = '#2f5bea';
  g.fillRect(0, h - 26, w, 26);
  g.fillStyle = '#2f5bea';
  g.font = '700 40px "Plus Jakarta Sans", sans-serif';
  g.fillText('SmartFactory', 146, 72);
  g.fillStyle = '#2f5bea';
  g.beginPath();
  g.moveTo(96, 40); g.lineTo(130, 58); g.lineTo(96, 76); g.lineTo(62, 58); g.closePath();
  g.fill();
});

export function buildTruck(a) {
  const root = new THREE.Group();
  const body = bodyMat(P.blue);
  const inner = new THREE.Group();
  root.add(inner);
  // cab towards +x
  blk(inner, body, 1.9, 2.0, 2.3, 3.1, 0.55, 0, 0.25);
  blk(inner, M.glass, 0.1, 0.8, 2.0, 4.06, 1.45, 0, 0.04);
  blk(inner, M.ink, 0.3, 0.2, 2.2, 4.1, 0.45, 0, 0.06);
  blk(inner, M.ink, 1.0, 0.25, 2.0, 2.4, 0.4, 0, 0.05);
  const sideMat = new THREE.MeshStandardMaterial({ map: truckSide, roughness: 0.55 });
  const trailer = mesh(inner, rbox(6.4, 2.6, 2.4, 0.12), M.white, -1.4, 0.65 + 1.3, 0);
  for (const sz of [-1, 1]) {
    const decal = mesh(inner, new THREE.PlaneGeometry(6.0, 1.5), sideMat, -1.4, 1.95, sz * 1.205, false);
    if (sz < 0) decal.rotation.y = Math.PI;
  }
  blk(inner, M.blue, 6.4, 0.16, 2.44, -1.4, 0.55, 0, 0.04);
  for (const x of [-3.6, -2.7, 2.9]) {
    for (const sz of [-1, 1]) mesh(inner, cylGeo(0.42, 0.42, 0.34, 18), M.rubber, x, 0.42, sz * 1.0).rotation.x = Math.PI / 2;
  }
  trailer.castShadow = true;
  return {
    root, tint: [body], top: 3.8,
    update(a) {
      const t = a.truck;
      root.visible = t.phase !== 'away';
      root.position.set(t.x, -0.3, t.z);
      root.rotation.y = -t.heading;
    },
  };
}

// ---------- storage ----------

export function buildWarehouse(def) {
  const root = new THREE.Group();
  const upright = new THREE.MeshStandardMaterial({ color: P.blue, roughness: 0.45 });
  const beam = new THREE.MeshStandardMaterial({ color: P.orange, roughness: 0.45 });
  const rows = [-34.6, -31.2, -30.0, -26.6]; // two single racks on the outside, one back-to-back pair
  const levels = [0.12, 1.6, 3.1, 4.6];
  const bay = 2.8;
  const bays = Math.floor((def.z1 - def.z0) / bay);
  const upGeo = new THREE.BoxGeometry(0.1, 5.8, 0.1);
  const beamGeo = new THREE.BoxGeometry(0.08, 0.12, bay);
  const ups = new THREE.InstancedMesh(upGeo, upright, rows.length * (bays + 1) * 2);
  const beams = new THREE.InstancedMesh(beamGeo, beam, rows.length * bays * levels.length * 2);
  const loadGeo = rbox(1.0, 1.05, 1.15, 0.06);
  const loads = new THREE.InstancedMesh(loadGeo, new THREE.MeshStandardMaterial({ roughness: 0.8 }), rows.length * bays * levels.length * 2);
  const m = new THREE.Matrix4();
  let iu = 0;
  let ib = 0;
  let il = 0;
  const slots = [];
  for (const rx of rows) {
    for (let b = 0; b <= bays; b++) {
      const z = def.z0 + b * bay;
      for (const dx of [-0.5, 0.5]) { m.makeTranslation(rx + dx, 2.9, z); ups.setMatrixAt(iu++, m); }
    }
    for (let b = 0; b < bays; b++) {
      const z = def.z0 + b * bay + bay / 2;
      for (const y of levels) {
        if (y > 0.5) for (const dx of [-0.5, 0.5]) { m.makeTranslation(rx + dx, y - 0.06, z); beams.setMatrixAt(ib++, m); }
        for (const dz of [-0.65, 0.65]) slots.push([rx, y + 0.53, z + dz]);
      }
    }
  }
  // Shuffle so the visible stock level empties the racks unevenly, like a real store.
  for (let i = slots.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [slots[i], slots[j]] = [slots[j], slots[i]];
  }
  const col = new THREE.Color();
  const tints = ['#d9a46b', '#cf9a60', '#e2b47f', '#a9bcff', '#d9a46b', '#c4935a'];
  slots.forEach(([x, y, z], i) => {
    m.makeTranslation(x, y, z);
    loads.setMatrixAt(il, m);
    loads.setColorAt(il, col.set(tints[i % tints.length]));
    il++;
  });
  ups.count = iu;
  beams.count = ib;
  for (const im of [ups, beams, loads]) { im.castShadow = true; im.receiveShadow = true; root.add(im); }
  const total = slots.length;
  // Dock buffer by the hall door where AGVs pick up
  pallet(root, -24.2, 0, -4.2);
  pallet(root, -24.2, 0, 4.2);
  return {
    root, tint: [], top: 6.4, labelAt: new THREE.Vector3((def.x0 + def.x1) / 2, 0, 0),
    update(a) { loads.count = Math.round(total * Math.min(1, a.stock / a.cap)); },
  };
}

export function buildFG(def) {
  const root = new THREE.Group();
  const nx = 4;
  const nz = 6;
  const cells = [];
  for (let i = 0; i < nx; i++) for (let j = 0; j < nz; j++) cells.push([def.x0 + 1.2 + i * 2.0, def.z0 + 1.1 + j * 1.95]);
  const palletGeo = rbox(1.2, 0.14, 1.0, 0.02);
  const pal = new THREE.InstancedMesh(palletGeo, M.wood, cells.length);
  const boxGeo = rbox(1.14, 0.9, 0.96, 0.06);
  const box = new THREE.InstancedMesh(boxGeo, M.kraft, cells.length);
  const wrap = new THREE.InstancedMesh(rbox(1.16, 0.06, 0.98, 0.02), M.blueSoft, cells.length);
  const m = new THREE.Matrix4();
  cells.forEach(([x, z], i) => {
    m.makeTranslation(x, 0.07, z); pal.setMatrixAt(i, m);
    m.makeTranslation(x, 0.14 + 0.45, z); box.setMatrixAt(i, m);
    m.makeTranslation(x, 0.14 + 0.65, z); wrap.setMatrixAt(i, m);
  });
  for (const im of [pal, box, wrap]) { im.castShadow = true; im.receiveShadow = true; im.count = 0; root.add(im); }
  return {
    root, tint: [], top: 1.8, labelAt: new THREE.Vector3((def.x0 + def.x1) / 2, 0, (def.z0 + def.z1) / 2),
    update(a) {
      const n = Math.min(cells.length, Math.ceil(a.count / 12));
      pal.count = n; box.count = n; wrap.count = n;
    },
  };
}

export const STATION_BUILDERS = { cnc: buildCNC, press: buildPress, robot: buildRobot, welder: buildRobot, oven: buildOven, qc: buildQC, pack: buildPack };

// ---------- products ----------

export const productGeo = {
  raw: rbox(0.36, 0.2, 0.3, 0.03),
  part: rbox(0.36, 0.24, 0.3, 0.05),
  carton: rbox(0.42, 0.3, 0.38, 0.03),
};
export const productMat = {
  raw: blankMat,
  part: new THREE.MeshStandardMaterial({ color: '#e7edff', roughness: 0.25, metalness: 0.35 }),
  carton: M.kraft,
};
