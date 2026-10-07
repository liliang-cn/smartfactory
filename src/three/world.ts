// The static world around a plant layout: hall slab and painted floor plan, walls with dock
// doors, roads, trees and neighbouring blocks. Everything is derived from the layout bounds.
import * as THREE from 'three';
import type { PlantLayout } from '@/core/layout';
import type { Campus } from '@/core/campus';
import { M, P, blk, bodyMat, canvasTex, cyl, mesh } from './kit';

type Box = PlantLayout['box'];

/**
 * `campus`: the plant stands among the company's other plants, which share the ground, the front
 * road and the surroundings (see buildCampus); it keeps only what lies within its own lot.
 */
export function buildWorld(L: PlantLayout, campus = false): THREE.Group {
  const g = new THREE.Group();
  const { box } = L;
  if (!campus) {
    const ground = mesh(g, new THREE.PlaneGeometry(1400, 1400), new THREE.MeshStandardMaterial({ color: P.ground, roughness: 1 }), 0, -0.32, 0, false);
    ground.rotation.x = -Math.PI / 2;
  }

  const W = box.x1 - box.x0;
  const D = box.z1 - box.z0;
  const cx = (box.x0 + box.x1) / 2;
  const cz = (box.z0 + box.z1) / 2;
  const outdoor = L.site === 'outdoor';
  blk(g, outdoor ? bodyMat('#bfe0a6') : M.white, W + 0.8, 0.3, D + 0.8, cx, -0.3, cz, 0.15);
  const floor = mesh(g, new THREE.PlaneGeometry(W, D), new THREE.MeshStandardMaterial({ map: floorTexture(L), roughness: 0.9 }), cx, 0.002, cz, false);
  floor.rotation.x = -Math.PI / 2;
  floor.receiveShadow = true;

  if (outdoor) buildFence(g, box);
  else buildWalls(g, box);
  buildRoads(g, L, campus);
  buildTrees(g, L, campus);
  if (!campus) buildNeighbourhood(g, box);
  return g;
}

// Everything painted on the floor lives in one canvas: line cells, aisles, zones, AGV tape, stencils.
function floorTexture(L: PlantLayout) {
  const { box } = L;
  const W = box.x1 - box.x0;
  const D = box.z1 - box.z0;
  const S = Math.min(28, 4096 / Math.max(W, D)); // px per metre
  return canvasTex(Math.ceil(W * S), Math.ceil(D * S), (c, w, h) => {
    const X = (x: number) => (x - box.x0) * S;
    const Z = (z: number) => (z - box.z0) * S;
    const outdoor = L.site === 'outdoor';
    c.fillStyle = outdoor ? '#d3eabc' : P.slab;
    c.fillRect(0, 0, w, h);
    if (outdoor) {
      // mowing stripes
      c.fillStyle = 'rgba(255,255,255,0.12)';
      for (let x = box.x0; x <= box.x1; x += 8) c.fillRect(X(x), 0, 4 * S, h);
    } else {
      c.strokeStyle = 'rgba(80,95,150,0.06)';
      c.lineWidth = 2;
      for (let x = box.x0; x <= box.x1; x += 6) { c.beginPath(); c.moveTo(X(x), 0); c.lineTo(X(x), h); c.stroke(); }
      for (let z = box.z0; z <= box.z1; z += 6) { c.beginPath(); c.moveTo(0, Z(z)); c.lineTo(w, Z(z)); c.stroke(); }
    }

    const rr = (x0: number, z0: number, x1: number, z1: number, fill: string | null, stroke: string | null, dash: number[] = []) => {
      c.beginPath();
      c.roundRect(X(x0), Z(z0), X(x1) - X(x0), Z(z1) - Z(z0), 0.6 * S);
      if (fill) { c.fillStyle = fill; c.fill(); }
      if (stroke) { c.setLineDash(dash); c.strokeStyle = stroke; c.lineWidth = 0.14 * S; c.stroke(); c.setLineDash([]); }
    };

    for (const l of L.lines) {
      // line cell, and the dashed aisle edge on its front
      if (outdoor) rr(l.x0 - 0.8, l.z - l.beltW / 2 - 0.6, l.outfeed.x + 1.4, l.z + l.beltW / 2 + 1.9, 'rgba(236,214,166,0.55)', '#d8b77c');
      else rr(l.x0 - 0.8, l.z - l.beltW / 2 - 0.6, l.outfeed.x + 1.4, l.z + l.beltW / 2 + 1.9, 'rgba(47,91,234,0.045)', P.yellow);
      c.strokeStyle = outdoor ? 'rgba(255,255,255,0.9)' : P.yellow;
      c.lineWidth = 0.12 * S;
      c.setLineDash([0.9 * S, 0.6 * S]);
      const ez = l.lane + 1.0;
      c.beginPath(); c.moveTo(X(L.warehouse.x1 + 0.6), Z(ez)); c.lineTo(X(L.fg.x0 - 0.4), Z(ez)); c.stroke();
      c.setLineDash([]);
    }
    rr(L.warehouse.x0 - 0.4, L.warehouse.z0 - 1, L.warehouse.x1 + 0.4, L.warehouse.z1 + 1, 'rgba(47,91,234,0.05)', P.yellow);
    rr(L.fg.x0, L.fg.z0, L.fg.x1, L.fg.z1, 'rgba(246,196,69,0.12)', P.yellow, [0.6 * S, 0.4 * S]);
    c.strokeStyle = 'rgba(191,138,82,0.5)';
    c.lineWidth = 0.05 * S;
    for (let x = L.fg.x0 + 0.5; x + 1.4 <= L.fg.x1; x += 2.0) for (let z = L.fg.z0 + 0.5; z + 1.2 <= L.fg.z1; z += 1.95) c.strokeRect(X(x), Z(z), 1.4 * S, 1.2 * S);

    // loading bays by the two dock doors
    const midZ = (box.z0 + box.z1) / 2;
    const hatch = (x0: number, z0: number, x1: number, z1: number) => {
      c.save();
      c.beginPath();
      c.rect(X(x0), Z(z0), X(x1) - X(x0), Z(z1) - Z(z0));
      c.clip();
      c.strokeStyle = 'rgba(246,196,69,0.7)';
      c.lineWidth = 0.12 * S;
      for (let k = -40; k < 80; k++) { c.beginPath(); c.moveTo(X(x0) + k * 0.5 * S, Z(z0)); c.lineTo(X(x0) + k * 0.5 * S - 4 * S, Z(z1)); c.stroke(); }
      c.restore();
      rr(x0, z0, x1, z1, null, P.yellow);
    };
    hatch(box.x0 + 0.3, midZ - 2.5, box.x0 + 1.8, midZ + 2.5);
    hatch(box.x1 - 1.8, midZ - 2.5, box.x1 - 0.3, midZ + 2.5);

    // AGV guide tape and docks
    c.strokeStyle = 'rgba(47,91,234,0.55)';
    c.lineWidth = 0.08 * S;
    for (const a of L.agvs) {
      c.beginPath();
      a.route.forEach((p, i) => (i ? c.lineTo(X(p.x), Z(p.z)) : c.moveTo(X(p.x), Z(p.z))));
      c.stroke();
      c.fillStyle = 'rgba(47,91,234,0.18)';
      for (const p of a.route) { c.beginPath(); c.arc(X(p.x), Z(p.z), 0.75 * S, 0, Math.PI * 2); c.fill(); }
    }

    c.fillStyle = outdoor ? 'rgba(52,110,64,0.6)' : 'rgba(47,91,234,0.5)';
    c.textBaseline = 'middle';
    const stencil = (txt: string, x: number, z: number, size: number) => {
      c.save();
      c.translate(X(x), Z(z));
      c.font = `800 ${size * S}px "Plus Jakarta Sans Variable", "PingFang SC", sans-serif`;
      c.textAlign = 'left';
      c.fillText(txt, 0, 0);
      c.restore();
    };
    for (const l of L.lines) stencil(`${l.id}  ${l.name}`, l.x0 + 1.6, l.lane + 0.15, 0.75);
    c.textAlign = 'center';
    const centred = (txt: string, x: number, z: number, size: number) => {
      c.font = `800 ${size * S}px "Plus Jakarta Sans Variable", "PingFang SC", sans-serif`;
      c.textAlign = 'center';
      c.fillText(txt, X(x), Z(z));
    };
    centred(L.warehouse.name, (L.warehouse.x0 + L.warehouse.x1) / 2, L.warehouse.z1 + 1.8, 1.0);
    centred(L.fg.name, (L.fg.x0 + L.fg.x1) / 2, L.fg.z1 + 1.2, 0.9);
  });
}

function buildWalls(g: THREE.Object3D, box: Box) {
  const H = 6.5;
  const wall = M.white;
  const { x0, x1, z0, z1 } = box;
  const midZ = (z0 + z1) / 2;
  // Tall back wall (far from the default camera) with a window band
  blk(g, wall, x1 - x0 + 0.6, H, 0.4, (x0 + x1) / 2, 0, z0 - 0.2, 0.08);
  blk(g, M.blue, x1 - x0 + 0.7, 0.5, 0.5, (x0 + x1) / 2, H - 0.5, z0 - 0.2, 0.1);
  const win = new THREE.MeshStandardMaterial({ color: '#cbdaff', emissive: '#e8efff', emissiveIntensity: 0.25, roughness: 0.15 });
  for (let x = x0 + 4; x < x1 - 2; x += 6) {
    blk(g, win, 3.6, 1.2, 0.06, x, 3.6, z0 + 0.02, 0.05);
    blk(g, M.blue, 0.3, H - 0.5, 0.5, x - 3, 0, z0 - 0.2, 0.06);
  }
  // Tall left wall with the inbound dock door
  const lw = (za: number, zb: number) => blk(g, wall, 0.4, H, zb - za, x0 - 0.2, 0, (za + zb) / 2, 0.08);
  lw(z0 - 0.4, midZ - 2.8);
  lw(midZ + 2.8, z1 + 0.4);
  blk(g, wall, 0.4, H - 4.4, 5.6, x0 - 0.2, 4.4, midZ, 0.06);
  blk(g, M.blue, 0.5, 0.5, z1 - z0 + 0.8, x0 - 0.2, H - 0.5, midZ, 0.1);
  const d = new THREE.Group();
  d.position.set(x0 - 0.2, 0, midZ);
  d.rotation.y = Math.PI / 2;
  g.add(d);
  blk(d, M.blue, 5.6, 0.3, 0.6, 0, 4.1, 0, 0.06);
  for (const sx of [-1, 1]) blk(d, M.blue, 0.3, 4.4, 0.6, sx * 2.8, 0, 0, 0.06);
  const slat = new THREE.MeshStandardMaterial({ color: '#dfe6fb', roughness: 0.6 });
  for (let i = 0; i < 6; i++) blk(d, slat, 5.2, 0.22, 0.12, 0, 2.6 + i * 0.25, -0.1, 0.03);
  // Low parapets on the camera-facing sides so the floor stays visible
  const low = 0.9;
  blk(g, wall, x1 - x0 + 0.8, low, 0.4, (x0 + x1) / 2, 0, z1 + 0.2, 0.12);
  blk(g, M.blue, x1 - x0 + 0.8, 0.12, 0.44, (x0 + x1) / 2, low, z1 + 0.2, 0.05);
  const rw = (za: number, zb: number) => {
    blk(g, wall, 0.4, low, zb - za, x1 + 0.2, 0, (za + zb) / 2, 0.12);
    blk(g, M.blue, 0.44, 0.12, zb - za, x1 + 0.2, low, (za + zb) / 2, 0.05);
  };
  rw(z0 - 0.4, midZ - 2.8);
  rw(midZ + 2.8, z1 + 0.4);
  for (const sz of [-1, 1]) blk(g, M.blue, 0.3, 4.2, 0.3, x1 + 0.2, 0, midZ + sz * 2.9, 0.06);
  blk(g, M.blue, 0.9, 0.3, 6.4, x1 + 0.5, 4.2, midZ, 0.08);
  for (const x of [x0 - 0.6, x1 + 0.6]) for (const sz of [-1, 1]) blk(g, M.ink, 0.3, 0.6, 0.3, x, 0, midZ + sz * 1.8, 0.06);
}

/** Outdoor sites: a low wooden fence with gates where the inbound and outbound roads meet it. */
function buildFence(g: THREE.Object3D, box: Box) {
  const wood = bodyMat('#c79a68');
  const midZ = (box.z0 + box.z1) / 2;
  const gate = (x: number, z: number) => (x === box.x0 || x === box.x1) && Math.abs(z - midZ) < 3;
  const run = (xa: number, za: number, xb: number, zb: number) => {
    const len = Math.hypot(xb - xa, zb - za);
    const n = Math.max(1, Math.round(len / 3));
    for (let i = 0; i <= n; i++) {
      const x = xa + ((xb - xa) * i) / n;
      const z = za + ((zb - za) * i) / n;
      if (!gate(x, z)) blk(g, wood, 0.14, 1.0, 0.14, x, 0, z, 0.03);
      if (i === n) break;
      const x2 = xa + ((xb - xa) * (i + 1)) / n;
      const z2 = za + ((zb - za) * (i + 1)) / n;
      if (gate(x, z) || gate(x2, z2)) continue;
      const along = xa === xb;
      for (const y of [0.45, 0.8]) blk(g, wood, along ? 0.06 : Math.abs(x2 - x), 0.08, along ? Math.abs(z2 - z) : 0.06, (x + x2) / 2, y, (z + z2) / 2, 0.02);
    }
  };
  run(box.x0, box.z0, box.x1, box.z0);
  run(box.x0, box.z1, box.x1, box.z1);
  run(box.x0, box.z0, box.x0, box.z1);
  run(box.x1, box.z0, box.x1, box.z1);
}

const roadTex = (len: number, horizontal: boolean) => canvasTex(horizontal ? 2048 : 128, horizontal ? 128 : 2048, (c, w, h) => {
    c.fillStyle = P.road;
    c.fillRect(0, 0, w, h);
    c.fillStyle = '#ffffff';
    const n = Math.floor(len / 6);
    for (let i = 0; i < n; i++) {
      if (horizontal) c.fillRect((i / n) * w, h / 2 - 3, (w / n) * 0.45, 6);
      else c.fillRect(w / 2 - 3, (i / n) * h, 6, (h / n) * 0.45);
    }
    c.fillStyle = 'rgba(255,255,255,0.9)';
    if (horizontal) { c.fillRect(0, 6, w, 4); c.fillRect(0, h - 10, w, 4); } else { c.fillRect(6, 0, 4, h); c.fillRect(w - 10, 0, 4, h); }
  });
function road(g: THREE.Object3D, x: number, z: number, w: number, d: number, horizontal: boolean) {
  const m = mesh(g, new THREE.PlaneGeometry(w, d), new THREE.MeshStandardMaterial({ map: roadTex(horizontal ? w : d, horizontal), roughness: 0.95 }), x, -0.31, z, false);
  m.rotation.x = -Math.PI / 2;
  m.receiveShadow = true;
}
const pave = new THREE.MeshStandardMaterial({ color: '#f5f6fb', roughness: 0.9 });

/** The road in front of a plant (or a row of plants) with its two pavements. */
function frontRoad(g: THREE.Object3D, x0: number, x1: number, z: number) {
  road(g, (x0 + x1) / 2, z, x1 - x0, 8, true);
  blk(g, pave, x1 - x0, 0.12, 1.4, (x0 + x1) / 2, -0.32, z - 4.7, 0.05);
  blk(g, pave, x1 - x0, 0.12, 1.4, (x0 + x1) / 2, -0.32, z + 4.7, 0.05);
}

function buildRoads(g: THREE.Object3D, L: PlantLayout, campus: boolean) {
  const { box, roads } = L;
  const span = box.x1 - box.x0 + 400;
  const back = campus ? box.z0 - 10 : box.z0 - 80;
  const sideLen = roads.front + 4 - back;
  if (!campus) frontRoad(g, (box.x0 + box.x1) / 2 - span / 2, (box.x0 + box.x1) / 2 + span / 2, roads.front);
  road(g, roads.left, (roads.front + back) / 2 - 2, 8, sideLen, false);
  road(g, roads.right, (roads.front + back) / 2 - 2, 8, sideLen, false);
  const midZ = (box.z0 + box.z1) / 2;
  blk(g, pave, box.x0 - roads.left - 4, 0.1, 12, (box.x0 + roads.left + 4) / 2, -0.32, midZ, 0.3);
  blk(g, pave, roads.right - 4 - box.x1, 0.1, 12, (box.x1 + roads.right - 4) / 2, -0.32, midZ, 0.3);
  const grass = (x: number, z: number, w: number, d: number) => blk(g, M.mint, w, 0.1, d, x, -0.32, z, 0.4);
  if (!campus) grass((box.x0 + box.x1) / 2, roads.front + 9, span * 0.7, 7);
  grass(roads.left - 8, midZ, 7, box.z1 - box.z0 + 20);
  grass(roads.right + 8, midZ, 7, box.z1 - box.z0 + 20);
  grass((box.x0 + box.x1) / 2, box.z0 - 5, box.x1 - box.x0, 4);
}

const j = () => (Math.random() - 0.5) * 2;

function buildTrees(g: THREE.Object3D, L: PlantLayout, campus: boolean) {
  const { box, roads } = L;
  const spots: [number, number][] = [];
  if (!campus) for (let x = box.x0 - 70; x <= box.x1 + 70; x += 7) spots.push([x + j(), roads.front + 9 + j()]);
  for (let z = box.z0 - 10; z <= box.z1 + 2; z += 6) { spots.push([roads.left - 8 + j(), z]); spots.push([roads.right + 8 + j(), z]); }
  for (let x = box.x0; x <= box.x1; x += 5.5) spots.push([x, box.z0 - 5 + j() * 0.5]);
  trees(g, spots);
}

function trees(g: THREE.Object3D, spots: [number, number][]) {
  const trunk = new THREE.InstancedMesh(new THREE.CylinderGeometry(0.12, 0.16, 1.4, 8), M.trunk, spots.length);
  const crown = new THREE.InstancedMesh(new THREE.SphereGeometry(1, 18, 14), M.tree, spots.length);
  const m = new THREE.Matrix4();
  const q = new THREE.Quaternion();
  const col = new THREE.Color();
  spots.forEach(([x, z], i) => {
    const s = 0.8 + Math.random() * 0.5;
    m.compose(new THREE.Vector3(x, 0.4, z), q, new THREE.Vector3(1, 1, 1));
    trunk.setMatrixAt(i, m);
    m.compose(new THREE.Vector3(x, 1.4 + s * 1.1, z), q, new THREE.Vector3(s, s * 1.25, s));
    crown.setMatrixAt(i, m);
    crown.setColorAt(i, col.set(Math.random() > 0.5 ? P.tree : P.treeDark));
  });
  for (const im of [trunk, crown]) { im.castShadow = true; im.receiveShadow = true; g.add(im); }
}

// Simple blocks in the distance so the plant sits in a place, not in a void.
function buildNeighbourhood(g: THREE.Object3D, box: Box) {
  const mats = [M.white, M.shell, new THREE.MeshStandardMaterial({ color: '#dfe6fb', roughness: 0.7 })];
  const lots: [number, number, number, number, number][] = [
    [box.x0 - 45, box.z0 - 25, 18, 6, 14], [box.x0 - 48, box.z0 + 6, 14, 8, 12], [box.x0 + 8, box.z0 - 30, 22, 7, 12],
    [box.x1 - 18, box.z0 - 32, 18, 9, 12], [box.x1 + 42, box.z0 - 22, 20, 7, 16], [box.x1 + 45, box.z0 + 8, 14, 10, 12],
  ];
  lots.forEach(([x, z, w, h, d], i) => {
    blk(g, mats[i % 3], w, h, d, x, -0.3, z, 0.3);
    blk(g, M.blue, w + 0.2, 0.3, d + 0.2, x, h - 0.3, z, 0.1);
    for (let k = 0; k < 3; k++) cyl(g, M.metal, 0.8, 0.5, x - w / 3 + k * (w / 3), h, z, 16);
  });
}

/**
 * What the plants of a company share when shown together (each plant's own lot comes from
 * buildWorld(L, true)): the ground, one front road per row with its grass and trees, the plant's
 * name planted in the grass before it, and the neighbourhood around the whole site.
 */
export function buildCampus(c: Campus): THREE.Group {
  const g = new THREE.Group();
  const { bounds } = c;
  const ground = mesh(g, new THREE.PlaneGeometry(2400, 2400), new THREE.MeshStandardMaterial({ color: P.ground, roughness: 1 }), 0, -0.32, 0, false);
  ground.rotation.x = -Math.PI / 2;
  const spots: [number, number][] = [];
  for (const row of c.rows) {
    const x0 = bounds.x0 - 120;
    const x1 = bounds.x1 + 120;
    frontRoad(g, x0, x1, row.front);
    blk(g, M.mint, x1 - x0, 0.1, 7, (x0 + x1) / 2, -0.32, row.front + 9, 0.4);
    const inRow = c.slots.filter((s) => Math.abs(s.oz + s.layout.roads.front - row.front) < 0.01);
    const sideRoads = inRow.flatMap((s) => [s.ox + s.layout.roads.left, s.ox + s.layout.roads.right]);
    const signs = inRow.map((s) => [s.ox + (s.layout.box.x0 + s.layout.box.x1) / 2 - 24, s.ox + (s.layout.box.x0 + s.layout.box.x1) / 2 + 24]);
    for (let x = x0; x <= x1; x += 7) {
      if (sideRoads.some((r) => Math.abs(x - r) < 6) || signs.some(([a, b]) => x > a && x < b)) continue;
      spots.push([x + j(), row.front + 9 + j()]);
    }
    for (const s of inRow) sign(g, `${s.layout.name}  ${s.layout.hall}`, s.ox + (s.layout.box.x0 + s.layout.box.x1) / 2, row.front + 9);
  }
  trees(g, spots);
  buildNeighbourhood(g, { x0: bounds.x0 + 16, x1: bounds.x1 - 16, z0: bounds.z0 + 14, z1: bounds.z1 });
  return g;
}

/** A plant's name laid out in the grass strip in front of it, readable from the overview. */
function sign(g: THREE.Object3D, text: string, x: number, z: number) {
  const W = 46;
  const D = 6;
  const S = 40;
  const tex = canvasTex(W * S, D * S, (c, w, h) => {
    c.font = `800 ${4.2 * S}px "Plus Jakarta Sans Variable", "PingFang SC", sans-serif`;
    c.textAlign = 'center';
    c.textBaseline = 'middle';
    c.fillStyle = 'rgba(31,63,176,0.8)';
    c.fillText(text, w / 2, h / 2 + 0.2 * S, w * 0.96);
  });
  const m = mesh(g, new THREE.PlaneGeometry(W, D), new THREE.MeshBasicMaterial({ map: tex, transparent: true, depthWrite: false }), x, -0.2, z, false);
  m.rotation.x = -Math.PI / 2;
  m.receiveShadow = false;
}
