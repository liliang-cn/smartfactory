// The ontology an industry pack declares (and a company adds to): kinds of things (classes) and how
// they are related, rules that follow from relations, named things (individuals), which products
// and machines of the pack are of which class, and constraints. Checked here, merged across the
// packs a company uses, and handed to the server's knowledge base (engine.ts processModel), which
// loads it as RDF, infers from it and checks the constraints.
import type { CompanyCfg, IndustryCfg, OntologyCfg } from './schema';

/** Classes every ontology has: the kinds of things in the knowledge graph. */
export const BUILTIN_CLASSES: Record<string, string> = {
  公司: 'company', 工厂: 'plant', 产线: 'line', 工位: 'station', 设备: 'machine', 产品: 'product', 供应商: 'supplier', 客户: 'customer', 工单: 'order',
  点检: 'check', 读数异常: 'anomaly', 校准: 'calibration', AI指令: 'command', 约束告警: 'violation',
};

/** Relations every ontology has: the relations of the process graph, [domain, range]. */
export const BUILTIN_RELATIONS: Record<string, [string, string]> = {
  有工厂: ['公司', '工厂'], 有产线: ['工厂', '产线'], 有工位: ['产线', '工位'], 生产: ['产线', '产品'], 设备是: ['工位', '设备'],
  下游: ['工位', '工位'], 供应: ['供应商', '产品'], 卖给: ['产品', '客户'],
};

/** Keys of an individual that are not relations. */
const OWN_KEYS = new Set(['a', 'note']);

const list = (x: string | string[] | undefined): string[] => (x == null ? [] : Array.isArray(x) ? x : [x]);

/** A problem: where in the ontology (path under `ontology`) and what. */
export type Issue = [path: (string | number)[], message: string];

interface Scope {
  /** product and machine keys bind may name (all the packs' for a company) */
  products: string[];
  machines: string[];
  /** ontologies this one is added to (a company's packs) */
  base?: OntologyCfg[];
}

/** What is wrong with an ontology, in plain Chinese; [] when nothing is. */
export function checkOntology(o: OntologyCfg, scope: Scope): Issue[] {
  const all = [...(scope.base ?? []), o];
  const issues: Issue[] = [];
  const classes = new Set([...Object.keys(BUILTIN_CLASSES), ...all.flatMap((x) => Object.keys(x.classes))]);
  const relations = new Set([...Object.keys(BUILTIN_RELATIONS), ...all.flatMap((x) => Object.keys(x.relations))]);
  for (const x of all) for (const r of Object.values(x.relations)) if (r.inverse) relations.add(r.inverse);
  const individuals = new Set(all.flatMap((x) => Object.keys(x.individuals)));
  const cls = (path: Issue[0], name: string) => {
    if (!classes.has(name)) issues.push([path, `没有「${name}」这个类（在 classes 里声明，或用内置的 ${Object.keys(BUILTIN_CLASSES).slice(0, 9).join(' ')}）`]);
  };
  const rel = (path: Issue[0], name: string) => {
    if (!relations.has(name)) issues.push([path, `没有「${name}」这个关系（在 relations 里声明）`]);
  };

  for (const [name, c] of Object.entries(o.classes)) {
    list(c.sub).forEach((s, i) => (s === name ? issues.push([['classes', name, 'sub', i], `「${name}」不能是它自己的上级类`]) : cls(['classes', name, 'sub', i], s)));
  }
  // the classes and their superclasses, all packs together, must not go round in a circle
  const up = new Map<string, Set<string>>();
  for (const x of all) for (const [name, c] of Object.entries(x.classes)) for (const s of list(c.sub)) up.set(name, (up.get(name) ?? new Set()).add(s));
  const cycle = findCycle(up);
  if (cycle) issues.push([['classes'], `类的上下级绕成了圈：${cycle.join(' → ')}`]);

  for (const [name, r] of Object.entries(o.relations)) {
    if (OWN_KEYS.has(name)) issues.push([['relations', name], `「${name}」留给个体自己用，关系换个名字`]);
    if (r.domain) cls(['relations', name, 'domain'], r.domain);
    if (r.range) cls(['relations', name, 'range'], r.range);
    if (r.inverse === name && !r.symmetric) issues.push([['relations', name, 'inverse'], `「${name}」的反向不能是它自己（相互的关系用 symmetric）`]);
  }
  const ruleIds = new Set<string>();
  o.rules.forEach((r, i) => {
    if (ruleIds.has(r.id)) issues.push([['rules', i, 'id'], `规则 ${r.id} 重复了`]);
    ruleIds.add(r.id);
    r.if.forEach((p, j) => rel(['rules', i, 'if', j], p));
    rel(['rules', i, 'then'], r.then);
  });
  for (const [name, ind] of Object.entries(o.individuals)) {
    for (const [k, v] of Object.entries(ind)) {
      if (k === 'note') continue;
      if (k === 'a') list(v).forEach((c, i) => cls(['individuals', name, 'a', i], c));
      else rel(['individuals', name, k], k);
    }
  }
  for (const [k, c] of Object.entries(o.bind.products)) {
    if (!scope.products.includes(k)) issues.push([['bind', 'products', k], `没有产品 ${k}（可用的有 ${scope.products.join('、') || '无'}）`]);
    cls(['bind', 'products', k], c);
  }
  for (const [k, c] of Object.entries(o.bind.machines)) {
    if (!scope.machines.includes(k)) issues.push([['bind', 'machines', k], `没有设备 ${k}（可用的有 ${scope.machines.join('、') || '无'}）`]);
    cls(['bind', 'machines', k], c);
  }
  for (const [k, ind] of Object.entries(o.bind.individuals)) {
    if (!scope.products.includes(k)) issues.push([['bind', 'individuals', k], `没有产品 ${k}（可用的有 ${scope.products.join('、') || '无'}）`]);
    if (!individuals.has(ind)) issues.push([['bind', 'individuals', k], `没有个体「${ind}」（在 individuals 里写出来）`]);
  }
  const ids = new Set<string>();
  o.constraints.forEach((c, i) => {
    if (ids.has(c.id)) issues.push([['constraints', i, 'id'], `约束 ${c.id} 重复了`]);
    ids.add(c.id);
    cls(['constraints', i, 'class'], c.class);
    if (c.field) {
      if (c.min == null && c.max == null) issues.push([['constraints', i], `约束 ${c.id} 看读数 ${c.field}，要给 min 或 max`]);
      if (c.min != null && c.max != null && c.min > c.max) issues.push([['constraints', i], `约束 ${c.id} 的 min 大于 max`]);
      if (c.requires) issues.push([['constraints', i], `约束 ${c.id} 只能是一种：看读数（field）或要求关系（requires）`]);
    } else if (c.requires) {
      rel(['constraints', i, 'requires'], c.requires);
      if (c.valueClass) cls(['constraints', i, 'valueClass'], c.valueClass);
    } else {
      issues.push([['constraints', i], `约束 ${c.id} 要么看读数（field 加 min / max），要么要求关系（requires）`]);
    }
  });
  return issues;
}

function findCycle(up: Map<string, Set<string>>): string[] | null {
  const state = new Map<string, 1 | 2>();
  const path: string[] = [];
  const visit = (n: string): string[] | null => {
    if (state.get(n) === 2) return null;
    if (state.get(n) === 1) return [...path.slice(path.indexOf(n)), n];
    state.set(n, 1);
    path.push(n);
    for (const s of up.get(n) ?? []) {
      const c = visit(s);
      if (c) return c;
    }
    path.pop();
    state.set(n, 2);
    return null;
  };
  for (const n of up.keys()) {
    const c = visit(n);
    if (c) return c;
  }
  return null;
}

/** An industry pack's ontology problems, with paths from the pack's root. */
export const industryOntologyIssues = (ind: { ontology?: OntologyCfg; products: object; machines: object }): Issue[] =>
  ind.ontology ? checkOntology(ind.ontology, { products: Object.keys(ind.products), machines: Object.keys(ind.machines) }).map(([p, m]) => [['ontology', ...p], m]) : [];

/** A company's ontology problems on top of its packs' ("ontology.bind.products.x：…"). */
export function companyOntologyIssues(company: CompanyCfg, packs: IndustryCfg[]): string[] {
  const base = packs.flatMap((p) => (p.ontology ? [p.ontology] : []));
  const own = company.ontology;
  if (!own && base.length < 2) return [];
  const scope = { products: [...new Set(packs.flatMap((p) => Object.keys(p.products)))], machines: [...new Set(packs.flatMap((p) => Object.keys(p.machines)))] };
  // two packs on their own are checked already; together their classes may still go round
  const issues = own ? checkOntology(own, { ...scope, base }) : checkOntology(EMPTY, { ...scope, base });
  return issues.map(([p, m]) => `${['ontology', ...p].join('.')}：${m}`);
}

const EMPTY: OntologyCfg = { classes: {}, relations: {}, rules: [], individuals: {}, bind: { products: {}, machines: {}, individuals: {} }, constraints: [] };

/** The ontology as the server's knowledge base reads it: one list per part, every name spelled out. */
export interface OntologyModel {
  classes: { name: string; sub: string[]; note?: string }[];
  relations: { name: string; label?: string; domain?: string; range?: string; transitive?: boolean; symmetric?: boolean; inverse?: string; note?: string }[];
  rules: { id: string; if: string[]; then: string; note?: string }[];
  individuals: { name: string; a: string[]; note?: string; facts: Record<string, string[]> }[];
  constraints: { id: string; class: string; field?: string; min?: number; max?: number; for?: number; requires?: string; valueClass?: string; say?: string; note?: string }[];
}

/**
 * A company's ontology: its packs' merged, its own on top (a class's superclasses and an
 * individual's classes and facts add up; a relation, rule or constraint of the same name or id is
 * replaced), with what binds the packs' products and machines to classes.
 */
export function companyOntology(company: CompanyCfg, packs: IndustryCfg[]) {
  const model: OntologyModel = { classes: [], relations: [], rules: [], individuals: [], constraints: [] };
  const classes = new Map<string, OntologyModel['classes'][number]>();
  const relations = new Map<string, OntologyModel['relations'][number]>();
  const rules = new Map<string, OntologyModel['rules'][number]>();
  const individuals = new Map<string, OntologyModel['individuals'][number]>();
  const constraints = new Map<string, OntologyModel['constraints'][number]>();
  const layers = [...packs.flatMap((p) => (p.ontology ? [p.ontology] : [])), ...(company.ontology ? [company.ontology] : [])];
  for (const o of layers) {
    for (const [name, c] of Object.entries(o.classes)) {
      const had = classes.get(name);
      classes.set(name, { name, sub: [...new Set([...(had?.sub ?? []), ...list(c.sub)])], note: c.note ?? had?.note });
    }
    for (const [name, r] of Object.entries(o.relations)) relations.set(name, { name, ...r });
    for (const r of o.rules) rules.set(r.id, { ...r });
    for (const [name, ind] of Object.entries(o.individuals)) {
      const had = individuals.get(name) ?? { name, a: [], facts: {} };
      for (const [k, v] of Object.entries(ind)) {
        if (k === 'a') had.a = [...new Set([...had.a, ...list(v)])];
        else if (k === 'note') had.note = list(v).join('；');
        else had.facts[k] = [...new Set([...(had.facts[k] ?? []), ...list(v)])];
      }
      individuals.set(name, had);
    }
    for (const c of o.constraints) constraints.set(c.id, { ...c });
  }
  model.classes = [...classes.values()];
  model.relations = [...relations.values()];
  model.rules = [...rules.values()];
  model.individuals = [...individuals.values()];
  model.constraints = [...constraints.values()];

  // what a pack's product or machine is: its own pack's bind, then the company's
  const bound = (pack: IndustryCfg, part: 'products' | 'machines' | 'individuals', key: string): string[] =>
    [pack.ontology?.bind[part][key], company.ontology?.bind[part][key]].filter((x): x is string => !!x);
  return {
    model,
    empty: layers.length === 0,
    productClasses: (pack: IndustryCfg, key: string) => [...new Set(bound(pack, 'products', key))],
    productIndividual: (pack: IndustryCfg, key: string) => bound(pack, 'individuals', key).at(-1) ?? '',
    machineClasses: (pack: IndustryCfg, key: string) => [...new Set(bound(pack, 'machines', key))],
  };
}
