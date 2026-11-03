// 修改这里: one part of a built company — a plant, an area, a line or a machine — changed by a
// form or by telling the AI, without touching the rest. The server checks every change against
// the part picked (anything outside is refused and named), says what it does in words and which
// machine tags (位号) would move, and keeps the config from before so it can be undone.
import { useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import {
  ArrowDownIcon, ArrowUpIcon, CheckIcon, CircleAlertIcon, CircleStopIcon, ExternalLinkIcon, LoaderCircleIcon, PlusIcon, ShieldAlertIcon,
  SparklesIcon, TagIcon, Trash2Icon, Undo2Icon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import type { IndustryCfg } from '@/config/schema';
import { cn } from '@/lib/utils';
import * as api from './api';
import { link, PageHead, useConsole } from './Console';
import { forget } from './layouts';
import { Panel } from './Panel';
import { go, query, usePath } from './router';
import { stepLines, THINKING, type Step } from './steps';

/* eslint-disable @typescript-eslint/no-explicit-any */
type Cfg = Record<string, any>;

const copy = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T;
const KIND: Record<api.ScopeNode['kind'], string> = { plant: '工厂', area: '车间', line: '产线', machine: '设备' };

function Field({ label, hint, children, className }: { label: string; hint?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <label className={cn('grid min-w-0 content-start gap-1.5', className)}>
      <span className="text-[12.5px] font-bold">{label}</span>
      {children}
      {hint && <small className="text-[12px] leading-snug text-muted-foreground">{hint}</small>}
    </label>
  );
}

/** A number field: empty is "not set" (the default applies). */
function Num({ value, onChange, step = 1, placeholder, label }: { value?: number; onChange(v: number | undefined): void; step?: number; placeholder?: string; label: string }) {
  return (
    <Input type="number" inputMode="decimal" step={step} value={value ?? ''} placeholder={placeholder} aria-label={label}
      onChange={(e) => onChange(e.target.value === '' ? undefined : Number(e.target.value))} className="h-9 rounded-xl" />
  );
}

const nodeOf = (q: URLSearchParams): api.ScopeNode => ({
  kind: (q.get('kind') ?? 'line') as api.ScopeNode['kind'], plant: q.get('plant') ?? '',
  area: q.get('area') ?? undefined, line: q.get('line') ?? undefined, asset: q.get('asset') ?? undefined,
});

// ---------- the form, one per kind of part ----------

interface FormProps {
  view: api.ScopeView;
  draft: Cfg;
  onDraft(d: Cfg): void;
  pack?: IndustryCfg;
}

function plantOf(view: api.ScopeView, d: Cfg) { return d.plants[view.scope.plant] as Cfg; }

function PlantForm({ view, draft, onDraft }: FormProps) {
  const p = plantOf(view, draft);
  const set = (f: (p: Cfg) => void) => { const d = copy(draft); f(d.plants[view.scope.plant]); onDraft(d); };
  const areas: Cfg[] = p.areas ?? [];
  const used = (name: string) => (p.lines as Cfg[]).some((l) => (l.area?.trim() || areas[0]?.name) === name);
  const rename = (i: number, name: string) => set((q) => {
    const old = q.areas[i].name;
    q.areas[i].name = name;
    for (const l of q.lines) if (l.area === old) l.area = name;
  });
  const move = (i: number, by: number) => set((q) => { const [a] = q.areas.splice(i, 1); q.areas.splice(i + by, 0, a); });
  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_88px] gap-3 max-sm:grid-cols-2">
        <Field label="工厂名称"><Input value={p.name} onChange={(e) => set((q) => { q.name = e.target.value; })} className="h-9 rounded-xl" /></Field>
        <Field label="厂房"><Input value={p.hall ?? ''} onChange={(e) => set((q) => { q.hall = e.target.value; })} className="h-9 rounded-xl" /></Field>
        <Field label="徽标"><Input value={p.badge ?? ''} maxLength={3} onChange={(e) => set((q) => { q.badge = e.target.value || undefined; })} className="h-9 rounded-xl" /></Field>
      </div>
      <div className="grid gap-2">
        <span className="text-[12.5px] font-bold">车间{areas.length === 0 && <span className="ml-1.5 font-normal text-muted-foreground">现在只有一个，就是厂房</span>}</span>
        {areas.map((a, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2 rounded-xl bg-surface/60 p-1.5">
            <Input value={a.name} onChange={(e) => rename(i, e.target.value)} className="h-9 min-w-0 flex-1 basis-40 rounded-xl bg-background" aria-label="车间名称" />
            <Input value={a.code ?? ''} placeholder={`A${i + 1}`} onChange={(e) => set((q) => { q.areas[i].code = e.target.value || undefined; })} className="h-9 w-24 rounded-xl bg-background font-mono" aria-label="车间编码" />
            <span className="flex gap-1">
              <Button variant="ghost" size="icon-sm" aria-label="往前挪" disabled={i === 0} onClick={() => move(i, -1)}><ArrowUpIcon /></Button>
              <Button variant="ghost" size="icon-sm" aria-label="往后挪" disabled={i === areas.length - 1} onClick={() => move(i, 1)}><ArrowDownIcon /></Button>
              <Button variant="ghost" size="icon-sm" aria-label="删掉这个车间" title={used(a.name) ? '车间里还有产线，先把线移走' : '删掉这个车间'} disabled={used(a.name)}
                className="hover:bg-danger-soft hover:text-destructive" onClick={() => set((q) => { q.areas.splice(i, 1); if (!q.areas.length) delete q.areas; })}><Trash2Icon /></Button>
            </span>
          </div>
        ))}
        <button type="button" onClick={() => set((q) => {
          // the hall becomes the first area, so its lines stay where they are
          if (!q.areas?.length) q.areas = [{ name: q.hall || '生产车间', note: '' }];
          let n = q.areas.length + 1;
          while (q.areas.some((a: Cfg) => a.name === `新车间 ${n}`)) n++;
          q.areas.push({ name: `新车间 ${n}`, note: '' });
        })} className="flex h-9 w-fit items-center gap-1.5 rounded-xl px-2.5 text-[13px] font-bold text-primary hover:bg-brand-soft">
          <PlusIcon className="size-4" />加一个车间
        </button>
        <small className="text-[12px] text-muted-foreground">车间的先后决定它在孪生里从后往前排的位置；产线在哪个车间，到产线或车间里改。</small>
      </div>
    </div>
  );
}

function AreaForm({ view, draft, onDraft }: FormProps) {
  const { scope, model } = view;
  const p = plantOf(view, draft);
  const listed = scope.areaIndex >= 0;
  const a: Cfg | undefined = listed ? p.areas[scope.areaIndex] : undefined;
  const set = (f: (p: Cfg) => void) => { const d = copy(draft); f(d.plants[scope.plant]); onDraft(d); };
  const lp = model.plants[scope.plant];
  const areas: Cfg[] = p.areas ?? [];
  const lineArea = (l: Cfg) => (l.area?.trim() || areas[0]?.name);
  const mine = (l: Cfg) => !listed || lineArea(l) === a!.name;
  return (
    <div className="grid gap-4">
      {listed ? (
        <div className="grid grid-cols-[minmax(0,1.4fr)_120px_minmax(0,1.4fr)] gap-3 max-sm:grid-cols-2">
          <Field label="车间名称">
            <Input value={a!.name} onChange={(e) => set((q) => {
              const old = q.areas[scope.areaIndex].name;
              q.areas[scope.areaIndex].name = e.target.value;
              for (const l of q.lines) if (l.area === old) l.area = e.target.value;
            })} className="h-9 rounded-xl" />
          </Field>
          <Field label="编码"><Input value={a!.code ?? ''} placeholder={`A${scope.areaIndex + 1}`} onChange={(e) => set((q) => { q.areas[scope.areaIndex].code = e.target.value || undefined; })} className="h-9 rounded-xl font-mono" /></Field>
          <Field label="备注" className="max-sm:col-span-2"><Input value={a!.note ?? ''} onChange={(e) => set((q) => { q.areas[scope.areaIndex].note = e.target.value; })} className="h-9 rounded-xl" /></Field>
        </div>
      ) : (
        <Field label="车间名称" hint="这个工厂只有一个车间，就是它的厂房。要分几个车间，到工厂里加。">
          <Input value={p.hall ?? ''} onChange={(e) => set((q) => { q.hall = e.target.value; })} className="h-9 rounded-xl" />
        </Field>
      )}
      <div className="grid gap-2">
        <span className="text-[12.5px] font-bold">车间里的产线</span>
        {(p.lines as Cfg[]).map((l, ci) => {
          const names = lp.lines.filter((x) => x.cfg === ci).map((x) => x.name);
          const inHere = mine(l);
          return (
            <div key={ci} className={cn('flex flex-wrap items-center gap-2 rounded-xl p-2 text-[13px]', inHere ? 'bg-surface/70' : 'opacity-70')}>
              <b className="min-w-0 flex-1 truncate">{names.join('、') || l.name}</b>
              {listed && areas.length > 1 && (inHere ? (
                <Select value={lineArea(l)} onValueChange={(v) => set((q) => { q.lines[ci].area = v; })}>
                  <SelectTrigger className="h-8 w-40 rounded-lg bg-background" aria-label="所在车间"><SelectValue /></SelectTrigger>
                  <SelectContent>{areas.map((x) => <SelectItem key={x.name} value={x.name}>{x.name}</SelectItem>)}</SelectContent>
                </Select>
              ) : (
                <>
                  <span className="text-muted-foreground">在{lineArea(l)}</span>
                  <Button variant="outline" size="sm" className="h-8 rounded-lg" onClick={() => set((q) => { q.lines[ci].area = a!.name; })}>移进来</Button>
                </>
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function StepRow({ st, i, n, tag, kinds, onChange, onMove, onRemove }: {
  st: Cfg; i: number; n: number; tag?: string; kinds: Record<string, api.MachineKind>; onChange(s: Cfg): void; onMove?(by: number): void; onRemove?(): void;
}) {
  const kind = kinds[st.machine];
  return (
    <div className="grid grid-cols-[52px_minmax(0,1.1fr)_minmax(0,1fr)_86px_auto] items-center gap-2 rounded-xl bg-surface/60 p-1.5 max-sm:grid-cols-[minmax(0,1fr)_86px_auto]">
      <span className="font-mono text-[12px] font-bold text-muted-foreground max-sm:col-span-3" title="位号">{tag ?? '新'}</span>
      <Input value={st.name ?? ''} placeholder={kind?.label} onChange={(e) => onChange({ ...st, name: e.target.value || undefined })} className="h-9 rounded-xl bg-background" aria-label="工位名称" />
      <Select value={st.machine} onValueChange={(v) => onChange({ ...st, machine: v })}>
        <SelectTrigger className="h-9 w-full rounded-xl bg-background max-sm:col-span-3 max-sm:row-start-3" aria-label="设备种类"><SelectValue /></SelectTrigger>
        <SelectContent>{Object.entries(kinds).map(([k, m]) => <SelectItem key={k} value={k}>{m.label}<span className="ml-1.5 font-mono text-[11.5px] text-muted-foreground">{m.code}</span></SelectItem>)}</SelectContent>
      </Select>
      <Num value={st.cycle} placeholder={kind ? String(kind.cycle) : ''} onChange={(v) => onChange({ ...st, cycle: v })} label="节拍（秒）" />
      <span className="flex">
        {onMove && <>
          <Button variant="ghost" size="icon-sm" aria-label="往前挪" disabled={i === 0} onClick={() => onMove(-1)}><ArrowUpIcon /></Button>
          <Button variant="ghost" size="icon-sm" aria-label="往后挪" disabled={i === n - 1} onClick={() => onMove(1)}><ArrowDownIcon /></Button>
        </>}
        {onRemove && <Button variant="ghost" size="icon-sm" aria-label="去掉这个工位" disabled={n <= 1} className="hover:bg-danger-soft hover:text-destructive" onClick={onRemove}><Trash2Icon /></Button>}
      </span>
    </div>
  );
}

function LineForm({ view, draft, onDraft, pack }: FormProps) {
  const { scope, model } = view;
  const p = plantOf(view, draft);
  const l: Cfg = p.lines[scope.line];
  const set = (f: (l: Cfg) => void) => { const d = copy(draft); f(d.plants[scope.plant].lines[scope.line]); onDraft(d); };
  const lp = model.plants[scope.plant];
  const kinds = model.machines[lp.industry] ?? {};
  const first = lp.lines.find((x) => x.cfg === scope.line);
  // tags as they are now, for the steps still where they were
  const was = (model.config.plants[scope.plant].lines[scope.line].steps ?? []) as Cfg[];
  const tagOf = (st: Cfg, i: number) => (was[i] && JSON.stringify(was[i]) === JSON.stringify(st) ? first?.stations[i]?.id : undefined);
  const areas: Cfg[] = p.areas ?? [];
  const [kind, setKind] = useState(Object.keys(kinds)[0] ?? '');
  const [at, setAt] = useState<string>('end');
  const steps: Cfg[] = l.steps ?? [];
  const slowest = first ? first.stations.reduce((m, s) => (s.cycle > m.cycle ? s : m), first.stations[0]) : undefined;
  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_minmax(0,1fr)] gap-3 max-sm:grid-cols-2">
        <Field label="产线名称" hint={l.repeat > 1 ? '{n} 换成第几条' : undefined}><Input value={l.name} onChange={(e) => set((x) => { x.name = e.target.value; })} className="h-9 rounded-xl" /></Field>
        <Field label="生产什么">
          <Select value={l.product} onValueChange={(v) => set((x) => { x.product = v; })}>
            <SelectTrigger className="h-9 w-full rounded-xl" aria-label="生产什么"><SelectValue /></SelectTrigger>
            <SelectContent>{Object.entries(pack?.products ?? { [l.product]: { name: l.product } }).map(([k, x]) => <SelectItem key={k} value={k}>{(x as { name: string }).name}</SelectItem>)}</SelectContent>
          </Select>
        </Field>
        {areas.length > 1 && (
          <Field label="在哪个车间">
            <Select value={l.area?.trim() || areas[0].name} onValueChange={(v) => set((x) => { x.area = v; })}>
              <SelectTrigger className="h-9 w-full rounded-xl" aria-label="在哪个车间"><SelectValue /></SelectTrigger>
              <SelectContent>{areas.map((a) => <SelectItem key={a.name} value={a.name}>{a.name}</SelectItem>)}</SelectContent>
            </Select>
          </Field>
        )}
        <Field label="传送带速度（米/秒）" hint={first ? `现在 ${first.speed}` : undefined}><Num value={l.speed} step={0.05} placeholder={first ? String(first.speed) : ''} onChange={(v) => set((x) => { x.speed = v; })} label="传送带速度" /></Field>
        <Field label="备注"><Input value={l.note ?? ''} onChange={(e) => set((x) => { x.note = e.target.value; })} className="h-9 rounded-xl" /></Field>
      </div>
      <div className="grid gap-2">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span className="text-[12.5px] font-bold">工位</span>
          {slowest && <small className="text-[12px] text-muted-foreground">整条线的节拍看最慢的工位：现在是「{slowest.name}」{slowest.cycle} 秒</small>}
        </div>
        <div className="grid grid-cols-[52px_minmax(0,1.1fr)_minmax(0,1fr)_86px_auto] gap-2 px-1.5 text-[12px] font-bold text-muted-foreground max-sm:hidden">
          <span>位号</span><span>名称</span><span>设备种类</span><span>节拍（秒）</span><span className="w-24" />
        </div>
        {steps.map((st, i) => (
          <StepRow key={i} st={st} i={i} n={steps.length} tag={tagOf(st, i)} kinds={kinds}
            onChange={(s) => set((x) => { x.steps[i] = s; })}
            onMove={(by) => set((x) => { const [s] = x.steps.splice(i, 1); x.steps.splice(i + by, 0, s); })}
            onRemove={() => set((x) => { x.steps.splice(i, 1); })} />
        ))}
        <div className="flex flex-wrap items-center gap-2 rounded-xl border border-dashed p-2">
          <Select value={kind} onValueChange={setKind}>
            <SelectTrigger className="h-9 min-w-0 flex-1 basis-44 rounded-xl" aria-label="加什么设备"><SelectValue placeholder="选设备种类" /></SelectTrigger>
            <SelectContent>{Object.entries(kinds).map(([k, m]) => <SelectItem key={k} value={k}>{m.label}</SelectItem>)}</SelectContent>
          </Select>
          <Select value={at} onValueChange={setAt}>
            <SelectTrigger className="h-9 min-w-0 flex-1 basis-44 rounded-xl" aria-label="加在哪里"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="end">加在最后（已有位号都不变）</SelectItem>
              {steps.map((s, i) => <SelectItem key={i} value={String(i)}>加在「{s.name ?? kinds[s.machine]?.label ?? s.machine}」前面</SelectItem>)}
            </SelectContent>
          </Select>
          <Button variant="outline" className="h-9 rounded-xl font-bold" disabled={!kind} onClick={() => set((x) => {
            const s = { machine: kind, name: kinds[kind]?.label };
            if (at === 'end') x.steps.push(s); else x.steps.splice(Number(at), 0, s);
          })}><PlusIcon />加一个工位</Button>
        </div>
        <small className="text-[12px] text-muted-foreground">位号按「设备编码-线号+这种设备在线上的第几台」编。加在中间时，排在后面的同种设备位号会往后移，预览里会列出来。</small>
      </div>
    </div>
  );
}

function MachineForm({ view, draft, onDraft }: FormProps) {
  const { scope, model } = view;
  const lp = model.plants[scope.plant];
  const kinds = model.machines[lp.industry] ?? {};
  const st: Cfg = draft.plants[scope.plant].lines[scope.line].steps[scope.step];
  const now = lp.lines.find((x) => x.cfg === scope.line)?.stations[scope.step];
  const kind = kinds[st.machine];
  const set = (f: (s: Cfg) => void) => { const d = copy(draft); f(d.plants[scope.plant].lines[scope.line].steps[scope.step]); onDraft(d); };
  const heat = (k: 'warn' | 'trip', v: number | undefined) => set((s) => {
    s.thermal = { ...s.thermal, [k]: v };
    if (s.thermal[k] === undefined) delete s.thermal[k];
    if (!Object.keys(s.thermal).length) delete s.thermal;
  });
  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-2 gap-3">
        <Field label="名称"><Input value={st.name ?? ''} placeholder={kind?.label} onChange={(e) => set((s) => { s.name = e.target.value || undefined; })} className="h-9 rounded-xl" /></Field>
        <Field label="设备种类" hint={st.machine !== model.config.plants[scope.plant].lines[scope.line].steps[scope.step].machine ? '换了种类，编码跟着变，位号也会变' : undefined}>
          <Select value={st.machine} onValueChange={(v) => set((s) => { s.machine = v; })}>
            <SelectTrigger className="h-9 w-full rounded-xl" aria-label="设备种类"><SelectValue /></SelectTrigger>
            <SelectContent>{Object.entries(kinds).map(([k, m]) => <SelectItem key={k} value={k}>{m.label}<span className="ml-1.5 font-mono text-[11.5px] text-muted-foreground">{m.code}</span></SelectItem>)}</SelectContent>
          </Select>
        </Field>
        <Field label="节拍（秒）" hint={kind ? `这种设备默认 ${kind.cycle} 秒` : undefined}>
          <Num value={st.cycle} placeholder={now ? String(now.cycle) : ''} onChange={(v) => set((s) => { s.cycle = v; })} label="节拍" />
        </Field>
        <span className="hidden max-sm:hidden sm:block" />
        <Field label="报警温度（°C）" hint={kind ? `运行时大约 ${kind.run}°C，报警至少要高 6°C；这种设备默认 ${kind.warn}°C` : undefined}>
          <Num value={st.thermal?.warn} placeholder={now ? String(now.warn) : ''} onChange={(v) => heat('warn', v)} label="报警温度" />
        </Field>
        <Field label="停机温度（°C）" hint={kind ? `要比报警温度高；默认 ${kind.trip}°C` : undefined}>
          <Num value={st.thermal?.trip} placeholder={now ? String(now.trip) : ''} onChange={(v) => heat('trip', v)} label="停机温度" />
        </Field>
      </div>
      <p className="text-[12.5px] text-muted-foreground">
        外形是这种设备共用的，要改它长什么样，到<a {...link(`/admin/industries/${encodeURIComponent(lp.industry)}`)} className="font-bold text-primary hover:underline">行业库</a>里让 AI 画外形。
      </p>
    </div>
  );
}

// ---------- the AI ----------

function AskAI({ company, view, onProposal }: { company: string; view: api.ScopeView; onProposal(r: api.ScopeResult | null): void }) {
  const [brief, setBrief] = useState('');
  const [steps, setSteps] = useState<Step[]>([]);
  const [answer, setAnswer] = useState('');
  const [error, setError] = useState<string>();
  const [refused, setRefused] = useState<string[]>([]);
  const [thinking, setThinking] = useState(false);
  const [ctrl, setCtrl] = useState<AbortController | null>(null);
  const run = async () => {
    const text = brief.trim();
    if (!text || ctrl) return;
    const c = new AbortController();
    setCtrl(c); setSteps([]); setAnswer(''); setError(undefined); setRefused([]); setThinking(true);
    onProposal(null);
    try {
      await api.scopeAI(company, view.scope.node, text, c.signal, (ev) => {
        if (ev.kind !== 'thinking') setThinking(false);
        switch (ev.kind) {
          case 'tool_call': setSteps((s) => [...s, { tool: ev.tool, done: false }]); break;
          case 'tool_result': setSteps((s) => {
            const out = [...s];
            const i = out.findLastIndex((x) => x.tool === ev.tool && !x.done);
            const r = typeof ev.result === 'string' ? ev.result : '';
            if (i >= 0) out[i] = { ...out[i], done: true, failed: r && !r.startsWith('{') ? r : undefined };
            return out;
          }); break;
          case 'proposal': onProposal(ev.result); break;
          case 'refused': setRefused((r) => [...new Set([...r, ...ev.result])]); break;
          case 'delta': setAnswer((a) => a + ev.text); break;
          case 'reset': setAnswer(''); break;
          case 'complete': if (ev.text) setAnswer(ev.text); break;
          case 'error': setError(ev.text); break;
        }
      });
    } catch (e) {
      if (!c.signal.aborted) setError((e as Error).message);
    } finally {
      setCtrl(null); setThinking(false);
    }
  };
  const busy = !!ctrl;
  const example = { plant: '加一个成品库车间，放在最后', area: '改名叫精整车间，备注写外协', line: '在这条线最后加一台在线检测', machine: '把报警温度改成 80' }[view.scope.node.kind];
  return (
    <div className="grid gap-3">
      <Textarea value={brief} onChange={(e) => setBrief(e.target.value)} rows={3} disabled={busy} aria-label="告诉 AI 这里要怎么改"
        onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); run(); } }}
        placeholder={`比如：${example}`} className="min-h-[84px] resize-none rounded-xl text-[14px] leading-relaxed" />
      <div className="flex items-center gap-2">
        <span className="text-[12px] text-muted-foreground">AI 能看到整个公司，但只会改{view.scope.title}；碰到别处的改动会被拦下</span>
        {busy
          ? <Button variant="outline" className="ml-auto h-9 shrink-0 rounded-xl font-bold" onClick={() => ctrl?.abort()}><CircleStopIcon />停下</Button>
          : <Button className="ml-auto h-9 shrink-0 rounded-xl font-bold" disabled={!brief.trim()} onClick={run}><SparklesIcon />让 AI 改</Button>}
      </div>
      {(steps.length > 0 || busy || answer || error || refused.length > 0) && (
        <div className="grid gap-2 rounded-xl bg-surface/60 px-4 py-3">
          <ol className="grid gap-1">
            {thinking && <li className="flex items-center gap-2 text-[12.5px] font-bold"><LoaderCircleIcon className="size-3.5 animate-spin text-primary" />{THINKING}</li>}
            {stepLines(steps).map((st, i) => (
              <li key={i} className="flex items-start gap-2 text-[12.5px]">
                {st.state === 'doing' ? <LoaderCircleIcon className="mt-0.5 size-3.5 shrink-0 animate-spin text-primary" />
                  : st.state === 'failed' ? <CircleAlertIcon className="mt-0.5 size-3.5 shrink-0 text-destructive" />
                    : <CheckIcon className="mt-0.5 size-3.5 shrink-0 text-success" />}
                <span className="min-w-0">
                  <span className={cn('font-bold', st.state === 'failed' && 'text-destructive')}>{st.text}</span>
                  {st.detail && <span className="block whitespace-pre-line break-words text-muted-foreground">{st.detail}</span>}
                </span>
              </li>
            ))}
          </ol>
          {refused.length > 0 && (
            <div className="flex gap-2 rounded-lg bg-danger-soft px-3 py-2 text-[12.5px] text-red-800">
              <ShieldAlertIcon className="mt-0.5 size-4 shrink-0" />
              <span>AI 想改的地方超出了{view.scope.title}，已经拦下，没有采用：{refused.join('；')}。要改那里，回到工厂结构里选中它再改。</span>
            </div>
          )}
          {answer && <p className="whitespace-pre-line text-[13.5px] leading-relaxed">{answer}</p>}
          {error && <p className="text-[13px] text-destructive">{error}</p>}
        </div>
      )}
    </div>
  );
}

// ---------- preview and save ----------

function PreviewCard({ r, busy, onSave, onDiscard }: { r: api.ScopeResult; busy: boolean; onSave(): void; onDiscard(): void }) {
  const moved = r.preview.tags.filter((t) => t.to);
  const gone = r.preview.tags.filter((t) => !t.to);
  return (
    <Panel title="保存前看一眼" aside={<span className="text-[12.5px] text-muted-foreground">{r.preview.changes.length} 处改动</span>}>
      {r.preview.changes.length === 0
        ? <p className="text-[13px] text-muted-foreground">和现在一样，没有要保存的改动。</p>
        : (
          <ul className="grid gap-1.5">
            {r.preview.changes.map((c, i) => <li key={i} className="flex gap-2 text-[13.5px] leading-snug"><span className="mt-[7px] size-1.5 shrink-0 rounded-full bg-primary" />{c}</li>)}
          </ul>
        )}
      {(moved.length > 0 || gone.length > 0) && (
        <div className="mt-4 rounded-xl border border-warning/40 bg-warning-soft px-3.5 py-3 text-[13px] leading-relaxed">
          <b className="flex items-center gap-1.5 text-[13.5px]"><TagIcon className="size-4" />位号会变</b>
          <ul className="mt-1.5 grid gap-1">
            {moved.map((t) => (
              <li key={t.from}><span className="font-mono font-bold">{t.from} → {t.to}</span>（{t.line}「{t.name}」）{t.attached?.length ? <b className="text-destructive">：{t.attached.join('，')}</b> : ''}</li>
            ))}
            {gone.map((t) => (
              <li key={t.from}><span className="font-mono font-bold">{t.from}</span> 不再有（{t.line}「{t.name}」）{t.attached?.length ? <b className="text-destructive">：{t.attached.join('，')}</b> : ''}</li>
            ))}
          </ul>
          <p className="mt-1.5 text-muted-foreground">接入的设备、历史读数、点检记录和工单都按位号记，旧位号上的这些不会跟着搬过去。保存后到「设备接入」里把设备改到新位号上。</p>
        </div>
      )}
      {r.preview.added.length > 0 && <p className="mt-3 text-[12.5px] text-muted-foreground">新的位号：<span className="font-mono">{r.preview.added.join('、')}</span></p>}
      {r.shared && <p className="mt-3 text-[12.5px] text-muted-foreground">{r.shared}</p>}
      <div className="mt-4 flex flex-wrap justify-end gap-2">
        <Button variant="ghost" className="h-10 rounded-xl font-bold" onClick={onDiscard} disabled={busy}>不要了</Button>
        <Button className="h-10 rounded-xl font-bold" onClick={onSave} disabled={busy || !r.preview.changes.length}>
          {busy ? <LoaderCircleIcon className="animate-spin" /> : <CheckIcon />}确认保存
        </Button>
      </div>
    </Panel>
  );
}

/** The 撤销上次修改 button: back to the config before the last save (of any kind). */
export function UndoButton({ company, versions, onDone, className }: { company: string; versions: number; onDone?(): void; className?: string }) {
  const [busy, setBusy] = useState(false);
  if (!versions) return null;
  return (
    <Button variant="outline" className={cn('h-10 rounded-xl font-bold', className)} disabled={busy} onClick={async () => {
      setBusy(true);
      try {
        await api.undoCompany(company);
        forget(company);
        toast.success('已撤销上次修改，孪生回到了改之前的样子');
        onDone?.();
      } catch (e) {
        toast.error((e as Error).message);
      } finally {
        setBusy(false);
      }
    }}><Undo2Icon />撤销上次修改</Button>
  );
}

export default function ScopeEditor({ company }: { company: string }) {
  const path = usePath();
  const node = useMemo(() => nodeOf(query(path)), [path]);
  const { ov, reload } = useConsole();
  const c = ov.companies.find((x) => x.id === company);
  const [view, setView] = useState<api.ScopeView | null>(null);
  const [error, setError] = useState<string>();
  const [draft, setDraft] = useState<Cfg | null>(null);
  const [pack, setPack] = useState<IndustryCfg>();
  const [mode, setMode] = useState<'form' | 'ai'>('form');
  const [result, setResult] = useState<api.ScopeResult | null>(null);
  const [problems, setProblems] = useState<{ msg: string; list: string[]; scope: boolean } | null>(null);
  const [busy, setBusy] = useState(false);
  const [n, setN] = useState(0); // reloads

  const key = JSON.stringify(node);
  useEffect(() => {
    let live = true;
    setError(undefined);
    api.scope(company, node).then((v) => {
      if (!live) return;
      setView(v); setDraft(copy(v.model.config)); setResult(null); setProblems(null);
      api.industry(v.model.plants[v.scope.plant].industry).then((p) => live && setPack(p), () => {});
    }, (e: Error) => live && setError(e.message));
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [company, key, n]);

  const fail = (e: unknown) => {
    const err = e as api.ApiError;
    setResult(null);
    setProblems({ msg: err.message, list: err.problems ?? [], scope: err.message.startsWith('只能改') });
  };
  const preview = async () => {
    if (!view || !draft) return;
    setBusy(true); setProblems(null);
    try {
      setResult(await api.scopePreview(company, { node: view.scope.node, base: view.base, config: draft }));
    } catch (e) { fail(e); } finally { setBusy(false); }
  };
  const save = async () => {
    if (!view || !result) return;
    setBusy(true);
    try {
      const r = await api.scopeApply(company, { node: view.scope.node, base: view.base, config: result.config });
      forget(company);
      await reload();
      toast.success(`${view.scope.title}已经改好，孪生里已经换上`);
      // the machine picked may have a new tag now
      const moved = node.asset && r.preview.tags.find((t) => t.from === node.asset && t.to);
      if (moved) go(api.scopeHref(company, { ...node, asset: moved.to }), true);
      else setN((x) => x + 1);
    } catch (e) { fail(e); } finally { setBusy(false); }
  };

  const back = { to: `/admin/customers/${encodeURIComponent(company)}`, label: c?.name ?? company };
  if (error) return <PageHead title="打不开要改的地方" sub={error} back={back} />;
  if (!view || !draft) return <PageHead title="正在读取…" back={back} />;
  const { scope, model } = view;
  const lp = model.plants[scope.plant];
  const crumbs = [c?.name ?? company, `工厂「${lp.name}」`];
  const lineArea = scope.line >= 0 ? lp.lines.find((l) => l.cfg === scope.line)?.area : node.area;
  const area = lp.areas.find((a) => a.code === lineArea);
  if (area && scope.node.kind !== 'area') crumbs.push(`车间「${area.name}」`);
  if (scope.node.kind === 'machine') crumbs.push(`产线「${lp.lines.find((l) => l.cfg === scope.line)?.name}」`);
  const twin = `/?company=${encodeURIComponent(company)}&plant=${encodeURIComponent(lp.id)}${node.asset ? `&asset=${encodeURIComponent(node.asset)}` : ''}`;
  const changed = JSON.stringify(draft) !== JSON.stringify(model.config);
  const Form = { plant: PlantForm, area: AreaForm, line: LineForm, machine: MachineForm }[scope.node.kind];

  return (
    <>
      <PageHead title={<span className="flex flex-wrap items-center gap-2.5"><span className="rounded-lg bg-brand-soft px-2 py-0.5 text-[13px] font-bold text-primary">修改{KIND[scope.node.kind]}</span>{scope.title}</span>}
        sub={crumbs.join(' › ')} back={back}>
        <Button asChild variant="outline" className="h-10 rounded-xl font-bold"><a href={twin}>在孪生里看<ExternalLinkIcon /></a></Button>
        <UndoButton company={company} versions={view.versions} onDone={async () => { await reload(); setN((x) => x + 1); }} />
      </PageHead>
      <div className="grid gap-5">
        {scope.shared && <p className="rounded-xl bg-brand-soft px-4 py-2.5 text-[13px] text-primary">{scope.shared}</p>}
        <Panel>
          <ToggleGroup type="single" value={mode} onValueChange={(v) => v && setMode(v as 'form' | 'ai')} variant="outline" className="mb-4 w-full max-w-md" aria-label="怎么改">
            <ToggleGroupItem value="form" className="h-9 flex-1 font-bold">用表单改</ToggleGroupItem>
            <ToggleGroupItem value="ai" className="h-9 flex-1 font-bold" disabled={!ov.model} title={ov.model ? undefined : '服务器还没有配置大模型'}><SparklesIcon />告诉 AI 这里要怎么改</ToggleGroupItem>
          </ToggleGroup>
          {mode === 'form' ? (
            <>
              <Form view={view} draft={draft} onDraft={(d) => { setDraft(d); setResult(null); setProblems(null); }} pack={pack} />
              <div className="mt-5 flex flex-wrap items-center justify-end gap-2">
                {changed && <Button variant="ghost" className="h-10 rounded-xl font-bold" onClick={() => { setDraft(copy(model.config)); setResult(null); setProblems(null); }}>还原</Button>}
                <Button className="h-10 rounded-xl font-bold" disabled={!changed || busy} onClick={preview}>
                  {busy && !result ? <LoaderCircleIcon className="animate-spin" /> : null}看看会改什么
                </Button>
              </div>
            </>
          ) : (
            <AskAI company={company} view={view} onProposal={(r) => { setResult(r); setProblems(null); }} />
          )}
        </Panel>
        {problems && (
          <div className={cn('flex gap-2.5 rounded-[18px] px-4 py-3 text-[13px] leading-relaxed', problems.scope ? 'bg-danger-soft text-red-800' : 'bg-danger-soft text-red-800')}>
            {problems.scope ? <ShieldAlertIcon className="mt-0.5 size-4 shrink-0" /> : <CircleAlertIcon className="mt-0.5 size-4 shrink-0" />}
            <div className="min-w-0">
              <b>{problems.scope ? '超出了要改的范围，没有采用' : '这样改通不过检查'}</b>
              <p className="break-words">{problems.msg}</p>
              {!problems.scope && problems.list.length > 1 && <ul className="mt-1 list-disc pl-4">{problems.list.map((x, i) => <li key={i}>{x}</li>)}</ul>}
            </div>
          </div>
        )}
        {result && <PreviewCard r={result} busy={busy} onSave={save} onDiscard={() => setResult(null)} />}
      </div>
    </>
  );
}
