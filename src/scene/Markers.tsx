// Strategy-game markers: a rounded footprint under the selected/hovered asset and a
// bobbing map pin above it. Both are children of the asset's model root.
import { useMemo, useRef } from 'react';
import { useFrame } from '@react-three/fiber';
import * as THREE from 'three';

function roundedRect(s: THREE.Shape | THREE.Path, x: number, y: number, w: number, h: number, r: number) {
  s.moveTo(x + r, y);
  s.lineTo(x + w - r, y); s.quadraticCurveTo(x + w, y, x + w, y + r);
  s.lineTo(x + w, y + h - r); s.quadraticCurveTo(x + w, y + h, x + w - r, y + h);
  s.lineTo(x + r, y + h); s.quadraticCurveTo(x, y + h, x, y + h - r);
  s.lineTo(x, y + r); s.quadraticCurveTo(x, y, x + r, y);
}

const noRaycast = () => {};

export function Footprint({ box, color, strength = 1 }: { box: THREE.Box3; color: string; strength?: number }) {
  const { fill, ring, pos } = useMemo(() => {
    const pad = 0.5;
    const w = box.max.x - box.min.x + pad * 2;
    const d = box.max.z - box.min.z + pad * 2;
    const r = Math.min(0.9, w / 4, d / 4);
    const outer = new THREE.Shape();
    roundedRect(outer, -w / 2, -d / 2, w, d, r);
    const ringShape = new THREE.Shape();
    roundedRect(ringShape, -w / 2, -d / 2, w, d, r);
    const hole = new THREE.Path();
    const t = 0.12;
    roundedRect(hole, -w / 2 + t, -d / 2 + t, w - 2 * t, d - 2 * t, Math.max(0.05, r - t));
    ringShape.holes.push(hole);
    return {
      fill: new THREE.ShapeGeometry(outer, 6),
      ring: new THREE.ShapeGeometry(ringShape, 6),
      pos: [(box.min.x + box.max.x) / 2, 0.03, (box.min.z + box.max.z) / 2] as const,
    };
  }, [box]);
  return (
    <group position={pos} rotation-x={-Math.PI / 2} renderOrder={2} userData={{ overlay: true }}>
      <mesh geometry={fill} raycast={noRaycast} userData={{ overlay: true }}>
        <meshBasicMaterial color={color} transparent opacity={0.16 * strength} depthWrite={false} />
      </mesh>
      <mesh geometry={ring} raycast={noRaycast} userData={{ overlay: true }}>
        <meshBasicMaterial color={color} transparent opacity={0.85 * strength} depthWrite={false} />
      </mesh>
    </group>
  );
}

const pinGeo = new THREE.LatheGeometry(
  [[0, 0], [0.07, 0.14], [0.19, 0.38], [0.29, 0.62], [0.33, 0.82], [0.3, 0.98], [0.2, 1.1], [0, 1.15]].map(([x, y]) => new THREE.Vector2(x, y)),
  28,
);
const eyeGeo = new THREE.SphereGeometry(0.13, 16, 12);
const eyeMat = new THREE.MeshStandardMaterial({ color: '#ffffff', roughness: 0.3 });

export function Pin({ color, x, y, z, phase = 0 }: { color: string; x: number; y: number; z: number; phase?: number }) {
  const ref = useRef<THREE.Group>(null);
  useFrame(({ clock }) => {
    const g = ref.current;
    if (!g) return;
    const t = clock.elapsedTime;
    g.position.y = y + Math.sin(t * 2.4 + phase) * 0.18;
    g.rotation.y = t * 1.2;
  });
  return (
    <group ref={ref} position={[x, y, z]} scale={0.95} userData={{ overlay: true }}>
      <mesh geometry={pinGeo} castShadow raycast={noRaycast} userData={{ overlay: true }}>
        <meshStandardMaterial color={color} roughness={0.35} />
      </mesh>
      {[0.24, -0.24].map((ez) => (
        <mesh key={ez} geometry={eyeGeo} material={eyeMat} position={[0, 0.8, ez]} scale={[1, 1, 0.6]} raycast={noRaycast} userData={{ overlay: true }} />
      ))}
    </group>
  );
}
