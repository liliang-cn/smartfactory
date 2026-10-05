// Writes JSON Schemas for the config files so editors can complete and check them.
// Run: npm run schema
import { writeFileSync } from 'node:fs';
import { z } from 'zod';
import { Company, Industry } from '../src/config/schema.ts';

const out = (name: string, schema: z.ZodType) => {
  const json = z.toJSONSchema(schema, { io: 'input', unrepresentable: 'any' });
  writeFileSync(new URL(`../src/config/schema/${name}.json`, import.meta.url), `${JSON.stringify(json, null, 2)}\n`);
  console.log(`src/config/schema/${name}.json`);
};
out('industry', Industry);
out('company', Company);
