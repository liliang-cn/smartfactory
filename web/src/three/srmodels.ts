// Models built from NVIDIA SimReady parts (see simready.ts): the collaborative arm, roller
// conveyors, the AMR and the forklift, pallet racking and the finished-goods pallets. Each
// keeps the contract of the procedural model it stands in for (same frame, same update).
import * as THREE from 'three';
import type { LineLayout, Zone } from '@/core/layout';
import { M, blk, clamp01, colorOf, smooth } from './kit';
import { andon, emitter, pallet, stack, type ModelHandle } from './common';
import { FG_PER_PALLET } from './logistics';
import type { BuildCtx } from './archetypes';
import { SRInstances, fit, hasSR, sr, srBox, type SRName } from './simready';

const rad = THREE.MathUtils.degToRad;

/** The jointed parts of a SimReady copy, re-hung on their joint parents so they move together. */
function rig(obj: THREE.Object3D) {
  const parts = new Map<string, THREE.Object3D>();
  obj.traverse((o) => { if (o.userData?.joints?.length) parts.set(o.name.replace(/_obj_00$/, ''), o); });
  obj.updateMatrixWorld(true);
  for (const [, o] of parts) {
    const parent = parts.get(String(o.userData.joints[0].parent).replace(/_obj_00$/, ''));
    if (parent && parent !== o) parent.attach(o);
  }
  const rest = new Map([...parts].map(([k, o]) => [k, o.quaternion.clone()]));
  const q = new THREE.Quaternion();
  const ax = new THREE.Vector3();
  return {
    has: (k: string) => parts.has(k),
    get: (k: string) => parts.get(k),
    /** turn a revolute part to `deg` from where it rests (degrees, about its own axis) */
    turn(k: string, deg: number) {
      const o = parts.get(k);
      if (!o) return;
      const j = o.userData.joints[0];
      // the axis is given in the model's frame; parents are at rest-rotation identity, so it is local
      q.setFromAxisAngle(ax.fromArray(j.axis).normalize(), rad(deg));
      o.quaternion.copy(rest.get(k)!).multiply(q);
    },
  };
}

// ---------- collaborative arm beside the belt ----------

export function buildArmSR(c: BuildCtx): ModelHandle {
  const root = new THREE.Group();
  const tool = c.look.tool ?? 'gripper';
  const welds = tool === 'welder' || tool === 'torch' || tool === 'spray';
  const [L] = c.size;
  const edge = c.beltW / 2;
  // the arm's shoulder sits about 1 m up and its forearm reaches 0.8 m: scale it to the belt
  const k = THREE.MathUtils.clamp((c.top + 0.9) / 1.45, 1.15, 1.9);
  const BZ = -(edge + 0.3 * k);
  const { obj, mats } = sr('arm', true);
  const arm = new THREE.Group();
  arm.add(obj);
  arm.scale.setScalar(k);
  arm.position.set(0, 0, BZ);
  // the model reaches along +x; turn it to face the belt (+z)
  arm.rotation.y = -Math.PI / 2;
  root.add(arm);
  const r = rig(obj);
  // the model is a mobile arm: lift it to full height so it reaches over the parts
  const lift = r.get('armheight_01');
  if (lift) lift.position.y += 0.18;

  // a kitting cart of parts on the far side, like a real cell
  if (hasSR('bins')) {
    const cart = fit(sr('bins').obj, 'bins', 0.7, 0.75);
    cart.position.set(Math.max(0, L / 2 - 0.45), 0, BZ - 1.15 * k);
    root.add(cart);
  }
  blk(root, M.ink, 0.45, 1.1, 0.4, L / 2 + 0.35, 0, BZ - 0.2, 0.05);
  const lamp = andon(root, L / 2 + 0.35, 1.1, BZ - 0.2);

  const fx = tool === 'welder' ? emitter(root, '#ffcf6b', 0.06, 9.8)
    : tool === 'torch' ? emitter(root, '#ff9a3c', 0.05, 6)
      : tool === 'spray' ? emitter(root, c.look.fluid ? colorOf(c.look.fluid) : '#9fc0ff', 0.09, 1.5, 90) : null;
  const flash = tool === 'welder' || tool === 'torch' ? new THREE.PointLight('#ffb35c', 0, 4, 2) : null;
  if (flash) root.add(flash);
  const tipNode = r.get('armShoulder_04');
  const tipLocal = new THREE.Vector3(0.32, -0.02, 0);
  const tip = new THREE.Vector3();

  // joint targets: [base yaw, shoulder, elbow, wrist]
  const HOME = [40, 0, -20, 0];
  const OVER = [0, -10, -35, 0];
  const DOWN = [0, -28, -10, 0];
  const pose = [...HOME];
  const want = [...HOME];
  const mix = (a: number[], b: number[], t: number) => { const s = smooth(clamp01(t)); for (let i = 0; i < 4; i++) want[i] = a[i] + (b[i] - a[i]) * s; };

  return {
    root, tint: mats, top: Math.max(2.4, c.top + 1.4),
    labelAt: new THREE.Vector3(0, 0, BZ),
    update(a, dt, t) {
      lamp.set(a.state, t);
      const p = a.progress;
      let working = false;
      if (a.state === 'running') {
        if (p < 0.2) mix(HOME, OVER, p / 0.2);
        else if (p < 0.32) mix(OVER, DOWN, (p - 0.2) / 0.12);
        else if (p < 0.7) {
          mix(DOWN, DOWN, 0);
          want[0] += welds ? Math.sin((p - 0.32) * 9) * 14 : Math.sin(p * 40) * 1.5;
          working = welds;
        } else if (p < 0.82) mix(DOWN, OVER, (p - 0.7) / 0.12);
        else mix(OVER, HOME, (p - 0.82) / 0.18);
      } else mix(HOME, HOME, 0);
      for (let i = 0; i < 4; i++) pose[i] += (want[i] - pose[i]) * (1 - Math.exp(-dt * 12));
      r.turn('armBase_01', pose[0]);
      r.turn('armShoulder_01', pose[1]);
      r.turn('armShoulder_02', pose[2]);
      r.turn('armShoulder_04', pose[3]);
      if (tipNode && (fx || flash)) {
        root.updateMatrixWorld(true);
        tip.copy(tipLocal);
        tipNode.localToWorld(tip);
        root.worldToLocal(tip);
        fx?.step(dt, working, tip, tool === 'spray' ? [0.8, -1.2, 0.8] : [2.4, 1.8, 2.4], c.beltY);
        if (flash) {
          flash.position.copy(tip);
          flash.intensity = working ? 3 + Math.random() * 6 : 0;
        }
      }
    },
  };
}

// ---------- roller conveyor ----------

export function buildConveyorSR(line: LineLayout): ModelHandle {
  const root = new THREE.Group();
  const len = line.x1 - line.x0;
  const Y = line.beltY;
  const W = line.beltW;
  root.position.set(line.x0 + len / 2, 0, line.z);
  // one roller section is 2 m long (z) and 1.15 m wide (x), rollers on top at 0.74 m
  const b = srBox('roller');
  const size = b.getSize(new THREE.Vector3());
  const n = Math.max(1, Math.round(len / size.z));
  const segL = len / n;
  const ROLL = 0.74 + 0.04;
  const inst = new SRInstances('roller', n);
  const m = new THREE.Matrix4();
  const s = new THREE.Vector3(W / size.x, Y / ROLL, segL / size.z);
  const q = new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(0, 1, 0), Math.PI / 2);
  const c = b.getCenter(new THREE.Vector3());
  for (let i = 0; i < n; i++) {
    // model centre to origin, scale to the line, quarter turn so it runs along x, then step along
    const place = new THREE.Matrix4().compose(new THREE.Vector3(-len / 2 + segL * (i + 0.5), 0, 0), q, s);
    m.makeTranslation(-c.x, -b.min.y, -c.z).premultiply(place);
    inst.set(i, m);
  }
  inst.count = n;
  inst.done();
  root.add(inst.group);
  blk(root, M.blue, 0.4, 0.34, 0.36, len / 2 - 0.3, Math.max(0, Y - 0.5), -(W / 2 + 0.3), 0.05);
  const lamp = andon(root, len / 2 - 0.3, Math.max(0.34, Y - 0.16), -(W / 2 + 0.3));
  // floor guards at both ends of the line
  if (hasSR('guard')) {
    const g = new SRInstances('guard', 4);
    let i = 0;
    for (const sx of [-1, 1]) for (const sz of [-1, 1]) {
      m.makeRotationY(Math.PI / 2).setPosition(sx * (len / 2 + 0.35), 0, sz * (W / 2 - 0.1));
      g.set(i++, m);
    }
    g.count = 4;
    g.done();
    root.add(g.group);
  }
  return {
    root, tint: [], top: Y + 0.7, labelAt: new THREE.Vector3(-len / 2 + 1.5, 0, 0),
    update(a, _dt, t) { lamp.set(a.state, t); },
  };
}

// ---------- AMR and forklift ----------

const cargoRaw = new THREE.MeshStandardMaterial({ color: '#aab6d3', roughness: 0.35, metalness: 0.5 });

export function buildAGVSR(forklift: boolean): ModelHandle {
  const root = new THREE.Group();
  const name: SRName = forklift ? 'forklift' : 'amr';
  const { obj, mats } = sr(name, true);
  // the AMR is 0.75 x 0.69 m; the sim moves 1.3 m carts, so it is shown at the cart's size
  const body = forklift ? fit(obj, name, 1.9, 0.9, 2.1, { turn: true }) : fit(obj, name, 1.3, 0.86, 0.34, { stretch: true });
  if (forklift) body.rotation.y = Math.PI;
  root.add(body);
  const deck = forklift ? 0.18 : 0.34;
  const cargo = new THREE.Group();
  if (forklift) cargo.position.x = 0.75;
  root.add(cargo);
  pallet(cargo, 0, deck, 0, 1.1, 0.8);
  const raw = stack(cargo, new THREE.BoxGeometry(0.3, 0.12, 0.26), cargoRaw, 3, 2, 3, 0.34, 0.13, 0.32, deck + 0.14);
  raw.count = 18;
  const fg = stack(cargo, new THREE.BoxGeometry(0.36, 0.28, 0.42), M.kraft, 3, 2, 2, 0.37, 0.29, 0.4, deck + 0.14);
  fg.count = 12;
  const ledMat = new THREE.MeshStandardMaterial({ color: '#1e2a52', emissive: '#22b573', emissiveIntensity: 1.6 });
  const led = new THREE.Mesh(new THREE.BoxGeometry(0.05, 0.05, 0.5), ledMat);
  led.position.set(forklift ? -0.2 : 0.66, forklift ? 2.15 : 0.22, 0);
  root.add(led);
  const ledC: Record<string, string> = { running: '#22b573', moving: '#22b573', waiting: '#f5a524', handling: '#4f7cff', fault: '#ef4444', maint: '#f5a524', stopped: '#33415f' };
  return {
    root, tint: mats, top: forklift ? 2.4 : 1.3,
    update(a, _dt, t) {
      const g = a.agv!;
      root.position.set(g.x, 0, g.z);
      root.rotation.y = -g.heading;
      cargo.visible = !!g.cargo;
      raw.visible = g.cargo?.kind === 'raw';
      fg.visible = g.cargo?.kind === 'fg';
      ledMat.emissive.set(ledC[a.state] || '#33415f');
      ledMat.emissiveIntensity = a.state === 'fault' ? (Math.sin(t * 8) > 0 ? 2 : 0.2) : 1.6;
    },
  };
}

// ---------- pallet racking ----------

export function buildWarehouseSR(def: Zone): ModelHandle {
  const root = new THREE.Group();
  // a rack section is 4 m long (x), 2.07 m deep (z), 3 m tall with two levels: run the rows along z
  const b = srBox('rack');
  const size = b.getSize(new THREE.Vector3());
  const c = b.getCenter(new THREE.Vector3());
  const D = def.z1 - def.z0;
  const k = 1.4; // taller racks: 4.2 m, three loaded levels
  const bayL = size.x * k * 0.75;
  const rowD = size.z * k * 0.55;
  const rows: number[] = [];
  // pairs of rows with an aisle between pairs
  for (let x = def.x0 + rowD / 2 + 0.6; x < def.x1 - rowD / 2; x += rowD) {
    rows.push(x);
    if (rows.length % 2 === 0) x += 3.0;
  }
  const bays = Math.max(1, Math.floor((D - 1) / bayL));
  const z0 = (def.z0 + def.z1) / 2 - (bays * bayL) / 2;
  const racks = new SRInstances('rack', rows.length * bays);
  const m = new THREE.Matrix4();
  const q = new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(0, 1, 0), Math.PI / 2);
  const sc = new THREE.Vector3(k * 0.75, k, k * 0.55);
  let i = 0;
  const slots: [number, number, number][] = [];
  const levels = [0.02, 1.48 * k * 0.98, 2.9 * k * 0.98].map((y) => Math.min(y, 4.3));
  for (const rx of rows) for (let bI = 0; bI < bays; bI++) {
    const z = z0 + bI * bayL + bayL / 2;
    m.makeTranslation(-c.x, -b.min.y, -c.z).premultiply(new THREE.Matrix4().compose(new THREE.Vector3(rx, 0, z), q, sc));
    racks.set(i++, m);
    for (const y of levels) for (const dz of [-bayL / 3, 0, bayL / 3]) slots.push([rx, y, z + dz]);
  }
  racks.count = i;
  racks.done();
  root.add(racks.group);
  for (let j = slots.length - 1; j > 0; j--) {
    const r = Math.floor(Math.random() * (j + 1));
    [slots[j], slots[r]] = [slots[r], slots[j]];
  }
  const loadName: SRName = hasSR('loaded') ? 'loaded' : 'pallet';
  const lb = srBox(loadName).getSize(new THREE.Vector3());
  const ls = Math.min((bayL / 3 - 0.12) / lb.x, (rowD - 0.2) / lb.z, 1.3 / lb.y);
  const loads = new SRInstances(loadName, slots.length, ls);
  slots.forEach(([x, y, z], j) => {
    m.makeRotationY(Math.PI / 2).setPosition(x, y, z);
    loads.set(j, m);
  });
  loads.count = slots.length;
  loads.done();
  root.add(loads.group);
  return {
    root, tint: [], top: 4.8, labelAt: new THREE.Vector3((def.x0 + def.x1) / 2, 0, (def.z0 + def.z1) / 2),
    update(a) { loads.count = Math.round(slots.length * Math.min(1, a.stock / a.cap)); },
  };
}

// ---------- finished goods on the floor ----------

const fgGrid = (def: Zone) => [Math.max(1, Math.floor((def.x1 - def.x0 - 0.4) / 2.0)), Math.max(1, Math.floor((def.z1 - def.z0 - 0.2) / 1.95))];

export function buildFGSR(def: Zone): ModelHandle {
  const root = new THREE.Group();
  const [nx, nz] = fgGrid(def);
  const cells: [number, number][] = [];
  for (let i = 0; i < nx; i++) for (let j = 0; j < nz; j++) cells.push([def.x0 + 1.2 + i * 2.0, def.z0 + 1.1 + j * 1.95]);
  const lb = srBox('loaded').getSize(new THREE.Vector3());
  const inst = new SRInstances('loaded', cells.length, Math.min(1.25 / lb.x, 1.05 / lb.z));
  const m = new THREE.Matrix4();
  const ctr = srBox('loaded').getCenter(new THREE.Vector3());
  cells.forEach(([x, z], i) => {
    m.makeTranslation(x - ctr.x, 0, z - ctr.z);
    inst.set(i, m);
  });
  inst.count = cells.length;
  inst.done();
  root.add(inst.group);
  return {
    root, tint: [], top: 1.8, labelAt: new THREE.Vector3((def.x0 + def.x1) / 2, 0, (def.z0 + def.z1) / 2),
    update(a) { inst.count = Math.min(cells.length, Math.ceil(a.count / FG_PER_PALLET)); },
  };
}

// ---------- line-side dressing for hand stations ----------

/** Bin carts behind a manual station instead of the drawn shelf. */
export function benchCarts(root: THREE.Object3D, L: number, z: number) {
  const n = Math.max(1, Math.floor((L - 0.2) / 0.8));
  const inst = new SRInstances('bins', n);
  const b = srBox('bins');
  const s = b.getSize(new THREE.Vector3());
  const c = b.getCenter(new THREE.Vector3());
  const k = Math.min(0.75 / s.x, 0.75 / s.z);
  const m = new THREE.Matrix4();
  for (let i = 0; i < n; i++) {
    m.makeTranslation(-c.x, -b.min.y, -c.z).premultiply(new THREE.Matrix4().makeScale(k, k, k)).premultiply(new THREE.Matrix4().makeTranslation(-((n - 1) * 0.8) / 2 + i * 0.8, 0, z));
    inst.set(i, m);
  }
  inst.count = n;
  inst.done();
  root.add(inst.group);
}

// ---------- what stands around a machine ----------

/**
 * Real things a shop puts next to some machines, placed just outside the machine's own box
 * (in the machine's frame; the line runs along x, the operator side is +z):
 * a floor fan and an extinguisher stand by anything hot, a crash rail behind a press.
 * The machines themselves stay drawn: SimReady has no presses, furnaces or machining centres.
 */
export function dressMachine(root: THREE.Object3D, archetype: string, hot: boolean) {
  const b = new THREE.Box3().setFromObject(root);
  if (b.isEmpty()) return;
  if (hot && hasSR('fan')) {
    const fan = fit(sr('fan').obj, 'fan', 0.6, 0.8, 1.1);
    fan.position.set(b.max.x + 0.55, 0, b.max.z + 0.2);
    fan.rotation.y = -Math.PI * 0.75; // blowing at the operator's place
    root.add(fan);
  }
  if (hot && hasSR('firestand')) {
    const fs = fit(sr('firestand').obj, 'firestand', 0.5, 0.5, 1.7);
    fs.position.set(b.min.x - 0.45, 0, b.max.z - 0.3);
    root.add(fs);
  }
  if (archetype === 'press' && hasSR('rail')) {
    const s = srBox('rail').getSize(new THREE.Vector3());
    const len = b.max.x - b.min.x;
    const n = Math.max(1, Math.round(len / s.x));
    for (let i = 0; i < n; i++) {
      const r = fit(sr('rail').obj, 'rail', len / n, 0.3, 1.0, { stretch: true });
      r.position.set(b.min.x + (i + 0.5) * (len / n), 0, b.min.z - 0.35);
      root.add(r);
    }
  }
}
