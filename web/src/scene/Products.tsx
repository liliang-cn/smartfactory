// Parts on the belts and rejected units flying into the scrap bin; on a walk-flow site, the groups
// of people on the walkway, at their tables or pitches, and in the queue at the door. This is a
// pool that changes every frame, so it is synced imperatively instead of through React state.
import { useRef } from 'react';
import { useFrame } from '@react-three/fiber';
import * as THREE from 'three';
import type { PlantRuntime } from '@/state/store';
import { productObject } from '@/three/products';
import { groupObject } from '@/three/venue';

const REJECT_MAT = new THREE.MeshStandardMaterial({ color: '#ff6b6b', roughness: 0.4 });

interface Flying { obj: THREE.Object3D; from: THREE.Vector3; to: THREE.Vector3; t: number }

export function Products({ plant }: { plant: PlantRuntime }) {
  const sim = plant.sim;
  const group = useRef<THREE.Group>(null);
  const pool = useRef(new Map<string, THREE.Object3D>());
  const flying = useRef<Flying[]>([]);
  const frame = useRef(0);

  useFrame((_, dt) => {
    const g = group.current;
    if (!g) return;
    const stamp = ++frame.current;
    const objs = pool.current;
    const L = plant.layout;

    const show = (key: string, look: string, make: () => THREE.Object3D) => {
      let obj = objs.get(key);
      if (!obj || obj.userData.kind !== look) {
        if (obj) g.remove(obj);
        obj = make();
        obj.userData.kind = look;
        obj.raycast = () => {};
        g.add(obj);
        objs.set(key, obj);
      }
      obj.userData.stamp = stamp;
      return obj;
    };

    for (const line of sim.lines) {
      const product = line.layout.product;
      for (const p of line.products) {
        const n = p.n ?? 0;
        const obj = show(p.id, `${p.kind}:${n}`, () => (n ? groupObject(product, p.kind, n) : productObject(product, p.kind)));
        const st = p.proc >= 0 ? line.stations[p.proc] : null;
        // a group at its table or pitch sits there, off the walkway
        const place = st?.slots && p.slot !== undefined ? sim.placeOf(line, p.slot)?.place : undefined;
        if (place) obj.position.set(place.x + 0.05, 0.02, place.z + (place.z < line.z ? 0.62 : -0.62));
        else obj.position.set(line.x0 + p.s, line.layout.beltY, line.z);
        // Parts with a spin anim turn while their unit is being worked on (fans, rotors…).
        if (st?.state === 'running') for (const sp of obj.userData.spinners as { obj: THREE.Object3D; axis: 'x' | 'y' | 'z'; speed: number }[]) sp.obj.rotation[sp.axis] += sp.speed * dt;
      }
    }

    // the queue at the door: groups waiting outside along the front, the first one nearest the way in
    const v = sim.venue;
    const first = sim.lines.find((l) => l.gate !== undefined);
    if (v && first) {
      const product = first.layout.product;
      const stage = first.layout.input;
      v.queue.forEach((w, i) => {
        const obj = show(`Q${w.at}:${w.n}`, `${stage}:${w.n}`, () => groupObject(product, stage, w.n));
        const row = Math.floor(i / 12);
        obj.position.set(first.x0 + 0.8 + (i % 12) * 1.45, 0.02, L.box.z1 + 1.5 + row * 1.4);
        obj.rotation.y = -Math.PI / 2;
      });
    }

    for (const [id, obj] of objs) {
      if (obj.userData.stamp !== stamp) {
        g.remove(obj);
        objs.delete(id);
      }
    }

    // Rejected units arc off the belt towards the scrap side.
    while (sim.events.length) {
      const e = sim.events.shift()!;
      if (e.type !== 'reject') continue;
      const line = sim.lineById[e.line];
      const obj = productObject(line.layout.product, e.kind);
      obj.traverse((o) => { if ((o as THREE.Mesh).isMesh) (o as THREE.Mesh).material = REJECT_MAT; });
      const from = new THREE.Vector3(e.x, line.layout.beltY, e.z);
      const to = new THREE.Vector3(e.x - 0.9, 0.3, e.z + line.layout.beltW / 2 + 0.8);
      obj.position.copy(from);
      g.add(obj);
      flying.current.push({ obj, from, to, t: 0 });
    }
    const fl = flying.current;
    for (let i = fl.length - 1; i >= 0; i--) {
      const f = fl[i];
      f.t += dt / 0.8;
      const k = Math.min(1, f.t);
      f.obj.position.lerpVectors(f.from, f.to, k);
      f.obj.position.y += Math.sin(k * Math.PI) * 0.8;
      f.obj.rotation.z = k * 4;
      if (f.t >= 1.4) {
        g.remove(f.obj);
        fl.splice(i, 1);
      }
    }
  });

  return <group ref={group} />;
}
