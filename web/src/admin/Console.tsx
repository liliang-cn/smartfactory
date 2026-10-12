// The console (/admin): where administrators build what the twin shows — customers with their
// plants and lines, the industry packs they are built from, who may do what, and what was done.
import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useState } from 'react';
import { ExternalLinkIcon, LogOutIcon, MenuIcon, SparklesIcon, type LucideIcon } from 'lucide-react';
import { Kbd } from '@/components/ui/kbd';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { Toaster } from '@/components/ui/sonner';
import { TooltipProvider } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import * as api from './api';
import { go, match, query, usePath } from './router';
import { SECTIONS, tabHref } from './sections';
import { AgentPanel, useAgent } from './AgentPanel';

// pages load on first visit
const Customers = lazy(() => import('./Customers'));
const Customer = lazy(() => import('./Customer'));
const Builder = lazy(() => import('./Builder'));
const Industries = lazy(() => import('./Industries'));
const Templates = lazy(() => import('./Templates'));
const Accounts = lazy(() => import('./Accounts'));
const Activity = lazy(() => import('./Activity'));

interface Ctx { ov: api.Overview; reload(): Promise<void> }
const ConsoleCtx = createContext<Ctx | null>(null);
export const useConsole = () => useContext(ConsoleCtx)!;

/** The one company this server runs (a company's own installation), or undefined where it runs several. */
export const ownCompany = (ov: api.Overview) =>
  ov.single || ov.runs?.length === 1 ? ov.companies.find((c) => c.id === (ov.runs?.[0] ?? c.id)) : undefined;

function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden>
      <path d="M16 3 28 9.5v13L16 29 4 22.5v-13z" fill="#2f5bea" />
      <path d="M16 3 28 9.5 16 16 4 9.5z" fill="#6f8ff5" />
      <path d="M16 16v13L4 22.5v-13z" fill="#1f3fb0" />
    </svg>
  );
}

function SignIn({ problem, onDone }: { problem?: string; onDone(): void }) {
  const [value, setValue] = useState('');
  return (
    <div className="grid min-h-dvh place-items-center bg-stage p-4">
      <form
        className="w-full max-w-[400px] rounded-[20px] bg-card p-6 shadow-float"
        onSubmit={(e) => { e.preventDefault(); if (value.trim()) { api.signIn(value); onDone(); } }}
      >
        <div className="flex items-center gap-2.5">
          <Mark className="size-8" />
          <div className="leading-tight">
            <h1 className="text-[17px] font-extrabold">SmartFactory 工作台</h1>
            <p className="text-[12.5px] text-muted-foreground">你的智能工厂</p>
          </div>
        </div>
        <label htmlFor="token" className="mt-6 block text-[13px] font-bold">管理员令牌</label>
        <Input id="token" type="password" autoComplete="off" autoFocus value={value} onChange={(e) => setValue(e.target.value)}
          placeholder="sf_…" className="mt-1.5 h-11 rounded-xl font-mono" />
        {problem && <p className="mt-2 text-[12.5px] text-destructive">{problem}</p>}
        <p className="mt-2 text-[12px] leading-relaxed text-muted-foreground">
          令牌只保存在这个标签页里，关掉就要重新登录。还没有令牌：在服务器上运行 <code className="rounded bg-surface px-1 font-mono">smartfactory token -role admin</code>。
        </p>
        <Button type="submit" className="mt-5 h-11 w-full rounded-xl text-[14px] font-bold" disabled={!value.trim()}>登录</Button>
      </form>
    </div>
  );
}

const item = 'flex h-9 items-center gap-2.5 rounded-xl px-3 text-[13.5px] font-bold text-muted-foreground transition-colors hover:bg-surface hover:text-foreground';
const itemHere = 'bg-brand-soft text-primary hover:bg-brand-soft hover:text-primary';

function Item({ to, here, icon: Icon, label, onPick }: { to: string; here: boolean; icon: LucideIcon; label: string; onPick?: () => void }) {
  return (
    <a href={to} aria-current={here ? 'page' : undefined} onClick={(e) => { if (e.metaKey || e.ctrlKey) return; e.preventDefault(); go(to); onPick?.(); }}
      className={cn(item, here && itemHere)}>
      <Icon className="size-[17px]" />{label}
    </a>
  );
}

function Nav({ path, onPick }: { path: string; onPick?: () => void }) {
  const { ov } = useConsole();
  const p = path.split('?')[0].replace(/\/+$/, '') || '/admin';
  // the company being looked at: this server's own on the home page, else the one in the path
  const m = match(p, '/admin/customers/:id') ?? match(p, '/admin/customers/:id/edit');
  const own = ownCompany(ov);
  const home = !!own && p === '/admin';
  const company = home ? own : ov.companies.find((c) => c.id === m?.id);
  const tab = query(path).get('tab') ?? 'scene';
  return (
    <nav className="grid gap-3" aria-label="工作台">
      {!own && ov.companies.length > 1 && (
        <Select value={company?.id ?? ''} onValueChange={(id) => { go(`/admin/customers/${encodeURIComponent(id)}`); onPick?.(); }}>
          <SelectTrigger className="h-10 w-full rounded-xl font-bold" aria-label="公司"><SelectValue placeholder="选一家公司" /></SelectTrigger>
          <SelectContent>{ov.companies.map((c) => <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>)}</SelectContent>
        </Select>
      )}
      {company && (
        <>
          {(own || ov.companies.length === 1) && (
            <div className="flex items-center gap-2 px-3">
              <span className="grid h-6 min-w-7 place-items-center rounded-md bg-primary px-1 text-[11px] font-extrabold text-primary-foreground">{company.short}</span>
              <b className="truncate text-[14px] font-extrabold">{company.name}</b>
            </div>
          )}
          {SECTIONS.map((g) => (
            <div key={g.group} className="grid gap-0.5">
              <span className="px-3 pb-1 text-[11.5px] font-bold text-muted-foreground/70">{g.group}</span>
              {g.tabs.map((t) => (
                <Item key={t.id} to={tabHref(company.id, home, t.id)} here={tab === t.id} icon={t.icon} label={t.label} onPick={onPick} />
              ))}
            </div>
          ))}
        </>
      )}
    </nav>
  );
}

function AskButton() {
  return (
    <button type="button" onClick={() => useAgent.getState().setOpen(true)}
      className="flex h-10 items-center gap-2.5 rounded-xl border bg-card px-3 text-[13.5px] font-bold transition-colors hover:border-primary/40 hover:bg-brand-soft">
      <SparklesIcon className="size-[17px] text-primary" />问工作台助手
      <Kbd className="ml-auto">⌘J</Kbd>
    </button>
  );
}

function MeCard({ me, onSignOut }: { me: api.Me; onSignOut(): void }) {
  const { ov } = useConsole();
  const role = { admin: '管理员', operator: '操作员', member: '成员' }[me.role as string] ?? me.role;
  return (
    <div className="flex items-center gap-2.5 rounded-2xl bg-surface p-2.5">
      <span className="grid size-9 shrink-0 place-items-center rounded-full bg-gradient-to-br from-amber-300 to-orange-400 font-extrabold text-white">{[...me.name][0]}</span>
      <span className="grid min-w-0 flex-1 leading-tight">
        <b className="truncate text-[13.5px]">{me.name}</b>
        <small className="truncate text-[11.5px] text-muted-foreground">{ownCompany(ov) ? role : me.everywhere ? `所有公司的${role}` : `${me.companies.join('、')} 的${role}`}</small>
      </span>
      <Button variant="ghost" size="icon-sm" className="rounded-lg text-muted-foreground" title="退出" aria-label="退出" onClick={onSignOut}>
        <LogOutIcon className="size-4" />
      </Button>
    </div>
  );
}

function Page({ path }: { path: string }) {
  let m: Record<string, string> | null;
  if ((m = match(path, '/admin/customers/:id/edit'))) return <Builder edit={m.id} />;
  if ((m = match(path, '/admin/customers/:id'))) return <Customer id={m.id} />;
  if (match(path, '/admin/new')) return <Builder />;
  if ((m = match(path, '/admin/industries/:id'))) return <Industries open={m.id} />;
  if (match(path, '/admin/industries')) return <Industries />;
  if (match(path, '/admin/templates')) return <Templates />;
  if (match(path, '/admin/accounts')) return <Accounts />;
  if (match(path, '/admin/activity')) return <Activity />;
  return <Customers />;
}

export default function Console() {
  const path = usePath();
  const [ov, setOv] = useState<api.Overview | null>(null);
  const [state, setState] = useState<'loading' | 'signin' | 'ready' | 'error'>('loading');
  const [problem, setProblem] = useState<string>();
  const [menu, setMenu] = useState(false);

  const reload = useCallback(async () => {
    if (!api.token()) { setState('signin'); return; }
    try {
      setOv(await api.overview());
      setState('ready');
    } catch (e) {
      const err = e as api.ApiError;
      if (err.status === 401 || err.status === 403) {
        api.signOut();
        setProblem(err.status === 401 ? '这个令牌不对，或者已经过期、被撤销' : err.message);
        setState('signin');
      } else {
        setProblem(err.message);
        setState('error');
      }
    }
  }, []);
  useEffect(() => { reload(); }, [reload]);
  useEffect(() => { document.title = 'SmartFactory 工作台'; }, []);

  if (state === 'loading') return <div className="min-h-dvh bg-stage" />;
  if (state === 'signin') return <SignIn problem={problem} onDone={() => { setProblem(undefined); setState('loading'); reload(); }} />;
  if (state === 'error' || !ov) {
    return (
      <div className="grid min-h-dvh place-items-center bg-stage p-4">
        <p className="rounded-2xl bg-danger-soft px-4 py-3 text-[13.5px] font-semibold text-destructive">{problem}</p>
      </div>
    );
  }
  const signOut = () => { api.signOut(); setOv(null); setProblem(undefined); setState('signin'); };
  return (
    <ConsoleCtx.Provider value={{ ov, reload }}>
      <TooltipProvider delayDuration={300}>
        <div className="min-h-dvh bg-stage lg:grid lg:grid-cols-[248px_minmax(0,1fr)]">
          <aside className="sticky top-0 hidden h-dvh flex-col gap-4 border-r bg-card p-4 lg:flex">
            <a href="/admin" onClick={(e) => { e.preventDefault(); go('/admin'); }} className="flex items-center gap-2.5 px-1.5 pt-1">
              <Mark className="size-8" />
              <span className="grid leading-tight">
                <b className="text-[15.5px] font-extrabold tracking-tight">SmartFactory</b>
                <small className="text-[12px] text-muted-foreground">工作台</small>
              </span>
            </a>
            <div className="-mx-1 min-h-0 flex-1 overflow-y-auto px-1 [scrollbar-width:thin]"><Nav path={path} /></div>
            <div className="grid gap-2">
              <AskButton />
              <a href="/" className="flex h-9 items-center gap-2 rounded-xl px-3 text-[13px] font-bold text-muted-foreground hover:bg-surface hover:text-foreground">
                <ExternalLinkIcon className="size-4" />打开孪生
              </a>
              <MeCard me={ov.me} onSignOut={signOut} />
            </div>
          </aside>
          <header className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b bg-card/95 px-3 backdrop-blur lg:hidden">
            <Button variant="ghost" size="icon" className="rounded-xl" aria-label="菜单" onClick={() => setMenu(true)}><MenuIcon /></Button>
            <Mark className="size-7" />
            <b className="text-[15px] font-extrabold">工作台</b>
            <Button size="sm" className="ml-auto h-9 rounded-xl bg-foreground font-bold text-background hover:bg-foreground/90" onClick={() => useAgent.getState().setOpen(true)}>
              <SparklesIcon />助手
            </Button>
          </header>
          <Sheet open={menu} onOpenChange={setMenu}>
            <SheetContent side="left" className="w-[280px] gap-5 p-4">
              <SheetTitle className="flex items-center gap-2.5 px-1.5 pt-1 text-[15.5px] font-extrabold"><Mark className="size-8" />SmartFactory 工作台</SheetTitle>
              <SheetDescription className="sr-only">工作台导航</SheetDescription>
              <div className="min-h-0 flex-1 overflow-y-auto"><Nav path={path} onPick={() => setMenu(false)} /></div>
              <div className="grid gap-2">
                <a href="/" className="flex h-9 items-center gap-2 rounded-xl px-3 text-[13px] font-bold text-muted-foreground hover:bg-surface"><ExternalLinkIcon className="size-4" />打开孪生</a>
                <MeCard me={ov.me} onSignOut={signOut} />
              </div>
            </SheetContent>
          </Sheet>
          <main className="min-w-0 px-8 pt-7 pb-16 max-lg:px-4 max-lg:pt-4">
            <Suspense fallback={null}><Page path={path} /></Suspense>
          </main>
        </div>
        <AgentPanel />
        <Toaster theme="light" position="top-center" />
      </TooltipProvider>
    </ConsoleCtx.Provider>
  );
}

/** A page's title row: the heading, what it is about, and its actions. */
export function PageHead({ title, sub, children, back }: { title: React.ReactNode; sub?: React.ReactNode; children?: React.ReactNode; back?: { to: string; label: string } }) {
  return (
    <div className="mb-6 max-lg:mb-4">
      {back && (
        <a href={back.to} onClick={(e) => { e.preventDefault(); go(back.to); }} className="mb-2 inline-block text-[13px] font-bold text-muted-foreground hover:text-primary">‹ {back.label}</a>
      )}
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-[min(100%,280px)] flex-1">
          <h1 className="text-[24px] leading-tight font-extrabold tracking-tight max-lg:text-[20px]">{title}</h1>
          {sub && <p className="mt-1 text-[13.5px] text-muted-foreground">{sub}</p>}
        </div>
        {children && <div className="flex flex-wrap items-center gap-2">{children}</div>}
      </div>
    </div>
  );
}

export const link = (to: string) => ({ href: to, onClick: (e: React.MouseEvent) => { if (e.metaKey || e.ctrlKey) return; e.preventDefault(); go(to); } });
