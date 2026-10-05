// Binds the simulation to the 3D scene: builds a model per asset, mirrors state every frame,
// and owns picking, labels, alarm pins and the selection footprint.
import * as THREE from 'three';
import { CSS2DObject } from 'three/addons/renderers/CSS2DRenderer.js';
import { LINE_Y, WAREHOUSE, FG_ZONE } from '../sim/layout.js';
import { STATE_LABEL } from '../sim/plant.js';
import {
  STATION_BUILDERS, buildAGV, buildBuffer, buildConveyor, buildFG, buildOutfeed, buildTruck, buildWarehouse,
  productGeo, productMat,
} from './models.js';
import { P, ramp } from './kit.js';

const ALERT_STATES = new Set(['fault', 'blocked', 'empty', 'full', 'maint']);

export class TwinView {
  constructor(stage, sim) {
    this.stage = stage;
    this.sim = sim;
    this.scene = stage.scene;
    this.models = new Map();
    this.pickables = [];
    this.mode = 'live';
    this.showAllLabels = false;
    this.selected = null;
    this.hovered = null;
    this.raycaster = new THREE.Raycaster();
    this.products = new Map();
    this.flying = [];
    this._frame = 0;
    this._build();
    this._buildMarkers();
  }

  _add(asset, model) {
    const root = model.root;
    this.scene.add(root);
    root.traverse((o) => {
      if (o.isMesh || o.isInstancedMesh) {
        o.userData.assetId = asset.id;
        this.pickables.push(o);
      }
    });
    for (const m of model.tint) m.userData.base = m.color.clone();
    const el = document.createElement('div');
    el.className = 'tag';
    el.innerHTML = `<i></i><b>${asset.id}</b><span></span>`;
    el.addEventListener('pointerdown', (e) => { e.stopPropagation(); this.onLabelClick?.(asset.id); });
    const label = new CSS2DObject(el);
    const at = model.labelAt ?? new THREE.Vector3();
    label.position.set(at.x, model.top, at.z);
    root.add(label);
    this.models.set(asset.id, { asset, model, label, el, lastState: '', lastText: '' });
  }

  _build() {
    const sim = this.sim;
    for (const line of sim.lines) {
      this._add(line.conveyor, buildConveyor(line));
      this._add(line.infeed, buildBuffer(line, line.infeed));
      this._add(line.outfeed, buildOutfeed(line, line.outfeed));
      for (const a of line.stations) {
        const model = STATION_BUILDERS[a.type](a);
        model.root.position.set(line.x0 + a.s, 0, line.z);
        model.root.rotation.y = line.z < 0 ? 0 : Math.PI;
        this._add(a, model);
      }
    }
    this._add(sim.wh, buildWarehouse(WAREHOUSE));
    this._add(sim.fg, buildFG(FG_ZONE));
    for (const g of sim.agvs) this._add(g.asset, buildAGV(g.asset));
    for (const t of sim.trucks) {
      const model = buildTruck(t.asset);
      this._add(t.asset, model);
    }
  }

  _buildMarkers() {
    // Map pin: a lathed teardrop with a white eye, the strategy-game way of saying "look here".
    const prof = [[0, 0], [0.07, 0.14], [0.19, 0.38], [0.29, 0.62], [0.33, 0.82], [0.3, 0.98], [0.2, 1.1], [0, 1.15]]
      .map(([x, y]) => new THREE.Vector2(x, y));
    const pinGeo = new THREE.LatheGeometry(prof, 28);
    const eyeGeo = new THREE.SphereGeometry(0.13, 16, 12);
    const eyeMat = new THREE.MeshStandardMaterial({ color: '#ffffff', roughness: 0.3 });
    const makePin = (color) => {
      const g = new THREE.Group();
      const body = new THREE.Mesh(pinGeo, new THREE.MeshStandardMaterial({ color, roughness: 0.35 }));
      body.castShadow = true;
      g.add(body);
      const eye = new THREE.Mesh(eyeGeo, eyeMat);
      eye.position.y = 0.8;
      eye.scale.set(1, 1, 0.6);
      eye.position.z = 0.24;
      g.add(eye);
      const eye2 = eye.clone();
      eye2.position.z = -0.24;
      g.add(eye2);
      g.scale.setScalar(0.95);
      return g;
    };
    this.selPin = makePin(P.blue);
    this.selPin.visible = false;
    this.scene.add(this.selPin);
    this.alarmPins = new Map();
    this.makePin = makePin;

    const fpMat = (fill, edge) => ({
      fill: new THREE.MeshBasicMaterial({ color: fill, transparent: true, opacity: 0.16, depthWrite: false }),
      edge: new THREE.MeshBasicMaterial({ color: edge, transparent: true, opacity: 0.85, depthWrite: false }),
    });
    this.fpSel = this._footprint(fpMat(P.blue, P.blue));
    this.fpHover = this._footprint(fpMat('#7ea0ff', '#7ea0ff'));
    this.fpHover.mats.fill.opacity = 0.1;
    this.fpHover.mats.edge.opacity = 0.5;
  }

  _footprint(mats) {
    const g = new THREE.Group();
    g.renderOrder = 2;
    g.visible = false;
    return { group: g, mats, id: null };
  }

  _localBox(root) {
    const pos = root.position.clone();
    const rot = root.rotation.y;
    const vis = root.visible;
    root.position.set(0, 0, 0);
    root.rotation.y = 0;
    root.visible = true;
    root.updateMatrixWorld(true);
    const box = new THREE.Box3();
    root.traverse((o) => {
      if ((o.isMesh || o.isInstancedMesh) && o.visible !== false && !o.userData.footprint) box.expandByObject(o);
    });
    root.position.copy(pos);
    root.rotation.y = rot;
    root.visible = vis;
    root.updateMatrixWorld(true);
    return box;
  }

  _placeFootprint(fp, id) {
    if (fp.id === id) return;
    fp.group.parent?.remove(fp.group);
    fp.group.clear();
    fp.id = id;
    if (!id) { fp.group.visible = false; return; }
    const { model } = this.models.get(id);
    const box = this._localBox(model.root);
    const pad = 0.5;
    const w = box.max.x - box.min.x + pad * 2;
    const d = box.max.z - box.min.z + pad * 2;
    const r = Math.min(0.9, w / 4, d / 4);
    const rr = (s, x0, y0, ww, hh, rad) => {
      s.moveTo(x0 + rad, y0);
      s.lineTo(x0 + ww - rad, y0); s.quadraticCurveTo(x0 + ww, y0, x0 + ww, y0 + rad);
      s.lineTo(x0 + ww, y0 + hh - rad); s.quadraticCurveTo(x0 + ww, y0 + hh, x0 + ww - rad, y0 + hh);
      s.lineTo(x0 + rad, y0 + hh); s.quadraticCurveTo(x0, y0 + hh, x0, y0 + hh - rad);
      s.lineTo(x0, y0 + rad); s.quadraticCurveTo(x0, y0, x0 + rad, y0);
    };
    const outer = new THREE.Shape();
    rr(outer, -w / 2, -d / 2, w, d, r);
    const fill = new THREE.Mesh(new THREE.ShapeGeometry(outer, 6), fp.mats.fill);
    const ring = new THREE.Shape();
    rr(ring, -w / 2, -d / 2, w, d, r);
    const hole = new THREE.Path();
    const t = 0.12;
    rr(hole, -w / 2 + t, -d / 2 + t, w - 2 * t, d - 2 * t, Math.max(0.05, r - t));
    ring.holes.push(hole);
    const edge = new THREE.Mesh(new THREE.ShapeGeometry(ring, 6), fp.mats.edge);
    for (const m of [fill, edge]) {
      m.rotation.x = -Math.PI / 2;
      m.userData.footprint = true;
      m.raycast = () => {};
      fp.group.add(m);
    }
    fp.group.position.set((box.min.x + box.max.x) / 2, 0.03, (box.min.z + box.max.z) / 2);
    model.root.add(fp.group);
    fp.group.visible = true;
  }

  pick(ndc) {
    this.raycaster.setFromCamera(ndc, this.stage.camera);
    const hits = this.raycaster.intersectObjects(this.pickables, false);
    for (const h of hits) {
      let o = h.object;
      let visible = true;
      while (o) { if (!o.visible) { visible = false; break; } o = o.parent; }
      if (visible) return h.object.userData.assetId;
    }
    return null;
  }

  setSelected(id) {
    this.selected = id;
    this._placeFootprint(this.fpSel, id);
    this.selPin.visible = !!id;
  }

  setHovered(id) {
    if (id === this.hovered) return;
    this.hovered = id;
    this._placeFootprint(this.fpHover, id && id !== this.selected ? id : null);
  }

  // World-space centre and rough size of an asset, for camera framing.
  frameOf(id) {
    const m = this.models.get(id);
    if (!m) return null;
    const box = new THREE.Box3().setFromObject(m.model.root);
    const size = box.getSize(new THREE.Vector3());
    const c = box.getCenter(new THREE.Vector3());
    c.y = Math.min(c.y, 1.2);
    return { center: c, size: Math.max(size.x, size.z) };
  }

  worldPos(id) {
    const m = this.models.get(id);
    if (!m) return null;
    const v = new THREE.Vector3();
    m.model.root.getWorldPosition(v);
    if (m.model.labelAt) v.add(m.model.labelAt);
    return v;
  }

  update(dt, t) {
    const sim = this.sim;
    this._frame++;
    const col = new THREE.Color();
    for (const [id, m] of this.models) {
      const a = m.asset;
      m.model.update(a, dt, t, sim);
      this._tint(m, t, col);

      // Label: always for moving vehicles and abnormal states, otherwise on hover/selection/toggle
      const alert = ALERT_STATES.has(a.state);
      const vehicle = a.type === 'agv' || (a.type === 'truck' && a.state !== 'away');
      m.label.visible = this.showAllLabels || alert || vehicle || id === this.selected || id === this.hovered;
      if (a.type === 'truck' && a.state === 'away') m.label.visible = false;
      if (m.lastState !== a.state) {
        m.lastState = a.state;
        m.el.dataset.state = a.state;
      }
      const text = a.type === 'agv' || a.type === 'truck' ? (a.task || STATE_LABEL[a.state]) : STATE_LABEL[a.state] ?? '';
      if (text !== m.lastText) {
        m.lastText = text;
        m.el.lastChild.textContent = text;
      }
      m.el.classList.toggle('is-sel', id === this.selected);

      // Alarm pins on faulted assets
      const pinned = a.state === 'fault';
      let pin = this.alarmPins.get(id);
      if (pinned && !pin) {
        pin = this.makePin(P.red);
        this.scene.add(pin);
        this.alarmPins.set(id, pin);
      } else if (!pinned && pin) {
        this.scene.remove(pin);
        this.alarmPins.delete(id);
        pin = null;
      }
      if (pin) this._placePin(pin, m, t, 0.5);
    }

    if (this.selected) {
      const m = this.models.get(this.selected);
      this.selPin.visible = !this.alarmPins.has(this.selected) && m.model.root.visible;
      this._placePin(this.selPin, m, t, 0);
    }
    this._syncProducts();
    this._drainEvents();
    this._animFlying(dt);
  }

  _placePin(pin, m, t, phase) {
    const root = m.model.root;
    const v = pin.position;
    root.getWorldPosition(v);
    if (m.model.labelAt) v.add(m.model.labelAt);
    v.y = m.model.top + 0.9 + Math.sin(t * 2.4 + phase) * 0.18;
    pin.rotation.y = t * 1.2;
  }

  _tint(m, t, col) {
    const a = m.asset;
    for (const mat of m.model.tint) {
      const base = mat.userData.base;
      mat.emissive.setRGB(0, 0, 0);
      if (this.mode === 'heat' && a.spec?.trip) {
        ramp((a.temp - 22) / (a.spec.trip - 22), mat.color);
      } else if (this.mode === 'util' && a.spec?.runRise !== undefined) {
        ramp(a.util, mat.color);
      } else if (this.mode !== 'live') {
        mat.color.copy(base).lerp(col.set('#e6e9f3'), 0.6);
      } else {
        mat.color.copy(base);
        if (a.state === 'fault') {
          const k = 0.5 + 0.5 * Math.sin(t * 6);
          mat.color.lerp(col.set('#ff9a9a'), 0.55 * k);
          mat.emissive.set('#ef4444').multiplyScalar(0.25 * k);
        } else if (a.state === 'stopped') {
          mat.color.lerp(col.set('#9aa3bd'), 0.35);
        } else if (a.state === 'maint') {
          mat.color.lerp(col.set('#ffd98a'), 0.35);
        }
      }
    }
  }

  _syncProducts() {
    const stamp = this._frame;
    for (const line of this.sim.lines) {
      for (const p of line.products) {
        let mesh = this.products.get(p.id);
        if (!mesh) {
          mesh = new THREE.Mesh(productGeo[p.kind], productMat[p.kind]);
          mesh.castShadow = true;
          mesh.userData.kind = p.kind;
          this.scene.add(mesh);
          this.products.set(p.id, mesh);
        }
        if (mesh.userData.kind !== p.kind) {
          mesh.geometry = productGeo[p.kind];
          mesh.material = productMat[p.kind];
          mesh.userData.kind = p.kind;
        }
        const h = mesh.geometry.userData.h ?? 0.25;
        mesh.position.set(line.x0 + p.s, LINE_Y + h / 2, line.z);
        mesh.userData.stamp = stamp;
      }
    }
    for (const [id, mesh] of this.products) {
      if (mesh.userData.stamp !== stamp) {
        this.scene.remove(mesh);
        this.products.delete(id);
      }
    }
  }

  _drainEvents() {
    const ev = this.sim.events;
    while (ev.length) {
      const e = ev.shift();
      if (e.type === 'reject') {
        const mesh = new THREE.Mesh(productGeo[e.kind] ?? productGeo.part, new THREE.MeshStandardMaterial({ color: '#ff6b6b', roughness: 0.4 }));
        mesh.castShadow = true;
        const side = Math.sign(-e.z);
        const from = new THREE.Vector3(e.x, LINE_Y + 0.15, e.z);
        const to = new THREE.Vector3(e.x - 0.9 * side, 0.5, e.z + side * 1.0);
        mesh.position.copy(from);
        this.scene.add(mesh);
        this.flying.push({ mesh, from, to, t: 0 });
      }
      this.onEvent?.(e);
    }
  }

  _animFlying(dt) {
    for (let i = this.flying.length - 1; i >= 0; i--) {
      const f = this.flying[i];
      f.t += dt / 0.8;
      const k = Math.min(1, f.t);
      f.mesh.position.lerpVectors(f.from, f.to, k);
      f.mesh.position.y += Math.sin(k * Math.PI) * 0.8;
      f.mesh.rotation.z = k * 4;
      if (f.t >= 1.4) {
        this.scene.remove(f.mesh);
        f.mesh.material.dispose();
        this.flying.splice(i, 1);
      }
    }
  }
}
