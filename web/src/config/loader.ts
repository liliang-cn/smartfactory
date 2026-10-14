// Finds every config file by convention, validates it, and resolves a company into plant layouts.
import { z } from 'zod';
import { layoutPlant, type PlantLayout } from '@/core/layout';
import { companyOntologyIssues } from './ontology';
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

export const industriesOf = (c: CompanyCfg) => [...new Set(c.plants.map((p) => p.industry ?? c.industry))];

/** Validates an industry pack from anywhere (a file, the server, an MCP client). */
export const validateIndustry = (raw: unknown, source = '行业包') => parse(Industry, raw, source);

/** Validates a company config from anywhere. */
export const validateCompany = (raw: unknown, source = '公司配置') => parse(Company, raw, source);

/** Lays out every plant of a company; a ConfigError names the plant that does not fit. */
export function layoutCompany(company: CompanyCfg, industries: Map<string, IndustryCfg>, source = '公司配置'): PlantLayout[] {
  const missing = industriesOf(company).filter((id) => !industries.has(id));
  if (missing.length) throw new ConfigError(source, [`找不到行业包 ${missing.join('、')}，先创建行业包`]);
  const ontology = companyOntologyIssues(company, industriesOf(company).map((id) => industries.get(id)!));
  if (ontology.length) throw new ConfigError(source, ontology);
  return company.plants.map((p, i) => {
    try {
      return layoutPlant(company, p, industries.get(p.industry ?? company.industry)!, i);
    } catch (e) {
      throw new ConfigError(source, [`工厂 ${p.name}：${(e as Error).message}`]);
    }
  });
}

function bundle(picked: CompanyEntry, all: CompanyEntry[], industries: Map<string, IndustryCfg>): CompanyBundle {
  const company = picked.cfg!;
  const plants = layoutCompany(company, industries, picked.path);
  return { company, plants, directory: all.map((c) => ({ id: c.id, name: c.cfg ? c.name : `${c.name}（配置有误）`, short: c.short })) };
}

/** The companies the server this page came from runs; null when there is no such server (vite alone). */
async function site(): Promise<string[] | null> {
  try {
    const r = await fetch('/api/site');
    return r.ok ? ((await r.json()) as { companies: string[] }).companies : null;
  } catch {
    return null;
  }
}

export async function loadCompany(wanted: string | null): Promise<CompanyBundle> {
  // a customer's own instance runs its company only: that is the page, whatever the link says
  const runs = await site();
  if (runs?.length && (!wanted || !runs.includes(wanted))) wanted = runs[0];
  // A broken file only blocks its own company; the others stay loadable from the switcher.
  const all = parseCompanies(await Promise.all(Object.entries(companyFiles).map(async ([path, load]) => [path, (await load()).default] as [string, unknown])));
  if (!all.length) throw new ConfigError('src/config/companies', ['没有任何公司配置，至少放一个 JSON 文件']);
  const directory = all.map((c) => ({ id: c.id, name: c.cfg ? c.name : `${c.name}（配置有误）`, short: c.short }));

  // A company created on the server: its config (and any industry packs it brings) come from there.
  if (wanted && !all.some((c) => c.id === wanted)) {
    const r = await fetch(`/api/config/company/${encodeURIComponent(wanted)}`).catch(() => null);
    if (r?.ok) {
      const d: { company: unknown; industries: Record<string, unknown> } = await r.json();
      const company = validateCompany(d.company, `服务器上的公司 ${wanted}`);
      const industries = new Map<string, IndustryCfg>();
      for (const [id, raw] of Object.entries(d.industries)) industries.set(id, validateIndustry(raw, `行业包 ${id}`));
      for (const id of industriesOf(company)) if (!industries.has(id)) industries.set(id, await loadIndustry(id));
      return { company, plants: layoutCompany(company, industries, `服务器上的公司 ${wanted}`), directory };
    }
  }

  const picked = all.find((c) => c.id === wanted) ?? all.find((c) => c.cfg) ?? all[0];
  if (!picked.cfg) parse(Company, (await companyFiles[picked.path]()).default, picked.path);
  const industries = new Map<string, IndustryCfg>();
  for (const id of industriesOf(picked.cfg!)) industries.set(id, await loadIndustry(id));
  return { ...bundle(picked, all, industries), directory };
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

/** Whether an industry pack ships with the app. */
export const builtinIndustry = (id: string) => `./industries/${id}.json` in industryFiles;

/** Every company that ships with the app, validated (broken files left out). */
export async function builtinCompanies(): Promise<CompanyCfg[]> {
  const raws = await Promise.all(Object.values(companyFiles).map(async (load) => (await load()).default));
  return raws.flatMap((raw) => {
    const r = Company.safeParse(raw);
    return r.success ? [r.data] : [];
  });
}

/**
 * A company's config and the industry packs it uses, from the app's files or (created at run
 * time) the server. extra resolves packs that are neither (created on the server, used elsewhere).
 */
export async function companyConfig(id: string, extra?: (id: string) => Promise<unknown>): Promise<{ company: CompanyCfg; industries: Map<string, IndustryCfg> }> {
  const industries = new Map<string, IndustryCfg>();
  let company: CompanyCfg;
  const file = companyFiles[`./companies/${id}.json`];
  if (file) company = validateCompany((await file()).default, `src/config/companies/${id}.json`);
  else {
    const r = await fetch(`/api/config/company/${encodeURIComponent(id)}`);
    if (!r.ok) throw new ConfigError(`公司 ${id}`, ['服务器上没有这家公司的配置']);
    const d: { company: unknown; industries: Record<string, unknown> } = await r.json();
    company = validateCompany(d.company, `服务器上的公司 ${id}`);
    for (const [k, raw] of Object.entries(d.industries)) industries.set(k, validateIndustry(raw, `行业包 ${k}`));
  }
  for (const k of industriesOf(company)) if (!industries.has(k)) industries.set(k, await industryConfig(k, extra));
  return { company, industries };
}

/** An industry pack: the app's file, or extra (the server) for one created at run time. */
export async function industryConfig(id: string, extra?: (id: string) => Promise<unknown>): Promise<IndustryCfg> {
  if (builtinIndustry(id) || !extra) return loadIndustry(id);
  return validateIndustry(await extra(id), `行业包 ${id}`);
}
