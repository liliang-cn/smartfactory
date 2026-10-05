// Camera moves requested from the UI (zoom, rotate, home, focus) and vehicle following.
import { useEffect, useRef } from 'react';
import { useFrame, useThree } from '@react-three/fiber';
import * as THREE from 'three';
import type { OrbitControls as OrbitControlsImpl } from 'three/addons/controls/OrbitControls.js';
import { models, useUI, type PlantRuntime } from '@/state/store';
import { frameOf } from '@/three/factory';
import { homeView } from '@/core/layout';

interface Fly { t: number; dur: number; fromT: THREE.Vector3; fromP: THREE.Vector3; toT: THREE.Vector3; toP: THREE.Vector3 }
const ease = (t: number) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);

export function CameraRig({ plant }: { plant: PlantRuntime }) {
  const camera = useThree((s) => s.camera);
  const controls = useThree((s) => s.controls) as unknown as OrbitControlsImpl | null;
  const fly = useRef<Fly | null>(null);
  const cam = useUI((s) => s.cam);

  // Scripted camera moves (demo recordings) reach the camera through window.twin.
  useEffect(() => {
    const twin = (window as unknown as { twin?: Record<string, unknown> }).twin;
    if (twin) Object.assign(twin, { camera, controls });
  }, [camera, controls]);

  useEffect(() => {
    if (!cam || !controls) return;
    const target = controls.target;
    const sph = new THREE.Spherical().setFromVector3(camera.position.clone().sub(target));
    const go = (toT: THREE.Vector3, s: THREE.Spherical, dur: number) => {
      const toP = toT.clone().add(new THREE.Vector3().setFromSpherical(s));
      fly.current = { t: 0, dur, fromT: target.clone(), fromP: camera.position.clone(), toT, toP };
    };
    switch (cam.op) {
      case 'in': sph.radius = Math.max(controls.minDistance, sph.radius * 0.7); go(target.clone(), sph, 0.45); break;
      case 'out': sph.radius = Math.min(controls.maxDistance, sph.radius * 1.4); go(target.clone(), sph, 0.45); break;
      case 'left': sph.theta -= Math.PI / 4; go(target.clone(), sph, 0.7); break;
      case 'right': sph.theta += Math.PI / 4; go(target.clone(), sph, 0.7); break;
      case 'home': {
        const h = homeView(plant.layout, (camera as THREE.PerspectiveCamera).aspect);
        fly.current = { t: 0, dur: 1.1, fromT: target.clone(), fromP: camera.position.clone(), toT: new THREE.Vector3(...h.target), toP: new THREE.Vector3(...h.pos) };
        break;
      }
      case 'focus': {
        const m = cam.id ? models.get(cam.id) : null;
        if (!m) break;
        const f = frameOf(m);
        sph.radius = THREE.MathUtils.clamp(f.size * 2 + 24, 28, 90);
        sph.phi = Math.min(sph.phi, 0.95);
        go(f.center, sph, 1.1);
        break;
      }
    }
  }, [cam, camera, controls, plant]);

  // Any manual orbit cancels a fly-to and stops following.
  useEffect(() => {
    if (!controls) return;
    const onStart = () => {
      fly.current = null;
      if (useUI.getState().follow) useUI.setState({ follow: false });
    };
    controls.addEventListener('start', onStart);
    return () => controls.removeEventListener('start', onStart);
  }, [controls]);

  const v = useRef(new THREE.Vector3());
  useFrame((_, dt) => {
    if (!controls) return;
    const f = fly.current;
    if (f) {
      f.t += dt;
      const k = ease(Math.min(1, f.t / f.dur));
      controls.target.lerpVectors(f.fromT, f.toT, k);
      camera.position.lerpVectors(f.fromP, f.toP, k);
      if (f.t >= f.dur) fly.current = null;
      return;
    }
    const { follow, selected } = useUI.getState();
    if (follow && selected) {
      const m = models.get(selected);
      if (!m) return;
      m.root.getWorldPosition(v.current);
      v.current.y = 0.8;
      const delta = v.current.sub(controls.target).multiplyScalar(1 - Math.exp(-dt * 5));
      controls.target.add(delta);
      camera.position.add(delta);
    }
  });

  return null;
}
