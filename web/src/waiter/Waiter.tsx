// The waiter's phone page (/waiter): the shop's tables as they stand, and a tap to say what
// happened at one — seated so many, paid and left, cleared, booked. Each tap is a report like a
// camera box's (source waiter:<name>), so the twin and the figures follow what the floor says.
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { z } from 'zod';
import { ChevronRightIcon, LoaderCircleIcon, LogOutIcon, MinusIcon, PlusIcon, XIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import type * as P from '@/protocol/sfp';

type Overview = z.infer<typeof P.TablesOverview>;
type Table = z.infer<typeof P.TableNow>;
type State = Table['state'];

const KEY = 'sf-waiter-token';
const NAME = 'sf-waiter-name';
const read = (k: string) => { try { return localStorage.getItem(k) ?? ''; } catch { return ''; } };
const write = (k: string, v: string) => { try { if (v) localStorage.setItem(k, v); else localStorage.removeItem(k); } catch { /* storage blocked */ } };

class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

async function call<T>(method: string, path: string, json?: unknown): Promise<T> {
  const t = read(KEY);
  const res = await fetch(path, {
    method,
    headers: { ...(json !== undefined && { 'Content-Type': 'application/json' }), ...(t && { Authorization: `Bearer ${t}` }) },
    body: json !== undefined ? JSON.stringify(json) : undefined,
  });
  const text = await res.text();
  let body: unknown = null;
  try { body = text ? JSON.parse(text) : null; } catch { body = null; }
  if (!res.ok) throw new ApiError(res.status, (body as { error?: string } | null)?.error ?? `请求失败（${res.status}）`);
  return body as T;
}

const STATE: Record<State, { label: string; card: string; chip: string }> = {
  free: { label: '空闲', card: 'border-success/40 bg-success-soft/40', chip: 'bg-success-soft text-success' },
  occupied: { label: '就餐中', card: 'border-primary/40 bg-brand-soft/60', chip: 'bg-primary text-primary-foreground' },
  reserved: { label: '已预订', card: 'border-warning/50 bg-warning-soft/50', chip: 'bg-warning-soft text-warning' },
  cleaning: { label: '清台中', card: 'border-maint/40 bg-maint-soft/50', chip: 'bg-maint-soft text-maint' },
};

const minutes = (t: Table) => Math.max(0, Math.round((Date.now() - t.since) / 60000));

function SignIn({ problem, onDone }: { problem?: string; onDone(): void }) {
  const [token, setToken] = useState('');
  const [name, setName] = useState(read(NAME));
  return (
    <div className="grid min-h-dvh place-items-center bg-stage p-4">
      <form className="w-full max-w-[400px] rounded-[20px] bg-card p-5 shadow-float"
        onSubmit={(e) => { e.preventDefault(); write(KEY, token.trim()); write(NAME, name.trim()); onDone(); }}>
        <h1 className="text-[19px] font-extrabold">桌态</h1>
        <p className="mt-1 text-[13.5px] text-muted-foreground">客人入座、结账离开、清台、预订，点一下桌子就报上去。</p>
        <label htmlFor="name" className="mt-5 block text-[14px] font-bold">你的名字</label>
        <Input id="name" value={name} onChange={(e) => setName(e.target.value)} placeholder="如 小王" className="mt-1.5 h-12 rounded-xl text-[15px]" />
        <label htmlFor="token" className="mt-4 block text-[14px] font-bold">令牌</label>
        <Input id="token" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder="sf_…" className="mt-1.5 h-12 rounded-xl font-mono text-[15px]" />
        {problem && <p className="mt-2 text-[13px] text-destructive">{problem}</p>}
        <p className="mt-2 text-[12.5px] leading-relaxed text-muted-foreground">令牌向店长要（桌态上报令牌，或操作员令牌），存在这部手机上。</p>
        <Button type="submit" className="mt-5 h-12 w-full rounded-xl text-[15px] font-bold" disabled={!token.trim() || !name.trim()}>开始</Button>
      </form>
    </div>
  );
}

/** What can happen at a table, from where it stands. */
function Sheet({ t, onClose, onReport }: { t: Table; onClose(): void; onReport(state: State, people?: number): Promise<void> }) {
  const [people, setPeople] = useState(Math.min(2, t.seats));
  const [busy, setBusy] = useState(false);
  const act = async (state: State, n?: number) => {
    setBusy(true);
    try { await onReport(state, n); onClose(); } finally { setBusy(false); }
  };
  const big = 'h-14 rounded-2xl text-[16px] font-bold';
  return (
    <div className="fixed inset-0 z-40 flex items-end bg-black/30" onClick={onClose}>
      <div className="w-full rounded-t-[22px] bg-card p-4 pb-[calc(16px+env(safe-area-inset-bottom))] shadow-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-2">
          <b className="font-mono text-[22px]">{t.id}</b>
          <span className={cn('rounded-lg px-2 py-1 text-[13px] font-bold', STATE[t.state].chip)}>{STATE[t.state].label}</span>
          <span className="text-[13px] text-muted-foreground">{t.area} · {t.seats} 座{t.state === 'occupied' ? ` · ${t.people} 人 ${minutes(t)} 分钟` : ''}</span>
          <Button variant="ghost" size="icon" className="ml-auto size-11 rounded-xl" aria-label="关闭" onClick={onClose}><XIcon /></Button>
        </div>
        <div className="mt-4 grid gap-2.5">
          {(t.state === 'free' || t.state === 'reserved') && (
            <>
              <div className="flex items-center justify-center gap-4 rounded-2xl bg-surface py-2">
                <Button variant="outline" size="icon" className="size-12 rounded-xl" aria-label="少一人" disabled={people <= 1} onClick={() => setPeople((n) => n - 1)}><MinusIcon /></Button>
                <b className="w-16 text-center text-[26px] tabular-nums">{people}<small className="text-[14px]"> 人</small></b>
                <Button variant="outline" size="icon" className="size-12 rounded-xl" aria-label="多一人" disabled={people >= 20} onClick={() => setPeople((n) => n + 1)}><PlusIcon /></Button>
              </div>
              <Button className={big} disabled={busy} onClick={() => act('occupied', people)}>入座 {people} 人</Button>
            </>
          )}
          {t.state === 'occupied' && <Button className={big} disabled={busy} onClick={() => act('cleaning')}>结账离开</Button>}
          {t.state !== 'free' && <Button variant={t.state === 'cleaning' ? 'default' : 'outline'} className={big} disabled={busy} onClick={() => act('free')}>{t.state === 'cleaning' ? '清台完成' : t.state === 'reserved' ? '取消预订' : '直接清空'}</Button>}
          {t.state === 'free' && <Button variant="outline" className={big} disabled={busy} onClick={() => act('reserved')}>预订留桌</Button>}
        </div>
      </div>
    </div>
  );
}

function companyOf(): string { return new URLSearchParams(location.search).get('company') ?? ''; }

export default function Waiter() {
  const [signedIn, setSignedIn] = useState(() => !!read(KEY) && !!read(NAME));
  const [problem, setProblem] = useState<string>();
  const [company, setCompany] = useState(companyOf);
  const [choices, setChoices] = useState<string[] | null>(null);
  const [ov, setOv] = useState<Overview | null>(null);
  const [plant, setPlant] = useState('');
  const [open, setOpen] = useState<Table | null>(null);
  const [error, setError] = useState<string>();
  const [, tick] = useState(0);

  const signOut = useCallback((why?: string) => { write(KEY, ''); setSignedIn(false); setProblem(why); }, []);
  const pick = useCallback((c: string) => {
    const q = new URLSearchParams(location.search);
    q.set('company', c);
    history.replaceState(null, '', `${location.pathname}?${q}`);
    setCompany(c);
  }, []);
  useEffect(() => {
    if (!signedIn || company) return;
    call<{ companies: string[] }>('GET', '/api/health').then(({ companies }) => {
      if (companies.length === 1) pick(companies[0]);
      else if (!companies.length) signOut('这个令牌不包括任何公司');
      else setChoices(companies);
    }, (e: Error) => setError(e.message));
  }, [signedIn, company, pick, signOut]);
  const load = useCallback(() => {
    if (!company) return;
    call<Overview>('GET', `/api/tables/${encodeURIComponent(company)}`).then((o) => { setOv(o); setError(undefined); }, (e: Error) => setError(e.message));
  }, [company]);
  useEffect(() => {
    if (!signedIn) return;
    load();
    const t = setInterval(() => { if (!document.hidden) load(); tick((n) => n + 1); }, 10_000);
    return () => clearInterval(t);
  }, [signedIn, load]);
  const plants = useMemo(() => ov?.plants ?? [], [ov]);
  const here = plant || plants[0]?.plant || '';
  const areas = useMemo(() => {
    const by = new Map<string, Table[]>();
    for (const t of ov?.tables ?? []) if (t.plant === here) by.set(t.area || t.lineName, [...(by.get(t.area || t.lineName) ?? []), t]);
    return [...by.entries()];
  }, [ov, here]);

  const report = async (t: Table, state: State, people?: number) => {
    try {
      const r = await call<{ accepted: number; items: { status: string; error?: string }[] }>('POST', `/api/report/${encodeURIComponent(company)}/tables`,
        [{ plant: t.plant, table: t.id, state, people, source: `waiter:${read(NAME)}` }]);
      if (!r.accepted) setError(r.items[0]?.error ?? '没报上去');
      load();
    } catch (e) {
      if (e instanceof ApiError && (e.status === 401 || e.status === 403)) signOut(e.message);
      else setError((e as Error).message);
    }
  };

  if (!signedIn) return <SignIn problem={problem} onDone={() => { setSignedIn(true); setProblem(undefined); }} />;
  if (!company) {
    return (
      <div className="min-h-dvh bg-stage p-3">
        <h1 className="px-1 py-3 text-[17px] font-extrabold">选公司</h1>
        <div className="grid gap-2">
          {choices?.map((c) => (
            <Button key={c} variant="outline" className="h-14 justify-between rounded-2xl bg-card text-[16px] font-bold" onClick={() => pick(c)}>{c}<ChevronRightIcon /></Button>
          )) ?? <LoaderCircleIcon className="mx-auto mt-10 animate-spin text-muted-foreground" />}
        </div>
      </div>
    );
  }
  const k = plants.find((p) => p.plant === here);
  return (
    <div className="min-h-dvh bg-stage pb-6">
      <header className="sticky top-0 z-20 bg-card/95 px-3 pt-2 pb-2 shadow-[0_1px_0_var(--border)] backdrop-blur">
        <div className="flex min-h-12 items-center gap-2">
          <div className="min-w-0 flex-1 leading-tight">
            <h1 className="truncate text-[17px] font-extrabold">{k?.name ?? '桌态'}</h1>
            {k && <p className="truncate text-[12.5px] text-muted-foreground tabular-nums">空 {k.free} · 就餐 {k.occupied} · 预订 {k.reserved} · 清台 {k.cleaning} · 今天 {k.sessions} 台</p>}
          </div>
          <Button variant="ghost" size="icon" className="size-11 rounded-xl text-muted-foreground" aria-label="退出" onClick={() => signOut()}><LogOutIcon /></Button>
        </div>
        {plants.length > 1 && (
          <div className="mt-1 flex gap-1 overflow-x-auto rounded-xl bg-muted p-1 [scrollbar-width:none]">
            {plants.map((p) => (
              <button key={p.plant} type="button" onClick={() => setPlant(p.plant ?? '')}
                className={cn('h-9 shrink-0 rounded-lg px-3 text-[14px] font-bold text-muted-foreground', p.plant === here && 'bg-background text-foreground shadow-sm')}>{p.name}</button>
            ))}
          </div>
        )}
      </header>
      {error && <p className="mx-3 mt-3 rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
      {!ov && <LoaderCircleIcon className="mx-auto mt-10 animate-spin text-muted-foreground" />}
      {ov && !ov.tables.length && <p className="m-3 rounded-2xl bg-card p-4 text-[14px] text-muted-foreground">这家公司没有餐桌。</p>}
      {areas.map(([area, list]) => (
        <section key={area} className="mx-3 mt-3">
          <h2 className="px-1 pb-1.5 text-[13px] font-bold text-muted-foreground">{area}</h2>
          <ul className="grid grid-cols-3 gap-2 max-[360px]:grid-cols-2">
            {list.map((t) => (
              <li key={t.id}>
                <button type="button" onClick={() => setOpen(t)} className={cn('grid min-h-24 w-full content-between rounded-2xl border-2 p-2.5 text-left active:scale-[0.98]', STATE[t.state].card)}>
                  <span className="flex items-center justify-between gap-1">
                    <b className="font-mono text-[18px]">{t.id}</b>
                    <small className="text-[12px] text-muted-foreground">{t.seats} 座</small>
                  </span>
                  <span className={cn('justify-self-start rounded-md px-1.5 py-0.5 text-[12px] font-bold', STATE[t.state].chip)}>{STATE[t.state].label}</span>
                  <small className="text-[12px] text-muted-foreground tabular-nums">
                    {t.state === 'occupied' ? `${t.people} 人 · ${minutes(t)} 分钟` : t.state === 'free' ? (t.reported ? '已上报' : '仿真') : `${minutes(t)} 分钟`}
                  </small>
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
      {open && <Sheet t={open} onClose={() => setOpen(null)} onReport={(s, n) => report(open, s, n)} />}
    </div>
  );
}
