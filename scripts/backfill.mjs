// Simulates a company's past days offline with the server's engine bundle and prints what
// `smartfactory history load` stores: the dims, then one JSON line per simulated hour.
// V8 runs the bundle about 20× faster than the server's embedded engine, which is why this
// is a script: 30 days of a three-plant company take minutes instead of an hour.
//
//   node scripts/backfill.mjs huaduan 30 [warmup days] [end ms] | smartfactory history load -config cfg.json -company huaduan -reset
//
// End defaults to the start of this hour; pass the hour live recording started, so the two never
// add up the same hour.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const [company, days = '30', warmup = '3', until] = process.argv.slice(2);
if (!company) {
  console.error('usage: node scripts/backfill.mjs <company> [days] [warmup days] [end ms]');
  process.exit(2);
}
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
globalThis.host = { log: (m) => console.error(m) };
(0, eval)(fs.readFileSync(path.join(root, 'server/internal/engine/js/engine.js'), 'utf8'));
const SF = globalThis.SF;
SF.init();

const out = (o) => process.stdout.write(JSON.stringify(o) + '\n');
const HOUR = 3600_000;
const end = Math.floor((until ? Number(until) : Date.now()) / HOUR) * HOUR;
const start = end - Number(days) * 24 * HOUR;
out({ dims: JSON.parse(SF.historyDims(company)), from: start, to: end });
// the warm-up runs in the days before the start, so what it leaves (open orders, lots) is dated right
SF.setClock(company, start - Number(warmup) * 86_400_000);
if (!SF.advance(company, Number(warmup) * 86400, 1)) throw new Error(`no company ${company}`);
SF.historyOn(company, start);
for (let h = 0; h < Number(days) * 24; h++) {
  SF.advance(company, 3600, 1);
  out({ at: start + (h + 1) * HOUR, history: JSON.parse(SF.history(company)) });
  if ((h + 1) % 24 === 0) console.error(`day ${(h + 1) / 24} of ${days}`);
}
