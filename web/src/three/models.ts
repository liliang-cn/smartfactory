// Ready-made models a look places beside its primitives (part shape `model`): the free low-poly
// models of Polyfork (CC BY 4.0), served by this server from /models/<id>.glb. The ones the
// built-in templates use ship with the program; any other is kept by the server the first time a
// look uses it. Each model is one file of vertex colours on one material, resting on y = 0 at its
// real size, its front towards +z.
//
// A part is a ModelSlot: an empty group until its model has loaded, then the model fitted into the
// part's box. Copies made of a slot before that (a product's template is cloned for every unit)
// are filled too when it arrives.
import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';

const loader = new GLTFLoader();
const loaded = new Map<string, Promise<THREE.Group | null>>();
const ready = new Map<string, { scene: THREE.Group; box: THREE.Box3 } | null>();
const waiting = new Map<string, Set<ModelSlot>>();

function load(id: string) {
  if (!loaded.has(id)) {
    loaded.set(id, loader.loadAsync(`/models/${id}.glb`).then((g) => {
      g.scene.traverse((o) => {
        const m = o as THREE.Mesh;
        if (!m.isMesh) return;
        m.castShadow = true;
        m.receiveShadow = true;
      });
      g.scene.updateMatrixWorld(true);
      ready.set(id, { scene: g.scene, box: new THREE.Box3().setFromObject(g.scene) });
      return g.scene;
    }, (e) => {
      console.warn(`model ${id} not loaded`, e);
      ready.set(id, null);
      return null;
    }).then((s) => {
      for (const slot of waiting.get(id) ?? []) slot.fill();
      waiting.delete(id);
      return s;
    }));
  }
  return loaded.get(id)!;
}

/** Loads models ahead, so the parts that use them are drawn at once (failures resolve too). */
export function preloadModels(ids: Iterable<string>) {
  return Promise.all([...new Set(ids)].map(load));
}

/** The models a list of parts uses. */
export function modelsOf(parts: { shape: string; model?: string }[] | undefined): string[] {
  return (parts ?? []).flatMap((p) => (p.shape === 'model' && p.model ? [p.model] : []));
}

/**
 * A model fitted into a box of size [w, h, d] centred on the slot's origin (the way a box
 * geometry is), filled as soon as the model is in. A model that cannot be loaded leaves a plain
 * box in its place, so the look keeps its shape.
 */
export class ModelSlot extends THREE.Group {
  model = '';
  size: [number, number, number] = [1, 1, 1];
  filled = false;

  constructor(model?: string, size?: [number, number, number]) {
    super();
    if (model && size) {
      this.model = model;
      this.size = size;
      this.wait();
    }
  }

  private wait() {
    if (ready.has(this.model)) this.fill();
    else {
      if (!waiting.has(this.model)) waiting.set(this.model, new Set());
      waiting.get(this.model)!.add(this);
      load(this.model);
    }
  }

  fill() {
    if (this.filled) return;
    this.filled = true;
    const [w, h, d] = this.size;
    const m = ready.get(this.model);
    if (!m) {
      const box = new THREE.Mesh(new THREE.BoxGeometry(w, h, d), new THREE.MeshStandardMaterial({ color: '#d9dde8', roughness: 0.8, transparent: true, opacity: 0.6 }));
      this.add(box);
      return;
    }
    const s = m.box.getSize(new THREE.Vector3());
    const c = m.box.getCenter(new THREE.Vector3());
    const obj = m.scene.clone(true);
    obj.position.set(-c.x, -c.y, -c.z);
    const fit = new THREE.Group();
    fit.add(obj);
    fit.scale.set(w / Math.max(s.x, 1e-6), h / Math.max(s.y, 1e-6), d / Math.max(s.z, 1e-6));
    this.add(fit);
  }

  // a copy of a slot waits for the model on its own (clone() comes here through the constructor)
  copy(source: this, recursive?: boolean) {
    super.copy(source, false);
    this.model = source.model;
    this.size = [source.size[0], source.size[1], source.size[2]];
    if (source.filled || !recursive) {
      if (recursive) for (const ch of source.children) this.add(ch.clone(true));
      this.filled = source.filled;
    } else this.wait();
    return this;
  }
}
