// NVIDIA SimReady models (CC BY 4.0), reduced for the web by scripts/simready.mjs and served
// from /simready/. They are loaded once before the twin draws; a model that failed to load
// leaves its builder drawing the procedural one instead.
import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { MeshoptDecoder } from 'three/addons/libs/meshopt_decoder.module.js';

export const SR_NAMES = [
  'arm', 'amr', 'forklift', 'roller', 'rack', 'pallet', 'loaded', 'crate', 'bench', 'bins', 'tools',
  'ibc', 'booth', 'guard', 'extinguisher', 'column', 'compressor', 'packtable', 'drum', 'fan', 'rail', 'firestand',
] as const;
export type SRName = (typeof SR_NAMES)[number];

/** the credit line under the twin, a link to every model's attribution (/models/credits) */
export const SR_CREDIT = '3D 模型：NVIDIA SimReady、Polyfork（CC BY 4.0）';

const scenes = new Map<SRName, THREE.Group>();
const boxes = new Map<SRName, THREE.Box3>();
let loading: Promise<void> | null = null;

export function loadSimReady(): Promise<void> {
  loading ??= (async () => {
    const loader = new GLTFLoader().setMeshoptDecoder(MeshoptDecoder);
    await Promise.all(SR_NAMES.map(async (n) => {
      try {
        const g = await loader.loadAsync(`/simready/${n}.glb`);
        g.scene.traverse((o) => {
          const m = o as THREE.Mesh;
          if (!m.isMesh) return;
          m.castShadow = true;
          m.receiveShadow = true;
          for (const mat of [m.material].flat()) {
            const s = mat as THREE.MeshPhysicalMaterial;
            // the scans are lit for a dark studio; in the bright hall they read better a little rougher
            if (s.isMeshStandardMaterial) s.envMapIntensity = 0.8;
            // Glass in a few models (the forklift's cab, the extinguisher stand's door) is
            // transmissive, and one transmissive material makes three.js draw the whole hall a
            // second time every frame. Plain see-through glass looks the same at this distance.
            if (s.isMeshPhysicalMaterial && s.transmission > 0) {
              s.transmission = 0;
              s.transparent = true;
              s.opacity = Math.min(s.opacity, 0.35);
              s.depthWrite = false;
            }
          }
        });
        g.scene.updateMatrixWorld(true);
        scenes.set(n, g.scene);
        boxes.set(n, new THREE.Box3().setFromObject(g.scene));
      } catch (e) {
        console.warn(`simready: ${n} not loaded`, e);
      }
    }));
  })();
  return loading;
}

export const hasSR = (n: SRName) => scenes.has(n);
export const srBox = (n: SRName) => boxes.get(n)!.clone();

/**
 * A copy of a model, sharing its geometry. With `own`, every material is copied too, so the
 * overlays can recolour this one copy; the copies are returned for the caller's tint list.
 */
export function sr(n: SRName, own = false): { obj: THREE.Group; mats: THREE.MeshStandardMaterial[] } {
  const obj = scenes.get(n)!.clone(true) as THREE.Group;
  const mats: THREE.MeshStandardMaterial[] = [];
  if (own) {
    const copies = new Map<THREE.Material, THREE.Material>();
    obj.traverse((o) => {
      const m = o as THREE.Mesh;
      if (!m.isMesh) return;
      const one = (mat: THREE.Material) => {
        if (!copies.has(mat)) {
          const c = mat.clone();
          copies.set(mat, c);
          if ((c as THREE.MeshStandardMaterial).isMeshStandardMaterial) mats.push(c as THREE.MeshStandardMaterial);
        }
        return copies.get(mat)!;
      };
      m.material = Array.isArray(m.material) ? m.material.map(one) : one(m.material);
    });
  }
  return { obj, mats };
}

/**
 * Scales a copy so its footprint fits `l` (x) by `d` (z) and stands on the floor, centred on
 * the parent's origin. `h` caps the height; `stretch` fills the box instead of keeping
 * proportions. `turn` rotates the model a quarter turn first (its long side then runs along x).
 */
export function fit(obj: THREE.Object3D, n: SRName, l: number, d: number, h = Infinity, o: { stretch?: boolean; turn?: boolean } = {}) {
  const b = srBox(n);
  const size = b.getSize(new THREE.Vector3());
  const sx = o.turn ? size.z : size.x;
  const sz = o.turn ? size.x : size.z;
  const wrap = new THREE.Group();
  wrap.add(obj);
  if (o.turn) obj.rotation.y = Math.PI / 2;
  const c = b.getCenter(new THREE.Vector3());
  // centre in x/z, base on y = 0 (before the turn)
  obj.position.set(-c.x, -b.min.y, -c.z);
  if (o.turn) obj.position.set(-c.z, -b.min.y, c.x);
  if (o.stretch) {
    wrap.scale.set(l / sx, Math.min(h, size.y) / size.y, d / sz);
  } else {
    const k = Math.min(l / sx, d / sz, h / size.y);
    wrap.scale.setScalar(k);
  }
  return wrap;
}

/**
 * Many copies of one model drawn as instanced meshes, one per part of the model. Set each
 * copy's place with `set`, how many are drawn with `count`.
 */
export class SRInstances {
  readonly group = new THREE.Group();
  private parts: THREE.InstancedMesh[] = [];
  private bake: THREE.Matrix4[] = [];
  private tmp = new THREE.Matrix4();

  constructor(n: SRName, max: number, scale = 1) {
    const root = scenes.get(n)!;
    root.updateMatrixWorld(true);
    const s = new THREE.Matrix4().makeScale(scale, scale, scale);
    root.traverse((o) => {
      const m = o as THREE.Mesh;
      if (!m.isMesh) return;
      const im = new THREE.InstancedMesh(m.geometry, m.material, max);
      im.castShadow = true;
      im.receiveShadow = true;
      im.count = 0;
      this.parts.push(im);
      this.bake.push(s.clone().multiply(m.matrixWorld));
      this.group.add(im);
    });
  }

  set(i: number, m: THREE.Matrix4) {
    this.parts.forEach((im, k) => {
      im.setMatrixAt(i, this.tmp.multiplyMatrices(m, this.bake[k]));
      im.instanceMatrix.needsUpdate = true;
    });
  }

  set count(n: number) {
    for (const im of this.parts) im.count = n;
  }

  /** Bounds need recomputing once the copies are placed, or picking and culling use the first one. */
  done() {
    for (const im of this.parts) { im.computeBoundingBox(); im.computeBoundingSphere(); }
  }
}
