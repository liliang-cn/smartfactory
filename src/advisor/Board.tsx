// 咨询档案: the advisor's engagement — goals, findings with their evidence, plans and their decisions.
import { useCallback, useEffect, useState, type ReactNode } from 'react';
import {
  ArrowRightIcon, CheckIcon, CircleAlertIcon, FlagIcon, GavelIcon, LoaderCircleIcon, RefreshCwIcon, RulerIcon, SendIcon, ShieldCheckIcon, TableIcon, XIcon,
} from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import { useLive } from '@/live/client';
import { AdvisorError, decide, getBoard, type Acceptance, type Board as BoardData, type Filter, type Finding, type GoalView, type Guard, type PlanView, type Verb } from './api';
import { day, dayTime, share, toNumber, type Model } from './model';
import { Empty, ResultTable, Skeleton, SqlFold } from './parts';

const STATE: Record<string, [string, string]> = {
  proposed: ['待采纳', 'bg-warning-soft text-warning'],
  adopted: ['实施中', 'bg-brand-soft text-primary'],
  rejected: ['已否决', 'bg-muted text-muted-foreground'],
  accepted: ['已验收', 'bg-success-soft text-success'],
};
const OUTCOME: Record<string, [string, string]> = {
  achieved: ['达成', 'bg-success-soft text-success'],
  missed: ['未达成', 'bg-danger-soft text-destructive'],
  guard_broken: ['护栏破了', 'bg-warning-soft text-warning'],
  no_data: ['无数据', 'bg-muted text-muted-foreground'],
};
const RULE: Record<string, string> = {
  self_approval: '不能采纳自己提出的计划', role_not_allowed: '角色无权决定', anonymous: '需要登录', already_decided: '计划已有决定',
  why_required: '需要填写理由', not_adopted: '计划尚未采纳', too_early: '还没到验收时间', already_accepted: '计划已验收', no_baseline: '量不出基线',
  not_found: '找不到记录', bad_action: '动作无法执行',
};
const COMMANDS: Record<string, string> = {
  toollimit: '计划换模点', retool: '立即换模', start: '启动', stop: '停机', reset: '复位', maint: '保养', maintenance: '保养', speed: '节拍', restock: '补料', ship: '发运',
};

const Pill = ({ c, children }: { c: string; children: ReactNode }) => (
  <span className={cn('inline-flex h-6 shrink-0 items-center rounded-full px-2.5 text-[12px] font-bold', c)}>{children}</span>
);

function Scope({ scope, model }: { scope?: Filter[]; model: Model }) {
  if (!scope?.length) return null;
  return (
    <span className="flex flex-wrap gap-1.5">
      {scope.map((f, i) => {
        const s = model.scope(f);
        return (
          <span key={i} className="inline-flex items-baseline gap-1 rounded-lg bg-muted px-2 py-0.5 text-[12px]">
            <span className="text-muted-foreground">{s.dim}</span><b className="font-semibold">{s.text}</b>
          </span>
        );
      })}
    </span>
  );
}

function Guards({ guards, model }: { guards?: Guard[]; model: Model }) {
  if (!guards?.length) return null;
  return (
    <span className="flex flex-wrap gap-1.5">
      {guards.map((g, i) => (
        <span key={i} className="inline-flex items-center gap-1 rounded-lg bg-warning-soft px-2 py-0.5 text-[12px] font-semibold text-warning">
          <ShieldCheckIcon className="size-3.5" />{model.guard(g)}
        </span>
      ))}
    </span>
  );
}

// ---- six stages ---------------------------------------------------------------------------

function stages(gv: GoalView, model: Model) {
  const plans = gv.plans ?? [];
  const findings = gv.findings ?? [];
  const queries = findings.reduce((n, f) => n + (f.evidence ?? []).length, 0);
  const adopted = plans.filter((p) => p.decision?.verdict === 'adopted');
  const accepted = plans.filter((p) => p.acceptance);
  const s = [
    { name: '目标', done: !!gv.goal.metric, note: gv.goal.metric ? model.label(gv.goal.metric) : '' },
    { name: '调查', done: queries > 0, note: queries ? `${queries} 次查询` : '' },
    { name: '结论', done: findings.length > 0, note: findings.length ? `${findings.length} 条` : '' },
    { name: '计划', done: plans.length > 0, note: plans.length ? `${plans.length} 个` : '' },
    { name: '实施', done: adopted.length > 0, note: adopted.length ? `采纳 ${adopted.length}` : '待采纳' },
    { name: '验收', done: accepted.length > 0, note: accepted.length ? OUTCOME[accepted[0].acceptance!.outcome]?.[0] ?? '' : adopted[0]?.due ? `${day(adopted[0].due)}` : '' },
  ];
  const i = s.findIndex((x) => !x.done);
  return s.map((x, j) => ({ ...x, now: j === i, note: i >= 0 && j > i ? '' : x.note }));
}

function Track({ gv, model }: { gv: GoalView; model: Model }) {
  const st = stages(gv, model);
  return (
    <ol className="grid grid-cols-6">
      {st.map((s, i) => (
        <li key={s.name} className="relative grid justify-items-center gap-1 text-center">
          {i > 0 && <span className={cn('absolute top-[15px] right-1/2 left-[-50%] h-[3px]', s.done || s.now ? 'bg-primary' : 'bg-[#e2e7f4]')} />}
          <span className={cn('relative grid size-8 place-items-center rounded-full text-[12px] font-extrabold tabular-nums',
            s.done ? 'bg-primary text-primary-foreground' : s.now ? 'bg-card text-primary ring-[3px] ring-primary' : 'bg-[#e9edf8] text-muted-foreground/70',
            s.now && 'shadow-[0_0_0_7px_#dbe4ff]')}>
            {s.done ? <CheckIcon className="size-4 stroke-[3]" /> : i + 1}
          </span>
          <b className={cn('mt-0.5 text-[12.5px]', s.now ? 'text-primary' : s.done ? '' : 'text-muted-foreground')}>{s.name}</b>
          <small className="min-h-[1.2em] w-full truncate px-0.5 text-[11px] text-muted-foreground max-sm:hidden">{s.note}</small>
        </li>
      ))}
    </ol>
  );
}

// ---- goal header ------------------------------------------------------------------------

function latest(gv: GoalView): Acceptance | undefined {
  for (const p of gv.plans ?? []) {
    if (p.plan.expect.metric !== gv.goal.metric) continue;
    const a = p.acceptance ?? p.progress;
    if (a?.measured?.value != null) return a;
  }
  return undefined;
}

function Numbers({ gv, model }: { gv: GoalView; model: Model }) {
  const g = gv.goal;
  const base = toNumber(g.baseline?.value);
  const target = toNumber(gv.target);
  const r = latest(gv);
  const now = toNumber(r?.measured?.value);
  const done = base !== null && target !== null && now !== null && base !== target ? Math.max(0, Math.min(1, (base - now) / (base - target))) : null;
  return (
    <div className="rounded-2xl bg-brand-soft p-4 lg:min-w-[320px]">
      <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
        <b className="text-accent-foreground">{model.label(g.metric)}</b>
        <span className="text-muted-foreground">{g.direction === 'down' ? '降' : '升'} {share(g.by)} · {g.within_days} 天</span>
      </div>
      <div className="mt-2 flex items-end gap-3">
        <div>
          <div className="text-[32px] leading-none font-extrabold tracking-tight tabular-nums max-md:text-[28px]">{model.fmt(g.metric, base)}</div>
          <div className="mt-1 text-[11.5px] text-muted-foreground">基线</div>
        </div>
        <ArrowRightIcon className="mb-5 size-5 shrink-0 text-primary/50" />
        <div>
          <div className="text-[32px] leading-none font-extrabold tracking-tight text-primary tabular-nums max-md:text-[28px]">{model.fmt(g.metric, target)}</div>
          <div className="mt-1 text-[11.5px] text-muted-foreground">目标</div>
        </div>
      </div>
      {g.baseline && <p className="mt-2.5 text-[11.5px] text-muted-foreground tabular-nums">{day(g.baseline.from)}–{day(g.baseline.to)} · {g.baseline.data_days} 天</p>}
      {r && now !== null && (
        <div className="mt-2.5 border-t border-primary/15 pt-2.5">
          <div className="flex items-baseline justify-between text-[12.5px]">
            <span>最新 <b className="tabular-nums">{model.fmt(g.metric, now)}</b></span>
            {done !== null && <b className={cn('tabular-nums', done > 0 ? 'text-primary' : 'text-muted-foreground')}>{Math.round(done * 100)}%</b>}
          </div>
          {done !== null && <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-primary/15"><i className="block h-full rounded-full bg-primary" style={{ width: `${done * 100}%` }} /></div>}
        </div>
      )}
      {g.baseline?.sql && <div className="mt-2"><SqlFold sql={g.baseline.sql} /></div>}
    </div>
  );
}

// ---- findings ----------------------------------------------------------------------------

function FindingCard({ f, model, flash }: { f: Finding; model: Model; flash: boolean }) {
  return (
    <article id={`finding-${f.id}`} className={cn('scroll-mt-28 rounded-2xl bg-card p-3.5 shadow-float transition-shadow', flash && 'ring-3 ring-primary/40')}>
      <div className="flex gap-2.5">
        <span className="grid h-6 min-w-6 shrink-0 place-items-center rounded-md bg-brand-soft px-1 text-[12px] font-extrabold text-primary">{f.id}</span>
        <p className="text-[13.5px] leading-relaxed">{f.says}</p>
      </div>
      {f.evidence?.length > 0 && (
        <ul className="mt-2.5 grid gap-1.5 pl-8.5 max-sm:pl-0">
          {f.evidence.map((e, i) => (
            <Collapsible asChild key={i}>
              <li>
                <CollapsibleTrigger className="group flex w-full items-center gap-2 rounded-xl bg-surface px-2.5 py-1.5 text-left outline-none hover:bg-brand-soft focus-visible:ring-2 focus-visible:ring-ring/40 data-[state=open]:bg-brand-soft">
                  <TableIcon className="size-3.5 shrink-0 text-primary" />
                  <span className="line-clamp-1 min-w-0 flex-1 text-[12.5px]">{e.asked || `证据 ${i + 1}`}</span>
                  <span className="shrink-0 text-[11.5px] text-muted-foreground tabular-nums">{e.row_count} 行</span>
                </CollapsibleTrigger>
                <CollapsibleContent className="grid gap-1.5 pt-2">
                  <ResultTable columns={e.columns ?? []} rows={e.rows ?? []} model={model} />
                  <SqlFold sql={e.sql} />
                </CollapsibleContent>
              </li>
            </Collapsible>
          ))}
        </ul>
      )}
    </article>
  );
}

// ---- plans --------------------------------------------------------------------------------

function Fact({ term, children }: { term: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[44px_minmax(0,1fr)] gap-2">
      <dt className="pt-0.5 text-[12px] text-muted-foreground">{term}</dt>
      <dd className="min-w-0 text-[13px] leading-relaxed">{children}</dd>
    </div>
  );
}

function reading(a: Acceptance, model: Model) {
  const now = toNumber(a.measured?.value);
  if (!a.measured || now === null) return `${model.label(a.metric)} 无数据`;
  return `${model.label(a.metric)} ${model.fmt(a.metric, a.baseline)} → ${model.fmt(a.metric, now)}（目标 ${model.fmt(a.metric, a.target)}，${a.measured.data_days} 天）`;
}

function Step({ icon, title, done, last, children }: { icon: ReactNode; title: string; done: boolean; last?: boolean; children: ReactNode }) {
  return (
    <li className="relative grid grid-cols-[26px_minmax(0,1fr)] gap-2.5 pb-4 last:pb-0">
      {!last && <span aria-hidden className={cn('absolute top-7 bottom-0.5 left-3 w-0.5 rounded-full', done ? 'bg-primary/35' : 'bg-border')} />}
      <span className={cn('grid size-[26px] place-items-center rounded-full', done ? 'bg-primary text-primary-foreground' : 'bg-card text-muted-foreground ring-2 ring-border')}>{icon}</span>
      <div className="min-w-0 pt-0.5 text-[12.5px] leading-relaxed">
        <b className="text-muted-foreground">{title}</b>
        <div className="mt-0.5">{children}</div>
      </div>
    </li>
  );
}

function Record({ pv, model }: { pv: PlanView; model: Model }) {
  const d = pv.decision;
  const acc = pv.acceptance;
  const left = pv.due ? Math.ceil((new Date(`${pv.due}T00:00:00`).getTime() - Date.now()) / 86400000) : null;
  return (
    <ol>
      <Step icon={<FlagIcon className="size-3.5" />} title="提出" done>
        <b>{pv.plan.by}</b> <span className="text-muted-foreground">{dayTime(pv.plan.at)}</span>
      </Step>
      <Step icon={<GavelIcon className="size-3.5" />} title={d?.verdict === 'rejected' ? '否决' : '采纳'} done={!!d}>
        {d ? (
          <>
            <b>{d.who}</b> <span className="text-muted-foreground">{dayTime(d.at)}</span>
            {d.why && <blockquote className="mt-1.5 rounded-r-lg border-l-[3px] border-primary/50 bg-card px-2.5 py-1.5">{d.why}</blockquote>}
            {d.baseline && (
              <div className="mt-1.5 flex justify-between gap-2 text-muted-foreground">
                <span>基线 {model.label(d.baseline.metric)}</span><b className="text-foreground tabular-nums">{model.fmt(d.baseline.metric, d.baseline.value)}</b>
              </div>
            )}
            {(d.guard_baselines ?? []).map((g, i) => (
              <div key={i} className="flex justify-between gap-2 text-muted-foreground">
                <span>护栏 {model.label(g.metric)}</span><b className="text-foreground tabular-nums">{model.fmt(g.metric, g.value)}</b>
              </div>
            ))}
          </>
        ) : <span className="text-muted-foreground">待定</span>}
      </Step>
      <Step icon={<RulerIcon className="size-3.5" />} title="验收" done={!!acc} last>
        {acc ? (
          <>
            <Pill c={OUTCOME[acc.outcome]?.[1] ?? 'bg-muted'}>{OUTCOME[acc.outcome]?.[0] ?? acc.outcome}</Pill>
            <p className="mt-1">{reading(acc, model)}</p>
            {(acc.guards ?? []).map((g, i) => (
              <p key={i} className={g.broken ? 'text-destructive' : 'text-muted-foreground'}>
                {model.label(g.guard.metric)} {model.fmt(g.guard.metric, g.baseline)} → {model.fmt(g.guard.metric, g.measured?.value)} {g.broken ? '✕' : '✓'}
              </p>
            ))}
          </>
        ) : pv.due && left !== null ? (
          <>
            <span className="flex items-baseline gap-1.5"><b className="text-[22px] leading-none font-extrabold tabular-nums">{Math.max(0, left)}</b><span className="text-muted-foreground">天 · {day(pv.due)}</span></span>
            {pv.progress && <p className="mt-1.5 rounded-lg bg-card px-2.5 py-1.5">{reading(pv.progress, model)}</p>}
          </>
        ) : <span className="text-muted-foreground">{pv.state === 'rejected' ? '—' : `采纳后 ${pv.plan.expect.within_days} 天`}</span>}
      </Step>
    </ol>
  );
}

function Actions({ pv }: { pv: PlanView }) {
  const actions = pv.plan.actions ?? [];
  if (!actions.length) return null;
  return (
    <ol className="grid gap-1.5">
      {actions.map((a, i) => {
        const run = (pv.actions ?? []).find((r) => r.index === i + 1) ?? pv.actions?.[i];
        const ok = run && !run.error && !run.skipped;
        const args = a.args ?? {};
        const chips = [
          args.asset != null && ['设备', String(args.asset)],
          args.line != null && ['产线', String(args.line)],
          args.command != null && ['指令', COMMANDS[String(args.command)] ?? String(args.command)],
          typeof args.arg === 'number' && ['参数', args.command === 'toollimit' || args.command === 'speed' ? `${Math.round(args.arg * 100)}%` : String(args.arg)],
        ].filter(Boolean) as [string, string][];
        return (
          <li key={i} className="flex gap-2.5 rounded-xl bg-surface p-2.5">
            <span className={cn('grid size-5 shrink-0 place-items-center rounded-full text-[11px] font-extrabold',
              ok ? 'bg-success text-white' : run?.error ? 'bg-destructive text-white' : 'bg-muted text-muted-foreground')}>
              {ok ? <CheckIcon className="size-3 stroke-[3]" /> : run?.error ? <XIcon className="size-3 stroke-[3]" /> : i + 1}
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap gap-1">
                {chips.map(([k, v]) => (
                  <span key={k} className="inline-flex items-baseline gap-1 rounded-md bg-card px-1.5 py-0.5 text-[11.5px]"><span className="text-muted-foreground">{k}</span><b>{v}</b></span>
                ))}
              </div>
              {a.why && <p className="mt-1 line-clamp-2 text-[12px] leading-relaxed text-muted-foreground">{a.why}</p>}
              {run?.error && <p className="mt-1 text-[12px] break-all text-destructive">{run.error}</p>}
              {ok && run?.result && <p className="mt-1 line-clamp-1 text-[12px] text-success">{run.result}</p>}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

type Ask = { verb: Verb; title: string; confirm: string; needWhy: boolean; danger?: boolean };

function usePhone() {
  const [phone, setPhone] = useState(() => window.matchMedia('(max-width: 767px)').matches);
  useEffect(() => {
    const m = window.matchMedia('(max-width: 767px)');
    const on = () => setPhone(m.matches);
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, []);
  return phone;
}

function Decisions({ company, pv, onChanged }: { company: string; pv: PlanView; onChanged: () => void }) {
  const [ask, setAsk] = useState<Ask | null>(null);
  const [why, setWhy] = useState('');
  const [busy, setBusy] = useState<Verb | null>(null);
  const [tried, setTried] = useState(false);
  const phone = usePhone();
  const id = pv.plan.id;
  async function run(verb: Verb, reason?: string) {
    setBusy(verb);
    try {
      await decide(company, id, verb, reason);
      toast.success({ adopt: `已采纳 ${id}`, reject: `已否决 ${id}`, measure: `已测量 ${id}`, accept: `已验收 ${id}` }[verb]);
      setAsk(null);
      setWhy('');
      onChanged();
    } catch (e) {
      const err = e as AdvisorError;
      // the refusal is final unless only the reason was missing; the toast must not sit under the sheet
      if (err.rule !== 'why_required') setAsk(null);
      toast.error(RULE[err.rule ?? ''] ?? (err.status === 403 ? '需要运营令牌' : '未执行'), { description: err.message });
    } finally {
      setBusy(null);
    }
  }
  if (pv.state !== 'proposed' && pv.state !== 'adopted') return null;
  return (
    <div className="flex flex-wrap justify-end gap-2 border-t bg-surface/70 px-4 py-2.5">
      {pv.state === 'proposed' ? (
        <>
          <Button variant="outline" className="h-9 rounded-xl px-4 text-destructive hover:text-destructive max-sm:flex-1"
            onClick={() => setAsk({ verb: 'reject', title: `否决 ${id}`, confirm: '否决', needWhy: true, danger: true })}>否决</Button>
          <Button className="h-9 rounded-xl px-4 font-bold max-sm:flex-1"
            onClick={() => setAsk({ verb: 'adopt', title: `采纳 ${id}`, confirm: pv.plan.actions?.length ? '采纳并下发' : '采纳', needWhy: false })}>
            <SendIcon />{pv.plan.actions?.length ? '采纳并下发' : '采纳'}
          </Button>
        </>
      ) : (
        <>
          <Button variant="outline" className="h-9 rounded-xl px-4 max-sm:flex-1" disabled={busy === 'measure'} onClick={() => run('measure')}>
            {busy === 'measure' ? <LoaderCircleIcon className="animate-spin" /> : <RulerIcon />}测量
          </Button>
          <Button variant="ghost" className="h-9 rounded-xl px-4 text-muted-foreground max-sm:flex-1"
            onClick={() => setAsk({ verb: 'accept', title: `验收 ${id}`, confirm: '验收', needWhy: false })}>提前验收</Button>
        </>
      )}
      <Sheet open={!!ask} onOpenChange={(o) => { if (!o) { setAsk(null); setTried(false); } }}>
        <SheetContent side={phone ? 'bottom' : 'right'} className={cn('gap-0 bg-card', phone ? 'rounded-t-[20px] pb-[env(safe-area-inset-bottom)]' : 'sm:max-w-md')}>
          <SheetHeader className="pb-2">
            <SheetTitle className="text-[16px] font-extrabold">{ask?.title}</SheetTitle>
            <SheetDescription className="sr-only">{pv.plan.does}</SheetDescription>
          </SheetHeader>
          <div className="grid gap-3 px-4">
            <p className="rounded-xl bg-surface px-3.5 py-3 text-[13.5px] leading-relaxed">{pv.plan.does}</p>
            {ask?.verb !== 'accept' && (
              <div className="grid gap-1.5">
                <label htmlFor={`why-${id}`} className="text-[13px] font-semibold">理由{ask?.needWhy ? '（必填）' : ''}</label>
                <Textarea id={`why-${id}`} rows={4} value={why} onChange={(e) => setWhy(e.target.value)} aria-invalid={tried && ask?.needWhy && !why.trim()} className="rounded-xl text-[14px]" />
                {tried && ask?.needWhy && !why.trim() && <p className="text-[12.5px] text-destructive">请填写理由</p>}
              </div>
            )}
          </div>
          <SheetFooter className="flex-row justify-end">
            <Button variant="outline" className="h-10 rounded-xl px-4 max-sm:flex-1" onClick={() => setAsk(null)}>取消</Button>
            <Button variant={ask?.danger ? 'destructive' : 'default'} className="h-10 rounded-xl px-5 font-bold max-sm:flex-1" disabled={!!busy}
              onClick={() => { setTried(true); if (ask && !(ask.needWhy && !why.trim())) run(ask.verb, why.trim() || undefined); }}>
              {busy && busy === ask?.verb && <LoaderCircleIcon className="animate-spin" />}{ask?.confirm}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  );
}

function PlanCard({ company, pv, findings, model, onChanged, onJump }: {
  company: string; pv: PlanView; findings: Finding[]; model: Model; onChanged: () => void; onJump: (id: string) => void;
}) {
  const operator = useLive((s) => s.operator);
  const p = pv.plan;
  const [label, tone] = STATE[pv.state] ?? [pv.state, 'bg-muted'];
  return (
    <article className="overflow-hidden rounded-2xl bg-card shadow-float">
      <div className="grid xl:grid-cols-[minmax(0,1fr)_280px]">
        <div className="min-w-0 p-4 max-md:p-3.5">
          <div className="flex items-center gap-2">
            <span className="text-[12.5px] font-extrabold text-muted-foreground">{p.id}</span>
            <Pill c={tone}>{label}</Pill>
          </div>
          <p className="mt-2 text-[15px] leading-relaxed font-semibold text-pretty">{p.does}</p>
          <dl className="mt-3 grid gap-2">
            <Fact term="预期">
              {model.label(p.expect.metric)} {p.expect.direction === 'down' ? '降' : '升'} <b>{share(p.expect.by)}</b> · {p.expect.within_days} 天
              {p.expect.scope?.length ? <div className="mt-1"><Scope scope={p.expect.scope} model={model} /></div> : null}
            </Fact>
            {p.guards?.length ? <Fact term="护栏"><Guards guards={p.guards} model={model} /></Fact> : null}
            {p.findings?.length ? (
              <Fact term="依据">
                <span className="flex flex-wrap gap-1.5">
                  {p.findings.map((id) => (
                    <button key={id} type="button" onClick={() => onJump(id)} title={findings.find((f) => f.id === id)?.says}
                      className="rounded-md bg-brand-soft px-1.5 py-0.5 text-[12px] font-extrabold text-primary hover:bg-primary hover:text-primary-foreground">{id}</button>
                  ))}
                </span>
              </Fact>
            ) : null}
            {p.cost && <Fact term="代价">{p.cost}</Fact>}
            {p.note && <Fact term="提醒"><span className="inline-flex gap-1.5 text-warning"><CircleAlertIcon className="mt-1 size-3.5 shrink-0" />{p.note}</span></Fact>}
            {p.actions?.length ? <Fact term="动作"><Actions pv={pv} /></Fact> : null}
          </dl>
        </div>
        <aside className="border-t bg-surface/70 p-4 max-md:p-3.5 xl:border-t-0 xl:border-l">
          <h4 className="mb-3 text-[12.5px] font-extrabold">决定记录</h4>
          <Record pv={pv} model={model} />
        </aside>
      </div>
      {operator && <Decisions company={company} pv={pv} onChanged={onChanged} />}
    </article>
  );
}

// ---- the board -------------------------------------------------------------------------------

function GoalBlock({ company, gv, model, onChanged }: { company: string; gv: GoalView; model: Model; onChanged: () => void }) {
  const g = gv.goal;
  const [flash, setFlash] = useState<string | null>(null);
  const jump = (id: string) => {
    document.getElementById(`finding-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    setFlash(id);
    setTimeout(() => setFlash(null), 1600);
  };
  return (
    <section className="grid gap-3">
      <div className="overflow-hidden rounded-2xl bg-card shadow-float">
        <header className="grid gap-4 p-5 max-md:p-3.5 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-start lg:gap-8">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-[12px] text-muted-foreground">
              <span className="rounded-md bg-primary px-1.5 py-0.5 font-extrabold text-primary-foreground">{g.id}</span>
              <span>{g.author} · {day(g.at)}</span>
            </div>
            <h2 className="mt-2 text-[21px] leading-snug font-extrabold tracking-tight text-balance max-md:text-[18px]">{g.what}</h2>
            <div className="mt-3 grid gap-2">
              <Scope scope={g.scope} model={model} />
              <Guards guards={g.guards} model={model} />
            </div>
          </div>
          {g.metric && <Numbers gv={gv} model={model} />}
        </header>
        <div className="border-t bg-surface/60 px-5 pt-4 pb-3 max-md:px-2">
          <Track gv={gv} model={model} />
        </div>
      </div>
      <div className="grid gap-3 xl:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
        <div className="grid content-start gap-2.5">
          <h3 className="px-1 text-[13px] font-extrabold text-muted-foreground">结论 <span className="tabular-nums">{gv.findings?.length ?? 0}</span></h3>
          {gv.findings?.length ? gv.findings.map((f) => <FindingCard key={f.id} f={f} model={model} flash={flash === f.id} />) : <Empty className="rounded-2xl bg-card shadow-float" />}
        </div>
        <div className="grid content-start gap-2.5">
          <h3 className="px-1 text-[13px] font-extrabold text-muted-foreground">计划 <span className="tabular-nums">{gv.plans?.length ?? 0}</span></h3>
          {gv.plans?.length
            ? gv.plans.map((pv) => <PlanCard key={pv.plan.id} company={company} pv={pv} findings={gv.findings ?? []} model={model} onChanged={onChanged} onJump={jump} />)
            : <Empty className="rounded-2xl bg-card shadow-float" />}
        </div>
      </div>
    </section>
  );
}

export function Board({ company, model }: { company: string; model: Model }) {
  const [data, setData] = useState<BoardData | null>(null);
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [goal, setGoal] = useState<string>('');
  const load = useCallback(() => {
    setLoading(true);
    getBoard(company).then((b) => { setData(b); setError(undefined); }, (e: Error) => setError(e.message)).finally(() => setLoading(false));
  }, [company]);
  useEffect(load, [load]);
  if (error && !data) return <div className="rounded-2xl bg-card p-6 text-center text-[13px] text-destructive shadow-float">{error}</div>;
  if (!data) return <div className="grid gap-3"><Skeleton className="h-56 rounded-2xl bg-card/70" /><Skeleton className="h-72 rounded-2xl bg-card/70" /></div>;
  const goals = data.goals ?? [];
  const loose: GoalView | null = data.loose_findings?.length || data.loose_plans?.length
    ? { goal: { id: '—', what: '其他', metric: '', direction: 'down', by: 0, within_days: 0, author: '', at: data.at }, findings: data.loose_findings ?? [], plans: data.loose_plans ?? [] }
    : null;
  const current = goals.find((g) => g.goal.id === goal) ?? goals[0];
  return (
    <div className="grid gap-3">
      <div className="flex items-center gap-2">
        {goals.length > 1 && (
          <div className="flex min-w-0 gap-0.5 overflow-x-auto rounded-xl bg-card p-[3px] shadow-float [scrollbar-width:none]">
            {goals.map((g) => (
              <button key={g.goal.id} type="button" onClick={() => setGoal(g.goal.id)} title={g.goal.what}
                className={cn('h-8 max-w-56 shrink-0 truncate rounded-lg px-3 text-[12.5px] font-bold text-muted-foreground', g === current && 'bg-brand-soft text-primary')}>
                {g.goal.id} {model.label(g.goal.metric)}
              </button>
            ))}
          </div>
        )}
        <span className="ml-auto text-[11.5px] text-muted-foreground tabular-nums">{dayTime(data.at)}</span>
        <Button variant="ghost" size="icon" className="size-8 rounded-lg text-muted-foreground" aria-label="刷新" onClick={load} disabled={loading}>
          <RefreshCwIcon className={cn('size-4', loading && 'animate-spin')} />
        </Button>
      </div>
      {current ? <GoalBlock company={company} gv={current} model={model} onChanged={load} /> : !loose && <Empty className="min-h-60 rounded-2xl bg-card shadow-float" />}
      {loose && <GoalBlock company={company} gv={loose} model={model} onChanged={load} />}
    </div>
  );
}
