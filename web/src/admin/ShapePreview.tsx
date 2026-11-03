// A drawing from the shape drawer, built by the twin's own code (the frame archetype and
// buildParts), so it looks here the way it will on the plant floor. A machine shows its
// envelope and the belt it has to keep clear; a product sits on a patch of floor.
import { useEffect, useRef } from 'react';
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import type { LookCfg, PartCfg } from '@/config/schema';
import { Part } from '@/config/schema';
import { ARCHETYPE_BUILDERS } from '@/three/archetypes';
import { M, P, TRIM, bodyMat, colorOf } from '@/three/kit';
import { animateParts, buildParts } from '@/three/parts';
import type { ModelHandle } from '@/three/common';

export interface PreviewSpec { target: 'machine' | 'product'; envelope: [number, number, number]; beltY: number; beltW: number; clear: number }

export default function ShapePreview({ parts, spec, color, className }: { parts: unknown[]; spec: PreviewSpec; color?: string; className?: string }) {
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const cfg: PartCfg[] = parts.map((p) => Part.parse(p));
    const renderer = new THREE.WebGLRenderer({ antialias: true });
    renderer.setPixelRatio(Math.min(2, window.devicePixelRatio));
    renderer.shadowMap.enabled = true;
    renderer.setClearColor('#f3f4f2');
    el.appendChild(renderer.domElement);
    const scene = new THREE.Scene();
    scene.add(new THREE.HemisphereLight('#ffffff', '#c9ccc6', 1.6));
    const sun = new THREE.DirectionalLight('#ffffff', 2.2);
    sun.position.set(6, 10, 7);
    sun.castShadow = true;
    sun.shadow.mapSize.set(1024, 1024);
    Object.assign(sun.shadow.camera, { left: -6, right: 6, top: 6, bottom: -6 });
    scene.add(sun);

    const [L, D, H] = spec.envelope;
    const machine = spec.target === 'machine';
    const span = machine ? Math.max(L, D, H) : Math.max(0.3, ...cfg.map((p) => Math.max(...p.size) * 2 + Math.abs(p.at[0]) + p.at[1]));
    const floor = new THREE.Mesh(new THREE.PlaneGeometry(span * 4, span * 4), new THREE.MeshStandardMaterial({ color: P.ground, roughness: 0.95 }));
    floor.rotation.x = -Math.PI / 2;
    floor.receiveShadow = true;
    scene.add(floor);
    const grid = new THREE.GridHelper(span * 4, Math.round(span * 4 / (machine ? 0.5 : 0.1)), '#c5c8c2', '#d9dbd6');
    grid.position.y = 0.001;
    scene.add(grid);

    let model: ModelHandle | null = null;
    const root = new THREE.Group();
    scene.add(root);
    if (machine) {
      // the twin's frame archetype (base plate, panel, lamp) under the parts, as on the plant floor
      const look = { archetype: 'frame', size: spec.envelope, parts: cfg } as LookCfg;
      model = ARCHETYPE_BUILDERS.frame({
        look, size: spec.envelope, beltY: spec.beltY, beltW: spec.beltW, top: spec.beltY + spec.clear - 0.15, plen: 0.6,
        body: bodyMat(colorOf(color, P.white)), accent: bodyMat(colorOf(undefined, TRIM)), rejects: false, hot: false,
      });
      root.add(model.root);
      // the belt through it, and the corridor it must keep clear
      const belt = new THREE.Mesh(new THREE.BoxGeometry(L + 1.6, 0.06, spec.beltW), M.greyDark);
      belt.position.set(0, spec.beltY - 0.03, 0);
      belt.receiveShadow = true;
      scene.add(belt);
      const corridor = new THREE.Mesh(new THREE.BoxGeometry(L, spec.clear + 0.05, spec.beltW),
        new THREE.MeshBasicMaterial({ color: '#3b82f6', transparent: true, opacity: 0.1, depthWrite: false }));
      corridor.position.set(0, spec.beltY - 0.05 + (spec.clear + 0.05) / 2, 0);
      scene.add(corridor);
      const env = new THREE.LineSegments(new THREE.EdgesGeometry(new THREE.BoxGeometry(L, H, D)), new THREE.LineBasicMaterial({ color: '#9aa1a9' }));
      env.position.y = H / 2;
      scene.add(env);
    }
    const animated = buildParts(root, cfg);

    const camera = new THREE.PerspectiveCamera(36, 1, 0.01, 200);
    const box = new THREE.Box3().setFromObject(root);
    const centre = box.getCenter(new THREE.Vector3());
    const size = box.getSize(new THREE.Vector3()).length() || span;
    camera.position.set(centre.x + size * 0.95, centre.y + size * 0.55, centre.z + size * 1.15);
    const controls = new OrbitControls(camera, renderer.domElement);
    controls.target.copy(centre);
    controls.enableDamping = true;
    controls.update();

    const resize = () => {
      const w = el.clientWidth || 1;
      const h = el.clientHeight || 1;
      renderer.setSize(w, h, false);
      renderer.domElement.style.width = '100%';
      renderer.domElement.style.height = '100%';
      camera.aspect = w / h;
      camera.updateProjectionMatrix();
    };
    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(el);

    const clock = new THREE.Clock();
    let t = 0;
    let raf = 0;
    const frame = () => {
      const dt = Math.min(0.05, clock.getDelta());
      t += dt;
      const progress = (t / 3) % 1;
      try {
        model?.update({ state: 'running', progress, speed: 1, temp: 22 } as never, dt, t, undefined as never);
      } catch { /* the preview has no plant around it */ }
      animateParts(animated, dt, t, true, progress, 0, 1);
      controls.update();
      renderer.render(scene, camera);
      raf = requestAnimationFrame(frame);
    };
    raf = requestAnimationFrame(frame);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      controls.dispose();
      renderer.dispose();
      el.removeChild(renderer.domElement);
    };
  }, [parts, spec, color]);
  return <div ref={host} className={className} aria-label="外形预览" />;
}
