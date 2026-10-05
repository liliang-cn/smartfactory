// The 3D view: renderer setup, lights, static world, every asset, belt products, camera.
import { useMemo, useRef } from 'react';
import { Canvas, useFrame, useThree } from '@react-three/fiber';
import { OrbitControls } from '@react-three/drei';
import * as THREE from 'three';
import { RoomEnvironment } from 'three/addons/environments/RoomEnvironment.js';
import { runtime, useUI, type PlantRuntime } from '@/state/store';
import { P } from '@/three/kit';
import { buildWorld } from '@/three/world';
import { homeView } from '@/core/layout';
import { AssetNode } from './AssetNode';
import { CameraRig } from './CameraRig';
import { LabelProjector } from './LabelProjector';
import { Products } from './Products';

const coarse = typeof window !== 'undefined' && window.matchMedia('(pointer: coarse)').matches;

// Steps every plant of the company with the frame clock (so off-screen plants keep producing
// and alarming) and tells React to re-read them 4 times a second.
function SimLoop({ active }: { active: PlantRuntime }) {
  const acc = useRef(0);
  useFrame((_, delta) => {
    const dt = Math.min(0.1, delta);
    let simDt = dt * useUI.getState().timeScale;
    while (simDt > 1e-6) {
      const h = Math.min(0.1, simDt);
      for (const p of runtime().plants) p.sim.step(h);
      simDt -= h;
    }
    // Only the plant on screen animates its events.
    for (const p of runtime().plants) if (p !== active) p.sim.events.length = 0;
    acc.current += dt;
    if (acc.current > 0.25) {
      acc.current = 0;
      useUI.getState().bump();
    }
  });
  return null;
}

function Environment() {
  const gl = useThree((s) => s.gl);
  const scene = useThree((s) => s.scene);
  useMemo(() => {
    const pmrem = new THREE.PMREMGenerator(gl);
    scene.environment = pmrem.fromScene(new RoomEnvironment(), 0.04).texture;
    scene.environmentIntensity = 0.45;
  }, [gl, scene]);
  return null;
}

function World({ plant }: { plant: PlantRuntime }) {
  const world = useMemo(() => buildWorld(plant.layout), [plant]);
  return <primitive object={world} />;
}

export function Scene({ plant }: { plant: PlantRuntime }) {
  const assets = useMemo(() => [...plant.sim.assets.values()], [plant]);
  const { box } = plant.layout;
  const home = homeView(plant.layout, window.innerWidth / Math.max(1, window.innerHeight));
  const size = Math.max(box.x1 - box.x0, box.z1 - box.z0);
  const sx = (box.x1 - box.x0) / 2 + 30;
  const sz = (box.z1 - box.z0) / 2 + 30;
  return (
    <Canvas
      className="!absolute inset-0 touch-none"
      shadows={{ type: THREE.PCFShadowMap }}
      dpr={[1, coarse ? 1.5 : 2]}
      camera={{ fov: 30, near: 1, far: 2000, position: home.pos }}
      onCreated={({ gl }) => {
        gl.toneMapping = THREE.NeutralToneMapping;
        gl.toneMappingExposure = 1.05;
      }}
      onPointerMissed={() => useUI.getState().select(null)}
    >
      <color attach="background" args={[P.ground]} />
      <fog attach="fog" args={[P.ground, size * 2.2, size * 4.5]} />
      <Environment />
      <hemisphereLight args={['#ffffff', '#c9cfee', 1.5]} />
      <directionalLight
        castShadow
        color="#fffaf2"
        intensity={2.4}
        position={[-30, 60, 35]}
        shadow-mapSize={coarse ? [2048, 2048] : [4096, 4096]}
        shadow-camera-left={-sx}
        shadow-camera-right={sx}
        shadow-camera-top={sz}
        shadow-camera-bottom={-sz}
        shadow-camera-near={10}
        shadow-camera-far={300}
        shadow-bias={-0.0004}
        shadow-normalBias={0.03}
      />
      <OrbitControls
        makeDefault
        target={home.target}
        enableDamping
        dampingFactor={0.08}
        minDistance={12}
        maxDistance={Math.max(190, size * 2.6)}
        minPolarAngle={0.25}
        maxPolarAngle={1.25}
        screenSpacePanning={false}
        mouseButtons={{ LEFT: THREE.MOUSE.ROTATE, MIDDLE: THREE.MOUSE.DOLLY, RIGHT: THREE.MOUSE.PAN }}
      />
      <SimLoop active={plant} />
      <CameraRig plant={plant} />
      <LabelProjector />
      <World plant={plant} />
      {assets.map((a) => <AssetNode key={a.id} asset={a} plant={plant} />)}
      <Products plant={plant} />
    </Canvas>
  );
}
