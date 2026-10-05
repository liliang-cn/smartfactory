// The parts DSL from the config: build primitives, and drive their `anim` bindings.
import * as THREE from 'three';
import type { PartCfg } from '@/config/schema';
import { colorOf, cylGeo, rbox } from './kit';

const matCache = new Map<string, THREE.MeshStandardMaterial>();
const D2R = Math.PI / 180;

function material(p: PartCfg, shared: boolean): THREE.MeshStandardMaterial {
  const key = `${p.color}|${p.metal ?? ''}|${p.rough ?? ''}|${p.glow ?? ''}|${p.opacity ?? ''}`;
  const make = () => new THREE.MeshStandardMaterial({
    color: colorOf(p.color),
    metalness: p.metal ?? (p.color === 'metal' ? 0.55 : 0),
    roughness: p.rough ?? (p.color === 'metal' ? 0.3 : p.color === 'glass' ? 0.08 : 0.55),
    emissive: p.glow ? colorOf(p.glow) : p.anim?.type === 'heat' ? '#ff6a1a' : p.anim?.type === 'blink' ? '#ef4444' : '#000000',
    emissiveIntensity: p.glow ? 1.2 : 0,
    transparent: p.opacity !== undefined || p.color === 'glass',
    opacity: p.opacity ?? (p.color === 'glass' ? 0.38 : 1),
    depthWrite: !(p.opacity !== undefined || p.color === 'glass'),
  });
  // Animated glows need their own material; static ones can be shared.
  if (!shared) return make();
  if (!matCache.has(key)) matCache.set(key, make());
  return matCache.get(key)!;
}

const geoCache = new Map<string, THREE.BufferGeometry>();
function geometry(p: PartCfg): THREE.BufferGeometry {
  const [a, b = a, c = a] = p.size;
  const key = `${p.shape}:${p.size.join(',')}:${p.round ?? ''}`;
  if (geoCache.has(key)) return geoCache.get(key)!;
  const geo = (() => {
    switch (p.shape) {
      case 'box': return rbox(a, b, c, p.round ?? Math.min(0.04, a / 6, b / 6, c / 6));
      case 'cylinder': return cylGeo(a, a, b, 28);
      case 'cone': return new THREE.ConeGeometry(a, b, 24);
      case 'sphere': return new THREE.SphereGeometry(a, 20, 14);
      case 'torus': return new THREE.TorusGeometry(a, p.size.length > 1 ? b : 0.05, 12, 40);
      case 'capsule': return new THREE.CapsuleGeometry(a, p.size.length > 1 ? b : a, 4, 12);
    }
  })();
  geoCache.set(key, geo);
  return geo;
}

export interface AnimatedPart { obj: THREE.Object3D; cfg: PartCfg; base: THREE.Vector3; mat: THREE.MeshStandardMaterial }

const box = new THREE.Box3();
const rot = new THREE.Matrix4();

/**
 * Build parts into `parent`. Convention: `at` is the centre of the bottom of the part's
 * bounding box after `rotate`, so a cylinder rotated to lie down still sits on `at`.
 * Spin happens about the part's own axis (its frame before `rotate`).
 */
export function buildParts(parent: THREE.Object3D, parts: PartCfg[]): AnimatedPart[] {
  const animated: AnimatedPart[] = [];
  for (const p of parts) {
    const animatedGlow = p.anim?.type === 'blink' || p.anim?.type === 'heat';
    const mat = material(p, !animatedGlow);
    const geo = geometry(p);
    const pivot = new THREE.Group();
    pivot.rotation.set(p.rotate[0] * D2R, p.rotate[1] * D2R, p.rotate[2] * D2R);
    if (!geo.boundingBox) geo.computeBoundingBox();
    box.copy(geo.boundingBox!).applyMatrix4(rot.makeRotationFromEuler(pivot.rotation));
    const c = box.getCenter(new THREE.Vector3());
    pivot.position.set(p.at[0] - c.x, p.at[1] - box.min.y, p.at[2] - c.z);
    const mesh = new THREE.Mesh(geo, mat);
    mesh.castShadow = true;
    mesh.receiveShadow = true;
    pivot.add(mesh);
    parent.add(pivot);
    if (p.anim) animated.push({ obj: p.anim.type === 'spin' ? mesh : pivot, cfg: p, base: pivot.position.clone(), mat });
  }
  return animated;
}

/**
 * Drive animated parts. `active` is whether the owner is working right now, `progress` the
 * cycle progress 0..1, `heat` a 0..1 temperature level, `speed` the machine speed factor.
 */
export function animateParts(list: AnimatedPart[], dt: number, t: number, active: boolean, progress: number, heat: number, speed = 1) {
  for (const a of list) {
    const an = a.cfg.anim!;
    const on = an.when === 'always' || active;
    switch (an.type) {
      case 'spin':
        if (on) a.obj.rotation[an.axis] += an.speed * speed * dt;
        break;
      case 'stroke': {
        const [w0, w1] = an.window;
        const k = on && progress > w0 && progress < w1 ? Math.sin((Math.PI * (progress - w0)) / (w1 - w0)) : 0;
        a.obj.position.copy(a.base);
        a.obj.position[an.axis] += an.to * k;
        break;
      }
      case 'blink':
        a.mat.emissiveIntensity = on && Math.sin(t * 8) > 0 ? 2.2 : 0;
        break;
      case 'heat':
        a.mat.emissiveIntensity = (on ? 0.4 : 0) + heat * 2.4;
        break;
    }
  }
}
