// 新建客户 / 改场景: a company built from an existing one — its industry, plants and lines —
// edited into the customer's own, with the floor plans laid out as you type by the same code the
// twin uses, and checked by the same schema the server checks.
import { useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import type { z } from 'zod';
import {
  CheckIcon, CircleAlertIcon, CircleStopIcon, CodeXmlIcon, ExternalLinkIcon, KeyRoundIcon, ListIcon, LoaderCircleIcon, MinusIcon, PlusIcon, SparklesIcon, Trash2Icon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { cn } from '@/lib/utils';
import { builtinCompanies, ConfigError, industriesOf, industryConfig, layoutCompany, validateCompany, validateIndustry } from '@/config/loader';
import type { Company as CompanySchema, IndustryCfg, LineCfg } from '@/config/schema';
import type { PlantLayout } from '@/core/layout';
import * as api from './api';
import { Blueprint, BlueprintLegend } from './Blueprint';
import { link, PageHead, useConsole } from './Console';
import { forget, loadCompany, useCompanies, type Loaded } from './layouts';
import { query, usePath } from './router';

type Draft = z.input<typeof CompanySchema>;
type PlantDraft = Draft['plants'][number];
type LineDraft = z.input<typeof LineCfg>;

const ID = /^[a-z0-9][a-z0-9-]{0,31}$/;

/** A kind of line an industry can run: product and routing, as some company already runs it. */
interface Kind { key: string; industry: string; label: string; detail: string; name: string; note: string; line: Omit<LineDraft, 'name' | 'repeat' | 'note' | 'order'> }

const kindKey = (industry: string, l: Partial<LineDraft>) => {
  const { name: _n, repeat: _r, note: _o, order: _x, ...rest } = l;
  return `${industry}|${JSON.stringify(rest, Object.keys(rest).sort())}`;
};

function kindsOf(templates: Map<string, Loaded>, industries: Map<string, IndustryCfg>): Kind[] {
  const out = new Map<string, Kind>();
  const describe = (ind: IndustryCfg, l: Omit<LineDraft, 'name' | 'repeat' | 'note' | 'order'>) => {
    const steps = l.steps ?? ind.routes[l.route ?? ''] ?? [];
    const labels = steps.map((s) => ind.machines[typeof s === 'string' ? s : s.machine]?.label ?? '?');
    return { label: ind.products[l.product]?.name ?? l.product, detail: `${labels.length} 道：${labels.slice(0, 4).join('、')}${labels.length > 4 ? '…' : ''}` };
  };
  for (const { company } of templates.values()) {
    for (const p of company.plants) {
      const iid = p.industry ?? company.industry;
      const ind = industries.get(iid);
      if (!ind) continue;
      for (const l of p.lines) {
        const { name, repeat: _r, note, order: _x, ...line } = l;
        const key = kindKey(iid, l);
        if (!out.has(key)) out.set(key, { key, industry: iid, ...describe(ind, line), name, note, line });
      }
    }
  }
  // an industry no company runs yet: each product down each routing
  for (const ind of industries.values()) {
    if ([...out.values()].some((k) => k.industry === ind.id)) continue;
    for (const product of Object.keys(ind.products)) {
      for (const route of Object.keys(ind.routes)) {
        const line = { product, route };
        const key = kindKey(ind.id, line);
        out.set(key, { key, industry: ind.id, ...describe(ind, line), name: `${ind.products[product].name} {n} 线`, note: '', line });
      }
    }
  }
  return [...out.values()];
}

const nextPlantId = (plants: PlantDraft[]) => {
  let n = plants.length + 1;
  while (plants.some((p) => p.id === `p${n}`)) n++;
  return `p${n}`;
};

/** The draft as the server takes it: names trimmed, nothing empty left over. */
function clean(d: Draft): Draft {
  return {
    ...d,
    id: d.id.trim(), name: d.name.trim(), short: d.short.trim(),
    plants: d.plants.map((p) => ({ ...p, name: p.name.trim(), hall: p.hall?.trim() || undefined, badge: p.badge?.trim() || undefined, lines: p.lines.map((l) => ({ ...l, name: l.name.trim() })) })),
  };
}

function Field({ label, hint, error, children, className }: { label: string; hint?: React.ReactNode; error?: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={cn('grid content-start gap-1.5', className)}>
      <span className="text-[12.5px] font-bold">{label}</span>
      {children}
      {error ? <small className="text-[12px] text-destructive">{error}</small> : hint && <small className="text-[12px] text-muted-foreground">{hint}</small>}
    </div>
  );
}

function Stepper({ value, onChange, min = 1, max = 12, label }: { value: number; onChange(v: number): void; min?: number; max?: number; label: string }) {
  return (
    <div className="flex h-9 items-center rounded-xl border bg-background" role="group" aria-label={label}>
      <button type="button" aria-label="减少" disabled={value <= min} onClick={() => onChange(value - 1)} className="grid size-9 place-items-center rounded-l-xl text-muted-foreground hover:bg-surface disabled:opacity-30"><MinusIcon className="size-3.5" /></button>
      <span className="w-7 text-center text-[13.5px] font-extrabold tabular-nums">{value}</span>
      <button type="button" aria-label="增加" disabled={value >= max} onClick={() => onChange(value + 1)} className="grid size-9 place-items-center rounded-r-xl text-muted-foreground hover:bg-surface disabled:opacity-30"><PlusIcon className="size-3.5" /></button>
    </div>
  );
}

function PlantEditor({ plant, index, kinds, onChange, onRemove, canRemove }: {
  plant: PlantDraft; index: number; kinds: Kind[]; onChange(p: PlantDraft): void; onRemove(): void; canRemove: boolean;
}) {
  const setLine = (i: number, l: LineDraft) => onChange({ ...plant, lines: plant.lines.map((x, j) => (j === i ? l : x)) });
  return (
    <section className="rounded-[18px] bg-card p-5 shadow-float max-sm:p-4">
      <div className="flex items-start gap-3">
        <span className="mt-6 grid h-9 min-w-10 place-items-center rounded-[10px] bg-primary px-1 text-[12.5px] font-extrabold text-primary-foreground">{plant.badge || `${index + 1}厂`}</span>
        <div className="grid min-w-0 flex-1 grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_88px] gap-3 max-sm:grid-cols-2">
          <Field label="工厂名称">
            <Input value={plant.name} onChange={(e) => onChange({ ...plant, name: e.target.value })} className="h-9 rounded-xl" placeholder="一厂" />
          </Field>
          <Field label="车间">
            <Input value={plant.hall ?? ''} onChange={(e) => onChange({ ...plant, hall: e.target.value })} className="h-9 rounded-xl" placeholder="生产车间" />
          </Field>
          <Field label="徽标">
            <Input value={plant.badge ?? ''} maxLength={3} onChange={(e) => onChange({ ...plant, badge: e.target.value })} className="h-9 rounded-xl" placeholder={`${index + 1}厂`} />
          </Field>
        </div>
        {canRemove && (
          <Button variant="ghost" size="icon-sm" className="mt-6 rounded-lg text-muted-foreground hover:bg-danger-soft hover:text-destructive" aria-label={`删掉${plant.name || '这个工厂'}`} title="删掉这个工厂" onClick={onRemove}>
            <Trash2Icon className="size-4" />
          </Button>
        )}
      </div>
      <div className="mt-4 grid gap-2">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto_32px] gap-2 px-1 text-[12px] font-bold text-muted-foreground max-sm:hidden">
          <span>产线名称</span><span>生产什么</span><span className="w-[92px]">几条</span><span />
        </div>
        {plant.lines.map((l, i) => {
          const current = kinds.find((k) => k.key === kindKey(k.industry, l));
          return (
            <div key={i} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto_32px] items-center gap-2 rounded-xl bg-surface/60 p-1.5 max-sm:grid-cols-[minmax(0,1fr)_auto_32px]">
              <Input value={l.name} onChange={(e) => setLine(i, { ...l, name: e.target.value })} className="h-9 rounded-xl bg-background" aria-label="产线名称"
                title="{n} 换成第几条" />
              <Select value={current?.key ?? ''} onValueChange={(v) => {
                const k = kinds.find((x) => x.key === v)!;
                const renamed = !current || l.name === current.name;
                setLine(i, { ...k.line, name: renamed ? k.name : l.name, note: renamed ? k.note : l.note, repeat: l.repeat });
              }}>
                <SelectTrigger className="h-9 w-full rounded-xl bg-background max-sm:col-span-3 max-sm:row-start-2" aria-label="生产什么">
                  <SelectValue placeholder="选一种产线">{current && <span className="truncate">{current.label}<span className="ml-1.5 text-muted-foreground">{current.detail.split('：')[0]}</span></span>}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {kinds.map((k) => (
                    <SelectItem key={k.key} value={k.key}>
                      <span className="grid leading-tight"><b className="text-[13px]">{k.label}</b><small className="text-[11.5px] text-muted-foreground">{k.detail}</small></span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Stepper value={l.repeat ?? 1} onChange={(v) => setLine(i, { ...l, repeat: v })} max={8} label="几条" />
              <Button variant="ghost" size="icon-sm" className="rounded-lg text-muted-foreground hover:bg-danger-soft hover:text-destructive disabled:opacity-0" aria-label="删掉这条产线"
                disabled={plant.lines.length <= 1} onClick={() => onChange({ ...plant, lines: plant.lines.filter((_, j) => j !== i) })}>
                <MinusIcon className="size-4" />
              </Button>
            </div>
          );
        })}
        <button type="button" disabled={!kinds.length}
          onClick={() => { const k = kinds[0]; onChange({ ...plant, lines: [...plant.lines, { ...k.line, name: k.name, note: k.note, repeat: 1 }] }); }}
          className="flex h-9 w-fit items-center gap-1.5 rounded-xl px-2.5 text-[13px] font-bold text-primary hover:bg-brand-soft">
          <PlusIcon className="size-4" />加一种产线
        </button>
      </div>
    </section>
  );
}

const TOOL_LABEL: Record<string, (a?: Record<string, unknown>) => string> = {
  list_industries: () => '看有哪些行业包',
  get_industry: (a) => `读行业包 ${a?.id ?? ''}`,
  list_companies: () => '看现有客户',
  get_company: (a) => `参考 ${a?.id ?? ''} 的配置`,
  propose_company: () => '检查配置',
};

interface Step { tool: string; args?: Record<string, unknown>; done: boolean; failed?: string }

/** 描述客户: the scene architect writes the config from a few sentences; each version that passes
 * the engine's check lands in the form below. */
function Architect({ draft, onDraft, edit }: { draft: Draft | null; onDraft(d: Draft): void; edit: boolean }) {
  const [brief, setBrief] = useState('');
  const [steps, setSteps] = useState<Step[]>([]);
  const [answer, setAnswer] = useState('');
  const [error, setError] = useState<string>();
  const [ctrl, setCtrl] = useState<AbortController | null>(null);
  const [drafts, setDrafts] = useState(0);
  const run = async () => {
    const text = brief.trim();
    if (!text || ctrl) return;
    const c = new AbortController();
    setCtrl(c);
    setSteps([]);
    setAnswer('');
    setError(undefined);
    setDrafts(0);
    try {
      // a form already filled in is changed, not started over
      const base = draft && (edit || draft.name.trim()) ? clean(draft) : undefined;
      await api.compose(text, base, c.signal, (ev) => {
        switch (ev.kind) {
          case 'tool_call': setSteps((s) => [...s, { tool: ev.tool, args: ev.args, done: false }]); break;
          case 'tool_result': setSteps((s) => {
            const out = [...s];
            const i = out.findLastIndex((x) => x.tool === ev.tool && !x.done);
            const r = typeof ev.result === 'string' ? ev.result : '';
            if (i >= 0) out[i] = { ...out[i], done: true, failed: /没通过|已经有|没有|看不到|要是/.test(r) ? r : undefined };
            return out;
          }); break;
          case 'draft': onDraft(ev.result as Draft); setDrafts((n) => n + 1); break;
          case 'delta': setAnswer((a) => a + ev.text); break;
          case 'reset': setAnswer(''); break;
          case 'complete': if (ev.text) setAnswer(ev.text); break;
          case 'error': setError(ev.text); break;
        }
      });
    } catch (e) {
      if (!c.signal.aborted) setError((e as Error).message);
    } finally {
      setCtrl(null);
    }
  };
  const busy = !!ctrl;
  return (
    <section className="overflow-hidden rounded-[18px] bg-card shadow-float">
      <div className="grid gap-3 p-5 max-sm:p-4">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="grid size-8 place-items-center rounded-[10px] bg-foreground text-background"><SparklesIcon className="size-4" /></span>
          <h2 className="text-[15px] font-extrabold">{edit ? '说要怎么改' : '描述工厂'}</h2>
          <span className="text-[12.5px] text-muted-foreground max-sm:basis-full">AI 按行业包写好放进下面的表单，你确认后再建</span>
        </div>
        <Textarea value={brief} onChange={(e) => setBrief(e.target.value)} rows={3} disabled={busy}
          onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); run(); } }}
          placeholder={edit ? '再加一个工厂做法兰，两条线；把连杆线去掉' : '江苏星河锻造，两个厂：常州厂做发动机曲轴，四条线；无锡厂做风电法兰和轴承环，各两条'}
          className="min-h-[84px] resize-none rounded-xl text-[14px] leading-relaxed" aria-label="描述" />
        <div className="flex items-center gap-2">
          <span className="text-[12px] text-muted-foreground max-sm:hidden">⌘ Enter 发送</span>
          {busy
            ? <Button variant="outline" className="ml-auto h-9 rounded-xl font-bold" onClick={() => ctrl?.abort()}><CircleStopIcon />停下</Button>
            : <Button className="ml-auto h-9 rounded-xl font-bold" disabled={!brief.trim()} onClick={run}><SparklesIcon />{edit ? '让 AI 改' : '让 AI 搭'}</Button>}
        </div>
      </div>
      {(steps.length > 0 || busy || answer || error) && (
        <div className="grid gap-2 border-t bg-surface/50 px-5 py-4 max-sm:px-4">
          <ol className="grid gap-1">
            {steps.map((st, i) => (
              <li key={i} className="flex items-start gap-2 text-[12.5px]">
                {!st.done ? <LoaderCircleIcon className="mt-0.5 size-3.5 shrink-0 animate-spin text-primary" />
                  : st.failed ? <CircleAlertIcon className="mt-0.5 size-3.5 shrink-0 text-destructive" />
                    : <CheckIcon className="mt-0.5 size-3.5 shrink-0 text-success" />}
                <span className="min-w-0">
                  <span className={cn('font-bold', st.failed && 'text-destructive')}>{(TOOL_LABEL[st.tool] ?? (() => st.tool))(st.args)}{st.tool === 'propose_company' && st.done && !st.failed ? '：通过，已放进表单' : ''}</span>
                  {st.failed && <span className="block whitespace-pre-line text-muted-foreground">{st.failed.replace(/^没通过：\n?/, '').slice(0, 300)}</span>}
                </span>
              </li>
            ))}
            {busy && !steps.some((st) => !st.done) && <li className="flex items-center gap-2 text-[12.5px] text-muted-foreground"><LoaderCircleIcon className="size-3.5 animate-spin" />在想</li>}
          </ol>
          {answer && <p className="text-[13.5px] leading-relaxed whitespace-pre-line">{answer}</p>}
          {error && <p className="rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
          {!busy && drafts > 0 && <p className="text-[12px] text-muted-foreground">表单和平面图已更新，没问题就{edit ? '保存' : '建'}。</p>}
        </div>
      )}
    </section>
  );
}

function Preview({ plants, problems, draft }: { plants: PlantLayout[]; problems: string[]; draft: Draft }) {
  const lines = plants.reduce((n, p) => n + p.lines.length, 0);
  const machines = plants.reduce((n, p) => n + p.lines.reduce((m, l) => m + l.stations.length, 0), 0);
  return (
    <div className="grid gap-4">
      <div className="flex items-center gap-3">
        <span className="grid h-10 min-w-11 place-items-center rounded-[10px] bg-foreground px-1.5 text-[14px] font-extrabold text-background">{draft.short || '简称'}</span>
        <span className="grid min-w-0 leading-tight">
          <b className={cn('truncate text-[16px] font-extrabold', !draft.name && 'text-muted-foreground')}>{draft.name || '客户名称'}</b>
          <small className="text-[12.5px] text-muted-foreground tabular-nums">{plants.length} 个工厂，{lines} 条产线，{machines} 台设备</small>
        </span>
      </div>
      {plants.map((p) => (
        <figure key={p.id} className="grid gap-1.5">
          <figcaption className="text-[12.5px] font-bold">{p.name} <span className="font-normal text-muted-foreground">{p.hall}</span></figcaption>
          <div className="rounded-xl bg-[color-mix(in_oklab,var(--primary)_4%,var(--card))] p-2.5">
            <Blueprint plant={p} across={36} />
          </div>
        </figure>
      ))}
      {plants.length > 0 && <BlueprintLegend plants={plants} />}
      {problems.length > 0 && (
        <ul className="grid gap-1 rounded-xl bg-danger-soft px-3 py-2.5 text-[12.5px] text-destructive">
          {problems.map((p, i) => <li key={i} className="flex gap-1.5"><CircleAlertIcon className="mt-0.5 size-3.5 shrink-0" />{p}</li>)}
        </ul>
      )}
    </div>
  );
}

function Done({ result, edit }: { result: api.CompanyCreated; edit: boolean }) {
  const machines = result.plants.reduce((n, p) => n + p.machines, 0);
  return (
    <div className="mx-auto grid max-w-[520px] justify-items-center gap-4 rounded-[20px] bg-card p-8 text-center shadow-float max-sm:p-6">
      <span className="grid size-12 place-items-center rounded-full bg-success-soft text-success"><CheckIcon className="size-6" /></span>
      <div>
        <h2 className="text-[20px] font-extrabold">{result.name} {edit ? '已更新' : '建好了'}</h2>
        <p className="mt-1 text-[13.5px] text-muted-foreground">{result.plants.length} 个工厂、{result.plants.reduce((n, p) => n + p.lines.length, 0)} 条产线、{machines} 台设备已经在服务器上运行</p>
      </div>
      <div className="flex flex-wrap justify-center gap-2">
        <Button asChild className="h-10 rounded-xl font-bold"><a href={`/?company=${encodeURIComponent(result.id)}`}>打开孪生<ExternalLinkIcon /></a></Button>
        <Button asChild variant="outline" className="h-10 rounded-xl font-bold"><a {...link(`/admin/customers/${encodeURIComponent(result.id)}?tab=accounts`)}><KeyRoundIcon />给客户发令牌</a></Button>
        <Button asChild variant="ghost" className="h-10 rounded-xl font-bold"><a {...link(`/admin/customers/${encodeURIComponent(result.id)}`)}>查看客户</a></Button>
      </div>
    </div>
  );
}

const SOURCE_LABEL: Record<api.TemplateSummary['source'], string> = { builtin: '内置模板', local: '这里存的模板', hub: '模板市场' };

export default function Builder({ edit }: { edit?: string }) {
  const { ov, reload } = useConsole();
  const path = usePath();
  // what lines can be made of: the built-in templates, the companies here, and templates picked
  const ids = useMemo(() => ov.companies.map((c) => c.id), [ov.companies]);
  const companies = useCompanies(ids);
  const [builtin, setBuiltin] = useState(new Map<string, Loaded>());
  const [picked, setPicked] = useState(new Map<string, Loaded>());
  const [list, setList] = useState<api.TemplateSummary[]>([]);
  // industry packs a template brings that this instance does not have yet: installed when building
  const [bundled, setBundled] = useState(new Map<string, unknown>());
  const [extra, setExtra] = useState(new Map<string, IndustryCfg>());
  const q = query(path);
  const [from, setFrom] = useState<string>(edit ? `company:${edit}` : q.get('template') ?? (q.get('from') ? `company:${q.get('from')}` : ''));
  const [draft, setDraft] = useState<Draft | null>(null);
  const [mode, setMode] = useState<'form' | 'json'>('form');
  const [json, setJson] = useState('');
  const [jsonError, setJsonError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [server, setServer] = useState<string[]>([]);
  const [done, setDone] = useState<api.CompanyCreated | null>(null);
  const [touched, setTouched] = useState(false);
  const noun = ov.single ? '公司' : '客户';

  useEffect(() => {
    api.templates().then(setList, () => setList([]));
    builtinCompanies().then(async (cs) => {
      const m = new Map<string, Loaded>();
      for (const c of cs) {
        try {
          const inds = new Map<string, IndustryCfg>();
          for (const id of industriesOf(c)) inds.set(id, await industryConfig(id));
          m.set(c.id, { company: c, industries: inds, plants: [] });
        } catch { /* a broken file */ }
      }
      setBuiltin(m);
    });
  }, []);

  const sources = useMemo(() => new Map<string, Loaded>([...builtin, ...companies, ...picked]), [builtin, companies, picked]);
  // industry packs no template uses yet start blank; fetch them when picked
  const unused = ov.industries.filter((i) => ![...sources.values()].some((t) => t.company.industry === i.id));
  const industries = useMemo(() => {
    const m = new Map<string, IndustryCfg>(extra);
    for (const t of sources.values()) for (const [k, v] of t.industries) m.set(k, v);
    return m;
  }, [sources, extra]);
  const kinds = useMemo(() => kindsOf(sources, industries), [sources, industries]);

  // a template as a draft: its structure, the name and id left for the customer's own
  const load = async (key: string): Promise<Draft | null> => {
    const [source, id] = [key.slice(0, key.indexOf(':')), key.slice(key.indexOf(':') + 1)];
    let company: unknown;
    let packs: Record<string, unknown> = {};
    if (source === 'company') company = (companies.get(id) ?? (await loadCompany(id))).company;
    else if (source === 'builtin' && builtin.has(id)) company = builtin.get(id)!.company;
    else {
      const t = await api.template(source as api.TemplateSummary['source'], id);
      company = t.company;
      packs = t.industries ?? {};
    }
    const inds = new Map<string, IndustryCfg>();
    for (const [pid, raw] of Object.entries(packs)) inds.set(pid, validateIndustry(raw, `行业包 ${pid}`));
    const cfg = validateCompany(company, `模板 ${id}`);
    for (const pid of industriesOf(cfg)) if (!inds.has(pid) && !industries.has(pid)) inds.set(pid, await industryConfig(pid, api.industry));
    setPicked((m) => new Map(m).set(key, { company: cfg, industries: inds, plants: [] }));
    if (Object.keys(packs).length) setBundled((m) => { const n = new Map(m); for (const [pid, raw] of Object.entries(packs)) n.set(pid, raw); return n; });
    const copy = structuredClone(company) as Draft;
    delete (copy as { $schema?: string }).$schema;
    return copy;
  };

  // start from the template (or the company being edited) once there is something to start from
  useEffect(() => {
    if (draft) return;
    const key = from || (builtin.size ? `builtin:${[...builtin.keys()][0]}` : '');
    if (!key || key.startsWith('blank:') || (key.startsWith('builtin:') && !builtin.size) || (key.startsWith('company:') && edit && !companies.has(edit))) return;
    let live = true;
    load(key).then((d) => {
      if (!live || !d) return;
      if (!edit) { d.id = ''; d.name = ''; d.short = ''; }
      setFrom(key);
      setDraft(d);
    }, (e: Error) => toast.error(e.message));
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [builtin, companies, from, draft, edit]);

  const startBlank = async (industryId: string) => {
    let ind = industries.get(industryId);
    if (!ind) {
      ind = await api.industry(industryId);
      setExtra((m) => new Map(m).set(industryId, ind!));
    }
    const product = Object.keys(ind.products)[0];
    const route = Object.keys(ind.routes)[0];
    setDraft({ id: '', name: '', short: '', industry: industryId, plants: [{ id: 'p1', name: '一厂', lines: [{ name: `${ind.products[product].name} {n} 线`, product, route, repeat: 1 }] }] });
  };

  const pickTemplate = async (v: string) => {
    setFrom(v);
    setServer([]);
    if (v.startsWith('blank:')) { startBlank(v.slice(6)); return; }
    try {
      const d = await load(v);
      if (d) setDraft({ ...d, id: draft?.id ?? '', name: draft?.name ?? '', short: draft?.short ?? '' });
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  // a pack the draft uses that nothing loaded yet (the architect picked it, or it came as JSON)
  const wanted = draft ? [...new Set([draft.industry, ...draft.plants.map((p) => p.industry)].filter((x): x is string => !!x))] : [];
  const missing = wanted.filter((i) => !industries.has(i) && ov.industries.some((x) => x.id === i)).join(',');
  useEffect(() => {
    for (const id of missing ? missing.split(',') : []) {
      api.industry(id).then((ind) => setExtra((m) => new Map(m).set(id, ind)), () => {});
    }
  }, [missing]);

  const checked = useMemo(() => {
    if (!draft) return { plants: [] as PlantLayout[], problems: [] as string[] };
    try {
      const cfg = validateCompany(clean(draft), '客户');
      const missing = industriesOf(cfg).filter((i) => !industries.has(i));
      if (missing.length) return { plants: [], problems: [`还没读到行业包 ${missing.join('、')}`] };
      return { plants: layoutCompany(cfg, industries, '客户'), problems: [] };
    } catch (e) {
      return { plants: [], problems: e instanceof ConfigError ? e.issues : [(e as Error).message] };
    }
  }, [draft, industries]);

  if (edit && !ov.companies.some((c) => c.id === edit && !c.builtin)) {
    return <PageHead title="这家公司不能在这里改" sub="随应用内置的公司改不了，可以照它新建一个" back={{ to: `/admin/customers/${encodeURIComponent(edit)}`, label: '客户' }} />;
  }
  if (!edit && (ov.single ? ov.companies.length > 0 : !!ov.runs)) {
    return <PageHead title={ov.single ? '这个实例已经建好了工厂' : '这是一个客户自己的实例'} sub="别的公司部署自己的实例" back={{ to: '/admin', label: noun }} />;
  }
  if (!ov.me.everywhere && !edit) {
    return <PageHead title={`新建${noun}要所有公司的管理员令牌`} sub={`你的令牌只管 ${ov.companies.map((c) => c.name).join('、')}`} back={{ to: '/admin', label: '客户' }} />;
  }
  if (done) return <><PageHead title={edit ? '改场景' : ov.single ? '建你的工厂' : '新建客户'} back={{ to: '/admin', label: noun }} /><Done result={done} edit={!!edit} /></>;

  const idTaken = !edit && !!draft?.id && ov.companies.some((c) => c.id === draft.id.trim());
  const idBad = !!draft?.id && !ID.test(draft.id.trim());
  const shortLong = (draft?.short.length ?? 0) > 4;
  const ready = !!draft && draft.name.trim() && draft.short.trim() && draft.id.trim() && !idTaken && !idBad && !shortLong && !checked.problems.length;
  const set = (patch: Partial<Draft>) => { setDraft((d) => d && { ...d, ...patch }); setServer([]); };
  const plantKinds = (p: PlantDraft) => kinds.filter((k) => k.industry === (p.industry ?? draft?.industry));

  const submit = async () => {
    if (!draft || !ready) { setTouched(true); return; }
    setBusy(true);
    setServer([]);
    try {
      // the packs the template brought that are not here yet
      const have = new Set(ov.industries.map((i) => i.id));
      const used = new Set([draft.industry, ...draft.plants.map((p) => p.industry)].filter(Boolean));
      for (const [pid, raw] of bundled) if (used.has(pid) && !have.has(pid)) await api.createIndustry(raw);
      const res = await api.createCompany(clean(draft));
      forget(res.id);
      await reload();
      toast.success(edit ? `${res.name} 已更新` : `${res.name} 建好了`);
      setDone(res);
      window.scrollTo(0, 0);
    } catch (e) {
      const err = e as api.ApiError;
      setServer(err.problems.length ? err.problems : [err.message]);
    } finally {
      setBusy(false);
    }
  };

  const toJson = () => { setJson(JSON.stringify(draft, null, 2)); setJsonError(undefined); setMode('json'); };
  const fromJson = () => {
    try {
      const d = JSON.parse(json) as Draft;
      if (edit) d.id = edit;
      setDraft(d);
      setMode('form');
    } catch (e) {
      setJsonError(`不是合法的 JSON：${(e as Error).message}`);
    }
  };

  const templateName = (id: string) => ov.companies.find((c) => c.id === id)?.name ?? id;
  return (
    <>
      <PageHead title={edit ? `改场景：${templateName(edit)}` : ov.single ? '建你的工厂' : '新建客户'} back={edit ? { to: ov.single ? '/admin' : `/admin/customers/${encodeURIComponent(edit)}`, label: templateName(edit) } : { to: '/admin', label: ov.single ? '模板' : '客户' }}
        sub={edit ? '保存后这家公司的仿真按新场景从头开始' : `从一个模板起步，改成${ov.single ? '你公司' : '客户'}的名字、工厂和产线`}>
        <ToggleGroup type="single" value={mode} onValueChange={(v) => { if (v === 'json' && mode === 'form') toJson(); if (v === 'form' && mode === 'json') fromJson(); }} className="rounded-xl bg-card p-0.5 shadow-float">
          <ToggleGroupItem value="form" className="h-9 gap-1.5 rounded-lg px-3 text-[13px] font-bold data-[state=on]:bg-brand-soft data-[state=on]:text-primary"><ListIcon className="size-4" />表单</ToggleGroupItem>
          <ToggleGroupItem value="json" className="h-9 gap-1.5 rounded-lg px-3 text-[13px] font-bold data-[state=on]:bg-brand-soft data-[state=on]:text-primary"><CodeXmlIcon className="size-4" />JSON</ToggleGroupItem>
        </ToggleGroup>
      </PageHead>

      {!draft && <div className="grid h-60 place-items-center"><LoaderCircleIcon className="animate-spin text-muted-foreground" /></div>}
      {draft && (
        <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(380px,480px)]">
          <div className="grid gap-5">
            {mode === 'json' ? (
              <section className="grid gap-3 rounded-[18px] bg-card p-5 shadow-float">
                <p className="text-[13px] text-muted-foreground">整份公司配置。用 MCP 的 scene_guide 让 AI 写好贴进来也行；切回表单时生效。</p>
                <Textarea value={json} onChange={(e) => setJson(e.target.value)} spellCheck={false} className="h-[560px] resize-y rounded-xl font-mono text-[12.5px]" aria-label="公司配置 JSON" />
                {jsonError && <p className="text-[12.5px] text-destructive">{jsonError}</p>}
              </section>
            ) : (
              <>
                {ov.assistants && (
                  <Architect draft={draft} edit={!!edit} onDraft={(d) => {
                    const next = { ...d } as Draft;
                    delete (next as { $schema?: string }).$schema;
                    if (edit) next.id = edit;
                    setDraft(next);
                    setServer([]);
                    if (next.industry) setFrom((f) => (sources.get(f)?.company.industry === next.industry ? f : ''));
                  }} />
                )}
                {!edit && (
                  <section className="rounded-[18px] bg-card p-5 shadow-float max-sm:p-4">
                    <Field label="或者从一个模板起步" hint="复制它的行业、工厂和产线，下面再改">
                      <Select value={from} onValueChange={pickTemplate}>
                        <SelectTrigger className="h-10 w-full rounded-xl"><SelectValue placeholder="选一个模板" /></SelectTrigger>
                        <SelectContent>
                          {(['builtin', 'local', 'hub'] as const).map((src) => {
                            const items = list.filter((t) => t.source === src);
                            if (!items.length) return null;
                            return (
                              <SelectGroup key={src}>
                                <SelectLabel>{SOURCE_LABEL[src]}</SelectLabel>
                                {items.map((t) => (
                                  <SelectItem key={`${src}:${t.id}`} value={`${src}:${t.id}`}>
                                    {`${t.name}（${t.industryName}，${t.plants.length} 个工厂${t.author && src !== 'builtin' ? `，${t.author}` : ''}）`}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            );
                          })}
                          {!ov.single && ov.companies.length > 0 && (
                            <SelectGroup>
                              <SelectLabel>现有{noun}</SelectLabel>
                              {ov.companies.map((c) => (
                                <SelectItem key={c.id} value={`company:${c.id}`}>
                                  {`${c.name}（${ov.industries.find((i) => i.id === c.industry)?.name ?? c.industry}，${c.plants.length} 个工厂）`}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          )}
                          {unused.length > 0 && (
                            <SelectGroup>
                              <SelectLabel>空白，只用行业包</SelectLabel>
                              {unused.map((i) => <SelectItem key={i.id} value={`blank:${i.id}`}>{i.name}</SelectItem>)}
                            </SelectGroup>
                          )}
                        </SelectContent>
                      </Select>
                    </Field>
                  </section>
                )}
                <section className="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1.3fr)] gap-4 rounded-[18px] bg-card p-5 shadow-float max-md:grid-cols-2 max-sm:p-4">
                  <Field label={`${noun}名称`} error={touched && !draft.name.trim() ? `填上${noun}的名称` : undefined} className="max-md:col-span-2">
                    <Input value={draft.name} onChange={(e) => set({ name: e.target.value })} placeholder="星河锻造集团" className="h-10 rounded-xl" autoFocus={!edit} />
                  </Field>
                  <Field label="简称" hint="顶栏徽标，最多 4 个字" error={shortLong ? '最多 4 个字' : touched && !draft.short.trim() ? '填一个简称' : undefined}>
                    <Input value={draft.short} onChange={(e) => set({ short: e.target.value })} placeholder="星河" className="h-10 rounded-xl" />
                  </Field>
                  <Field label="标识" hint={edit ? '标识改不了' : '链接和数据主题里用，小写字母、数字、-'}
                    error={idTaken ? '已经有这个标识了' : idBad ? '只能用小写字母、数字和 -' : touched && !draft.id.trim() ? '填一个标识' : undefined}>
                    <Input value={draft.id} disabled={!!edit} onChange={(e) => set({ id: e.target.value.toLowerCase() })} placeholder="xinghe" className="h-10 rounded-xl font-mono" spellCheck={false} />
                  </Field>
                </section>
                {draft.plants.map((p, i) => (
                  <PlantEditor key={i} plant={p} index={i} kinds={plantKinds(p)} canRemove={draft.plants.length > 1}
                    onChange={(np) => set({ plants: draft.plants.map((x, j) => (j === i ? np : x)) })}
                    onRemove={() => set({ plants: draft.plants.filter((_, j) => j !== i) })} />
                ))}
                <button type="button"
                  onClick={() => {
                    const base = draft.plants.at(-1)!;
                    const id = nextPlantId(draft.plants);
                    set({ plants: [...draft.plants, { ...structuredClone(base), id, name: `${draft.plants.length + 1} 厂`, badge: undefined, lines: [structuredClone(base.lines[0])] }] });
                  }}
                  className="flex h-12 items-center justify-center gap-2 rounded-[18px] border-2 border-dashed text-[13.5px] font-bold text-muted-foreground hover:border-primary/50 hover:bg-brand-soft/40 hover:text-primary">
                  <PlusIcon className="size-4" />加一个工厂
                </button>
              </>
            )}
          </div>
          <aside className="grid gap-4 rounded-[20px] bg-card p-5 shadow-float xl:sticky xl:top-6 max-sm:p-4">
            <Preview plants={checked.plants} problems={[...checked.problems, ...server]} draft={draft} />
            <div className="flex items-center gap-2 border-t pt-4">
              {touched && !ready && !checked.problems.length && <span className="text-[12.5px] text-destructive">左边还有没填好的</span>}
              <Button className="ml-auto h-11 rounded-xl px-5 text-[14px] font-bold" disabled={busy || mode === 'json' || !!checked.problems.length} onClick={submit}>
                {busy && <LoaderCircleIcon className="animate-spin" />}{edit ? '保存' : '建好它'}
              </Button>
            </div>
            {mode === 'json' && <p className="-mt-2 text-right text-[12px] text-muted-foreground">切回表单再保存</p>}
          </aside>
        </div>
      )}
    </>
  );
}
