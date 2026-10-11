// Products are pure config: every stage is a list of parts. Templates are built once per
// stage and cloned per unit; line-side pallets stack whatever fits on them.
import * as THREE from 'three';
import type { ProductInfo } from '@/core/layout';
import { buildParts } from './parts';
import { rbox } from './kit';

const cache = new Map<string, THREE.Group>();

export function productObject(product: ProductInfo, stage: string): THREE.Group {
  const key = `${product.key}:${stage}`;
  if (!cache.has(key)) {
    const g = new THREE.Group();
    buildParts(g, product.stages[stage] ?? product.stages[product.order[0]]);
    g.userData.h = (product.dims[stage] ?? product.max).h;
    cache.set(key, g);
  }
  const tpl = cache.get(key)!;
  const o = tpl.clone();
  o.userData.h = tpl.userData.h;
  // Parts with an `anim` spin while the unit is being processed (fans, rotors…).
  o.userData.spinners = [] as THREE.Object3D[];
  const parts = product.stages[stage] ?? [];
  let i = 0;
  o.children.forEach((pivot) => {
    const cfg = parts[i++];
    if (cfg?.anim?.type === 'spin') (o.userData.spinners as { obj: THREE.Object3D; axis: 'x' | 'y' | 'z'; speed: number }[]).push({ obj: pivot.children[0], axis: cfg.anim.axis, speed: cfg.anim.speed });
  });
  return o;
}

/** How many units of a stage fit on a pallet, as [nx, nz, layers, pitchX, pitchY, pitchZ, palletW]. */
export function stackLayout(product: ProductInfo, stage: string, maxUnits: number) {
  const d = product.dims[stage] ?? product.max;
  const big = d.l > 1.5;
  const palletW = big ? d.l + 0.4 : Math.max(1.2, d.l + 0.1);
  const palletD = big ? d.d + 0.4 : 1.0;
  const nx = Math.max(1, Math.floor(palletW / (d.l + 0.02)));
  const nz = Math.max(1, Math.floor(palletD / (d.d + 0.02)));
  const layers = big ? 1 : Math.max(1, Math.min(Math.ceil(maxUnits / (nx * nz)), Math.floor(2.2 / Math.max(0.05, d.h))));
  return { nx, nz, layers, px: d.l + 0.02, py: d.h + 0.005, pz: d.d + 0.02, palletW, palletD, big, dims: d };
}

export const unitBox = (d: { l: number; h: number; d: number }) => rbox(d.l, d.h, d.d, Math.min(0.03, d.h / 4));
