// Moves the DOM name tags (see ui/Labels) to where their asset is on screen, every frame.
import { useFrame } from '@react-three/fiber';
import * as THREE from 'three';
import { labelEls, models } from '@/state/store';

const v = new THREE.Vector3();

export function LabelProjector() {
  useFrame(({ camera, size }) => {
    for (const [id, el] of labelEls) {
      const m = models.get(id);
      if (!m) continue;
      const at = m.labelAt;
      v.set(at?.x ?? 0, m.top, at?.z ?? 0).applyMatrix4(m.root.matrixWorld).project(camera);
      const off = v.z > 1 || !m.root.visible;
      el.style.visibility = off ? 'hidden' : '';
      if (off) continue;
      const x = (v.x * 0.5 + 0.5) * size.width;
      const y = (-v.y * 0.5 + 0.5) * size.height;
      el.style.transform = `translate3d(${x.toFixed(1)}px, ${y.toFixed(1)}px, 0) translate(-50%, calc(-100% - 6px))`;
      el.style.zIndex = String(Math.round((1 - v.z) * 1e4));
    }
  });
  return null;
}
