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

export async function loadCompany(wanted: string | null): Promise<CompanyBundle> {
  // A broken file only blocks its own company; the others stay loadable from the switcher.
  const all = await Promise.all(Object.entries(companyFiles).map(async ([path, load]) => {
    const raw = (await load()).default;
    const r = Company.safeParse(raw);
    const loose = (raw ?? {}) as Partial<CompanyCfg>;
    return { path, cfg: r.success ? r.data : null, id: loose.id ?? fileId(path), name: loose.name ?? fileId(path), short: (loose.short ?? '?').slice(0, 4) };
  }));
  if (!all.length) throw new ConfigError('src/config/companies', ['没有任何公司配置，至少放一个 JSON 文件']);
  all.sort((a, b) => a.id.localeCompare(b.id));
  const picked = all.find((c) => c.id === wanted) ?? all.find((c) => c.cfg) ?? all[0];
  if (!picked.cfg) parse(Company, (await companyFiles[picked.path]()).default, picked.path);
  const company = picked.cfg!;

  const industries = new Map<string, IndustryCfg>();
  for (const id of new Set(company.plants.map((p) => p.industry ?? company.industry))) industries.set(id, await loadIndustry(id));

  const plants = company.plants.map((p, i) => {
    try {
      return layoutPlant(company, p, industries.get(p.industry ?? company.industry)!, i);
    } catch (e) {
      throw new ConfigError(picked.path, [`工厂 ${p.name}：${(e as Error).message}`]);
    }
  });
  return { company, plants, directory: all.map((c) => ({ id: c.id, name: c.cfg ? c.name : `${c.name}（配置有误）`, short: c.short })) };
}
