import * as THREE from 'three';
import './style.css';
import { PlantSim } from './sim/plant.js';
import { createStage } from './view/env.js';
import { TwinView } from './view/twin.js';
import { UI } from './ui/ui.js';

const sim = new PlantSim();
const stageEl = document.getElementById('stage');
const stage = createStage(stageEl);
const view = new TwinView(stage, sim);
const { camera, controls } = stage;

const state = { selected: null, follow: false, timeScale: 1, mode: 'live', labels: false };
const HOME = { target: new THREE.Vector3(-2, 0, 0), pos: new THREE.Vector3(40, 46, 58) };

// ---------- camera moves ----------

let fly = null;
const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);
function flyTo(target, pos, dur = 1.0) {
  fly = { t: 0, dur, fromT: controls.target.clone(), fromP: camera.position.clone(), toT: target.clone(), toP: pos.clone() };
}
function orbitTo({ radius, theta, phi, target = controls.target }, dur) {
  const sph = new THREE.Spherical().setFromVector3(camera.position.clone().sub(controls.target));
  if (radius !== undefined) sph.radius = radius;
  if (theta !== undefined) sph.theta = theta;
  if (phi !== undefined) sph.phi = phi;
  flyTo(target, target.clone().add(new THREE.Vector3().setFromSpherical(sph)), dur);
}
function currentSph() {
  return new THREE.Spherical().setFromVector3(camera.position.clone().sub(controls.target));
}
function focus(id) {
  const f = view.frameOf(id);
  if (!f) return;
  const radius = THREE.MathUtils.clamp(f.size * 2 + 24, 28, 90);
  orbitTo({ radius, phi: Math.min(currentSph().phi, 0.95), target: f.center }, 1.1);
}

// ---------- actions shared by the 3D view, panels and keyboard ----------

const act = {
  select(id, doFocus = false) {
    if (id && !sim.assets.has(id)) return;
    state.selected = id;
    if (!id || !sim.assets.get(id)?.agv && !sim.assets.get(id)?.truck) state.follow = false;
    view.setSelected(id);
    ui.openInspector(id);
    if (id) {
      const line = sim.assets.get(id).line;
      if (line && ui.trackerLine !== line.id) { ui.trackerLine = line.id; ui.renderTracker(true); }
    }
    if (id && doFocus) focus(id);
    ui.renderFleet(true);
  },
  focus,
  command(id, cmd, arg) {
    const r = sim.command(id, cmd, arg);
    ui.toast(r.msg, r.ok ? 'ok' : 'err');
    ui.renderFleet(true);
  },
  setSpeed(s) {
    state.timeScale = s;
    ui.renderTop();
  },
  setMode(m) {
    state.mode = m;
    view.mode = m;
    ui.renderTop();
  },
  toggleLabels() {
    state.labels = !state.labels;
    view.showAllLabels = state.labels;
    ui.renderTop();
  },
  toggleFollow() {
    state.follow = !state.follow;
    if (state.follow && state.selected) focus(state.selected);
    ui.renderInspector();
  },
  cam(op) {
    const s = currentSph();
    if (op === 'in') orbitTo({ radius: Math.max(controls.minDistance, s.radius * 0.7) }, 0.45);
    if (op === 'out') orbitTo({ radius: Math.min(controls.maxDistance, s.radius * 1.4) }, 0.45);
    if (op === 'left') orbitTo({ theta: s.theta - Math.PI / 4 }, 0.7);
    if (op === 'right') orbitTo({ theta: s.theta + Math.PI / 4 }, 0.7);
    if (op === 'home') { state.follow = false; flyTo(HOME.target, HOME.pos, 1.1); }
  },
};

const ui = new UI(sim, act, state);
view.onLabelClick = (id) => act.select(id, false);

// ---------- pointer input on the canvas ----------

const ndc = new THREE.Vector2();
let down = null;
let hoverQueued = null;
const canvas = stage.renderer.domElement;

canvas.addEventListener('pointerdown', (e) => {
  down = { x: e.clientX, y: e.clientY, t: performance.now() };
  if (fly) fly = null;
});
canvas.addEventListener('pointerup', (e) => {
  if (!down) return;
  const moved = Math.hypot(e.clientX - down.x, e.clientY - down.y);
  if (e.button === 0 && moved < 6 && performance.now() - down.t < 500) {
    const id = pickAt(e);
    act.select(id);
  }
  down = null;
});
canvas.addEventListener('dblclick', (e) => {
  const id = pickAt(e);
  if (id) act.select(id, true);
});
canvas.addEventListener('pointermove', (e) => { hoverQueued = e; });
canvas.addEventListener('pointerleave', () => { hoverQueued = null; view.setHovered(null); ui.tooltip(null); stageEl.classList.remove('hovering'); });
controls.addEventListener('start', () => { if (state.follow) { state.follow = false; ui.renderInspector(); } });

function pickAt(e) {
  const r = canvas.getBoundingClientRect();
  ndc.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
  return view.pick(ndc);
}

function processHover() {
  if (!hoverQueued) return;
  const e = hoverQueued;
  hoverQueued = null;
  if (e.buttons) { ui.tooltip(null); return; }
  const id = pickAt(e);
  view.setHovered(id);
  stageEl.classList.toggle('hovering', !!id);
  const r = canvas.getBoundingClientRect();
  ui.tooltip(id ? sim.assets.get(id) : null, e.clientX - r.left, e.clientY - r.top);
}

// ---------- keyboard ----------

window.addEventListener('keydown', (e) => {
  if (e.target.closest('input, textarea')) return;
  if (e.key === '/') { e.preventDefault(); document.getElementById('search').focus(); }
  else if (e.key === 'Escape') act.select(null);
  else if (e.key === ' ') { e.preventDefault(); act.setSpeed(state.timeScale === 0 ? 1 : 0); }
  else if (e.key === 'f' || e.key === 'F') { if (state.selected) focus(state.selected); }
  else if (e.key === 'h' || e.key === 'H') act.cam('home');
  else if (e.key === 'l' || e.key === 'L') act.toggleLabels();
  else if (e.key === '1') act.setMode('live');
  else if (e.key === '2') act.setMode('heat');
  else if (e.key === '3') act.setMode('util');
});

// ---------- main loop ----------

let last = performance.now();
let uiAcc = 0;
const followPrev = new THREE.Vector3();
let followInit = false;

function frame(now) {
  const dt = Math.min(0.1, (now - last) / 1000);
  last = now;
  let simDt = dt * state.timeScale;
  while (simDt > 1e-6) {
    const h = Math.min(0.1, simDt);
    sim.step(h);
    simDt -= h;
  }

  view.update(dt, now / 1000);

  if (fly) {
    fly.t += dt;
    const k = ease(Math.min(1, fly.t / fly.dur));
    controls.target.lerpVectors(fly.fromT, fly.toT, k);
    camera.position.lerpVectors(fly.fromP, fly.toP, k);
    if (fly.t >= fly.dur) fly = null;
  } else if (state.follow && state.selected) {
    const p = view.worldPos(state.selected);
    if (p) {
      p.y = 0.8;
      if (!followInit) { followPrev.copy(controls.target); followInit = true; }
      const delta = p.clone().sub(controls.target).multiplyScalar(1 - Math.exp(-dt * 5));
      controls.target.add(delta);
      camera.position.add(delta);
    }
  } else followInit = false;
  controls.update();

  processHover();
  uiAcc += dt;
  if (uiAcc > 0.25) {
    uiAcc = 0;
    ui.renderTop();
    ui.renderInspector();
    ui.renderTracker();
    ui.renderFleet();
  }

  stage.renderer.render(stage.scene, camera);
  stage.labels.render(stage.scene, camera);
  requestAnimationFrame(frame);
}

ui.renderTop();
ui.renderTracker(true);
ui.renderFleet(true);
requestAnimationFrame((t) => {
  last = t;
  frame(t);
  document.getElementById('loading').classList.add('done');
});

// Handy for poking at the twin from the console.
window.twin = { sim, view, stage, act, state };
