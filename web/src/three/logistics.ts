// Line-side and hall logistics: belts, line-side pallets, AGVs, trucks, the raw-material
// store and the finished-goods area. All sized from the layout and the product dims.
import * as THREE from 'three';
import type { LineLayout, ProductInfo, Zone } from '@/core/layout';
import { M, P, blk, bodyMat, canvasTex, colorOf, cyl, cylGeo, mesh, rbox } from './kit';
import { andon, pallet, stack, type ModelHandle } from './common';
import { stackLayout, unitBox } from './products';

export function buildConveyor(line: LineLayout, walk = false, outdoor = false): ModelHandle {
  if (walk) return buildWalkway(line, outdoor);
  const root = new THREE.Group();
  const len = line.x1 - line.x0;
  const Y = line.beltY;
  const W = line.beltW;
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
  mesh(root, new THREE.BoxGeometry(len, 0.05, W - 0.04), beltMat, 0, Y - 0.025, 0);
  const sideH = Math.min(0.2, Y - 0.1);
  for (const sz of [-1, 1]) {
    blk(root, frame, len + 0.1, sideH, 0.08, 0, Y - 0.02 - sideH, sz * (W / 2 + 0.02), 0.03);
    blk(root, M.white, len, 0.05, 0.05, 0, Y + 0.08, sz * (W / 2 + 0.05), 0.02);
  }
  const legH = Y - 0.02 - sideH;
  if (legH > 0.05) {
    for (let x = -len / 2 + 0.6; x < len / 2; x += 2.2) {
      for (const sz of [-1, 1]) blk(root, M.greyDark, 0.07, legH, 0.07, x, 0, sz * (W / 2 - 0.05), 0.02);
    }
  }
  for (const sx of [-1, 1]) mesh(root, cylGeo(Math.min(0.1, Y / 2 - 0.02), Math.min(0.1, Y / 2 - 0.02), W, 16), M.metal, (sx * len) / 2, Y - 0.08, 0).rotation.x = Math.PI / 2;
  blk(root, M.blue, 0.4, 0.34, 0.36, len / 2 - 0.3, Math.max(0, Y - 0.5), -(W / 2 + 0.25), 0.05);
  const lamp = andon(root, len / 2 - 0.3, Math.max(0.34, Y - 0.16), -(W / 2 + 0.25));
  return {
    root, tint: [frame], top: Y + 0.7, labelAt: new THREE.Vector3(-len / 2 + 1.5, 0, 0),
    update(a, dt, t) {
      lamp.set(a.state, t);
      if (a.state === 'running') beltTex.offset.x -= (line.speed * a.speed * dt) / 0.5;
    },
  };
}

/** The "conveyor" of a walk-flow line: a paved walkway people (or vehicles) move along. */
function buildWalkway(line: LineLayout, outdoor: boolean): ModelHandle {
  const root = new THREE.Group();
  const len = line.x1 - line.x0;
  const W = line.beltW;
  root.position.set(line.x0 + len / 2, 0, line.z);
  const tex = canvasTex(256, 64, (g, w, h) => {
    g.fillStyle = outdoor ? '#efe3c8' : '#e6e9f4';
    g.fillRect(0, 0, w, h);
    g.fillStyle = outdoor ? '#e2d2b0' : '#d8dcec';
    // irregular paving stones
    for (let x = 4; x < w; x += 30) for (let y = 4; y < h; y += 30) {
      g.beginPath();
      g.roundRect(x + ((y / 30) % 2) * 12, y, 24, 22, 6);
      g.fill();
    }
  });
  tex.wrapS = THREE.RepeatWrapping;
  tex.repeat.set(len / 2, 1);
  const path = new THREE.MeshStandardMaterial({ map: tex, roughness: 0.95 });
  const edge = bodyMat(outdoor ? '#c9a873' : '#cfd5ea');
  mesh(root, new THREE.BoxGeometry(len, 0.05, W), path, 0, 0.025, 0);
  for (const sz of [-1, 1]) blk(root, edge, len, 0.07, 0.1, 0, 0, sz * (W / 2 + 0.05), 0.03);
  // a lamp post at the end carries the status light
  blk(root, M.greyDark, 0.08, 1.4, 0.08, len / 2 - 0.3, 0, -(W / 2 + 0.3), 0.02);
  const lamp = andon(root, len / 2 - 0.3, 1.4, -(W / 2 + 0.3));
  return {
    root, tint: [edge], top: 1.8, labelAt: new THREE.Vector3(-len / 2 + 1.5, 0, 0),
    update(a, _dt, t) {
      lamp.set(a.state, t);
    },
  };
}

/** Line-side stock shown as boxes the size of the stage, in the colour of its main part. */
function sideStock(root: THREE.Object3D, product: ProductInfo, stage: string, maxUnits: number) {
  const s = stackLayout(product, stage, maxUnits);
  const main = product.stages[stage]?.[0];
  const mat = new THREE.MeshStandardMaterial({ color: colorOf(main?.color, P.kraft), roughness: 0.5, metalness: main?.metal ?? 0 });
  if (!s.big) pallet(root, 0, 0, 0, s.palletW, s.palletD);
  const im = stack(root, unitBox(s.dims), mat, s.nx, s.nz, s.layers, s.px, s.py, s.pz, s.big ? 0 : 0.14);
  return { im, max: s.nx * s.nz * s.layers, palletW: s.palletW };
}

/**
 * Walk-flow lines carry people, not parts: their supplies and returns are shown as generic
 * crates on a pallet (food packs in, dishes and rubbish out) instead of stacks of the unit.
 */
function crateStock(root: THREE.Object3D, maxUnits: number, color: string) {
  const nx = 4, nz = 3;
  const layers = Math.max(1, Math.ceil(maxUnits / (nx * nz)));
  pallet(root, 0, 0, 0, nx * 0.55, nz * 0.45);
  const mat = new THREE.MeshStandardMaterial({ color, roughness: 0.55 });
  const im = stack(root, unitBox({ l: 0.5, h: 0.32, d: 0.4 }), mat, nx, nz, layers, 0.55, 0.34, 0.45, 0.14);
  return { im, max: nx * nz * layers, palletW: nx * 0.55 };
}

export function buildBuffer(line: LineLayout, walk = false): ModelHandle {
  const root = new THREE.Group();
  root.position.set(line.infeed.x, 0, line.infeed.z);
  const { im, max } = walk ? crateStock(root, line.infeed.cap, P.kraft) : sideStock(root, line.product, line.input, line.infeed.cap);
  return {
    root, tint: [], top: 1.8,
    update(a) { im.count = Math.min(max, a.stock); },
  };
}

export function buildOutfeed(line: LineLayout, walk = false): ModelHandle {
  const root = new THREE.Group();
  root.position.set(line.outfeed.x, 0, line.outfeed.z);
  const last = line.product.order[line.product.order.length - 1];
  const { im, max, palletW } = walk ? crateStock(root, line.outfeed.cap, '#9fb2d8') : sideStock(root, line.product, last, line.outfeed.cap);
  blk(root, M.blue, 0.06, 1.4, Math.max(1.1, line.beltW), palletW / 2 + 0.15, 0, 0, 0.02);
  return {
    root, tint: [], top: 2.4,
    update(a) { im.count = Math.min(max, a.count); },
  };
}

// ---------- vehicles ----------

const cargoRaw = new THREE.MeshStandardMaterial({ color: '#aab6d3', roughness: 0.35, metalness: 0.5 });

export function buildAGV(forklift = false): ModelHandle {
  const root = new THREE.Group();
  const body = bodyMat(P.yellow);
  blk(root, M.ink, 1.24, 0.14, 0.8, 0, 0.05, 0, 0.05);
  blk(root, body, 1.3, 0.22, 0.86, 0, 0.12, 0, 0.1);
  if (forklift) {
    // a driver's forklift: mast and forks at the front, overhead guard, counterweight
    for (const sz of [-0.3, 0.3]) blk(root, M.greyDark, 0.08, 2.0, 0.08, 0.7, 0.1, sz, 0.02);
    for (const sz of [-0.22, 0.22]) blk(root, M.greyDark, 0.9, 0.05, 0.1, 1.1, 0.12, sz, 0.01);
    for (const sx of [-0.45, 0.35]) for (const sz of [-0.38, 0.38]) blk(root, M.ink, 0.05, 1.6, 0.05, sx, 0.34, sz, 0.01);
    blk(root, body, 0.95, 0.06, 0.86, -0.05, 1.94, 0, 0.02);
    blk(root, body, 0.35, 0.5, 0.82, -0.5, 0.34, 0, 0.06);
  }
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
  const raw = stack(cargo, rbox(0.3, 0.12, 0.26, 0.02), cargoRaw, 3, 2, 3, 0.34, 0.13, 0.32, 0.5);
  raw.count = 18;
  const fg = stack(cargo, rbox(0.36, 0.28, 0.42, 0.03), M.kraft, 3, 2, 2, 0.37, 0.29, 0.4, 0.5);
  fg.count = 12;
  const led: Record<string, string> = { running: '#22b573', moving: '#22b573', waiting: '#f5a524', handling: '#4f7cff', fault: '#ef4444', maint: '#f5a524', stopped: '#33415f' };
  return {
    root, tint: [body], top: 1.3,
    update(a, _dt, t) {
      const g = a.agv!;
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

export function buildTruck(): ModelHandle {
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
      const t = a.truck!;
      root.visible = t.phase !== 'away';
      root.position.set(t.x, -0.3, t.z);
      root.rotation.y = -t.heading;
    },
  };
}

// ---------- storage ----------

export function buildWarehouse(def: Zone): ModelHandle {
  const root = new THREE.Group();
  const upright = new THREE.MeshStandardMaterial({ color: P.blue, roughness: 0.45 });
  const beam = new THREE.MeshStandardMaterial({ color: P.orange, roughness: 0.45 });
  // two single racks on the outside, one back-to-back pair in the middle
  const rows = [1.4, 4.8, 6.0, 9.4].map((o) => def.x0 + o * ((def.x1 - def.x0) / 11));
  const levels = [0.12, 1.6, 3.1, 4.6];
  const bay = 2.8;
  const bays = Math.max(1, Math.floor((def.z1 - def.z0) / bay));
  const z0 = (def.z0 + def.z1) / 2 - (bays * bay) / 2;
  const ups = new THREE.InstancedMesh(new THREE.BoxGeometry(0.1, 5.8, 0.1), upright, rows.length * (bays + 1) * 2);
  const beams = new THREE.InstancedMesh(new THREE.BoxGeometry(0.08, 0.12, bay), beam, rows.length * bays * levels.length * 2);
  const loads = new THREE.InstancedMesh(rbox(1.0, 1.05, 1.15, 0.06), new THREE.MeshStandardMaterial({ roughness: 0.8 }), rows.length * bays * levels.length * 2);
  const m = new THREE.Matrix4();
  let iu = 0;
  let ib = 0;
  const slots: [number, number, number][] = [];
  for (const rx of rows) {
    for (let b = 0; b <= bays; b++) for (const dx of [-0.5, 0.5]) { m.makeTranslation(rx + dx, 2.9, z0 + b * bay); ups.setMatrixAt(iu++, m); }
    for (let b = 0; b < bays; b++) {
      const z = z0 + b * bay + bay / 2;
      for (const y of levels) {
        if (y > 0.5) for (const dx of [-0.5, 0.5]) { m.makeTranslation(rx + dx, y - 0.06, z); beams.setMatrixAt(ib++, m); }
        for (const dz of [-0.65, 0.65]) slots.push([rx, y + 0.53, z + dz]);
      }
    }
  }
  // Shuffle so a falling stock level empties the racks unevenly, like a real store.
  for (let i = slots.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [slots[i], slots[j]] = [slots[j], slots[i]];
  }
  const col = new THREE.Color();
  const tints = ['#d9a46b', '#cf9a60', '#e2b47f', '#a9bcff', '#d9a46b', '#c4935a'];
  slots.forEach(([x, y, z], i) => {
    m.makeTranslation(x, y, z);
    loads.setMatrixAt(i, m);
    loads.setColorAt(i, col.set(tints[i % tints.length]));
  });
  ups.count = iu;
  beams.count = ib;
  for (const im of [ups, beams, loads]) { im.castShadow = true; im.receiveShadow = true; root.add(im); }
  return {
    root, tint: [], top: 6.4, labelAt: new THREE.Vector3((def.x0 + def.x1) / 2, 0, (def.z0 + def.z1) / 2),
    update(a) { loads.count = Math.round(slots.length * Math.min(1, a.stock / a.cap)); },
  };
}

export const FG_PER_PALLET = 12;
const fgGrid = (def: Zone) => [Math.max(1, Math.floor((def.x1 - def.x0 - 0.4) / 2.0)), Math.max(1, Math.floor((def.z1 - def.z0 - 0.2) / 1.95))];
/** How many units the finished-goods floor can show. */
export const fgCapacity = (def: Zone) => fgGrid(def)[0] * fgGrid(def)[1] * FG_PER_PALLET;

export function buildFG(def: Zone): ModelHandle {
  const root = new THREE.Group();
  const [nx, nz] = fgGrid(def);
  const cells: [number, number][] = [];
  for (let i = 0; i < nx; i++) for (let j = 0; j < nz; j++) cells.push([def.x0 + 1.2 + i * 2.0, def.z0 + 1.1 + j * 1.95]);
  const pal = new THREE.InstancedMesh(rbox(1.2, 0.14, 1.0, 0.02), M.wood, cells.length);
  const box = new THREE.InstancedMesh(rbox(1.14, 0.9, 0.96, 0.06), M.kraft, cells.length);
  const wrap = new THREE.InstancedMesh(rbox(1.16, 0.06, 0.98, 0.02), M.blueSoft, cells.length);
  const m = new THREE.Matrix4();
  cells.forEach(([x, z], i) => {
    m.makeTranslation(x, 0.07, z); pal.setMatrixAt(i, m);
    m.makeTranslation(x, 0.14 + 0.45, z); box.setMatrixAt(i, m);
    m.makeTranslation(x, 0.14 + 0.65, z); wrap.setMatrixAt(i, m);
  });
  // all cells shown until the first update, so the footprint of the selection outline has a size
  for (const im of [pal, box, wrap]) { im.castShadow = true; im.receiveShadow = true; root.add(im); }
  return {
    root, tint: [], top: 1.8, labelAt: new THREE.Vector3((def.x0 + def.x1) / 2, 0, (def.z0 + def.z1) / 2),
    update(a) {
      const n = Math.min(cells.length, Math.ceil(a.count / FG_PER_PALLET));
      pal.count = n; box.count = n; wrap.count = n;
    },
  };
}
