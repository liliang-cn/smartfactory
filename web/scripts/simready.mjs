// Fetches the NVIDIA SimReady models the twin draws (web-ready copies from the
// codepawl/simready-glb dataset, CC BY 4.0), cuts them down to a triangle budget and
// writes them to public/simready/. Run it again after changing the list:
//   node scripts/simready.mjs [cacheDir]
import fs from 'node:fs';
import path from 'node:path';
import { Logger, NodeIO } from '@gltf-transform/core';
import { ALL_EXTENSIONS, EXTMeshoptCompression } from '@gltf-transform/extensions';
import { dedup, flatten, join, meshopt, prune, resample, simplify, textureCompress, weld } from '@gltf-transform/functions';
import { MeshoptDecoder, MeshoptEncoder, MeshoptSimplifier } from 'meshoptimizer';
import sharp from 'sharp';

const SRC = 'https://huggingface.co/datasets/codepawl/simready-glb/resolve/main/glb';

// name in the twin -> [SimReady asset, triangle budget, keep the part tree (for joints)]
export const MODELS = {
  arm: ['robotic_system_arm_b01', 60000, true],
  amr: ['robotics_small_bot_a01', 24000],
  forklift: ['vehicle_forklift_blue_a01', 36000],
  roller: ['conveyor_belt_a05', 5000],
  rack: ['rack_large_empty_a01', 5000],
  pallet: ['pallet_a01', 2100],
  loaded: ['pallet_assembly_brown_e04', 2000],
  crate: ['crate_blue_a01', 3000],
  bench: ['furniture_workbench_standard_e01', 24000],
  bins: ['equipment_multi_tier_bin_cart_c01', 16000],
  tools: ['equipment_rolling_tool_cabinet_a01', 10000],
  ibc: ['ibcTank_steel_stainless_a01', 12000],
  booth: ['equipment_sprayBooth_painting_a01', 60000],
  guard: ['machineGuard_a01', 3000],
  extinguisher: ['fireProtection_extinguisher_portable_a01', 3000],
  column: ['structure_wall_column_a01', 9700],
  compressor: ['equipment_airCompressor_a01', 20000],
  packtable: ['table_packing_heavyduty_c01', 10000],
  drum: ['drum_steel_a01', 4000],
  fan: ['climate_shopFan_floorAxial_a01', 2500],
  rail: ['rail_guard_a09', 1200],
  firestand: ['fireExtinguisher_stand_a01', 1500],
};

const here = path.dirname(new URL(import.meta.url).pathname);
const out = path.join(here, '../public/simready');
const cache = process.argv[2] ?? path.join(here, '../.simready-cache');
fs.mkdirSync(out, { recursive: true });
fs.mkdirSync(cache, { recursive: true });

await MeshoptDecoder.ready;
await MeshoptEncoder.ready;
await MeshoptSimplifier.ready;
const io = new NodeIO()
  .setLogger(new Logger(Logger.Verbosity.ERROR))
  .registerExtensions(ALL_EXTENSIONS)
  .registerDependencies({ 'meshopt.decoder': MeshoptDecoder, 'meshopt.encoder': MeshoptEncoder });

const tris = (doc) => {
  let n = 0;
  for (const m of doc.getRoot().listMeshes()) for (const p of m.listPrimitives()) {
    const ix = p.getIndices();
    n += (ix ? ix.getCount() : p.getAttribute('POSITION').getCount()) / 3;
  }
  return n;
};

const credits = [];
for (const [name, [asset, budget, tree]] of Object.entries(MODELS)) {
  const raw = path.join(cache, `${asset}.glb`);
  if (!fs.existsSync(raw)) {
    const res = await fetch(`${SRC}/${asset}.glb`);
    if (!res.ok) throw new Error(`${asset}: HTTP ${res.status}`);
    fs.writeFileSync(raw, Buffer.from(await res.arrayBuffer()));
  }
  const doc = await io.read(raw);
  const before = tris(doc);
  await doc.transform(dedup(), weld());
  if (before > budget) {
    // meshopt simplification stops at its error bound, so step the ratio down until it fits
    for (let ratio = budget / before, i = 0; i < 4 && tris(doc) > budget * 1.15; i++, ratio *= 0.7) {
      await doc.transform(simplify({ simplifier: MeshoptSimplifier, ratio, error: 0.004 * (i + 1), lockBorder: false }));
    }
  }
  if (!tree) await doc.transform(flatten(), join({ keepNamed: false }));
  await doc.transform(
    resample(),
    prune(),
    textureCompress({ encoder: sharp, targetFormat: 'webp', resize: [512, 512], quality: 80 }),
    meshopt({ encoder: MeshoptEncoder, level: 'medium' }),
  );
  doc.createExtension(EXTMeshoptCompression).setRequired(true);
  const file = path.join(out, `${name}.glb`);
  await io.write(file, doc);
  const kb = Math.round(fs.statSync(file).size / 1024);
  console.log(`${name.padEnd(13)} ${asset.padEnd(40)} ${String(before).padStart(8)} -> ${String(tris(doc)).padStart(6)} tris  ${kb} KB`);
  credits.push(`${name}.glb: ${asset}`);
}
fs.writeFileSync(path.join(out, 'CREDITS.txt'), [
  '3D models by NVIDIA SimReady (https://huggingface.co/datasets/nvidia/simready-assets),',
  'licensed CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/).',
  'Web copies from https://huggingface.co/datasets/codepawl/simready-glb, then reduced',
  '(fewer triangles, 512 px textures) by scripts/simready.mjs.',
  '',
  ...credits,
  '',
].join('\n'));
