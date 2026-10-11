// Where each plant of a company stands when they are all shown together: rows of plants that
// share a front road, with a grass strip and its own side roads between neighbours. The number
// of columns follows the screen, so a phone stacks the plants and a monitor lines them up.
import type { PlantLayout } from './layout';

export interface CampusSlot { layout: PlantLayout; ox: number; oz: number }
export interface CampusRow { front: number; x0: number; x1: number; back: number }
export interface Campus {
  slots: CampusSlot[];
  rows: CampusRow[];
  bounds: { x0: number; x1: number; z0: number; z1: number };
}

/** grass between one plant's right road and the next one's left road, edge to edge */
const GAP = 14;
const ROAD = 8;
/** behind the back wall: grass strip and trees */
const BACK = 10;
/** between a row's back and the front road of the row behind it (its front grass and trees) */
const ROW_GAP = 16;

const width = (L: PlantLayout) => L.roads.right - L.roads.left + ROAD + GAP;

export function campusLayout(plants: PlantLayout[], aspect: number): Campus {
  const n = plants.length;
  // Landscape screens line the plants up (up to four in a row); portrait ones stack them.
  const cols = aspect >= 1 ? Math.ceil(n / Math.ceil(n / 4)) : n <= 3 ? 1 : 2;

  const slots: CampusSlot[] = [];
  const rows: CampusRow[] = [];
  let front = 0;
  for (let i = 0; i < n; i += cols) {
    const row = plants.slice(i, i + cols);
    const total = row.reduce((s, L) => s + width(L), 0) - GAP;
    let x = -total / 2;
    let back = Infinity;
    for (const L of row) {
      const ox = x + ROAD / 2 - L.roads.left;
      const oz = front - L.roads.front;
      slots.push({ layout: L, ox, oz });
      back = Math.min(back, oz + L.box.z0 - BACK);
      x += width(L);
    }
    rows.push({ front, x0: -total / 2, x1: total / 2, back });
    front = back - ROW_GAP;
  }
  const x0 = Math.min(...rows.map((r) => r.x0)) - 16;
  const x1 = Math.max(...rows.map((r) => r.x1)) + 16;
  // Centre the site on the origin, where the sun and its shadow camera look.
  const z0 = rows.at(-1)!.back - 4;
  const z1 = rows[0].front + 14;
  const cz = (z0 + z1) / 2;
  for (const s of slots) s.oz -= cz;
  for (const r of rows) { r.front -= cz; r.back -= cz; }
  return { slots, rows, bounds: { x0, x1, z0: z0 - cz, z1: z1 - cz } };
}
