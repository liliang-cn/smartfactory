// Shapes drawn into a built-in industry pack. The pack itself stays as shipped; what was drawn
// is kept beside it (the server's data directory) as an overlay and laid on top whenever the pack
// is loaded, so deleting the overlay brings the built-in look back.
//
//   { id: 'forging',
//     machines: { press: { look: { archetype: 'frame', parts: [...] } } },     a machine kind's whole look
//     products: { conrod: { stages: { forged: { parts: [...] } } } } }          a product stage's parts

export interface ShapeOverlay {
  id: string;
  machines?: Record<string, { look: Record<string, unknown> }>;
  products?: Record<string, { stages: Record<string, { parts: unknown[] }> }>;
}

/** Whether an overlay holds nothing (all of it was reset). */
export const emptyOverlay = (o: ShapeOverlay | null | undefined) =>
  !o || (!Object.keys(o.machines ?? {}).length && !Object.keys(o.products ?? {}).length);

/** A pack (raw JSON) with the overlay laid on it. Kinds or stages the pack does not have are ignored. */
export function applyShapeOverlay(raw: unknown, overlay: ShapeOverlay | null | undefined): unknown {
  if (emptyOverlay(overlay) || typeof raw !== 'object' || !raw) return raw;
  const pack = JSON.parse(JSON.stringify(raw)) as {
    machines?: Record<string, Record<string, unknown>>;
    products?: Record<string, { stages?: Record<string, Record<string, unknown>> }>;
  };
  for (const [k, m] of Object.entries(overlay!.machines ?? {})) {
    if (pack.machines?.[k] && m?.look) pack.machines[k].look = m.look;
  }
  for (const [k, p] of Object.entries(overlay!.products ?? {})) {
    for (const [s, st] of Object.entries(p?.stages ?? {})) {
      const stage = pack.products?.[k]?.stages?.[s];
      if (stage && st?.parts) stage.parts = st.parts;
    }
  }
  return pack;
}
