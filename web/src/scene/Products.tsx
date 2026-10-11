// Parts on the belts and rejected units flying into the scrap bin. This is a pool that
// changes every frame, so it is synced imperatively instead of through React state.
import { useRef } from 'react';
import { useFrame } from '@react-three/fiber';
import * as THREE from 'three';
import type { PlantRuntime } from '@/state/store';
import { productObject } from '@/three/products';

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

    for (const line of sim.lines) {
      const product = line.layout.product;
      for (const p of line.products) {
        let obj = objs.get(p.id);
        if (!obj || obj.userData.kind !== p.kind) {
          if (obj) g.remove(obj);
          obj = productObject(product, p.kind);
          obj.userData.kind = p.kind;
          obj.raycast = () => {};
          g.add(obj);
          objs.set(p.id, obj);
        }
        obj.position.set(line.x0 + p.s, line.layout.beltY, line.z);
        obj.userData.stamp = stamp;
        // Parts with a spin anim turn while their unit is being worked on (fans, rotors…).
        const st = p.proc >= 0 ? line.stations[p.proc] : null;
        if (st?.state === 'running') for (const sp of obj.userData.spinners as { obj: THREE.Object3D; axis: 'x' | 'y' | 'z'; speed: number }[]) sp.obj.rotation[sp.axis] += sp.speed * dt;
      }
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
