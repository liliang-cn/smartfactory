// Every plant of the company on one site. Each plant keeps its own lot (hall, models, side
// roads); the site adds the shared ground and front roads. A card over each plant shows its
// figures; clicking the card or the plant flies there and opens the plant.
import { memo, useMemo, useRef, useState } from 'react';
import { Canvas, useFrame, useThree, type ThreeEvent } from '@react-three/fiber';
import { OrbitControls } from '@react-three/drei';
import * as THREE from 'three';
import { BellIcon, GaugeIcon, OctagonAlertIcon, PackageIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { campusLayout, type Campus, type CampusSlot } from '@/core/campus';
import { homeView } from '@/core/layout';
import { runtime, useTick, useUI, type PlantRuntime } from '@/state/store';
import type { Asset } from '@/sim/types';
import { applyTint, buildModel, initTint, type ModelHandle } from '@/three/factory';
import { P } from '@/three/kit';
import { buildCampus, buildWorld } from '@/three/world';
import { plantFigures } from '@/ui/group';
import { rate } from '@/ui/status';
import { CameraRig, type View } from './CameraRig';
import { Pin } from './Markers';
import { Products } from './Products';
import { Environment, SimLoop, coarse } from './Scene';

const PHI = 0.92;
// from the front left: the far end of a row of plants rises to the right, clear of the panels
const THETA = -0.42;

/** Where a plant's card hangs: over the middle of its back wall. */
const cardAnchor = (s: CampusSlot) => new THREE.Vector3(s.ox + (s.layout.box.x0 + s.layout.box.x1) / 2, 7.5, s.oz + s.layout.box.z0);

/** Moves the plant cards (plain DOM over the canvas) to their anchors every frame. */
function CardProjector({ campus, els }: { campus: Campus; els: Map<string, HTMLElement> }) {
  const anchors = useMemo(() => campus.slots.map((s) => [s.layout.id, cardAnchor(s)] as const), [campus]);
  const v = useMemo(() => new THREE.Vector3(), []);
  useFrame(({ camera, size }) => {
    // widths first, then all the writes: one layout per frame
    const widths = anchors.map(([id]) => els.get(id)?.offsetWidth ?? 0);
    anchors.forEach(([id, at], i) => {
      const el = els.get(id);
      if (!el) return;
      v.copy(at).project(camera);
      el.style.visibility = v.z > 1 ? 'hidden' : '';
      // kept on screen sideways, so a plant at the edge still shows its figures
      const half = widths[i] / 2 + 6;
      const x = Math.min(size.width - half, Math.max(half, (v.x * 0.5 + 0.5) * size.width));
      el.style.transform = `translate3d(${x.toFixed(1)}px, ${((-v.y * 0.5 + 0.5) * size.height).toFixed(1)}px, 0) translate(-50%, calc(-100% - 4px))`;
      el.style.zIndex = String(Math.round((1 - v.z) * 1e4));
    });
  });
  return null;
}

/**
 * The overview: looking down at the site from the front, as close as fits it between the KPI
 * cards at the top and the panels at the bottom.
 */
function campusHome(c: Campus, aspect: number): View {
  const cam = new THREE.PerspectiveCamera(30, aspect, 1, 5000);
  const phone = aspect < 0.8;
  const [yb, yt] = phone ? [-0.76, 0.54] : [-0.76, 0.34];
  const corners = c.slots.flatMap(({ layout: L, ox, oz }) =>
    [ox + L.roads.left - 4, ox + L.roads.right + 4].flatMap((x) => [oz + L.box.z0 - 2, oz + L.roads.front + 4].flatMap((z) => [0, 8].map((y) => new THREE.Vector3(x, y, z)))));
  const { x0, x1, z0, z1 } = c.bounds;
  // a single column of plants is seen nearly head-on, or the far ones would drift off the side
  // and from higher up, so the stack fills a tall screen
  const column = c.rows.length === c.slots.length;
  const dir = new THREE.Vector3().setFromSpherical(new THREE.Spherical(1, column ? 0.74 : PHI, column ? -0.12 : THETA));
  const fwd = new THREE.Vector3(-dir.x, 0, -dir.z).normalize();
  const right = new THREE.Vector3(fwd.z * -1, 0, fwd.x);
  const t = new THREE.Vector3();
  let d = Math.max(x1 - x0, z1 - z0) * 1.5;
  const v = new THREE.Vector3();
  for (let i = 0; i < 40; i++) {
    cam.position.copy(t).addScaledVector(dir, d);
    cam.lookAt(t);
    cam.updateMatrixWorld();
    let [mnx, mxx, mny, mxy] = [Infinity, -Infinity, Infinity, -Infinity];
    for (const p of corners) {
      v.copy(p).project(cam);
      mnx = Math.min(mnx, v.x); mxx = Math.max(mxx, v.x);
      mny = Math.min(mny, v.y); mxy = Math.max(mxy, v.y);
    }
    const k = Math.max((mxx - mnx) / 1.9, (mxy - mny) / (yt - yb));
    d *= 1 + (k - 1) * 0.8;
    const span = d * Math.tan((15 * Math.PI) / 180);
    t.addScaledVector(fwd, ((mxy + mny) / 2 - (yt + yb) / 2) * span * 0.6);
    t.addScaledVector(right, ((mxx + mnx) / 2) * span * aspect * 0.6);
  }
  const pos = t.clone().addScaledVector(dir, d);
  return { target: [t.x, 0, t.z], pos: [pos.x, pos.y, pos.z] };
}

const noRaycast = () => {};
const TINY = 0.25;

/** Freezes the transforms of something that never moves, so the frame loop skips recomputing them. */
function freeze(o: THREE.Object3D) {
  o.traverse((x) => {
    x.updateMatrix();
    x.matrixAutoUpdate = false;
  });
  return o;
}

/**
 * Three plants make several times the draw calls of one, and the shadow pass doubles them. From
 * the overview a shadow a few frames old is invisible, so the shadow map is redrawn at 10 Hz.
 */
function ShadowRate() {
  const gl = useThree((s) => s.gl);
  const n = useRef(0);
  useMemo(() => { gl.shadowMap.autoUpdate = false; gl.shadowMap.needsUpdate = true; }, [gl]);
  useFrame(() => {
    if (++n.current % 6 === 0) gl.shadowMap.needsUpdate = true;
  });
  return null;
}

const CampusPlant = memo(function CampusPlant({ slot, plant, hovered, onHover, parts }: {
  slot: CampusSlot; plant: PlantRuntime; hovered: boolean; onHover: (id: string | null) => void; parts: boolean;
}) {

  const L = slot.layout;
  const world = useMemo(() => freeze(buildWorld(L, true)), [L]);
  const models = useMemo(() => [...plant.sim.assets.values()].map((a) => {
    const m = buildModel(a, L);
    initTint(m);
    m.root.traverse((o) => {
      o.raycast = noRaycast;
      // Details under half a metre (lamps, bolts, handles) are a pixel or two from the
      // overview but cost a draw call each, twice with the shadow; they are left out here.
      const g = (o as THREE.Mesh).geometry;
      if ((o as THREE.Mesh).isMesh && !(o as THREE.InstancedMesh).isInstancedMesh && g) {
        g.boundingSphere ?? g.computeBoundingSphere();
        if (g.boundingSphere!.radius * Math.max(o.scale.x, o.scale.y, o.scale.z) < TINY) o.visible = false;
      }
    });
    return { a, m };
  }), [plant, L]);
  const { box } = L;
  const cx = (box.x0 + box.x1) / 2;
  const cz = (box.z0 + box.z1) / 2;

  useFrame(({ clock }, dt) => {
    const t = clock.elapsedTime;
    const mode = useUI.getState().mode;
    for (const { a, m } of models) {
      m.update(a, dt, t, plant.sim);
      applyTint(m, a, mode, t);
      // Trucks leave along roads that run through the neighbours' lots here.
      if (a.type === 'truck') m.root.visible &&= m.root.position.x > box.x0 - 14 && m.root.position.x < box.x1 + 10 && m.root.position.z > box.z0 - 8;
    }
  });

  const open = () => useUI.getState().camera('focus', L.id);
  const onClick = (e: ThreeEvent<MouseEvent>) => {
    e.stopPropagation();
    if (e.delta > 6) return;
    open();
  };

  return (
    <group position={[slot.ox, 0, slot.oz]} ref={(g) => { if (g) { g.updateMatrix(); g.matrixAutoUpdate = false; } }}>
      <primitive object={world} />
      {models.map(({ a, m }) => <primitive key={a.id} object={m.root} />)}
      {parts && <Products plant={plant} />}
      <Faults models={models} />
      <mesh
        position={[cx, 3.5, cz]}
        onClick={onClick}
        onPointerOver={(e) => { e.stopPropagation(); if (e.pointerType !== 'touch') { onHover(L.id); document.body.style.cursor = 'pointer'; } }}
        onPointerOut={() => { onHover(null); document.body.style.cursor = ''; }}
      >
        <boxGeometry args={[box.x1 - box.x0 + 2, 7, box.z1 - box.z0 + 2]} />
        <meshBasicMaterial transparent opacity={0} depthWrite={false} colorWrite={false} />
      </mesh>
      {hovered && <Outline box={box} />}
    </group>
  );
});

/** A big bobbing pin over every faulted asset, visible from the overview. */
function Faults({ models }: { models: { a: Asset; m: ModelHandle }[] }) {
  useTick();
  return (
    <>
      {models.filter(({ a }) => a.state === 'fault').map(({ a, m }) => (
        <group key={a.id} position={[m.root.position.x + (m.labelAt?.x ?? 0), m.top + 1, m.root.position.z + (m.labelAt?.z ?? 0)]} scale={5}>
          <Pin color={P.red} x={0} y={0} z={0} phase={0.5} />
        </group>
      ))}
    </>
  );
}

function Outline({ box }: { box: { x0: number; x1: number; z0: number; z1: number } }) {
  const geo = useMemo(() => {
    const p = 2.5;
    const t = 0.9;
    const [x0, x1, z0, z1] = [box.x0 - p, box.x1 + p, box.z0 - p, box.z1 + p];
    const s = new THREE.Shape();
    s.moveTo(x0, z0); s.lineTo(x1, z0); s.lineTo(x1, z1); s.lineTo(x0, z1); s.lineTo(x0, z0);
    const h = new THREE.Path();
    h.moveTo(x0 + t, z0 + t); h.lineTo(x0 + t, z1 - t); h.lineTo(x1 - t, z1 - t); h.lineTo(x1 - t, z0 + t); h.lineTo(x0 + t, z0 + t);
    s.holes.push(h);
    return new THREE.ShapeGeometry(s);
  }, [box]);
  return (
    <mesh geometry={geo} rotation-x={Math.PI / 2} position-y={0.05} raycast={noRaycast} renderOrder={2}>
      <meshBasicMaterial color={P.blue} transparent opacity={0.85} depthWrite={false} side={THREE.DoubleSide} />
    </mesh>
  );
}

/** A figure with its label; on phones the label gives way to an icon. */
function Stat({ label, icon, value, tone }: { label: string; icon: React.ReactNode; value: string | number; tone?: 'bad' | 'muted' }) {
  return (
    <span className={cn('grid min-w-11 justify-items-center leading-tight max-md:flex max-md:min-w-0 max-md:items-center max-md:gap-0.5 max-md:[&>svg]:size-3', tone === 'bad' && 'text-destructive', tone === 'muted' && 'text-muted-foreground/70')}>
      <span className="hidden max-md:contents">{icon}</span>
      <b className="text-[17px] font-extrabold tabular-nums max-md:text-xs">{value}</b>
      <small className="whitespace-nowrap text-[11px] text-muted-foreground max-md:hidden">{label}</small>
    </span>
  );
}

const PlantCard = memo(function PlantCard({ plant, hovered, onHover, els }: { plant: PlantRuntime; hovered: boolean; onHover: (id: string | null) => void; els: Map<string, HTMLElement> }) {
  useTick();
  const L = plant.layout;
  const f = plantFigures(plant);
  return (
    <button
      type="button"
      ref={(el) => {
        if (el) els.set(L.id, el);
        else els.delete(L.id);
      }}
      onClick={() => useUI.getState().camera('focus', L.id)}
      onPointerEnter={() => onHover(L.id)}
      onPointerLeave={() => onHover(null)}
      className={cn(
        'pointer-events-auto absolute left-0 top-0 grid gap-1.5 rounded-2xl bg-card/95 px-3 pb-2 pt-2.5 text-left shadow-float ring-2 ring-transparent will-change-transform max-md:flex max-md:items-center max-md:gap-2 max-md:rounded-full max-md:py-1 max-md:pl-1 max-md:pr-2.5',
        hovered && 'ring-primary',
        f.faults > 0 && !hovered && 'ring-destructive/70',
      )}
    >
      <span className="flex items-center gap-2 whitespace-nowrap">
        <span className="grid h-6 min-w-7 place-items-center rounded-md bg-primary px-1 text-[11px] font-extrabold text-primary-foreground max-md:h-5 max-md:rounded-full max-md:px-1.5 max-md:text-[10px]">{L.badge}</span>
        <b className="text-[14px] font-extrabold max-md:hidden">{L.name} {L.hall}</b>
      </span>
      <span className="grid grid-cols-4 gap-3 max-md:flex max-md:gap-2">
        <Stat label="OEE" icon={<GaugeIcon />} value={`${(f.oee * 100).toFixed(0)}%`} />
        <Stat label={`${L.unit}/时`} icon={<PackageIcon />} value={rate(f.tp)} />
        <Stat label="报警" icon={<BellIcon />} value={f.alarms} tone={f.alarms ? 'bad' : 'muted'} />
        <Stat label="故障" icon={<OctagonAlertIcon />} value={f.faults} tone={f.faults ? 'bad' : 'muted'} />
      </span>
    </button>
  );
});

export function CampusScene() {
  const rt = runtime();
  const [hovered, setHovered] = useState<string | null>(null);
  const campus = useMemo(() => campusLayout(rt.plants.map((p) => p.layout), window.innerWidth / Math.max(1, window.innerHeight)), [rt]);
  const home = campusHome(campus, window.innerWidth / Math.max(1, window.innerHeight));
  const { x0, x1, z0, z1 } = campus.bounds;
  const size = Math.max(x1 - x0, z1 - z0);
  const sh = size / 2 + 20;
  // the overview's distance, so the far plants stay clear of the haze however the site is framed
  const far = Math.max(size, Math.hypot(home.pos[0] - home.target[0], home.pos[1] - home.target[1], home.pos[2] - home.target[2]));
  // Parts on belts are too small to see from the overview; they are drawn for one plant at most.
  const parts = rt.plants.filter((p) => p.layout.id === hovered);
  const els = useRef(new Map<string, HTMLElement>()).current;
  const flyTo = (id: string, aspect: number) => {
    const slot = campus.slots.find((s) => s.layout.id === id);
    if (!slot) return null;
    const h = homeView(slot.layout, aspect);
    const at = (v: [number, number, number]): [number, number, number] => [v[0] + slot.ox, v[1], v[2] + slot.oz];
    return { target: at(h.target), pos: at(h.pos), then: () => useUI.getState().setPlant(id) };
  };
  return (
    <>
    <Canvas
      className="!absolute inset-0 touch-none"
      shadows={{ type: THREE.PCFShadowMap }}
      dpr={[1, coarse ? 1.5 : 2]}
      camera={{ fov: 30, near: 2, far: 5000, position: home.pos }}
      onCreated={({ gl }) => {
        gl.toneMapping = THREE.NeutralToneMapping;
        gl.toneMappingExposure = 1.05;
      }}
    >
      <color attach="background" args={[P.ground]} />
      <fog attach="fog" args={[P.ground, far * 1.4, far * 3.5]} />
      <Environment />
      <hemisphereLight args={['#ffffff', '#c9cfee', 1.5]} />
      <directionalLight
        castShadow
        color="#fffaf2"
        intensity={2.4}
        position={[-60, 120, 70]}
        shadow-mapSize={coarse ? [2048, 2048] : [4096, 4096]}
        shadow-camera-left={-sh}
        shadow-camera-right={sh}
        shadow-camera-top={sh}
        shadow-camera-bottom={-sh}
        shadow-camera-near={10}
        shadow-camera-far={600}
        shadow-bias={-0.0006}
        shadow-normalBias={0.06}
      />
      <OrbitControls
        makeDefault
        target={home.target}
        enableDamping
        dampingFactor={0.08}
        minDistance={20}
        maxDistance={far * 1.6}
        minPolarAngle={0.25}
        maxPolarAngle={1.25}
        screenSpacePanning={false}
        mouseButtons={{ LEFT: THREE.MOUSE.ROTATE, MIDDLE: THREE.MOUSE.DOLLY, RIGHT: THREE.MOUSE.PAN }}
      />
      <SimLoop active={parts} />
      <ShadowRate />
      <CameraRig home={(aspect) => campusHome(campus, aspect)} flyTo={flyTo} />
      <CampusGround campus={campus} />
      {campus.slots.map((s) => (
        <CampusPlant key={s.layout.id} slot={s} plant={rt.byId[s.layout.id]} hovered={hovered === s.layout.id} onHover={setHovered} parts={hovered === s.layout.id} />
      ))}
      <CardProjector campus={campus} els={els} />
    </Canvas>
    <div className="pointer-events-none absolute inset-0 z-10 overflow-hidden">
      {campus.slots.map((s) => (
        <PlantCard key={s.layout.id} plant={rt.byId[s.layout.id]} hovered={hovered === s.layout.id} onHover={setHovered} els={els} />
      ))}
    </div>
    </>
  );
}

function CampusGround({ campus }: { campus: Campus }) {
  const g = useMemo(() => freeze(buildCampus(campus)), [campus]);
  return <primitive object={g} />;
}
