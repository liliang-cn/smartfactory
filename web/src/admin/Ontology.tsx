// 本体: a company's ontology — the kinds of things its industry declares and how many of each there
// are (given and inferred), how they relate, the rules that follow, the constraints and where they
// stand (a machine whose reading is out of range now, a thing missing what it must have) — and a
// card for one class or thing with its facts, each inferred one with why.
import { useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';
import { BoxIcon, ChevronRightIcon, LoaderCircleIcon, PlayIcon, ShapesIcon, WorkflowIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import * as api from './api';
import { KIND, type Kind } from './GraphView';

const kindLabel = (k: string) => (k === 'class' ? '类' : k === 'thing' ? '本体里的东西' : KIND[k as Kind]?.label ?? k);

/** A name to open: a class or a thing, dashed when it is so by inference. */
function Name({ name, inferred, kind, onPick }: { name: string; inferred?: boolean; kind?: string; onPick(name: string): void }) {
  return (
    <button type="button" onClick={() => onPick(name)} title={inferred ? '推理得出' : undefined}
      className={cn('inline-flex max-w-full items-center gap-1.5 rounded-lg px-2 py-0.5 text-[12.5px] font-bold hover:bg-brand-soft hover:text-primary',
        inferred ? 'border border-dashed border-[color-mix(in_oklab,var(--primary)_45%,transparent)] text-foreground/85' : 'bg-surface')}>
      {kind && kind !== 'thing' && kind !== 'class' && KIND[kind as Kind] && <span className="size-2 shrink-0 rounded-full" style={{ background: KIND[kind as Kind].color }} />}
      <span className="truncate">{name}</span>
    </button>
  );
}

/** Why an inferred fact holds, a step a line, the arrow standing out. */
function Why({ steps }: { steps: string[] }) {
  return (
    <ol className="grid gap-1 rounded-lg bg-surface px-2.5 py-2 text-[12px] leading-relaxed text-muted-foreground">
      {steps.map((s, i) => {
        const [from, to] = s.split(' → ');
        return (
          <li key={i} className="break-words">
            {from}{to != null && <> <b className="text-primary">→</b> <span className="font-bold text-foreground">{to}</span></>}
          </li>
        );
      })}
    </ol>
  );
}

/** One fact seen from me: what relates me to what (or what relates to me), and why when inferred. */
function Fact({ f, me, onPick }: { f: api.OntFact; me: string; onPick(name: string): void }) {
  const [open, setOpen] = useState(false);
  const mine = f.subject === me;
  const other = mine ? f.object : f.subject;
  return (
    <li className="grid gap-1">
      <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[12.5px]">
        {mine && f.relation ? <span className="text-muted-foreground">{f.relation}</span> : null}
        <Name name={other} inferred={f.inferred} onPick={onPick} />
        {!mine ? <span className="text-muted-foreground">{f.relation} 它</span> : null}
        {f.inferred && (
          <button type="button" onClick={() => setOpen(!open)} className={cn('rounded-md px-1.5 py-px text-[11px] font-bold', open ? 'bg-primary text-primary-foreground' : 'bg-brand-soft text-primary')}>
            推理{open ? '' : ' · 为什么'}
          </button>
        )}
      </div>
      {open && f.why && f.why.length > 0 && <Why steps={f.why} />}
    </li>
  );
}

function Section({ title, children, count }: { title: string; count?: number; children: React.ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <span className="text-[12px] font-bold text-muted-foreground">{title}{count != null && <span className="ml-1 font-normal tabular-nums">{count}</span>}</span>
      {children}
    </div>
  );
}

/** The things of a class, optionally only those with a relation to a value. */
function Instances({ company, cls, first, onPick }: { company: string; cls: string; first: api.OntologyInstances; onPick(name: string): void }) {
  const [rel, setRel] = useState('');
  const [value, setValue] = useState('');
  const [list, setList] = useState(first);
  const [busy, setBusy] = useState(false);
  useEffect(() => setList(first), [first]);
  const filter = async () => {
    setBusy(true);
    try {
      setList(await api.ontologyInstances(company, cls, rel.trim(), value.trim()));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const given = list.instances.filter((x) => !x.inferred).length;
  return (
    <Section title={`${cls}有这些`} count={list.total}>
      <form className="flex gap-1.5" onSubmit={(e) => { e.preventDefault(); filter(); }}>
        <Input value={rel} onChange={(e) => setRel(e.target.value)} placeholder="关系，如 含过敏原" className="h-8 min-w-0 flex-1 rounded-lg text-[12.5px]" aria-label="按关系筛选" />
        <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder="值" className="h-8 min-w-0 flex-1 rounded-lg text-[12.5px]" aria-label="关系的值" />
        <Button type="submit" variant="outline" size="sm" className="h-8 rounded-lg" disabled={busy || (!!rel.trim() !== !!value.trim())}>{busy ? <LoaderCircleIcon className="animate-spin" /> : '筛'}</Button>
      </form>
      {list.relation && <p className="text-[12px] text-muted-foreground">{list.relation} 是 {list.value} 的：</p>}
      <div className="flex flex-wrap gap-1.5">
        {list.instances.map((x) => <Name key={x.name} name={x.name} kind={x.kind} inferred={x.inferred || !!x.fact?.inferred} onPick={onPick} />)}
        {list.total > list.instances.length && <span className="text-[12px] text-muted-foreground">等 {list.total} 个</span>}
        {!list.instances.length && <span className="text-[12.5px] text-muted-foreground">没有</span>}
      </div>
      {list.instances.length > 0 && <p className="text-[11.5px] text-muted-foreground">实线框是直接给定的（{given} 个），虚线框是推理出来的</p>}
    </Section>
  );
}

/** A class or a thing with what the ontology says about it. */
export function OntologyCard({ company, name, onPick, onGraph }: { company: string; name: string; onPick(name: string): void; onGraph?(name: string): void }) {
  const [t, setT] = useState<api.OntologyThing | null>(null);
  const [error, setError] = useState<string>();
  const top = useRef<HTMLDivElement>(null);
  useEffect(() => {
    setT(null);
    setError(undefined);
    api.ontologyThing(company, name).then(setT, (e: Error) => setError(e.message));
    // below the ontology on a narrow screen: brought into view
    if (!matchMedia('(min-width: 80rem)').matches) top.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, [company, name]);
  if (error) return <p ref={top} className="text-[13px] text-muted-foreground">{error}</p>;
  if (!t) return <div ref={top} className="grid h-32 place-items-center"><LoaderCircleIcon className="animate-spin text-muted-foreground" /></div>;
  const isClass = t.kind === 'class';
  const inGraph = !isClass && t.kind !== 'thing';
  const inferred = [...(t.classes ?? []), ...(t.facts ?? []), ...(t.back ?? []), ...(t.super ?? [])].filter((f) => f.inferred).length;
  return (
    <div ref={top} className="grid scroll-mt-4 gap-3.5">
      <div className="flex items-center gap-2.5">
        <span className="grid size-9 shrink-0 place-items-center rounded-xl text-white" style={{ background: isClass ? KIND.class.color : KIND[t.kind as Kind]?.color ?? '#5b6b85' }}>
          {isClass ? <ShapesIcon className="size-4" /> : <BoxIcon className="size-4" />}
        </span>
        <span className="grid min-w-0 leading-tight">
          <b className="truncate text-[15px] font-extrabold">{t.name}</b>
          <small className="text-[12px] text-muted-foreground">{kindLabel(t.kind)}{t.also?.length ? `，也叫 ${t.also.join('、')}` : ''}{inferred ? `，${inferred} 条是推理出的` : ''}</small>
        </span>
        {inGraph && onGraph && (
          <Button variant="ghost" size="sm" className="ml-auto shrink-0 rounded-lg text-[12px]" onClick={() => onGraph(t.name)}><WorkflowIcon />图谱</Button>
        )}
      </div>
      {t.note && <p className="text-[12.5px] text-muted-foreground">{t.note}</p>}
      {isClass ? (
        <>
          {!!t.super?.length && (
            <Section title="是一种">
              <ul className="grid gap-1.5">{t.super.map((f) => <Fact key={f.object} f={{ ...f, relation: '' }} me={t.name} onPick={onPick} />)}</ul>
            </Section>
          )}
          {!!t.subs?.length && <Section title="下面还分"><div className="flex flex-wrap gap-1.5">{t.subs.map((s) => <Name key={s} name={s} onPick={onPick} />)}</div></Section>}
          {!!t.relations?.length && <Section title="相关的关系"><ul className="grid gap-0.5 text-[12.5px]">{t.relations.map((r) => <li key={r}>{r}</li>)}</ul></Section>}
          {!!t.constraints?.length && <Section title="约束"><ul className="grid gap-0.5 text-[12.5px]">{t.constraints.map((r) => <li key={r}>{r}</li>)}</ul></Section>}
          {t.instances && <Instances company={company} cls={t.name} first={t.instances} onPick={onPick} />}
        </>
      ) : (
        <>
          <Section title="是">
            <ul className="grid gap-1.5">{(t.classes ?? []).map((f) => <Fact key={f.object} f={{ ...f, relation: '' }} me={t.name} onPick={onPick} />)}</ul>
          </Section>
          {!!t.facts?.length && <Section title="它的事实" count={t.facts.length}><ul className="grid gap-1.5">{t.facts.map((f) => <Fact key={f.relation + f.object} f={f} me={t.name} onPick={onPick} />)}</ul></Section>}
          {!!t.back?.length && <Section title="关于它" count={t.back.length}><ul className="grid gap-1.5">{t.back.map((f) => <Fact key={f.subject + f.relation} f={f} me={t.name} onPick={onPick} />)}</ul></Section>}
        </>
      )}
    </div>
  );
}

const time = (ms?: number) => (ms ? new Date(ms).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '');

function Constraints({ o, onPick }: { o: api.Ontology; onPick(name: string): void }) {
  if (!o.constraints.length) return null;
  return (
    <ul className="grid gap-2">
      {o.constraints.map((c) => {
        const bad = c.status === 'violated';
        return (
          <li key={c.id} className={cn('grid gap-1.5 rounded-xl border p-3', bad && 'border-[color-mix(in_oklab,var(--destructive)_35%,transparent)] bg-danger-soft/50')}>
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span className={cn('size-2.5 rounded-full', bad ? 'bg-destructive' : 'bg-success')} />
              <b className="text-[13px]">{c.id}</b>
              <span className={cn('rounded-md px-1.5 py-px text-[11px] font-bold', c.live ? 'bg-warning-soft text-warning' : 'bg-brand-soft text-primary')}>
                {c.live ? `实时 · 看着 ${c.watching} 台` : '检查模型'}
              </span>
              <span className={cn('ml-auto text-[12px] font-bold', bad ? 'text-destructive' : 'text-success')}>
                {bad ? (c.live ? `${c.violations.length} 台正超限` : `${c.violations.length} 处不满足`) : '都满足'}
              </span>
            </div>
            <p className="text-[12.5px] text-muted-foreground">{c.text}</p>
            {c.violations.length > 0 && (
              <ul className="grid gap-1">
                {c.violations.map((v, i) => (
                  <li key={i} className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[12.5px]">
                    <Name name={v.name} onPick={onPick} />
                    <span className="min-w-0 flex-1 text-destructive">{v.text}</span>
                    {v.since ? <span className="text-[11.5px] text-muted-foreground tabular-nums">{time(v.since)} 起</span> : null}
                  </li>
                ))}
              </ul>
            )}
          </li>
        );
      })}
    </ul>
  );
}

/** The classes as a tree: what is a kind of what, each with how many things are of it. */
function ClassTree({ o, picked, onPick }: { o: api.Ontology; picked: string | null; onPick(name: string): void }) {
  const byName = useMemo(() => new Map(o.classes.map((c) => [c.name, c])), [o]);
  const kids = useMemo(() => {
    const m = new Map<string, string[]>();
    for (const c of o.classes) for (const s of c.sub) if (byName.has(s)) m.set(s, [...(m.get(s) ?? []), c.name]);
    return m;
  }, [o, byName]);
  const roots = o.classes.filter((c) => !c.sub.some((s) => byName.has(s)));
  const [open, setOpen] = useState<Set<string>>(() => new Set(o.classes.filter((c) => kids.has(c.name)).map((c) => c.name)));
  const toggle = (n: string) => setOpen((s) => {
    const next = new Set(s);
    if (next.has(n)) next.delete(n);
    else next.add(n);
    return next;
  });
  const row = (name: string, depth: number, path: string): React.ReactNode => {
    const c = byName.get(name)!;
    const ch = kids.get(name) ?? [];
    const isOpen = open.has(name) && depth < 12;
    return (
      <li key={path}>
        <div className={cn('flex min-h-8 items-center gap-1 rounded-lg pr-2 hover:bg-surface', picked === name && 'bg-brand-soft')} style={{ paddingLeft: depth * 16 }}>
          <button type="button" onClick={() => toggle(name)} aria-label={isOpen ? '收起' : '展开'} disabled={!ch.length}
            className="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground disabled:opacity-0">
            <ChevronRightIcon className={cn('size-3.5 transition-transform', isOpen && 'rotate-90')} />
          </button>
          <button type="button" onClick={() => onPick(name)} className="flex min-w-0 flex-1 items-baseline gap-2 py-1 text-left">
            <b className={cn('shrink-0 text-[13px]', c.builtin && 'font-semibold text-foreground/80')}>{name}</b>
            {c.note && <span className="truncate text-[11.5px] text-muted-foreground">{c.note}</span>}
          </button>
          <span className="shrink-0 text-[12px] tabular-nums">
            {c.asserted > 0 && <b>{c.asserted}</b>}
            {c.inferred > 0 && <span className="ml-1 text-primary">+{c.inferred} 推理</span>}
            {c.asserted + c.inferred === 0 && <span className="text-muted-foreground/60">0</span>}
          </span>
        </div>
        {isOpen && ch.length > 0 && <ul>{ch.map((k) => row(k, depth + 1, `${path}/${k}`))}</ul>}
      </li>
    );
  };
  // built-in classes nothing is declared under, apart: they are the knowledge graph's kinds
  const plain = roots.filter((c) => c.builtin && !kids.has(c.name));
  return (
    <div className="grid gap-2">
      <ul className="grid">{roots.filter((c) => !plain.includes(c)).map((c) => row(c.name, 0, c.name))}</ul>
      {plain.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 border-t pt-2">
          <span className="text-[11.5px] text-muted-foreground">内置</span>
          {plain.map((c) => (
            <button key={c.name} type="button" onClick={() => onPick(c.name)} className="rounded-lg bg-surface px-2 py-0.5 text-[12px] hover:bg-brand-soft hover:text-primary">
              {c.name} <span className="tabular-nums text-muted-foreground">{c.asserted}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function Sparql({ company }: { company: string }) {
  const [q, setQ] = useState('SELECT ?x ?y WHERE { ?x a c:食材 . ?x r:含过敏原 ?y }');
  const [a, setA] = useState<api.SPARQLAnswer | null>(null);
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      setA(await api.ontologySPARQL(company, q));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="grid gap-2">
      <p className="text-[12px] text-muted-foreground">只读。可直接用 c:（类）、r:（关系）、t:（东西）、rdf:、rdfs:、owl:，推理出的事实也查得到。</p>
      <Textarea value={q} onChange={(e) => setQ(e.target.value)} rows={3} className="font-mono text-[12.5px]" aria-label="SPARQL" />
      <div><Button size="sm" className="rounded-lg" onClick={run} disabled={busy || !q.trim()}>{busy ? <LoaderCircleIcon className="animate-spin" /> : <PlayIcon />}查询</Button></div>
      {a && (a.boolean != null ? <p className="text-[13px] font-bold">{a.boolean ? '是' : '否'}</p> : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-[12.5px]">
            <thead className="bg-surface text-left"><tr>{a.vars.map((v) => <th key={v} className="px-2 py-1 font-bold">{v}</th>)}</tr></thead>
            <tbody>{a.rows.map((r, i) => <tr key={i} className="border-t">{a.vars.map((v) => <td key={v} className="px-2 py-1">{r[v]}</td>)}</tr>)}</tbody>
          </table>
          {!a.rows.length && <p className="p-2 text-[12.5px] text-muted-foreground">没有结果</p>}
          {a.cut && <p className="border-t p-2 text-[12px] text-muted-foreground">只列了前 {a.rows.length} 行</p>}
        </div>
      ))}
    </div>
  );
}

/** The ontology at a glance: constraints first, then the classes, relations and rules. */
export function OntologyView({ company, admin, picked, onPick }: { company: string; admin: boolean; picked: string | null; onPick(name: string): void }) {
  const [o, setO] = useState<api.Ontology | null>(null);
  const [error, setError] = useState<string>();
  const [builtins, setBuiltins] = useState(false);
  const [sparql, setSparql] = useState(false);
  useEffect(() => {
    setO(null);
    api.ontology(company).then(setO, (e: Error) => setError(e.message));
    // a live constraint's state changes with the readings
    const t = setInterval(() => api.ontology(company).then(setO, () => {}), 15000);
    return () => clearInterval(t);
  }, [company]);
  if (error) return <p className="text-[13px] text-destructive">{error}</p>;
  if (!o) return <div className="grid h-60 place-items-center"><LoaderCircleIcon className="animate-spin text-muted-foreground" /></div>;
  const own = o.relations.filter((r) => !r.builtin);
  const declared = o.classes.filter((c) => !c.builtin).length;
  return (
    <div className="grid gap-5">
      <p className="text-[12.5px] text-muted-foreground">
        {o.declared
          ? <>行业包声明了 <b className="text-foreground tabular-nums">{declared}</b> 个类、<b className="text-foreground tabular-nums">{own.length}</b> 个关系、<b className="text-foreground tabular-nums">{o.rules.length}</b> 条规则；给定的事实 <b className="text-foreground tabular-nums">{o.explicit}</b> 条，推理出 <b className="text-primary tabular-nums">{o.inferred}</b> 条。</>
          : '这家公司的行业包还没有声明本体，下面只有内置的类和关系。在行业包里加 ontology（类、关系、规则、约束）就有了。'}
      </p>
      {o.inconsistencies?.length ? <p className="rounded-lg bg-warning-soft px-3 py-2 text-[12.5px] text-warning">推理发现矛盾：{o.inconsistencies.join('；')}</p> : null}
      {o.constraints.length > 0 && <Section title="约束"><Constraints o={o} onPick={onPick} /></Section>}
      <Section title="类"><ClassTree o={o} picked={picked} onPick={onPick} /></Section>
      <Section title="关系">
        <ul className="grid gap-1.5">
          {own.map((r) => (
            <li key={r.name} className="grid gap-0.5 text-[12.5px]">
              <span><b>{r.name}</b><span className="ml-2 text-muted-foreground">{r.text}</span></span>
              {r.note && <span className="text-[11.5px] text-muted-foreground">{r.note}</span>}
            </li>
          ))}
          {!own.length && <li className="text-[12.5px] text-muted-foreground">没有声明关系</li>}
        </ul>
        <button type="button" onClick={() => setBuiltins(!builtins)} className="justify-self-start text-[12px] font-bold text-primary">{builtins ? '收起内置关系' : `内置关系 ${o.relations.length - own.length} 个`}</button>
        {builtins && <ul className="grid gap-0.5 text-[12px] text-muted-foreground">{o.relations.filter((r) => r.builtin).map((r) => <li key={r.name}>{r.text}</li>)}</ul>}
      </Section>
      {o.rules.length > 0 && (
        <Section title="规则">
          <ul className="grid gap-1.5">
            {o.rules.map((r) => (
              <li key={r.id} className="grid gap-0.5 text-[12.5px]">
                <span><b>{r.id}</b><span className="ml-2 font-mono text-[12px] text-muted-foreground">{r.text}</span></span>
                {r.note && <span className="text-[11.5px] text-muted-foreground">{r.note}</span>}
              </li>
            ))}
          </ul>
        </Section>
      )}
      {admin && o.declared && (
        <div className="grid gap-2 border-t pt-3">
          <button type="button" onClick={() => setSparql(!sparql)} className="justify-self-start text-[12px] font-bold text-primary">{sparql ? '收起 SPARQL' : '用 SPARQL 查'}</button>
          {sparql && <Sparql company={company} />}
        </div>
      )}
    </div>
  );
}
