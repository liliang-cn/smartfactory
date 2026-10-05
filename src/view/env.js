// Renderer, camera, lights and the static world: hall slab, painted floor, walls, roads, trees.
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { CSS2DRenderer } from 'three/addons/renderers/CSS2DRenderer.js';
import { RoomEnvironment } from 'three/addons/environments/RoomEnvironment.js';
import { AGVS, FG_ZONE, LINES, WAREHOUSE } from '../sim/layout.js';
import { M, P, blk, canvasTex, cyl, mesh } from './kit.js';

export const HALL = { x0: -38, x1: 35, z0: -19, z1: 19 };

export function createStage(container) {
  const renderer = new THREE.WebGLRenderer({ antialias: true, powerPreference: 'high-performance' });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = THREE.PCFShadowMap;
  renderer.toneMapping = THREE.NeutralToneMapping;
  renderer.toneMappingExposure = 1.05;
  container.appendChild(renderer.domElement);

  const labels = new CSS2DRenderer();
  labels.domElement.className = 'labels-layer';
  container.appendChild(labels.domElement);

  const scene = new THREE.Scene();
  scene.background = new THREE.Color(P.ground);
  scene.fog = new THREE.Fog(P.ground, 140, 260);
  const pmrem = new THREE.PMREMGenerator(renderer);
  scene.environment = pmrem.fromScene(new RoomEnvironment(), 0.04).texture;
  scene.environmentIntensity = 0.45;

  scene.add(new THREE.HemisphereLight('#ffffff', '#c9cfee', 1.5));
  const sun = new THREE.DirectionalLight('#fffaf2', 2.4);
  sun.position.set(-30, 60, 35);
  sun.castShadow = true;
  sun.shadow.mapSize.set(4096, 4096);
  const sc = sun.shadow.camera;
  sc.left = -62; sc.right = 62; sc.top = 42; sc.bottom = -42; sc.near = 10; sc.far = 160;
  sun.shadow.bias = -0.0004;
  sun.shadow.normalBias = 0.03;
  sun.shadow.radius = 4;
  scene.add(sun);

  const camera = new THREE.PerspectiveCamera(30, 1, 1, 600);
  camera.position.set(40, 46, 58);
  const controls = new OrbitControls(camera, renderer.domElement);
  controls.target.set(-2, 0, 0);
  controls.enableDamping = true;
  controls.dampingFactor = 0.08;
  controls.minDistance = 12;
  controls.maxDistance = 190;
  controls.minPolarAngle = 0.25;
  controls.maxPolarAngle = 1.25;
  controls.screenSpacePanning = false;
  controls.mouseButtons = { LEFT: THREE.MOUSE.ROTATE, MIDDLE: THREE.MOUSE.DOLLY, RIGHT: THREE.MOUSE.PAN };

  const resize = () => {
    const w = container.clientWidth;
    const h = container.clientHeight;
    renderer.setSize(w, h);
    labels.setSize(w, h);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
  };
  new ResizeObserver(resize).observe(container);
  resize();

  buildWorld(scene);
  return { renderer, labels, scene, camera, controls, sun };
}

function buildWorld(scene) {
  const g = new THREE.Group();
  scene.add(g);

  const ground = mesh(g, new THREE.PlaneGeometry(700, 700), new THREE.MeshStandardMaterial({ color: P.ground, roughness: 1 }), 0, -0.32, 0, false);
  ground.rotation.x = -Math.PI / 2;

  // Hall slab with the painted floor plan on top
  const W = HALL.x1 - HALL.x0;
  const D = HALL.z1 - HALL.z0;
  const cx = (HALL.x0 + HALL.x1) / 2;
  const cz = (HALL.z0 + HALL.z1) / 2;
  blk(g, M.white, W + 0.8, 0.3, D + 0.8, cx, -0.3, cz, 0.15);
  const floor = mesh(g, new THREE.PlaneGeometry(W, D), new THREE.MeshStandardMaterial({ map: floorTexture(W, D), roughness: 0.9 }), cx, 0.002, cz, false);
  floor.rotation.x = -Math.PI / 2;
  floor.receiveShadow = true;

  buildWalls(g);
  buildRoads(g);
  buildTrees(g);
  buildNeighbourhood(g);
}

// Everything painted on the hall floor lives in one canvas: lanes, zones, AGV tape, stencils.
function floorTexture(W, D) {
  const S = 28; // px per metre
  return canvasTex(W * S, D * S, (c, w, h) => {
    const X = (x) => (x - HALL.x0) * S;
    const Z = (z) => (z - HALL.z0) * S;
    c.fillStyle = P.slab;
    c.fillRect(0, 0, w, h);
    c.strokeStyle = 'rgba(80,95,150,0.06)';
    c.lineWidth = 2;
    for (let x = HALL.x0; x <= HALL.x1; x += 6) { c.beginPath(); c.moveTo(X(x), 0); c.lineTo(X(x), h); c.stroke(); }
    for (let z = HALL.z0; z <= HALL.z1; z += 6) { c.beginPath(); c.moveTo(0, Z(z)); c.lineTo(w, Z(z)); c.stroke(); }

    const rr = (x0, z0, x1, z1, fill, stroke, dash = []) => {
      const r = 0.6 * S;
      c.beginPath();
      c.roundRect(X(x0), Z(z0), X(x1) - X(x0), Z(z1) - Z(z0), r);
      if (fill) { c.fillStyle = fill; c.fill(); }
      if (stroke) { c.setLineDash(dash); c.strokeStyle = stroke; c.lineWidth = 0.14 * S; c.stroke(); c.setLineDash([]); }
    };

    // Line cells
    for (const L of LINES) rr(L.x0 - 1.4, L.z - 3.3, L.x1 + 2.4, L.z + 3.3, 'rgba(47,91,234,0.045)', P.yellow);
    // Main aisle edges
    c.strokeStyle = P.yellow;
    c.lineWidth = 0.14 * S;
    c.setLineDash([0.9 * S, 0.6 * S]);
    for (const z of [-4.6, 4.6]) { c.beginPath(); c.moveTo(X(-23), Z(z)); c.lineTo(X(33.5), Z(z)); c.stroke(); }
    c.setLineDash([]);
    // Warehouse and finished goods zones
    rr(WAREHOUSE.x0 - 0.4, WAREHOUSE.z0 - 1.6, WAREHOUSE.x1 + 0.4, WAREHOUSE.z1 + 1.6, 'rgba(47,91,234,0.05)', P.yellow);
    rr(FG_ZONE.x0, FG_ZONE.z0, FG_ZONE.x1, FG_ZONE.z1, 'rgba(246,196,69,0.12)', P.yellow, [0.6 * S, 0.4 * S]);
    // Pallet bays in the FG zone
    c.strokeStyle = 'rgba(191,138,82,0.5)';
    c.lineWidth = 0.05 * S;
    for (let i = 0; i < 4; i++) for (let j = 0; j < 6; j++) {
      c.strokeRect(X(FG_ZONE.x0 + 0.5 + i * 2.0), Z(FG_ZONE.z0 + 0.5 + j * 1.95), 1.4 * S, 1.2 * S);
    }
    // Docks: hatched loading bays by the doors
    const hatch = (x0, z0, x1, z1) => {
      c.save();
      c.beginPath();
      c.rect(X(x0), Z(z0), X(x1) - X(x0), Z(z1) - Z(z0));
      c.clip();
      c.strokeStyle = 'rgba(246,196,69,0.7)';
      c.lineWidth = 0.12 * S;
      for (let k = -40; k < 80; k++) { c.beginPath(); c.moveTo(X(x0) + k * 0.5 * S, Z(z0)); c.lineTo(X(x0) + k * 0.5 * S - 4 * S, Z(z1)); c.stroke(); }
      c.restore();
      rr(x0, z0, x1, z1, null, P.yellow);
    };
    hatch(HALL.x0 + 0.3, -2.5, HALL.x0 + 2.6, 2.5);
    hatch(HALL.x1 - 2.6, -2.5, HALL.x1 - 0.3, 2.5);

    // AGV guide tape
    c.strokeStyle = 'rgba(47,91,234,0.55)';
    c.lineWidth = 0.08 * S;
    c.lineJoin = 'round';
    for (const a of AGVS) {
      c.beginPath();
      a.route.forEach((p, i) => (i ? c.lineTo(X(p.x), Z(p.z)) : c.moveTo(X(p.x), Z(p.z))));
      c.closePath();
      c.stroke();
      for (const p of a.route.filter((q) => q.act)) {
        c.fillStyle = 'rgba(47,91,234,0.18)';
        c.beginPath();
        c.arc(X(p.x), Z(p.z), 0.75 * S, 0, Math.PI * 2);
        c.fill();
      }
    }

    // Stencilled zone names
    c.fillStyle = 'rgba(47,91,234,0.5)';
    c.textBaseline = 'middle';
    const stencil = (txt, x, z, size, rot = 0) => {
      c.save();
      c.translate(X(x), Z(z));
      c.rotate(rot);
      c.font = `800 ${size * S}px "Plus Jakarta Sans", "PingFang SC", sans-serif`;
      c.textAlign = 'center';
      c.fillText(txt, 0, 0);
      c.restore();
    };
    for (const L of LINES) stencil(`${L.id}  ${L.name}`, -13.5, L.z + (L.z < 0 ? 3.9 : -3.9), 0.9);
    stencil('原料立体库', (WAREHOUSE.x0 + WAREHOUSE.x1) / 2, WAREHOUSE.z1 + 2.4, 1.1);
    stencil('成品暂存区', (FG_ZONE.x0 + FG_ZONE.x1) / 2, FG_ZONE.z1 + 1.2, 0.9);
    stencil('主通道', 4, 0, 1.4);
  });
}

function buildWalls(g) {
  const H = 6.5;
  const wall = M.white;
  const { x0, x1, z0, z1 } = HALL;
  // Tall back wall (far from the default camera)
  blk(g, wall, x1 - x0 + 0.6, H, 0.4, (x0 + x1) / 2, 0, z0 - 0.2, 0.08);
  blk(g, M.blue, x1 - x0 + 0.7, 0.5, 0.5, (x0 + x1) / 2, H - 0.5, z0 - 0.2, 0.1);
  const win = new THREE.MeshStandardMaterial({ color: '#cbdaff', emissive: '#e8efff', emissiveIntensity: 0.25, roughness: 0.15 });
  for (let x = x0 + 4; x < x1 - 2; x += 6) {
    blk(g, win, 3.6, 1.2, 0.06, x, 3.6, z0 + 0.02, 0.05);
    blk(g, M.blue, 0.3, H - 0.5, 0.5, x - 3, 0, z0 - 0.2, 0.06);
  }
  // Tall left wall with the inbound dock door
  const lw = (za, zb) => blk(g, wall, 0.4, H, zb - za, x0 - 0.2, 0, (za + zb) / 2, 0.08);
  lw(z0 - 0.4, -2.8);
  lw(2.8, z1 + 0.4);
  blk(g, wall, 0.4, H - 4.4, 5.6, x0 - 0.2, 4.4, 0, 0.06);
  blk(g, M.blue, 0.5, 0.5, z1 - z0 + 0.8, x0 - 0.2, H - 0.5, 0, 0.1);
  dockDoor(g, x0 - 0.2, 0, Math.PI / 2);
  // Low parapets on the camera-facing sides so the floor stays visible
  const low = 0.9;
  blk(g, wall, x1 - x0 + 0.8, low, 0.4, (x0 + x1) / 2, 0, z1 + 0.2, 0.12);
  blk(g, M.blue, x1 - x0 + 0.8, 0.12, 0.44, (x0 + x1) / 2, low, z1 + 0.2, 0.05);
  const rw = (za, zb) => {
    blk(g, wall, 0.4, low, zb - za, x1 + 0.2, 0, (za + zb) / 2, 0.12);
    blk(g, M.blue, 0.44, 0.12, zb - za, x1 + 0.2, low, (za + zb) / 2, 0.05);
  };
  rw(z0 - 0.4, -2.8);
  rw(2.8, z1 + 0.4);
  // outbound dock canopy
  for (const sz of [-1, 1]) blk(g, M.blue, 0.3, 4.2, 0.3, x1 + 0.2, 0, sz * 2.9, 0.06);
  blk(g, M.blue, 0.9, 0.3, 6.4, x1 + 0.5, 4.2, 0, 0.08);
  // dock bumpers
  for (const x of [x0 - 0.6, x1 + 0.6]) {
    for (const sz of [-1, 1]) blk(g, M.ink, 0.3, 0.6, 0.3, x, 0, sz * 1.8, 0.06);
  }
}

function dockDoor(g, x, z, rot) {
  const d = new THREE.Group();
  d.position.set(x, 0, z);
  d.rotation.y = rot;
  g.add(d);
  blk(d, M.blue, 5.6, 0.3, 0.6, 0, 4.1, 0, 0.06);
  for (const sx of [-1, 1]) blk(d, M.blue, 0.3, 4.4, 0.6, sx * 2.8, 0, 0, 0.06);
  // Half-open roller shutter
  const slat = new THREE.MeshStandardMaterial({ color: '#dfe6fb', roughness: 0.6 });
  for (let i = 0; i < 6; i++) blk(d, slat, 5.2, 0.22, 0.12, 0, 2.6 + i * 0.25, -0.1, 0.03);
}

function buildRoads(g) {
  const roadTex = (len, horizontal) => canvasTex(horizontal ? 2048 : 128, horizontal ? 128 : 2048, (c, w, h) => {
    c.fillStyle = P.road;
    c.fillRect(0, 0, w, h);
    c.fillStyle = '#ffffff';
    const n = Math.floor(len / 6);
    for (let i = 0; i < n; i++) {
      if (horizontal) c.fillRect((i / n) * w, h / 2 - 3, (w / n) * 0.45, 6);
      else c.fillRect(w / 2 - 3, (i / n) * h, 6, (h / n) * 0.45);
    }
    c.fillStyle = 'rgba(255,255,255,0.9)';
    if (horizontal) { c.fillRect(0, 6, w, 4); c.fillRect(0, h - 10, w, 4); }
    else { c.fillRect(6, 0, 4, h); c.fillRect(w - 10, 0, 4, h); }
  });
  const road = (x, z, w, d, horizontal) => {
    const len = horizontal ? w : d;
    const m = mesh(g, new THREE.PlaneGeometry(w, d), new THREE.MeshStandardMaterial({ map: roadTex(len, horizontal), roughness: 0.95 }), x, -0.31, z, false);
    m.rotation.x = -Math.PI / 2;
    m.receiveShadow = true;
  };
  road(0, 27, 320, 8, true);
  road(-50, -36, 8, 118, false);
  road(42, -36, 8, 118, false);
  // Kerbs / pavements
  const pave = new THREE.MeshStandardMaterial({ color: '#f5f6fb', roughness: 0.9 });
  blk(g, pave, 320, 0.12, 1.4, 0, -0.32, 22.3, 0.05);
  blk(g, pave, 320, 0.12, 1.4, 0, -0.32, 31.7, 0.05);
  // Truck aprons between road and dock doors
  blk(g, pave, 8.4, 0.1, 12, -42.2, -0.32, 0, 0.3);
  blk(g, pave, 3.4, 0.1, 12, 36.7, -0.32, 0, 0.3);
  // Parking bays and grass verges
  const grass = (x, z, w, d) => blk(g, M.mint, w, 0.1, d, x, -0.32, z, 0.4);
  grass(0, 36, 200, 7);
  grass(-58, -10, 7, 40);
  grass(50, -10, 7, 40);
  grass(-4, -24, 76, 4);
}

function buildTrees(g) {
  const spots = [];
  for (let x = -100; x <= 100; x += 7) spots.push([x + Math.random() * 2, 36 + (Math.random() - 0.5) * 3]);
  for (let z = -30; z <= 10; z += 6) { spots.push([-58 + (Math.random() - 0.5) * 2, z]); spots.push([50 + (Math.random() - 0.5) * 2, z]); }
  for (let x = -38; x <= 32; x += 5.5) spots.push([x, -24 + (Math.random() - 0.5)]);
  const trunk = new THREE.InstancedMesh(new THREE.CylinderGeometry(0.12, 0.16, 1.4, 8), M.trunk, spots.length);
  const crown = new THREE.InstancedMesh(new THREE.SphereGeometry(1, 18, 14), M.tree, spots.length);
  const m = new THREE.Matrix4();
  const q = new THREE.Quaternion();
  const col = new THREE.Color();
  spots.forEach(([x, z], i) => {
    const s = 0.8 + Math.random() * 0.5;
    m.compose(new THREE.Vector3(x, 0.4, z), q, new THREE.Vector3(1, 1, 1));
    trunk.setMatrixAt(i, m);
    m.compose(new THREE.Vector3(x, 1.4 + s * 1.1, z), q, new THREE.Vector3(s, s * 1.25, s));
    crown.setMatrixAt(i, m);
    crown.setColorAt(i, col.set(Math.random() > 0.5 ? P.tree : P.treeDark));
  });
  for (const im of [trunk, crown]) { im.castShadow = true; im.receiveShadow = true; g.add(im); }
}

// Simple blocks in the distance so the plant sits in a place, not in a void.
function buildNeighbourhood(g) {
  const bmat = [M.white, M.shell, new THREE.MeshStandardMaterial({ color: '#dfe6fb', roughness: 0.7 })];
  const lots = [[-80, -44, 18, 6, 14], [-84, -14, 14, 8, 12], [-30, -46, 22, 7, 12], [12, -48, 18, 9, 12], [76, -44, 20, 7, 16], [80, -14, 14, 10, 12]];
  for (const [x, z, w, h, d] of lots) {
    blk(g, bmat[Math.floor(Math.random() * 3)], w, h, d, x, -0.3, z, 0.3);
    blk(g, M.blue, w + 0.2, 0.3, d + 0.2, x, h - 0.3, z, 0.1);
    for (let k = 0; k < 3; k++) cyl(g, M.metal, 0.8, 0.5, x - w / 3 + k * (w / 3), h, z, 16);
  }
}
