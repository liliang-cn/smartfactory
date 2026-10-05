// One asset in the scene: its procedural model, picking, overlay tint, pins, footprint.
// Its name tag is plain DOM in <Labels>, positioned by <LabelProjector>.
import { memo, useEffect, useMemo } from 'react';
import { useFrame, type ThreeEvent } from '@react-three/fiber';
import type { Asset } from '@/sim/types';
import { models, useTick, useUI, type PlantRuntime } from '@/state/store';
import { applyTint, buildModel, initTint, localBox } from '@/three/factory';
import { P } from '@/three/kit';
import { Footprint, Pin } from './Markers';

let lastTap = { id: '', t: 0 };

export const AssetNode = memo(function AssetNode({ asset, plant }: { asset: Asset; plant: PlantRuntime }) {
  const model = useMemo(() => {
    const m = buildModel(asset, plant.layout);
    initTint(m);
    return m;
  }, [asset, plant]);
  const box = useMemo(() => localBox(model.root), [model]);
  useEffect(() => {
    models.set(asset.id, model);
    return () => { models.delete(asset.id); };
  }, [asset.id, model]);

  useTick();
  const selected = useUI((s) => s.selected === asset.id);
  const hovered = useUI((s) => s.hovered === asset.id);

  useFrame(({ clock }, dt) => {
    model.update(asset, dt, clock.elapsedTime, plant.sim);
    applyTint(model, asset, useUI.getState().mode, clock.elapsedTime);
  });

  const onClick = (e: ThreeEvent<MouseEvent>) => {
    e.stopPropagation();
    if (e.delta > 6) return;
    const now = performance.now();
    const again = lastTap.id === asset.id && now - lastTap.t < 380;
    lastTap = { id: asset.id, t: now };
    useUI.getState().select(asset.id, again);
  };
  const onDoubleClick = (e: ThreeEvent<MouseEvent>) => {
    e.stopPropagation();
    useUI.getState().select(asset.id, true);
  };
  const onOver = (e: ThreeEvent<PointerEvent>) => {
    e.stopPropagation();
    if (e.pointerType === 'touch') return;
    document.body.style.cursor = 'pointer';
    useUI.getState().hover(asset.id, e.nativeEvent.clientX, e.nativeEvent.clientY);
  };
  const onMove = (e: ThreeEvent<PointerEvent>) => {
    if (e.pointerType === 'touch' || e.buttons) return;
    e.stopPropagation();
    useUI.getState().hover(asset.id, e.nativeEvent.clientX, e.nativeEvent.clientY);
  };
  const onOut = () => {
    document.body.style.cursor = '';
    if (useUI.getState().hovered === asset.id) useUI.getState().hover(null);
  };

  const at = model.labelAt ?? { x: 0, z: 0 };

  return (
    <primitive object={model.root} onClick={onClick} onDoubleClick={onDoubleClick} onPointerOver={onOver} onPointerMove={onMove} onPointerOut={onOut}>
      {selected && <Footprint box={box} color={P.blue} />}
      {hovered && !selected && <Footprint box={box} color="#7ea0ff" strength={0.6} />}
      {asset.state === 'fault' && <Pin color={P.red} x={at.x} y={model.top + 0.9} z={at.z} phase={0.5} />}
      {selected && asset.state !== 'fault' && <Pin color={P.blue} x={at.x} y={model.top + 0.9} z={at.z} />}
    </primitive>
  );
});
