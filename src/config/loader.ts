// Finds every config file by convention, validates it, and resolves a company into plant layouts.
import { z } from 'zod';
import { layoutPlant, type PlantLayout } from '@/core/layout';
import { Company, Industry, type CompanyCfg, type IndustryCfg } from './schema';

z.config(z.locales.zhCN());

const industryFiles = import.meta.glob<{ default: unknown }>('./industries/*.json');
const companyFiles = import.meta.glob<{ default: unknown }>('./companies/*.json');

/** Raised when a config file does not match the schema; carries readable issue lines. */
export class ConfigError extends Error {
  constructor(public file: string, public issues: string[]) {
    super(`${file}\n${issues.join('\n')}`);
  }
}

const fileId = (path: string) => path.replace(/^.*\/(.+)\.json$/, '$1');

function parse<T>(schema: z.ZodType<T>, data: unknown, file: string): T {
  const r = schema.safeParse(data);
  if (r.success) return r.data;
  throw new ConfigError(file, r.error.issues.map((i) => `${i.path.join('.') || '(根)'}：${i.message}`));
}

export const companyIds = Object.keys(companyFiles).map(fileId);

async function loadIndustry(id: string): Promise<IndustryCfg> {
  const path = `./industries/${id}.json`;
  const load = industryFiles[path];
  if (!load) throw new ConfigError(path, [`找不到行业包 ${id}（应放在 src/config/industries/${id}.json）`]);
  return parse(Industry, (await load()).default, path);
}

export interface CompanyBundle {
  company: CompanyCfg;
  plants: PlantLayout[];
  /** every company's id and name, for the switcher */
  directory: { id: string; name: string; short: string }[];
}

interface CompanyEntry { path: string; cfg: CompanyCfg | null; id: string; name: string; short: string }

/** Validates every company file; broken ones stay listed (marked) so the switcher can show them. */
function parseCompanies(raws: [string, unknown][]): CompanyEntry[] {
  const all = raws.map(([path, raw]) => {
    const r = Company.safeParse(raw);
    const loose = (raw ?? {}) as Partial<CompanyCfg>;
    return { path, cfg: r.success ? r.data : null, id: loose.id ?? fileId(path), name: loose.name ?? fileId(path), short: (loose.short ?? '?').slice(0, 4) };
  });
  return all.sort((a, b) => a.id.localeCompare(b.id));
}

const industriesOf = (c: CompanyCfg) => [...new Set(c.plants.map((p) => p.industry ?? c.industry))];

function bundle(picked: CompanyEntry, all: CompanyEntry[], industries: Map<string, IndustryCfg>): CompanyBundle {
  const company = picked.cfg!;
  const plants = company.plants.map((p, i) => {
    try {
      return layoutPlant(company, p, industries.get(p.industry ?? company.industry)!, i);
    } catch (e) {
      throw new ConfigError(picked.path, [`工厂 ${p.name}：${(e as Error).message}`]);
    }
  });
  return { company, plants, directory: all.map((c) => ({ id: c.id, name: c.cfg ? c.name : `${c.name}（配置有误）`, short: c.short })) };
}

export async function loadCompany(wanted: string | null): Promise<CompanyBundle> {
  // A broken file only blocks its own company; the others stay loadable from the switcher.
  const all = parseCompanies(await Promise.all(Object.entries(companyFiles).map(async ([path, load]) => [path, (await load()).default] as [string, unknown])));
  if (!all.length) throw new ConfigError('src/config/companies', ['没有任何公司配置，至少放一个 JSON 文件']);
  const picked = all.find((c) => c.id === wanted) ?? all.find((c) => c.cfg) ?? all[0];
  if (!picked.cfg) parse(Company, (await companyFiles[picked.path]()).default, picked.path);
  const industries = new Map<string, IndustryCfg>();
  for (const id of industriesOf(picked.cfg!)) industries.set(id, await loadIndustry(id));
  return bundle(picked, all, industries);
}

/**
 * Every valid company at once, from files already in memory (the server bundles them eagerly).
 * Companies whose files do not validate are reported and skipped.
 */
export function loadAll(companyRaw: Record<string, unknown>, industryRaw: Record<string, unknown>, warn: (msg: string) => void): CompanyBundle[] {
  const all = parseCompanies(Object.entries(companyRaw));
  const industries = new Map<string, IndustryCfg>();
  for (const [path, raw] of Object.entries(industryRaw)) {
    try {
      industries.set(fileId(path), parse(Industry, raw, path));
    } catch (e) {
      warn((e as Error).message);
    }
  }
  const out: CompanyBundle[] = [];
  for (const c of all) {
    if (!c.cfg) {
      warn(`${c.path}: 配置有误，已跳过`);
      continue;
    }
    const missing = industriesOf(c.cfg).filter((id) => !industries.has(id));
    if (missing.length) {
      warn(`${c.path}: 找不到行业包 ${missing.join(', ')}，已跳过`);
      continue;
    }
    try {
      out.push(bundle(c, all, industries));
    } catch (e) {
      warn((e as Error).message);
    }
  }
  return out;
}
