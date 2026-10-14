// Asset -> 3D model, plus the per-frame helpers the scene needs around a model:
// overlay tinting and the local footprint used for the selection outline.
import * as THREE from 'three';
import type { PlantLayout } from '@/core/layout';
import type { Asset } from '@/sim/types';
import type { ViewMode } from '@/state/store';
import { ARCHETYPE_BUILDERS } from './archetypes';
import type { ModelHandle } from './common';
import { P, bodyMat, colorOf, ramp } from './kit';
import { buildAGV, buildBuffer, buildConveyor, buildFG, buildOutfeed, buildTruck, buildWarehouse } from './logistics';
import { animateParts, buildParts } from './parts';

export type { ModelHandle };

const accents = new Map<string, THREE.MeshStandardMaterial>();
const accentMat = (c: string) => {
  if (!accents.has(c)) accents.set(c, new THREE.MeshStandardMaterial({ color: c, roughness: 0.45 }));
  return accents.get(c)!;
};

function buildStation(a: Asset, plant: PlantLayout): ModelHandle {
  const line = plant.lines.find((l) => l.id === a.line!.id)!;
  const st = line.stations.find((s) => s.id === a.id)!;
  const product = line.product;
  // Each machine is sized around the product as it arrives there.
  let kind = line.input;
  for (const s of line.stations) {
    if (s === st) break;
    kind = s.makes ?? kind;
  }
  const dims = product.dims[kind] ?? product.max;
  const look = st.machine.look;
  const model = ARCHETYPE_BUILDERS[look.archetype]({
    look,
    size: st.size,
    beltY: line.beltY,
    beltW: line.beltW,
    top: line.beltY + dims.h,
    plen: dims.l,
    body: bodyMat(colorOf(look.color, look.archetype === 'robot' ? P.yellow : P.white)),
    accent: accentMat(colorOf(look.accent, P.blue)),
    rejects: !!st.measure || st.machine.rejectRate !== undefined,
    hot: st.machine.thermal.run > 80,
  });
  model.root.position.set(st.x, 0, line.z);
  // Anything the archetype doesn't have comes from the config's `parts`.
  const extra = buildParts(model.root, look.parts);
  if (extra.length) {
    const update = model.update.bind(model);
    model.update = (as, dt, t, sim) => {
      update(as, dt, t, sim);
      const heat = as.spec?.trip && as.spec.trip > 22 ? Math.max(0, Math.min(1, (as.temp - 22) / (as.spec.trip - 22))) : 0;
      animateParts(extra, dt, t, as.state === 'running', as.progress, heat, as.speed);
    };
  }
  return model;
}

export function buildModel(a: Asset, plant: PlantLayout): ModelHandle {
  const ll = a.line ? plant.lines.find((l) => l.id === a.line!.id)! : null;
  switch (a.type) {
    case 'conveyor': return buildConveyor(ll!, plant.flow === 'walk', plant.site === 'outdoor');
    case 'buffer': return buildBuffer(ll!, plant.flow === 'walk');
    case 'outfeed': return buildOutfeed(ll!, plant.flow === 'walk');
    case 'warehouse': return buildWarehouse(plant.warehouse);
    case 'fgzone': return buildFG(plant.fg);
    case 'agv': return buildAGV();
    case 'truck': return buildTruck();
    default: return buildStation(a, plant);
  }
}

export function initTint(model: ModelHandle) {
  for (const m of model.tint) m.userData.base = m.color.clone();
}

const tmp = new THREE.Color();
export function applyTint(model: ModelHandle, a: Asset, mode: ViewMode, t: number) {
  for (const mat of model.tint) {
    const base = mat.userData.base as THREE.Color;
    mat.emissive.setRGB(0, 0, 0);
    if (mode === 'heat' && a.spec?.trip) {
      // a fridge (trip below ambient) is at the cold end of the ramp
      ramp(a.spec.trip > 22 ? (a.temp - 22) / (a.spec.trip - 22) : 0, mat.color);
    } else if (mode === 'util' && a.spec?.runRise !== undefined) {
      ramp(a.util, mat.color);
    } else if (mode !== 'live') {
      mat.color.copy(base).lerp(tmp.set('#e6e9f3'), 0.6);
    } else {
      mat.color.copy(base);
      if (a.state === 'fault') {
        const k = 0.5 + 0.5 * Math.sin(t * 6);
        mat.color.lerp(tmp.set('#ff9a9a'), 0.55 * k);
        mat.emissive.set('#ef4444').multiplyScalar(0.25 * k);
      } else if (a.state === 'stopped') {
        mat.color.lerp(tmp.set('#9aa3bd'), 0.35);
      } else if (a.state === 'maint') {
        mat.color.lerp(tmp.set('#ffd98a'), 0.35);
      }
    }
  }
}

/** Bounding box of a model in its own frame (ignores where it currently stands). */
export function localBox(root: THREE.Object3D): THREE.Box3 {
  const pos = root.position.clone();
  const rot = root.rotation.y;
  const vis = root.visible;
  root.position.set(0, 0, 0);
  root.rotation.y = 0;
  root.visible = true;
  root.updateMatrixWorld(true);
  const box = new THREE.Box3();
  root.traverse((o) => {
    if (((o as THREE.Mesh).isMesh || (o as THREE.InstancedMesh).isInstancedMesh) && !o.userData.overlay) box.expandByObject(o);
  });
  root.position.copy(pos);
  root.rotation.y = rot;
  root.visible = vis;
  root.updateMatrixWorld(true);
  return box;
}

/** World-space centre and rough size, for camera framing. */
export function frameOf(model: ModelHandle) {
  // Meshes only: idle spark particles are parked far below the floor and would drag the centre away.
  const box = new THREE.Box3();
  model.root.updateMatrixWorld(true);
  model.root.traverse((o) => {
    if (((o as THREE.Mesh).isMesh || (o as THREE.InstancedMesh).isInstancedMesh) && !o.userData.overlay) box.expandByObject(o);
  });
  const size = box.getSize(new THREE.Vector3());
  const c = box.getCenter(new THREE.Vector3());
  c.y = Math.min(c.y, 1.2);
  return { center: c, size: Math.max(size.x, size.z) };
}
