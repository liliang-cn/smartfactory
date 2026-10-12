// The phone page (/check): an operator at a machine picks it (or opens it from the code printed on
// it), sees which rounds are due, fills a form in and sends it. With no network the round stays
// on the phone with the time it was taken and goes once the phone is back online.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangleIcon, CameraIcon, CheckCircle2Icon, ChevronLeftIcon, ChevronRightIcon, ClockIcon, CloudOffIcon, LoaderCircleIcon,
  LogOutIcon, SearchIcon, SendIcon, Trash2Icon, XIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import * as api from './api';

// ---------- where the page is: ?company=&plant=&asset=&form= ----------

interface Where { company?: string; plant?: string; asset?: string; form?: string }

function here(): Where {
  const q = new URLSearchParams(location.search);
  return { company: q.get('company') ?? undefined, plant: q.get('plant') ?? undefined, asset: q.get('asset') ?? undefined, form: q.get('form') ?? undefined };
}

function useWhere(): [Where, (w: Where, replace?: boolean) => void] {
  const [w, setW] = useState(here);
  useEffect(() => {
    const on = () => setW(here());
    addEventListener('popstate', on);
    return () => removeEventListener('popstate', on);
  }, []);
  const go = useCallback((next: Where, replace = false) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(next)) if (v) q.set(k, v);
    history[replace ? 'replaceState' : 'pushState'](null, '', `${location.pathname}?${q}`);
    setW(next);
    scrollTo(0, 0);
  }, []);
  return [w, go];
}

// ---------- time ----------

const pad = (n: number) => String(n).padStart(2, '0');
function when(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  const t = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  return d.toDateString() === new Date().toDateString() ? t : `${d.getMonth() + 1}-${d.getDate()} ${t}`;
}
function span(ms: number): string {
  const m = Math.max(1, Math.round(ms / 60000));
  if (m < 60) return `${m} 分钟`;
  const h = Math.floor(m / 60);
  return h < 48 ? `${h} 小时${m % 60 ? ` ${m % 60} 分` : ''}` : `${Math.floor(h / 24)} 天`;
}
const localInput = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;

// ---------- small parts ----------

function Status({ r }: { r: api.Round }) {
  const [label, tone] = {
    overdue: [`逾期${r.since ? ` ${span(Date.now() - Date.parse(r.since))}` : ''}`, 'bg-danger-soft text-destructive'],
    due: [r.due ? `${when(r.due)} 前做` : '待做', 'bg-warning-soft text-warning'],
    done: ['本轮已做', 'bg-success-soft text-success'],
    open: ['需要时填', 'bg-surface text-muted-foreground'],
  }[r.status];
  return <span className={cn('shrink-0 rounded-lg px-2 py-1 text-[12.5px] font-bold', tone)}>{label}</span>;
}

function Header({ title, sub, back, right }: { title: React.ReactNode; sub?: React.ReactNode; back?: () => void; right?: React.ReactNode }) {
  return (
    <header className="sticky top-0 z-20 flex min-h-16 items-center gap-2 bg-card/95 px-3 py-2 shadow-[0_1px_0_var(--border)] backdrop-blur">
      {back && (
        <Button variant="ghost" size="icon" className="size-11 shrink-0 rounded-xl" aria-label="返回" onClick={back}><ChevronLeftIcon className="size-6" /></Button>
      )}
      <div className={cn('min-w-0 flex-1 leading-tight', !back && 'pl-1')}>
        <h1 className="truncate text-[17px] font-extrabold">{title}</h1>
        {sub && <p className="truncate text-[12.5px] text-muted-foreground">{sub}</p>}
      </div>
      {right}
    </header>
  );
}

function Card({ children, className, ...rest }: React.ComponentProps<'section'>) {
  return <section className={cn('rounded-[18px] bg-card p-4 shadow-float', className)} {...rest}>{children}</section>;
}

/** What waits on the phone: how many, sending now, and those the server would not take. */
function Waiting() {
  const [list, setList] = useState(api.queued);
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const on = () => setList(api.queued());
    addEventListener('sf-check-queue', on);
    addEventListener('storage', on);
    return () => { removeEventListener('sf-check-queue', on); removeEventListener('storage', on); };
  }, []);
  if (!list.length) return null;
  const refused = list.filter((q) => q.refused);
  const waiting = list.length - refused.length;
  return (
    <div className="mx-3 mt-3 rounded-2xl border border-warning/40 bg-warning-soft/60 p-3 text-[13.5px]">
      <div className="flex items-center gap-2">
        <CloudOffIcon className="size-5 shrink-0 text-warning" />
        <span className="min-w-0 flex-1">
          {waiting > 0 && <><b>{waiting} 条</b>存在手机上，联网后自动发送。</>}
          {refused.length > 0 && <><b className="text-destructive">{refused.length} 条</b>没被收下。</>}
        </span>
        {waiting > 0 && (
          <Button variant="outline" className="h-10 rounded-xl font-bold" disabled={busy || !navigator.onLine}
            onClick={async () => { setBusy(true); await api.flush(); setBusy(false); }}>
            {busy ? <LoaderCircleIcon className="animate-spin" /> : <SendIcon />}现在发
          </Button>
        )}
        {refused.length > 0 && <Button variant="ghost" className="h-10 rounded-xl" onClick={() => setOpen((o) => !o)}>{open ? '收起' : '看看'}</Button>}
      </div>
      {open && (
        <ul className="mt-2 grid gap-2">
          {refused.map((q) => (
            <li key={q.input.id} className="flex items-start gap-2 rounded-xl bg-card p-2.5">
              <div className="min-w-0 flex-1">
                <b className="block">{q.label}</b>
                <small className="text-[12px] text-muted-foreground">{when(q.input.taken)} 记的</small>
                <p className="text-[12.5px] text-destructive">{q.refused}</p>
              </div>
              <Button variant="ghost" size="icon" className="size-10 rounded-xl text-muted-foreground" aria-label="不要了" onClick={() => api.dismiss(q.input.id!)}><Trash2Icon /></Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ---------- sign in, pick the company ----------

function SignIn({ problem, onDone }: { problem?: string; onDone(): void }) {
  const [value, setValue] = useState('');
  return (
    <div className="grid min-h-dvh place-items-center bg-stage p-4">
      <form className="w-full max-w-[400px] rounded-[20px] bg-card p-5 shadow-float"
        onSubmit={(e) => { e.preventDefault(); if (value.trim()) { api.signIn(value); onDone(); } }}>
        <h1 className="text-[19px] font-extrabold">点检 · 人工记录</h1>
        <p className="mt-1 text-[13.5px] text-muted-foreground">在设备旁边填点检表、首件检验、化验结果。</p>
        <label htmlFor="token" className="mt-5 block text-[14px] font-bold">操作员令牌</label>
        <Input id="token" type="password" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)}
          placeholder="sf_…" className="mt-1.5 h-12 rounded-xl font-mono text-[15px]" />
        {problem && <p className="mt-2 text-[13px] text-destructive">{problem}</p>}
        <p className="mt-2 text-[12.5px] leading-relaxed text-muted-foreground">令牌向管理员要，登录后存在这部手机上，下次扫码直接打开。</p>
        <Button type="submit" className="mt-5 h-12 w-full rounded-xl text-[15px] font-bold" disabled={!value.trim()}>登录</Button>
      </form>
    </div>
  );
}

// ---------- the list of machines ----------

function Home({ ov, company, go, onSignOut }: { ov: api.Overview; company: string; go(w: Where): void; onSignOut(): void }) {
  const [q, setQ] = useState('');
  const open = (m: api.Machine) => go({ company, plant: m.plant, asset: m.asset });
  const todo = ov.machines
    .filter((m) => m.rounds.some((r) => r.status === 'overdue' || r.status === 'due'))
    .sort((a, b) => Number(b.rounds.some((r) => r.status === 'overdue')) - Number(a.rounds.some((r) => r.status === 'overdue')));
  const needle = q.trim().toLowerCase();
  const found = needle ? ov.machines.filter((m) => `${m.asset} ${m.name} ${m.lineName} ${m.label}`.toLowerCase().includes(needle)) : [];
  const lines = useMemo(() => {
    const by = new Map<string, api.Machine[]>();
    for (const m of ov.machines) {
      const k = `${m.plantName} · ${m.lineName}`;
      by.set(k, [...(by.get(k) ?? []), m]);
    }
    return [...by.entries()];
  }, [ov.machines]);
  const row = (m: api.Machine) => {
    const late = m.rounds.filter((r) => r.status === 'overdue').length;
    const due = m.rounds.filter((r) => r.status === 'due').length;
    return (
      <li key={`${m.plant}/${m.asset}`}>
        <button type="button" onClick={() => open(m)} className="flex min-h-16 w-full items-center gap-3 rounded-2xl px-3 py-2.5 text-left active:bg-surface">
          <span className="min-w-0 flex-1">
            <b className="block truncate text-[15.5px]">{m.name}</b>
            <small className="block truncate text-[12.5px] text-muted-foreground">{m.asset} · {m.lineName}</small>
          </span>
          {late > 0 && <span className="rounded-lg bg-danger-soft px-2 py-1 text-[12.5px] font-bold text-destructive">逾期 {late}</span>}
          {due > 0 && <span className="rounded-lg bg-warning-soft px-2 py-1 text-[12.5px] font-bold text-warning">待做 {due}</span>}
          <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" />
        </button>
      </li>
    );
  };
  return (
    <>
      <Header title="点检 · 人工记录" sub={`${ov.who}${ov.operator ? '' : '（只能看）'}`}
        right={<Button variant="ghost" size="icon" className="size-11 rounded-xl text-muted-foreground" aria-label="退出" onClick={onSignOut}><LogOutIcon /></Button>} />
      <Waiting />
      <div className="grid gap-3 p-3">
        <label className="relative block">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-3.5 size-5 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="设备编号或名称" aria-label="找设备"
            className="h-12 rounded-2xl bg-card pl-11 text-[15px] shadow-float" />
        </label>
        {needle ? (
          <Card className="p-1.5">
            {found.length ? <ul>{found.map(row)}</ul> : <p className="p-3 text-[14px] text-muted-foreground">没有对得上的设备。只有设了表单的设备在这里。</p>}
          </Card>
        ) : (
          <>
            <Card className="p-1.5">
              <h2 className="px-3 pt-2.5 pb-1 text-[14px] font-extrabold">该做的</h2>
              {todo.length ? <ul>{todo.map(row)}</ul> : <p className="flex items-center gap-2 px-3 pb-3 text-[14px] text-muted-foreground"><CheckCircle2Icon className="size-5 text-success" />这一轮都做完了。</p>}
            </Card>
            {lines.map(([name, ms]) => (
              <Card key={name} className="p-1.5">
                <h2 className="px-3 pt-2.5 pb-1 text-[13px] font-bold text-muted-foreground">{name}</h2>
                <ul>{ms.map(row)}</ul>
              </Card>
            ))}
            {ov.machines.length === 0 && <Card><p className="text-[14px] text-muted-foreground">还没有设备要填表。管理员在后台「点检」页设好表单后，这里会列出要填的设备。</p></Card>}
          </>
        )}
      </div>
    </>
  );
}

// ---------- one machine ----------

function lastText(v: api.Value): string {
  if (v.kind === 'photo') return '照片';
  if (v.num !== undefined) return `${v.num}${v.unit ? ` ${v.unit}` : ''}`;
  return v.text ?? '';
}

/** a machine's rounds: what is late first, what is done last */
const RANK: Record<api.Round['status'], number> = { overdue: 0, due: 1, open: 2, done: 3 };

function MachinePage({ data, company, go, offline }: { data: api.MachineChecks; company: string; go(w: Where): void; offline: boolean }) {
  const m = data.machine;
  return (
    <>
      <Header title={m.name} sub={`${m.asset} · ${m.plantName} ${m.lineName}`} back={() => go({ company })} />
      <Waiting />
      <div className="grid gap-3 p-3">
        {offline && <p className="flex items-center gap-2 rounded-2xl bg-surface p-3 text-[13px] text-muted-foreground"><CloudOffIcon className="size-4" />没有网络：照样能填，填好的先存在手机上。</p>}
        <Card className="p-1.5">
          <ul>
            {[...m.rounds].sort((a, b) => RANK[a.status] - RANK[b.status]).map((r) => {
              const f = data.forms.find((x) => x.id === r.form);
              return (
                <li key={r.form}>
                  <button type="button" disabled={!f} onClick={() => go({ company, plant: m.plant, asset: m.asset, form: r.form })}
                    className="flex min-h-[72px] w-full items-center gap-3 rounded-2xl px-3 py-2.5 text-left active:bg-surface">
                    <span className="min-w-0 flex-1">
                      <b className="block truncate text-[16px]">{r.formName}</b>
                      <small className="block truncate text-[12.5px] text-muted-foreground">
                        {r.last ? `上次 ${when(r.last)} ${r.lastBy ?? ''}${r.lastBad ? `，${r.lastBad} 项异常` : ''}` : '还没填过'}
                      </small>
                    </span>
                    <Status r={r} />
                    <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" />
                  </button>
                </li>
              );
            })}
          </ul>
        </Card>
        {data.last.map((s) => (
          <Card key={s.id}>
            <h2 className="flex items-baseline gap-2 text-[14px] font-extrabold">{s.formName}<small className="ml-auto text-[12.5px] font-semibold text-muted-foreground">{when(s.taken)} {s.by}</small></h2>
            <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[14px]">
              {s.values.filter((v) => v.kind !== 'photo').map((v) => (
                <div key={v.key} className="contents">
                  <dt className="text-muted-foreground">{v.label}</dt>
                  <dd className={cn('font-semibold tabular-nums', v.bad && 'text-destructive')}>{lastText(v)}{v.bad && v.num !== undefined ? `（${v.why}）` : ''}</dd>
                </div>
              ))}
            </dl>
          </Card>
        ))}
      </div>
    </>
  );
}

// ---------- the form ----------

type Answer = string | boolean | undefined;

/** A number's spec: the form's own, or the machine's for its quality measure. */
function specOf(f: api.Field, m: api.Machine): { lo?: number; hi?: number } {
  if (f.lo !== undefined || f.hi !== undefined) return { lo: f.lo, hi: f.hi };
  if (f.twin === 'measure' && m.measure) return { lo: m.measure.lo, hi: m.measure.hi };
  return {};
}
const specText = ({ lo, hi }: { lo?: number; hi?: number }) =>
  lo !== undefined && hi !== undefined ? `${lo}～${hi}` : hi !== undefined ? `≤ ${hi}` : lo !== undefined ? `≥ ${lo}` : '';
const toNum = (s: string) => Number(s.trim().replace('，', '.'));

function judge(f: api.Field, m: api.Machine, a: Answer): string | null {
  if (a === undefined || a === '') return null;
  if (f.kind === 'number') {
    const x = toNum(String(a));
    if (!Number.isFinite(x)) return '要填数字';
    const s = specOf(f, m);
    if (s.hi !== undefined && x > s.hi) return `超上限 ${s.hi}`;
    if (s.lo !== undefined && x < s.lo) return `低于下限 ${s.lo}`;
    return null;
  }
  if ((f.kind === 'choice' || f.kind === 'yesno') && f.bad?.includes(String(a))) return '异常';
  return null;
}

function FieldCard({ f, m, value, set, last, missing }: {
  f: api.Field; m: api.Machine; value: Answer; set(v: Answer): void; last?: { v: api.Value; s: api.Submission }; missing: boolean;
}) {
  const bad = judge(f, m, value);
  const spec = f.kind === 'number' ? specText(specOf(f, m)) : '';
  const unit = f.unit || (f.twin === 'measure' ? m.measure?.unit : '') || '';
  const fileRef = useRef<HTMLInputElement>(null);
  const [shrinking, setShrinking] = useState(false);
  return (
    <Card id={`f-${f.key}`} className={cn('grid gap-2.5', missing && 'ring-2 ring-destructive/60', bad && 'ring-2 ring-destructive/40')}>
      <div className="flex items-baseline gap-2">
        <h3 className="text-[16px] font-extrabold">{f.label}{f.required && <span className="text-destructive"> *</span>}</h3>
        {spec && <small className="ml-auto shrink-0 text-[13px] font-semibold text-muted-foreground tabular-nums">标准 {spec}{unit && ` ${unit}`}</small>}
      </div>
      {f.kind === 'number' && (
        <div className="flex items-center gap-2">
          <Input type="text" inputMode="decimal" enterKeyHint="next" value={(value as string) ?? ''} onChange={(e) => set(e.target.value)}
            aria-label={f.label} aria-invalid={!!bad}
            className={cn('h-14 rounded-xl text-[22px] font-bold tabular-nums', bad && 'border-destructive text-destructive')} />
          {unit && <span className="shrink-0 text-[15px] font-bold text-muted-foreground">{unit}</span>}
        </div>
      )}
      {(f.kind === 'choice' || f.kind === 'yesno') && (
        <div className={cn('grid gap-2', (f.options?.length ?? 0) <= 3 ? 'grid-cols-[repeat(auto-fit,minmax(0,1fr))]' : 'grid-cols-2')}>
          {f.options?.map((o) => {
            const on = value === o;
            const isBad = f.bad?.includes(o);
            return (
              <button key={o} type="button" aria-pressed={on} onClick={() => set(on ? undefined : o)}
                className={cn('h-14 rounded-xl border-2 px-2 text-[16px] font-bold transition-colors',
                  on ? (isBad ? 'border-destructive bg-destructive text-white' : 'border-primary bg-primary text-primary-foreground') : 'border-border bg-card active:bg-surface')}>
                {o}
              </button>
            );
          })}
        </div>
      )}
      {f.kind === 'text' && (
        <Textarea value={(value as string) ?? ''} onChange={(e) => set(e.target.value)} aria-label={f.label} maxLength={500} rows={3} className="min-h-24 rounded-xl text-[15px]" />
      )}
      {f.kind === 'photo' && (
        <>
          <input ref={fileRef} type="file" accept="image/*" capture="environment" className="hidden"
            onChange={async (e) => {
              const file = e.target.files?.[0];
              e.target.value = '';
              if (!file) return;
              setShrinking(true);
              try { set(await api.shrink(file)); } finally { setShrinking(false); }
            }} />
          {typeof value === 'string' && value ? (
            <div className="relative">
              <img src={value} alt={f.label} className="max-h-64 w-full rounded-xl object-cover" />
              <Button variant="secondary" size="icon" className="absolute top-2 right-2 size-10 rounded-full" aria-label="去掉照片" onClick={() => set(undefined)}><XIcon /></Button>
            </div>
          ) : (
            <Button variant="outline" className="h-14 rounded-xl text-[15px] font-bold" disabled={shrinking} onClick={() => fileRef.current?.click()}>
              {shrinking ? <LoaderCircleIcon className="animate-spin" /> : <CameraIcon className="size-5" />}拍照
            </Button>
          )}
        </>
      )}
      {(bad || missing || last) && (
        <p className="flex flex-wrap gap-x-3 gap-y-0.5 text-[13px]">
          {missing && <span className="font-bold text-destructive">这一项要填</span>}
          {bad && <span className="flex items-center gap-1 font-bold text-destructive"><AlertTriangleIcon className="size-4" />{bad}</span>}
          {last && f.kind !== 'photo' && (
            <span className="text-muted-foreground">上次 <b className={cn('font-semibold', last.v.bad ? 'text-destructive' : 'text-foreground')}>{lastText(last.v)}</b>（{last.s.by} {when(last.s.taken)}）</span>
          )}
        </p>
      )}
    </Card>
  );
}

type Sent = { kind: 'sent'; sub: api.Submission } | { kind: 'kept' } | { kind: 'error'; text: string; problems: string[] };

function FormPage({ form, data, company, go, operator, onSent }: {
  form: api.Form; data: api.MachineChecks; company: string; go(w: Where): void; operator: boolean; onSent(): void;
}) {
  const m = data.machine;
  const [values, setValues] = useState<Record<string, Answer>>({});
  const [taken, setTaken] = useState(() => new Date());
  const [pickTime, setPickTime] = useState(false);
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState<Sent | null>(null);
  const last = data.last.find((s) => s.form === form.id);
  const missing = form.fields.filter((f) => f.required && (values[f.key] === undefined || values[f.key] === ''));
  const back = () => go({ company, plant: m.plant, asset: m.asset });

  const send = async () => {
    setTried(true);
    if (missing.length) {
      document.getElementById(`f-${missing[0].key}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
      return;
    }
    const out: api.Input['values'] = {};
    for (const f of form.fields) {
      const v = values[f.key];
      if (v === undefined || v === '') continue;
      out[f.key] = f.kind === 'number' && Number.isFinite(toNum(String(v))) ? toNum(String(v)) : v;
    }
    const input: api.Input = { id: api.newID(), form: form.id, plant: m.plant, asset: m.asset, taken: taken.toISOString(), values: out };
    setBusy(true);
    try {
      const sub = await api.submit(company, input);
      setSent({ kind: 'sent', sub });
      onSent();
      api.flush();
    } catch (e) {
      if (api.transient(e)) {
        const ok = api.enqueue({ company, input, label: `${m.name} · ${form.name}` });
        setSent(ok ? { kind: 'kept' } : { kind: 'error', text: '手机存不下了（照片太多），先联网发掉存着的', problems: [] });
      } else {
        const err = e as api.ApiError;
        setSent({ kind: 'error', text: err.message, problems: err.problems });
      }
    } finally {
      setBusy(false);
    }
  };

  if (sent?.kind === 'sent' || sent?.kind === 'kept') {
    const bad = sent.kind === 'sent' ? sent.sub.values.filter((v) => v.bad || v.flag) : [];
    return (
      <>
        <Header title={form.name} sub={`${m.name} · ${m.asset}`} />
        <div className="grid gap-3 p-3">
          <Card className="grid justify-items-center gap-2 py-8 text-center">
            {sent.kind === 'sent'
              ? <><CheckCircle2Icon className="size-14 text-success" /><h2 className="text-[20px] font-extrabold">已记录</h2><p className="text-[14px] text-muted-foreground">{when(sent.sub.taken)} · {sent.sub.by}</p></>
              : <><CloudOffIcon className="size-14 text-warning" /><h2 className="text-[20px] font-extrabold">存在手机上了</h2><p className="text-[14px] text-muted-foreground">现在没有网络。联网后自动发送，记录时间是 {when(taken.toISOString())}。</p></>}
          </Card>
          {bad.length > 0 && (
            <Card className="grid gap-2">
              <h3 className="flex items-center gap-1.5 text-[15px] font-extrabold text-destructive"><AlertTriangleIcon className="size-5" />{bad.length} 项要注意</h3>
              <ul className="grid gap-1.5 text-[14px]">
                {bad.map((v) => (
                  <li key={v.key}>
                    <b>{v.label}</b> {lastText(v)}{v.bad && v.num !== undefined ? `，${v.why}` : ''}
                    {v.flag && <span className="block text-[12.5px] text-muted-foreground">这个数不太可能（{v.flag}），已保存但孪生不用它，请核对后重填。</span>}
                  </li>
                ))}
              </ul>
              <p className="text-[12.5px] text-muted-foreground">已在孪生里报给这台设备，按规定报班长。</p>
            </Card>
          )}
          <Button className="h-14 rounded-2xl text-[16px] font-bold" onClick={back}>回到 {m.name}</Button>
          <Button variant="outline" className="h-14 rounded-2xl text-[16px] font-bold" onClick={() => go({ company })}>换一台设备</Button>
        </div>
      </>
    );
  }

  return (
    <>
      <Header title={form.name} sub={`${m.name} · ${m.asset}`} back={back} />
      <div className="grid gap-3 p-3 pb-28">
        {form.note && <p className="rounded-2xl bg-brand-soft p-3 text-[14px] leading-relaxed text-accent-foreground">{form.note}</p>}
        {!operator && <p className="rounded-2xl bg-warning-soft p-3 text-[14px] text-warning">你的令牌只能看，不能提交。要操作员的令牌。</p>}
        {form.fields.map((f) => {
          const lv = last?.values.find((v) => v.key === f.key);
          return (
            <FieldCard key={f.key} f={f} m={m} value={values[f.key]} set={(v) => setValues((s) => ({ ...s, [f.key]: v }))}
              last={lv && last ? { v: lv, s: last } : undefined} missing={tried && missing.includes(f)} />
          );
        })}
        <Card className="grid gap-2">
          <div className="flex items-center gap-2">
            <ClockIcon className="size-5 text-muted-foreground" />
            <span className="text-[15px]">记录时间 <b className="tabular-nums">{when(taken.toISOString())}</b></span>
            <Button variant="ghost" className="ml-auto h-10 rounded-xl" onClick={() => setPickTime((p) => !p)}>{pickTime ? '收起' : '改时间'}</Button>
          </div>
          {pickTime && (
            <Input type="datetime-local" value={localInput(taken)} max={localInput(new Date())} aria-label="记录时间"
              onChange={(e) => { const d = new Date(e.target.value); if (!Number.isNaN(d.getTime())) setTaken(d); }} className="h-12 rounded-xl text-[15px]" />
          )}
          <p className="text-[12.5px] text-muted-foreground">取样、测量的时间。化验结果后出的，改成取样的时间。</p>
        </Card>
        {sent?.kind === 'error' && (
          <div className="rounded-2xl bg-danger-soft p-3 text-[14px] text-destructive">
            <b>没有提交：{sent.text}</b>
            {sent.problems.length > 1 && <ul className="mt-1 list-disc pl-5">{sent.problems.map((p, i) => <li key={i}>{p}</li>)}</ul>}
          </div>
        )}
      </div>
      <div className="fixed inset-x-0 bottom-0 z-20 bg-card/95 p-3 pb-[calc(12px+env(safe-area-inset-bottom))] shadow-sheet backdrop-blur">
        <Button className="h-14 w-full rounded-2xl text-[17px] font-bold" disabled={busy || !operator} onClick={send}>
          {busy ? <LoaderCircleIcon className="animate-spin" /> : <SendIcon className="size-5" />}提交
        </Button>
      </div>
    </>
  );
}

// ---------- the page ----------

const cacheKey = (c: string) => `sf-check-overview:${c}`;

export default function Check() {
  const [where, go] = useWhere();
  const [signedIn, setSignedIn] = useState(() => !!api.token());
  const [problem, setProblem] = useState<string>();
  const [companies, setCompanies] = useState<string[] | null>(null);
  const [ov, setOv] = useState<api.Overview | null>(null);
  const [mc, setMc] = useState<api.MachineChecks | null>(null);
  const [offline, setOffline] = useState(!navigator.onLine);
  const [error, setError] = useState<string>();
  const company = where.company;

  const signOut = useCallback((why?: string) => {
    api.signOut();
    setSignedIn(false);
    setProblem(why);
    setOv(null);
  }, []);
  const failed = useCallback((e: unknown) => {
    if (e instanceof api.ApiError && (e.status === 401 || e.status === 403)) signOut(e.status === 401 ? '令牌不对或已失效' : e.message);
    else if (api.transient(e)) setOffline(true);
    else setError((e as Error).message);
  }, [signOut]);

  // which company: the link says, or the only one the token covers
  useEffect(() => {
    if (!signedIn || company) return;
    api.companies().then((cs) => {
      if (cs.length === 1) go({ company: cs[0] }, true);
      else if (cs.length === 0) signOut('这个令牌不包括任何公司');
      else setCompanies(cs);
    }, failed);
  }, [signedIn, company, go, signOut, failed]);

  const load = useCallback(() => {
    if (!company) return;
    api.overview(company).then((o) => {
      setOv(o);
      setOffline(false);
      try { localStorage.setItem(cacheKey(company), JSON.stringify(o)); } catch { /* storage full */ }
    }, (e) => {
      failed(e);
      try { const c = localStorage.getItem(cacheKey(company)); if (c) setOv((o) => o ?? JSON.parse(c)); } catch { /* storage blocked */ }
    });
  }, [company, failed]);
  useEffect(() => { if (signedIn) load(); }, [signedIn, load]);

  const loadMachine = useCallback(() => {
    if (!company || !where.plant || !where.asset) return;
    api.machine(company, where.plant, where.asset).then((d) => { setMc(d); setOffline(false); }, failed);
  }, [company, where.plant, where.asset, failed]);
  useEffect(() => { setMc(null); setError(undefined); if (signedIn) loadMachine(); }, [signedIn, loadMachine]);

  // what waits on the phone goes as soon as it can
  useEffect(() => {
    if (!signedIn) return;
    const online = () => { setOffline(false); api.flush().then((n) => n && load()); };
    const off = () => setOffline(true);
    addEventListener('online', online);
    addEventListener('offline', off);
    online();
    const t = setInterval(() => { if (navigator.onLine && !document.hidden) { api.flush(); load(); } }, 30_000);
    return () => { removeEventListener('online', online); removeEventListener('offline', off); clearInterval(t); };
  }, [signedIn, load]);

  if (!signedIn) return <SignIn problem={problem} onDone={() => { setSignedIn(true); setProblem(undefined); }} />;
  if (!company) {
    return (
      <div className="min-h-dvh bg-stage">
        <Header title="选公司" />
        <div className="grid gap-2 p-3">
          {companies?.map((c) => (
            <Button key={c} variant="outline" className="h-14 justify-between rounded-2xl bg-card text-[16px] font-bold" onClick={() => go({ company: c })}>{c}<ChevronRightIcon /></Button>
          )) ?? <LoaderCircleIcon className="mx-auto mt-10 animate-spin text-muted-foreground" />}
        </div>
      </div>
    );
  }
  if (!ov) {
    return (
      <div className="grid min-h-dvh place-items-center bg-stage p-6 text-center text-[14px] text-muted-foreground">
        {error ? <p className="text-destructive">{error}</p> : offline ? <p><CloudOffIcon className="mx-auto mb-2 size-8" />没有网络，联网后再打开。</p> : <LoaderCircleIcon className="animate-spin" />}
      </div>
    );
  }

  // a machine's page: from the server, or, without a network, from what the phone last had
  let data: api.MachineChecks | null = null;
  if (where.plant && where.asset) {
    if (mc && mc.machine.plant === where.plant && mc.machine.asset === where.asset) data = mc;
    else {
      const m = ov.machines.find((x) => x.plant === where.plant && x.asset === where.asset);
      if (m && (offline || mc === null)) data = { machine: m, forms: ov.forms.filter((f) => m.rounds.some((r) => r.form === f.id)), last: [], operator: ov.operator };
    }
  }
  const afterSend = () => { load(); loadMachine(); };
  let page: React.ReactNode;
  if (where.plant && where.asset && !data) {
    page = (
      <>
        <Header title={where.asset} back={() => go({ company })} />
        <p className="p-6 text-center text-[14px] text-muted-foreground">{error ?? '这台设备没有要填的表。'}</p>
      </>
    );
  } else if (data && where.form) {
    const form = data.forms.find((f) => f.id === where.form);
    page = form
      ? <FormPage key={`${data.machine.asset}/${form.id}`} form={form} data={data} company={company} go={go} operator={ov.operator} onSent={afterSend} />
      : <><Header title="没有这张表" back={() => go({ company, plant: where.plant, asset: where.asset })} /></>;
  } else if (data) {
    page = <MachinePage data={data} company={company} go={go} offline={offline} />;
  } else {
    page = <Home ov={ov} company={company} go={go} onSignOut={() => signOut()} />;
  }
  return <div className="mx-auto min-h-dvh max-w-[640px] bg-stage">{page}</div>;
}
