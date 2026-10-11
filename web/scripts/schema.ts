// Writes JSON Schemas for the config files so editors can complete and check them, and the
// protocol standard (src/protocol/sfp.ts) as ../protocol/sfp.schema.json.
// Run: npm run schema
import { writeFileSync } from 'node:fs';
import { z } from 'zod';
import { Company, Industry } from '../src/config/schema.ts';
import { Acceptance, DEFS, Filter, Finding, Guard, Measurement, PlanView, PlantState, PROTOCOL, Source } from '../src/protocol/sfp.ts';

const write = (path: string, json: unknown) => {
  writeFileSync(new URL(`../${path}`, import.meta.url), `${JSON.stringify(json, null, 2)}\n`);
  console.log(path);
};

const out = (name: string, schema: z.ZodType) => write(`src/config/schema/${name}.json`, z.toJSONSchema(schema, { io: 'input', unrepresentable: 'any' }));
out('industry', Industry);
out('company', Company);

// Every definition (and the parts several of them share) under $defs, referring to each other.
const reg = z.registry<{ id: string }>();
for (const [id, s] of Object.entries({ ...DEFS, PlantState, Source, Filter, Guard, Measurement, Finding, Acceptance, PlanView })) reg.add(s as z.ZodType, { id });
const { schemas } = z.toJSONSchema(reg, { io: 'output', uri: (id) => `#/$defs/${id}`, unrepresentable: 'any' });
const defs = Object.fromEntries(Object.entries(schemas).map(([id, s]) => {
  const { $schema: _, $id: __, ...rest } = s as Record<string, unknown>;
  return [id, rest];
}));
write('../protocol/sfp.schema.json', {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: `https://factory.superleo.cn/protocol/sfp-${PROTOCOL}.schema.json`,
  title: `SmartFactory protocol ${PROTOCOL}`,
  $defs: defs,
});
