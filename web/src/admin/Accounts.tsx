// 账号: who holds a token, for which companies and with what role; issue one, revoke one.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import { CheckIcon, CopyIcon, KeyRoundIcon, LoaderCircleIcon, PlusIcon } from 'lucide-react';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import * as api from './api';
import { PageHead, useConsole } from './Console';

export function ago(iso?: string): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const s = (Date.now() - t) / 1000;
  if (s < 90) return '刚刚';
  if (s < 3600) return `${Math.round(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.round(s / 3600)} 小时前`;
  if (s < 86400 * 30) return `${Math.round(s / 86400)} 天前`;
  return new Date(t).toLocaleDateString('zh-CN');
}

const ROLE_TONE: Record<api.Role, string> = {
  visitor: 'bg-surface text-muted-foreground', member: 'bg-surface text-foreground', inspector: 'bg-success-soft text-success', operator: 'bg-warning-soft text-warning', admin: 'bg-brand-soft text-primary',
  erp: 'bg-success-soft text-success', reporter: 'bg-success-soft text-success',
};
export const RoleChip = ({ role }: { role: api.Role }) => (
  <span className={cn('inline-block rounded-md px-1.5 py-0.5 text-[12px] font-bold whitespace-nowrap', ROLE_TONE[role])}>{api.ROLE_LABEL[role]}</span>
);

function Status({ a }: { a: api.Account }) {
  if (a.revoked) return <span className="text-[12.5px] text-muted-foreground" title={`${a.revokedBy ?? ''} ${new Date(a.revoked).toLocaleString('zh-CN')}`}>已撤销</span>;
  if (a.expired) return <span className="text-[12.5px] text-destructive">{a.expires} 过期了</span>;
  return <span className="text-[12.5px] text-success">{a.expires ? `用到 ${a.expires}` : '有效'}</span>;
}

const FROM: Record<api.Account['from'], string> = { legacy: '最初的共用令牌', config: '服务器配置文件', issued: '后台签发' };

export function Issue({ open, onOpenChange, company, onIssued, holder = '', as = 'operator', note }: {
  open: boolean; onOpenChange(o: boolean): void; company?: string; onIssued(): void;
  /** who it is for, filled in (a system's name) */
  holder?: string;
  /** the role picked at first */
  as?: Exclude<api.Role, 'visitor'>;
  /** what to do with it, under the token once issued */
  note?: React.ReactNode;
}) {
  const { ov } = useConsole();
  const [name, setName] = useState('');
  const [role, setRole] = useState<Exclude<api.Role, 'visitor'>>(as);
  const [all, setAll] = useState(ov.single);
  // table reports are for shops and sites where guests sit, not for a plant
  const seated = useMemo(() => ov.companies.some((c) => c.plants.some((p) => p.site !== 'hall')), [ov]);
  const [picked, setPicked] = useState<string[]>(company ? [company] : []);
  const [expires, setExpires] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [done, setDone] = useState<api.IssuedToken | null>(null);
  useEffect(() => {
    if (open) { setName(holder); setRole(as); setAll(false); setPicked(company ? [company] : []); setExpires(''); setError(undefined); setDone(null); }
  }, [open, company, holder, as]);
  const ready = name.trim() && (all || picked.length);
  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      setDone(await api.issue({ name: name.trim(), role, companies: all ? null : picked, ...(expires && { expires }) }));
      onIssued();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const first = done ? (done.account.companies[0] ?? ov.companies[0]?.id) : '';
  const twin = done ? `${location.origin}/?company=${encodeURIComponent(first)}&token=${done.token}` : '';
  const copy = (text: string, what: string) => { navigator.clipboard.writeText(text).then(() => toast.success(`${what}已复制`)); };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[520px] rounded-[20px]">
        {!done ? (
          <>
            <DialogHeader>
              <DialogTitle className="text-[17px] font-extrabold">发令牌</DialogTitle>
              <DialogDescription>令牌只在发出的这一刻显示一次，服务器只存它的指纹。</DialogDescription>
            </DialogHeader>
            <form className="grid gap-5" onSubmit={(e) => { e.preventDefault(); if (ready) submit(); }}>
              <div className="grid gap-1.5">
                <Label htmlFor="holder" className="text-[13px] font-bold">给谁</Label>
                <Input id="holder" value={name} onChange={(e) => setName(e.target.value)} placeholder="张工，或者一个系统的名字" className="h-10 rounded-xl" autoFocus maxLength={40} />
              </div>
              <div className="grid gap-1.5">
                <span className="text-[13px] font-bold">角色</span>
                <RadioGroup value={role} onValueChange={(v) => setRole(v as typeof role)} className="grid gap-2">
                  {(['member', 'operator', 'admin', 'erp', 'reporter'] as const).filter((r) => r !== 'reporter' || seated).map((r) => (
                    <Label key={r} htmlFor={`role-${r}`} className={cn('flex cursor-pointer items-start gap-3 rounded-xl border p-3 font-normal transition-colors', role === r && 'border-primary bg-brand-soft/50')}>
                      <RadioGroupItem id={`role-${r}`} value={r} className="mt-0.5" />
                      <span className="grid gap-0.5"><b className="text-[13.5px]">{api.ROLE_LABEL[r]}</b><small className="text-[12px] text-muted-foreground">{api.ROLE_NOTE[r]}</small></span>
                    </Label>
                  ))}
                </RadioGroup>
              </div>
              {!ov.single && <div className="grid gap-1.5">
                <span className="text-[13px] font-bold">哪些公司</span>
                <div className="flex flex-wrap gap-2">
                  {ov.me.everywhere && (
                    <Label className={cn('flex cursor-pointer items-center gap-2 rounded-xl border px-3 py-2 text-[13px] font-bold', all && 'border-primary bg-brand-soft/50 text-primary')}>
                      <Checkbox checked={all} onCheckedChange={(v) => setAll(v === true)} />所有公司，包括以后新建的
                    </Label>
                  )}
                  {!all && ov.companies.map((c) => {
                    const on = picked.includes(c.id);
                    return (
                      <Label key={c.id} className={cn('flex cursor-pointer items-center gap-2 rounded-xl border px-3 py-2 text-[13px] font-bold', on && 'border-primary bg-brand-soft/50 text-primary')}>
                        <Checkbox checked={on} onCheckedChange={(v) => setPicked((p) => (v === true ? [...p, c.id] : p.filter((x) => x !== c.id)))} />{c.name}
                      </Label>
                    );
                  })}
                </div>
              </div>}
              <div className="grid gap-1.5">
                <Label htmlFor="expires" className="text-[13px] font-bold">用到哪天 <small className="font-normal text-muted-foreground">不填就一直有效，可以随时撤销</small></Label>
                <Input id="expires" type="date" value={expires} onChange={(e) => setExpires(e.target.value)} min={new Date().toISOString().slice(0, 10)} className="h-10 w-48 rounded-xl" />
              </div>
              {error && <p className="rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
              <DialogFooter>
                <Button type="button" variant="ghost" className="rounded-xl" onClick={() => onOpenChange(false)}>取消</Button>
                <Button type="submit" className="rounded-xl font-bold" disabled={!ready || busy}>{busy && <LoaderCircleIcon className="animate-spin" />}发令牌</Button>
              </DialogFooter>
            </form>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2 text-[17px] font-extrabold"><span className="grid size-7 place-items-center rounded-full bg-success-soft text-success"><CheckIcon className="size-4" /></span>{done.account.name}的令牌</DialogTitle>
              <DialogDescription>现在复制下来交给 {done.account.name}。关掉这个窗口就再也看不到了。</DialogDescription>
            </DialogHeader>
            <div className="grid gap-3">
              <button type="button" onClick={() => copy(done.token, '令牌')}
                className="flex items-center gap-2 rounded-xl border-2 border-dashed border-primary/40 bg-brand-soft/40 px-3 py-3 text-left hover:bg-brand-soft">
                <KeyRoundIcon className="size-4 shrink-0 text-primary" />
                <code className="min-w-0 flex-1 font-mono text-[13px] break-all">{done.token}</code>
                <CopyIcon className="size-4 shrink-0 text-muted-foreground" />
              </button>
              {note}
              {done.account.role === 'erp' && !note && (() => {
                // the order API: where to send, and an order to try it with (due a week ahead)
                const co = done.account.companies[0] ?? company ?? ov.companies[0]?.id ?? '';
                const endpoint = `${location.origin}/api/erp/${co}/orders`;
                const ahead = new Date(Date.now() + 8 * 3600_000 + 7 * 86_400_000).toISOString().slice(0, 10);
                const curl = `curl -X POST ${endpoint} \\\n  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \\\n  -d '{"no":"MO-1001","partNo":"料号","qty":200,"due":"${ahead} 17:00","customer":"…"}'`;
                return (
                  <div className="grid gap-1.5 text-[12.5px]">
                    <p className="px-2 text-muted-foreground">ERP 把生产工单发到这个地址，请求头带这个令牌：</p>
                    <button type="button" onClick={() => copy(endpoint, '地址')} className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-left font-mono hover:bg-surface"><CopyIcon className="size-3.5 shrink-0 text-muted-foreground" /><span className="break-all">{endpoint}</span></button>
                    <pre className="overflow-x-auto rounded-xl bg-surface px-3 py-2 font-mono text-[12px] leading-relaxed text-muted-foreground">{curl}</pre>
                  </div>
                );
              })()}
              <div className={cn('grid gap-1.5 text-[12.5px]', (note || done.account.role === 'erp') && 'hidden')}>
                <button type="button" onClick={() => copy(twin, '孪生链接')} className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-left font-bold hover:bg-surface">
                  <CopyIcon className="size-3.5 text-muted-foreground" />复制带令牌的孪生链接<span className="ml-auto font-normal text-muted-foreground">给车间大屏这类不登录的设备用</span>
                </button>
                {done.account.role === 'admin' && (
                  <button type="button" onClick={() => copy(`${location.origin}/admin?token=${done.token}`, '后台链接')} className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-left font-bold hover:bg-surface">
                    <CopyIcon className="size-3.5 text-muted-foreground" />复制带令牌的后台链接
                  </button>
                )}
                <p className="px-2 text-muted-foreground">给别的系统接入时：请求头带 <code className="font-mono">Authorization: Bearer 令牌</code>。</p>
              </div>
            </div>
            <DialogFooter><Button className="rounded-xl font-bold" onClick={() => onOpenChange(false)}>复制好了</Button></DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** The tokens the signed-in administrator manages; with company, only those covering it. */
export function AccountList({ company }: { company?: string }) {
  const { ov } = useConsole();
  const [list, setList] = useState<api.Account[] | null>(null);
  const [error, setError] = useState<string>();
  const [issuing, setIssuing] = useState(false);
  const [revoking, setRevoking] = useState<api.Account | null>(null);
  // revoked ones stay in sight, greyed, so the list says what happened to them
  const [showRevoked, setShowRevoked] = useState(true);
  const load = useCallback(() => { api.accounts().then(setList, (e: Error) => setError(e.message)); }, []);
  useEffect(load, [load]);
  const names = useMemo(() => new Map(ov.companies.map((c) => [c.id, c.name])), [ov.companies]);
  const mine = (list ?? []).filter((a) => !company || !a.companies.length || a.companies.includes(company));
  const revoked = mine.filter((a) => a.revoked).length;
  const shown = mine.filter((a) => showRevoked || !a.revoked);
  return (
    <section className="rounded-[18px] bg-card shadow-float">
      <div className="flex flex-wrap items-center gap-2 p-5 pb-3 max-sm:p-4">
        <h2 className="text-[15px] font-extrabold">{company ? `能进 ${names.get(company) ?? company} 的系统接入令牌` : '系统接入令牌'}</h2>
        <span className="basis-full text-[12.5px] text-muted-foreground order-last">给设备网关、ERP、收银系统、AI 客户端这类系统用。人用上面的账号登录。</span>
        <span className="text-[12.5px] text-muted-foreground tabular-nums">{mine.length - revoked} 个有效</span>
        <div className="ml-auto flex items-center gap-2">
          {revoked > 0 && (
            <button type="button" onClick={() => setShowRevoked((v) => !v)} className={cn('h-8 rounded-lg px-2.5 text-[12.5px] font-bold', showRevoked ? 'bg-surface text-foreground' : 'text-muted-foreground hover:bg-surface')}>
              已撤销 {revoked}
            </button>
          )}
          <Button className="h-9 rounded-xl font-bold" disabled={!ov.issue} title={ov.issue ? undefined : '服务器没有数据目录，令牌只能写进配置文件'} onClick={() => setIssuing(true)}><PlusIcon />发令牌</Button>
        </div>
      </div>
      {error && <p className="mx-5 mb-4 text-[13px] text-destructive">{error}</p>}
      {!list && !error && <div className="h-24" />}
      {list && (
        <Table>
          <TableHeader>
            <TableRow className="text-[12px]">
              <TableHead className="pl-5">持有人</TableHead><TableHead>角色</TableHead>{!ov.single && <TableHead>公司</TableHead>}
              <TableHead className="max-md:hidden">来源</TableHead><TableHead className="max-sm:hidden">最近使用</TableHead><TableHead>状态</TableHead><TableHead className="pr-5" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((a) => (
              <TableRow key={`${a.from}-${a.key}`} className={cn('text-[13px]', a.revoked && 'opacity-55')}>
                <TableCell className="pl-5">
                  <b className="font-bold">{a.name}</b>{a.key === ov.me.key && <span className="ml-1.5 rounded bg-brand-soft px-1 text-[11px] font-bold text-primary">你</span>}
                  <div className="font-mono text-[11px] text-muted-foreground">{a.key}</div>
                </TableCell>
                <TableCell><RoleChip role={a.role} /></TableCell>
                {!ov.single && <TableCell className="max-w-[220px] truncate">{a.companies.length ? a.companies.map((c) => names.get(c) ?? c).join('、') : <span className="font-bold">所有公司</span>}</TableCell>}
                <TableCell className="text-muted-foreground max-md:hidden">{a.from === 'issued' ? `${a.createdBy ?? ''}签发于 ${a.created ? new Date(a.created).toLocaleDateString('zh-CN') : ''}` : FROM[a.from]}</TableCell>
                <TableCell className="text-muted-foreground max-sm:hidden">{ago(a.used) || '没用过'}</TableCell>
                <TableCell><Status a={a} /></TableCell>
                <TableCell className="pr-5 text-right">
                  {a.from === 'issued' && !a.revoked && a.key !== ov.me.key && (
                    <Button variant="ghost" size="sm" className="rounded-lg text-muted-foreground hover:bg-danger-soft hover:text-destructive" onClick={() => setRevoking(a)}>撤销</Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {list && !shown.length && <p className="pb-8 text-center text-[13px] text-muted-foreground">还没有令牌</p>}
      <div className="h-2" />
      <Issue open={issuing} onOpenChange={setIssuing} company={company} onIssued={load} />
      <AlertDialog open={!!revoking} onOpenChange={(o) => !o && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>撤销{revoking?.name}的令牌？</AlertDialogTitle>
            <AlertDialogDescription>立刻失效：用它打开的页面和接入的系统马上不能用了。要恢复只能重新发一个。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>不撤销</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={async () => {
              const a = revoking!;
              try {
                await api.revoke(a.key);
                toast.success(`${a.name}的令牌已撤销`);
                load();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}>撤销</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

const PERSON_ROLES = ['member', 'inspector', 'operator', 'admin'] as const;

/** A password nobody has to think up: 12 letters and digits without look-alikes. */
function randomPassword(): string {
  const abc = 'abcdefghjkmnpqrstuvwxyzACDEFGHJKLMNPQRTUVWXY2346789';
  const b = new Uint8Array(12);
  crypto.getRandomValues(b);
  return [...b].map((x) => abc[x % abc.length]).join('');
}

function PersonDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange(v: boolean): void; onDone(): void }) {
  const [f, setF] = useState({ name: '', login: '', role: 'operator', password: randomPassword() });
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const [made, setMade] = useState<api.User | null>(null);
  const [copied, setCopied] = useState(false);
  const close = (v: boolean) => {
    onOpenChange(v);
    if (!v) { setMade(null); setProblem(undefined); setCopied(false); setF({ name: '', login: '', role: 'operator', password: randomPassword() }); }
  };
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="max-w-[460px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle>{made ? `${made.name} 的账号建好了` : '加一个人'}</DialogTitle>
          <DialogDescription>{made ? '把登录名和这个临时密码告诉他。他第一次登录时会被要求换成自己的密码。' : '每个人一个账号，操作记录里才分得清是谁。'}</DialogDescription>
        </DialogHeader>
        {made ? (
          <div className="grid gap-3">
            <div className="grid gap-1 rounded-xl bg-surface p-3 text-[13.5px]">
              <span>登录名：<b className="font-mono">{made.login}</b></span>
              <span>临时密码：<b className="font-mono">{f.password}</b></span>
              <span className="text-[12px] text-muted-foreground">只显示这一次，关掉就看不到了；忘了可以再重设。</span>
            </div>
            <DialogFooter className="gap-2">
              <Button variant="outline" className="h-10 rounded-xl font-bold" onClick={() => { navigator.clipboard.writeText(`登录名：${made.login}\n临时密码：${f.password}\n地址：${location.origin}`).then(() => setCopied(true)); }}>
                {copied ? <CheckIcon /> : <CopyIcon />}{copied ? '复制好了' : '复制'}
              </Button>
              <Button className="h-10 rounded-xl font-bold" onClick={() => close(false)}>好了</Button>
            </DialogFooter>
          </div>
        ) : (
          <form className="grid gap-3" onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setProblem(undefined);
            try {
              setMade(await api.createUser({ name: f.name.trim(), login: f.login.trim(), role: f.role, password: f.password }));
              onDone();
            } catch (err) {
              setProblem((err as Error).message);
            } finally {
              setBusy(false);
            }
          }}>
            <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
              <div className="grid gap-1.5">
                <Label htmlFor="p-name" className="text-[13px] font-bold">姓名</Label>
                <Input id="p-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="张班长" className="h-10 rounded-xl" autoFocus maxLength={40} />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="p-login" className="text-[13px] font-bold">登录名</Label>
                <Input id="p-login" value={f.login} onChange={(e) => setF({ ...f, login: e.target.value })} placeholder="手机号或工号" className="h-10 rounded-xl" maxLength={40} />
              </div>
            </div>
            <div className="grid gap-1.5">
              <span className="text-[13px] font-bold">角色</span>
              <RadioGroup value={f.role} onValueChange={(v) => setF({ ...f, role: v })} className="grid gap-2">
                {PERSON_ROLES.map((r) => (
                  <Label key={r} htmlFor={`p-role-${r}`} className={cn('flex cursor-pointer items-start gap-3 rounded-xl border p-3 font-normal transition-colors', f.role === r && 'border-primary bg-brand-soft/50')}>
                    <RadioGroupItem id={`p-role-${r}`} value={r} className="mt-0.5" />
                    <span className="grid gap-0.5"><b className="text-[13.5px]">{api.ROLE_LABEL[r]}</b><small className="text-[12px] text-muted-foreground">{api.ROLE_NOTE[r]}</small></span>
                  </Label>
                ))}
              </RadioGroup>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="p-pw" className="text-[13px] font-bold">临时密码</Label>
              <div className="flex gap-2">
                <Input id="p-pw" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} className="h-10 rounded-xl font-mono" />
                <Button type="button" variant="outline" className="h-10 rounded-xl" onClick={() => setF({ ...f, password: randomPassword() })}>换一个</Button>
              </div>
            </div>
            {problem && <p className="text-[12.5px] text-destructive" role="alert">{problem}</p>}
            <DialogFooter>
              <Button type="submit" className="h-10 rounded-xl font-bold" disabled={busy || !f.name.trim() || !f.login.trim() || f.password.length < 8}>
                {busy && <LoaderCircleIcon className="animate-spin" />}建账号
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** The people with an account: who they are, their role, when they last signed in. */
export function PeopleList() {
  const { ov } = useConsole();
  const [data, setData] = useState<{ users: api.User[]; mustLogin: boolean } | null>(null);
  const [error, setError] = useState<string>();
  const [adding, setAdding] = useState(false);
  const [reset, setReset] = useState<{ user: api.User; password: string; done: boolean } | null>(null);
  const load = useCallback(() => { api.users().then(setData, (e: Error) => setError(e.message)); }, []);
  useEffect(load, [load]);
  const act = async (f: () => Promise<unknown>, ok: string) => {
    try { await f(); toast.success(ok); load(); } catch (e) { toast.error((e as Error).message); }
  };
  if (error) return <p className="rounded-xl bg-danger-soft px-4 py-3 text-[13px] text-destructive">{error}</p>;
  return (
    <section className="rounded-[18px] bg-card shadow-float">
      <div className="flex flex-wrap items-center gap-2 p-5 pb-3 max-sm:p-4">
        <h2 className="text-[15px] font-extrabold">人</h2>
        <span className="text-[12.5px] text-muted-foreground tabular-nums">{data ? `${data.users.filter((u) => !u.disabled).length} 个账号在用` : ''}</span>
        <Button className="ml-auto h-9 rounded-xl font-bold" disabled={!data} onClick={() => setAdding(true)}><PlusIcon />加一个人</Button>
      </div>
      {data && (
        <label className="mx-5 mb-3 flex cursor-pointer items-start gap-3 rounded-xl border p-3 max-sm:mx-4">
          <Checkbox checked={data.mustLogin} onCheckedChange={(v) => act(() => api.setMustLogin(v === true), v === true ? '现在要登录才能看' : '访客也能看了')} className="mt-0.5" />
          <span className="grid gap-0.5">
            <b className="text-[13.5px]">必须登录</b>
            <small className="text-[12px] text-muted-foreground">开着：孪生、点检和实时数据只给有账号的人和接入的系统看。关掉：不登录的人也能看孪生（只能看，不能操作）。</small>
          </span>
        </label>
      )}
      {!data ? (
        <div className="grid h-24 place-items-center"><LoaderCircleIcon className="animate-spin text-muted-foreground" /></div>
      ) : data.users.length === 0 ? (
        <p className="px-5 pb-5 text-[13px] text-muted-foreground">还没有人有账号。点「加一个人」给班长、质检员、操作员各建一个。</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="text-[12px]">
              <TableHead className="pl-5">姓名</TableHead><TableHead>角色</TableHead><TableHead className="max-sm:hidden">最近登录</TableHead><TableHead>状态</TableHead><TableHead className="pr-5" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.users.map((u) => {
              const self = `u:${u.id}` === ov.me.key;
              return (
                <TableRow key={u.id} className={cn('text-[13px]', u.disabled && 'opacity-55')}>
                  <TableCell className="pl-5">
                    <b className="font-bold">{u.name}</b>{self && <span className="ml-1.5 rounded bg-brand-soft px-1 text-[11px] font-bold text-primary">你</span>}
                    <div className="font-mono text-[11px] text-muted-foreground">{u.login}</div>
                  </TableCell>
                  <TableCell>
                    <select aria-label={`${u.name}的角色`} value={u.role} disabled={self} className="h-8 rounded-lg border bg-background px-2 text-[12.5px] font-bold"
                      onChange={(e) => act(() => api.updateUser(u.id, { role: e.target.value }), `${u.name} 现在是${api.ROLE_LABEL[e.target.value as api.Role]}`)}>
                      {PERSON_ROLES.map((r) => <option key={r} value={r}>{api.ROLE_LABEL[r]}</option>)}
                    </select>
                  </TableCell>
                  <TableCell className="text-muted-foreground max-sm:hidden">{ago(u.lastIn) || '没登录过'}</TableCell>
                  <TableCell>{u.disabled ? <span className="text-[12.5px] text-muted-foreground">已停用</span> : u.mustChange ? <span className="text-[12.5px] text-warning">等他换密码</span> : <span className="text-[12.5px] text-success">在用</span>}</TableCell>
                  <TableCell className="pr-5 text-right whitespace-nowrap">
                    {!self && (
                      <>
                        <Button variant="ghost" size="sm" className="rounded-lg text-[12.5px]" onClick={() => setReset({ user: u, password: randomPassword(), done: false })}>重设密码</Button>
                        <Button variant="ghost" size="sm" className={cn('rounded-lg text-[12.5px]', !u.disabled && 'text-destructive hover:bg-danger-soft hover:text-destructive')}
                          onClick={() => act(() => api.updateUser(u.id, { disabled: !u.disabled }), u.disabled ? `${u.name} 可以登录了` : `${u.name} 停用了，他已登录的页面马上退出`)}>
                          {u.disabled ? '启用' : '停用'}
                        </Button>
                      </>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
      <PersonDialog open={adding} onOpenChange={setAdding} onDone={load} />
      <Dialog open={!!reset} onOpenChange={(v) => { if (!v) setReset(null); }}>
        <DialogContent className="max-w-[420px] rounded-[20px]">
          <DialogHeader>
            <DialogTitle>重设 {reset?.user.name} 的密码</DialogTitle>
            <DialogDescription>{reset?.done ? '把这个临时密码告诉他，他下次登录会被要求换掉。他已登录的地方都退出了。' : '给一个临时密码，他已登录的地方会退出。'}</DialogDescription>
          </DialogHeader>
          {reset && (
            <div className="grid gap-3">
              <Input value={reset.password} readOnly={reset.done} onChange={(e) => setReset({ ...reset, password: e.target.value })} className="h-10 rounded-xl font-mono" aria-label="临时密码" />
              <DialogFooter>
                {reset.done
                  ? <Button className="h-10 rounded-xl font-bold" onClick={() => setReset(null)}>好了</Button>
                  : <Button className="h-10 rounded-xl font-bold" disabled={reset.password.length < 8} onClick={async () => {
                      try { await api.updateUser(reset.user.id, { password: reset.password }); setReset({ ...reset, done: true }); load(); } catch (e) { toast.error((e as Error).message); }
                    }}>重设</Button>}
              </DialogFooter>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </section>
  );
}

export default function Accounts() {
  return (
    <>
      <PageHead title="账号" sub="每个人一个账号、每个系统一个令牌，操作记录里才分得清是谁" />
      <div className="grid gap-5">
        <PeopleList />
        <AccountList />
      </div>
    </>
  );
}
