// Models of a site people come to rather than a factory: its places (a table with its chairs, a
// pitch with its tent), the groups of people themselves, the storeroom shelves and the pick-up
// counter, and the vehicles that serve it (a cold-chain van, a courier on a scooter).
import * as THREE from 'three';
import type { ProductInfo, Zone } from '@/core/layout';
import type { PartCfg } from '@/config/schema';
import type { Asset } from '@/sim/types';
import type { ModelHandle } from './common';
import { M, P, blk, bodyMat, canvasTex, cyl, cylGeo, mesh, rbox } from './kit';
import { buildParts } from './parts';
import { productObject } from './products';

// ---------- places ----------

/** The ring on the floor around a place shows its state at a glance. */
const STATE_COLOR: Record<string, string> = { free: '#22b573', occupied: '#2f5bea', reserved: '#f5a524', cleaning: '#9aa3bd' };

export function buildPlace(a: Asset): ModelHandle {
  const pl = a.place!;
  const root = new THREE.Group();
  root.position.set(pl.x, 0, pl.z);
  const pitch = pl.overnight;
  const ringMat = new THREE.MeshStandardMaterial({ color: STATE_COLOR.free, roughness: 0.6, transparent: true, opacity: 0.85 });
  const W = pitch ? 3.6 : Math.min(2.2, 0.9 + pl.seats * 0.2);
  const D = pitch ? 3.6 : 1.9;
  // a flat outline of the place's floor, coloured by its state
  const ring = new THREE.Mesh(new THREE.RingGeometry(0, 1, 4, 1), ringMat);
  ring.rotation.x = -Math.PI / 2;
  ring.rotation.z = Math.PI / 4;
  ring.scale.set(W * 0.72, D * 0.72, 1);
  ring.position.y = 0.05;
  ring.receiveShadow = true;
  root.add(ring);
  const tint: THREE.MeshStandardMaterial[] = [];
  const busy = new THREE.Group();
  root.add(busy);
  let tent: THREE.Object3D | null = null;
  if (pitch) {
    // a wooden pitch deck; a tent goes up while it is let
    const deck = bodyMat('#d8c3a0');
    tint.push(deck);
    blk(root, deck, 2.6, 0.06, 2.2, 0, 0.02, -0.2, 0.02);
    tent = new THREE.Group();
    const fly = new THREE.MeshStandardMaterial({ color: '#e0752f', roughness: 0.6 });
    const cone = mesh(tent, cylGeo(0.02, 1.15, 1.5, 4), fly, 0, 0.75, -0.3);
    cone.rotation.y = Math.PI / 4;
    blk(tent, M.ink, 0.4, 0.7, 0.04, 0, 0, 0.5, 0.01);
    root.add(tent);
    cyl(root, M.greyDark, 0.25, 0.08, 1.2, 0, 0.9, 16);
  } else {
    // a table sized for its seats, a hotpot burner in the middle, chairs round it
    const top = bodyMat('#c79a68');
    tint.push(top);
    const tl = 0.7 + Math.max(0, pl.seats - 2) * 0.28;
    blk(root, top, tl, 0.05, 0.8, 0, 0.72, 0, 0.02);
    cyl(root, M.greyDark, 0.06, 0.72, 0, 0, 0, 10);
    blk(root, M.ink, 0.36, 0.04, 0.36, 0, 0.77, 0, 0.01);
    const chair = new THREE.MeshStandardMaterial({ color: '#e2412b', roughness: 0.55 });
    const per = Math.ceil(pl.seats / 2);
    for (let i = 0; i < pl.seats; i++) {
      const side = i < per ? -1 : 1;
      const k = i < per ? i : i - per;
      const n = i < per ? per : pl.seats - per;
      const x = (k - (n - 1) / 2) * (tl / Math.max(1, n));
      cyl(root, chair, 0.16, 0.45, x, 0, side * 0.62, 14);
    }
    // the pot: steams while a group is eating
    cyl(busy, M.metal, 0.18, 0.1, 0, 0.81, 0, 18);
    cyl(busy, new THREE.MeshStandardMaterial({ color: '#d9361e', roughness: 0.35 }), 0.165, 0.02, 0, 0.9, 0, 18);
  }
  return {
    root, tint, top: pitch ? 2.2 : 1.6,
    update(as) {
      ringMat.color.set(STATE_COLOR[as.state] ?? STATE_COLOR.free);
      ringMat.opacity = as.state === 'free' ? 0.5 : 0.85;
      busy.visible = as.state === 'occupied';
      if (tent) tent.visible = as.state === 'occupied';
    },
  };
}

// ---------- people ----------

interface Person { parts: PartCfg[]; x: number; z: number }

/** The people in a stage's parts: each capsule (a body) with the spheres just above it (head, hair). */
function people(parts: PartCfg[]) {
  const persons: Person[] = [];
  const props: PartCfg[] = [];
  for (const p of parts) {
    if (p.shape === 'capsule') {
      persons.push({ parts: [p], x: p.at[0], z: p.at[2] });
      continue;
    }
    const owner = p.shape === 'sphere' ? persons.find((q) => Math.abs(q.x - p.at[0]) < 0.06 && Math.abs(q.z - p.at[2]) < 0.06 && p.at[1] > 0.5) : undefined;
    if (owner) owner.parts.push(p);
    else props.push(p);
  }
  return { persons, props };
}

/** where the n people of a group stand, round its centre */
const SPOTS: [number, number][] = [[-0.38, 0.1], [0, -0.12], [0.38, 0.12], [-0.2, 0.42], [0.22, -0.42], [0.62, -0.2], [-0.62, -0.2], [0, 0.5]];

const groups = new Map<string, THREE.Group>();

/**
 * A group of n people at a stage: the stage's own people repeated to make up the number, and its
 * other parts (what they carry). Stages without people look as configured.
 */
export function groupObject(product: ProductInfo, stage: string, n: number): THREE.Group {
  const parts = product.stages[stage] ?? product.stages[product.order[0]];
  const { persons, props } = people(parts);
  if (!persons.length || !n) return productObject(product, stage);
  const key = `${product.key}:${stage}:${n}`;
  let tpl = groups.get(key);
  if (!tpl) {
    const out: PartCfg[] = [...props];
    for (let i = 0; i < n; i++) {
      const src = persons[i % persons.length];
      const [x, z] = SPOTS[i % SPOTS.length];
      for (const q of src.parts) out.push({ ...q, at: [q.at[0] - src.x + x, q.at[1], q.at[2] - src.z + z] });
    }
    tpl = new THREE.Group();
    buildParts(tpl, out);
    groups.set(key, tpl);
  }
  const o = tpl.clone();
  o.userData.spinners = [];
  return o;
}

// ---------- storeroom and counter ----------

/** A storeroom: a few shelving units with boxes and crates, as full as the stock. */
export function buildShelves(def: Zone): ModelHandle {
  const root = new THREE.Group();
  const frame = new THREE.MeshStandardMaterial({ color: '#c9d1e4', roughness: 0.4, metalness: 0.3 });
  const W = def.x1 - def.x0;
  const cx = (def.x0 + def.x1) / 2;
  const units = Math.max(2, Math.floor((def.z1 - def.z0) / 3.2));
  const z0 = (def.z0 + def.z1) / 2 - ((units - 1) * 3.2) / 2;
  const slots: [number, number, number][] = [];
  for (let u = 0; u < units; u++) {
    for (const dx of [-W / 4, W / 4]) {
      const x = cx + dx;
      const z = z0 + u * 3.2;
      for (const y of [0.05, 0.75, 1.45]) {
        blk(root, frame, 2.4, 0.04, 0.6, x, y, z, 0.01);
        for (const k of [-0.8, 0, 0.8]) slots.push([x + k, y + 0.04, z]);
      }
      for (const sx of [-1.2, 1.2]) for (const sz of [-0.28, 0.28]) blk(root, frame, 0.04, 1.9, 0.04, x + sx, 0, z + sz, 0.01);
    }
  }
  const box = new THREE.InstancedMesh(rbox(0.6, 0.4, 0.5, 0.04), new THREE.MeshStandardMaterial({ roughness: 0.7 }), slots.length);
  const m = new THREE.Matrix4();
  const col = new THREE.Color();
  const tints = ['#d9a46b', '#f4f6f9', '#9fd0b4', '#e8cf9a', '#bcd0f5'];
  slots.forEach(([x, y, z], i) => {
    m.makeTranslation(x, y + 0.2, z);
    box.setMatrixAt(i, m);
    box.setColorAt(i, col.set(tints[i % tints.length]));
  });
  box.castShadow = true;
  root.add(box);
  return {
    root, tint: [], top: 2.4, labelAt: new THREE.Vector3(cx, 0, (def.z0 + def.z1) / 2),
    update(a) { box.count = Math.round(slots.length * Math.min(1, a.cap ? a.stock / a.cap : 0.8)); },
  };
}

/** A pick-up counter for delivery orders: bags waiting for the riders. */
export function buildCounter(def: Zone): ModelHandle {
  const root = new THREE.Group();
  const cx = (def.x0 + def.x1) / 2;
  const z = def.z1 - 0.6;
  const body = bodyMat('#f4f6f9');
  blk(root, body, 3.2, 1.0, 0.7, cx, 0, z, 0.04);
  blk(root, new THREE.MeshStandardMaterial({ color: '#e2412b', roughness: 0.5 }), 3.24, 0.12, 0.74, cx, 1.0, z, 0.02);
  const sign = canvasTex(256, 64, (g, w, h) => {
    g.fillStyle = '#e2412b';
    g.fillRect(0, 0, w, h);
    g.fillStyle = '#ffffff';
    g.font = '700 34px "Plus Jakarta Sans", sans-serif';
    g.textAlign = 'center';
    g.fillText('外卖取餐', w / 2, 44);
  });
  const board = mesh(root, new THREE.PlaneGeometry(1.6, 0.4), new THREE.MeshStandardMaterial({ map: sign, roughness: 0.6 }), cx, 2.1, z, false);
  board.rotation.y = 0;
  cyl(root, M.greyDark, 0.03, 2.0, cx - 0.7, 0, z - 0.3, 8);
  cyl(root, M.greyDark, 0.03, 2.0, cx + 0.7, 0, z - 0.3, 8);
  const bag = new THREE.InstancedMesh(rbox(0.3, 0.32, 0.22, 0.03), new THREE.MeshStandardMaterial({ color: '#f6c445', roughness: 0.6 }), 12);
  const m = new THREE.Matrix4();
  for (let i = 0; i < 12; i++) {
    m.makeTranslation(cx - 1.3 + (i % 6) * 0.5, 1.28 + Math.floor(i / 6) * 0.34, z);
    bag.setMatrixAt(i, m);
  }
  bag.castShadow = true;
  root.add(bag);
  return {
    root, tint: [body], top: 2.6, labelAt: new THREE.Vector3(cx, 0, z),
    update(a) { bag.count = Math.min(12, a.count); },
  };
}

// ---------- vehicles ----------

const vanSide = canvasTex(512, 160, (g, w, h) => {
  g.fillStyle = '#ffffff';
  g.fillRect(0, 0, w, h);
  g.fillStyle = '#38b6e8';
  g.fillRect(0, h - 30, w, 30);
  g.fillStyle = '#1f6fb0';
  g.font = '800 46px "Plus Jakarta Sans", sans-serif';
  g.fillText('冷链配送', 40, 82);
  g.font = '600 26px "Plus Jakarta Sans", sans-serif';
  g.fillText('0–4 ℃', 330, 80);
});

/** A refrigerated box van: a white box with a cooling unit on the front. */
export function buildVan(): ModelHandle {
  const root = new THREE.Group();
  const inner = new THREE.Group();
  root.add(inner);
  const body = bodyMat('#ffffff');
  blk(inner, body, 1.4, 1.5, 1.9, 1.9, 0.45, 0, 0.2);
  blk(inner, M.glass, 0.08, 0.6, 1.7, 2.62, 1.2, 0, 0.03);
  const box = mesh(inner, rbox(3.4, 2.0, 2.0, 0.08), M.white, -0.5, 0.5 + 1.0, 0);
  box.castShadow = true;
  for (const sz of [-1, 1]) {
    const decal = mesh(inner, new THREE.PlaneGeometry(3.2, 1.0), new THREE.MeshStandardMaterial({ map: vanSide, roughness: 0.55 }), -0.5, 1.5, sz * 1.005, false);
    if (sz < 0) decal.rotation.y = Math.PI;
  }
  blk(inner, M.metal, 0.3, 0.5, 1.2, 1.2, 2.35, 0, 0.04);
  for (const x of [-1.6, 1.8]) for (const sz of [-1, 1]) mesh(inner, cylGeo(0.36, 0.36, 0.28, 16), M.rubber, x, 0.36, sz * 0.85).rotation.x = Math.PI / 2;
  return {
    root, tint: [body], top: 3.0,
    update(a) {
      const t = a.truck!;
      root.visible = t.phase !== 'away';
      root.position.set(t.x, -0.3, t.z);
      root.rotation.y = -t.heading;
    },
  };
}

/** A courier on an electric scooter with a delivery box. */
export function buildRider(): ModelHandle {
  const root = new THREE.Group();
  const frame = bodyMat('#2f5bea');
  blk(root, frame, 1.3, 0.25, 0.36, 0, 0.3, 0, 0.08);
  blk(root, frame, 0.18, 0.7, 0.2, 0.55, 0.4, 0, 0.04);
  blk(root, M.ink, 0.06, 0.06, 0.6, 0.62, 1.1, 0, 0.02);
  for (const x of [-0.5, 0.55]) mesh(root, cylGeo(0.25, 0.25, 0.12, 16), M.rubber, x, 0.25, 0).rotation.x = Math.PI / 2;
  const box = blk(root, new THREE.MeshStandardMaterial({ color: '#f6c445', roughness: 0.5 }), 0.5, 0.48, 0.48, -0.45, 0.6, 0, 0.05);
  box.castShadow = true;
  // the rider, in the platform's jacket and helmet
  const jacket = new THREE.MeshStandardMaterial({ color: '#f6c445', roughness: 0.6 });
  mesh(root, new THREE.CapsuleGeometry(0.17, 0.42, 4, 10), jacket, 0.05, 1.05, 0);
  mesh(root, new THREE.SphereGeometry(0.15, 14, 10), new THREE.MeshStandardMaterial({ color: '#f6c445', roughness: 0.3 }), 0.08, 1.58, 0);
  return {
    root, tint: [frame], top: 2.0,
    update(a) {
      const t = a.truck!;
      root.visible = t.phase !== 'away';
      root.position.set(t.x, 0, t.z);
      root.rotation.y = -t.heading;
      box.visible = true;
    },
  };
}

export { P };
