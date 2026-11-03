// Signing in with an account (a login and a password), for the twin, the check page and the
// console. The session lives in an httpOnly cookie the page never sees; systems keep using tokens.
import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import type { AuthState } from '@/admin/api';

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-SF-Via': 'page' }, body: JSON.stringify(body) });
  const b = await res.json().catch(() => null) as { error?: string } | null;
  if (!res.ok) throw new Error(b?.error ?? `请求失败（${res.status}）`);
  return b as T;
}

/** What the server says about signing in: who this browser is, whether visitors are kept out. */
export async function fetchAuthState(): Promise<AuthState | null> {
  try {
    const res = await fetch('/api/auth/state');
    return res.ok ? await res.json() as AuthState : null;
  } catch {
    return null;
  }
}

export const signInWith = (login: string, password: string) => post<AuthState>('/api/auth/signin', { login, password });
export const changePassword = (old: string, next: string) => post<{ ok: boolean }>('/api/auth/password', { old, next });
export const signOutSession = () => post<{ ok: boolean }>('/api/auth/signout', {}).catch(() => null);

/** The login and password fields with their button; onDone once the session is set. */
export function PasswordForm({ onDone, big, autoFocus = true }: { onDone(s: AuthState): void; big?: boolean; autoFocus?: boolean }) {
  const [login, setLogin] = useState('');
  const [password, setPassword] = useState('');
  const [problem, setProblem] = useState<string>();
  const [busy, setBusy] = useState(false);
  const h = big ? 'h-12 text-[15px]' : 'h-11 text-[14px]';
  return (
    <form className="grid gap-1.5" onSubmit={async (e) => {
      e.preventDefault();
      if (!login.trim() || !password) return;
      setBusy(true);
      setProblem(undefined);
      try {
        onDone(await signInWith(login.trim(), password));
      } catch (err) {
        setProblem((err as Error).message);
        setPassword('');
      } finally {
        setBusy(false);
      }
    }}>
      <label htmlFor="sf-login" className="text-[13px] font-bold">登录名</label>
      <Input id="sf-login" autoComplete="username" autoFocus={autoFocus} value={login} onChange={(e) => setLogin(e.target.value)} placeholder="手机号或工号" className={`${h} rounded-xl`} />
      <label htmlFor="sf-password" className="mt-2 text-[13px] font-bold">密码</label>
      <Input id="sf-password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} className={`${h} rounded-xl`} />
      {problem && <p className="mt-1 text-[12.5px] text-destructive" role="alert">{problem}</p>}
      <Button type="submit" className={`mt-4 ${h} w-full rounded-xl font-bold`} disabled={busy || !login.trim() || !password}>{busy ? '登录中…' : '登录'}</Button>
      <p className="mt-1 text-[12px] leading-relaxed text-muted-foreground">没有账号或忘了密码，找管理员。登录会保持 30 天。</p>
    </form>
  );
}

/** A page of its own for a factory that keeps visitors out. */
export function SignInPage({ title, sub, onDone }: { title: string; sub: string; onDone(s: AuthState): void }) {
  return (
    <div className="grid min-h-dvh place-items-center bg-stage p-4">
      <div className="w-full max-w-[400px] rounded-[20px] bg-card p-6 shadow-float max-sm:p-5">
        <h1 className="text-[19px] font-extrabold">{title}</h1>
        <p className="mt-1 mb-5 text-[13.5px] text-muted-foreground">{sub}</p>
        <PasswordForm onDone={onDone} big />
      </div>
    </div>
  );
}

export function SignInDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange(v: boolean): void; onDone(s: AuthState): void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[400px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle>登录</DialogTitle>
          <DialogDescription>登录后按你的角色看数据、填点检或操作设备。</DialogDescription>
        </DialogHeader>
        <PasswordForm onDone={onDone} />
      </DialogContent>
    </Dialog>
  );
}

/** Change one's own password; forced when an administrator set it. */
export function PasswordDialog({ open, onOpenChange, forced }: { open: boolean; onOpenChange(v: boolean): void; forced?: boolean }) {
  const [old, setOld] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [problem, setProblem] = useState<string>();
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);
  const mismatch = !!again && next !== again;
  return (
    <Dialog open={open} onOpenChange={(v) => { if (!forced || done) onOpenChange(v); }}>
      <DialogContent className="max-w-[400px] rounded-[20px]" showCloseButton={!forced || done}>
        <DialogHeader>
          <DialogTitle>{forced ? '先换一个你自己的密码' : '修改密码'}</DialogTitle>
          <DialogDescription>{forced ? '现在的密码是管理员给你设的，换成只有你知道的。' : '换了以后，你在别的设备上的登录会退出。'}</DialogDescription>
        </DialogHeader>
        {done ? (
          <div className="grid gap-3">
            <p className="text-[13.5px]">密码改好了。</p>
            <Button className="h-11 rounded-xl font-bold" onClick={() => onOpenChange(false)}>好</Button>
          </div>
        ) : (
          <form className="grid gap-1.5" onSubmit={async (e) => {
            e.preventDefault();
            if (mismatch || next.length < 8) return;
            setBusy(true);
            setProblem(undefined);
            try {
              await changePassword(old, next);
              setDone(true);
            } catch (err) {
              setProblem((err as Error).message);
            } finally {
              setBusy(false);
            }
          }}>
            <label htmlFor="pw-old" className="text-[13px] font-bold">现在的密码</label>
            <Input id="pw-old" type="password" autoComplete="current-password" value={old} onChange={(e) => setOld(e.target.value)} className="h-11 rounded-xl" />
            <label htmlFor="pw-new" className="mt-2 text-[13px] font-bold">新密码</label>
            <Input id="pw-new" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} placeholder="至少 8 位" className="h-11 rounded-xl" />
            <label htmlFor="pw-again" className="mt-2 text-[13px] font-bold">再输一遍</label>
            <Input id="pw-again" type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} className="h-11 rounded-xl" />
            {(mismatch || problem) && <p className="mt-1 text-[12.5px] text-destructive" role="alert">{mismatch ? '两次输的不一样' : problem}</p>}
            <Button type="submit" className="mt-4 h-11 rounded-xl font-bold" disabled={busy || !old || next.length < 8 || mismatch || !again}>改密码</Button>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
