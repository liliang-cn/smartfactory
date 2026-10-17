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
  visitor: 'bg-surface text-muted-foreground', member: 'bg-surface text-foreground', operator: 'bg-warning-soft text-warning', admin: 'bg-brand-soft text-primary',
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
  const [all, setAll] = useState(false);
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
                  {(['member', 'operator', 'admin', 'erp', 'reporter'] as const).map((r) => (
                    <Label key={r} htmlFor={`role-${r}`} className={cn('flex cursor-pointer items-start gap-3 rounded-xl border p-3 font-normal transition-colors', role === r && 'border-primary bg-brand-soft/50')}>
                      <RadioGroupItem id={`role-${r}`} value={r} className="mt-0.5" />
                      <span className="grid gap-0.5"><b className="text-[13.5px]">{api.ROLE_LABEL[r]}</b><small className="text-[12px] text-muted-foreground">{api.ROLE_NOTE[r]}</small></span>
                    </Label>
                  ))}
                </RadioGroup>
              </div>
              <div className="grid gap-1.5">
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
              </div>
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
              <div className={cn('grid gap-1.5 text-[12.5px]', note && 'hidden')}>
                <button type="button" onClick={() => copy(twin, '孪生链接')} className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-left font-bold hover:bg-surface">
                  <CopyIcon className="size-3.5 text-muted-foreground" />复制带令牌的孪生链接<span className="ml-auto font-normal text-muted-foreground">打开就是{api.ROLE_LABEL[done.account.role]}</span>
                </button>
                {done.account.role === 'admin' && (
                  <button type="button" onClick={() => copy(`${location.origin}/admin?token=${done.token}`, '后台链接')} className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-left font-bold hover:bg-surface">
                    <CopyIcon className="size-3.5 text-muted-foreground" />复制带令牌的后台链接
                  </button>
                )}
                <p className="px-2 text-muted-foreground">接 MCP 的客户端：请求头带 <code className="font-mono">Authorization: Bearer 令牌</code>。</p>
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
  const [showRevoked, setShowRevoked] = useState(false);
  const load = useCallback(() => { api.accounts().then(setList, (e: Error) => setError(e.message)); }, []);
  useEffect(load, [load]);
  const names = useMemo(() => new Map(ov.companies.map((c) => [c.id, c.name])), [ov.companies]);
  const mine = (list ?? []).filter((a) => !company || !a.companies.length || a.companies.includes(company));
  const revoked = mine.filter((a) => a.revoked).length;
  const shown = mine.filter((a) => showRevoked || !a.revoked);
  return (
    <section className="rounded-[18px] bg-card shadow-float">
      <div className="flex flex-wrap items-center gap-2 p-5 pb-3 max-sm:p-4">
        <h2 className="text-[15px] font-extrabold">{company ? `能进 ${names.get(company) ?? company} 的令牌` : '令牌'}</h2>
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
              <TableHead className="pl-5">持有人</TableHead><TableHead>角色</TableHead><TableHead>公司</TableHead>
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
                <TableCell className="max-w-[220px] truncate">{a.companies.length ? a.companies.map((c) => names.get(c) ?? c).join('、') : <span className="font-bold">所有公司</span>}</TableCell>
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
            <AlertDialogDescription>立刻失效：页面、实时连接里的操作和 MCP 都按访客对待。要恢复只能重新发一个。</AlertDialogDescription>
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

export default function Accounts() {
  const { ov } = useConsole();
  return (
    <>
      <PageHead title="账号" sub={ov.me.everywhere ? '每个人、每个系统各用自己的令牌，操作记录里才分得清是谁' : `你管理 ${ov.companies.map((c) => c.name).join('、')} 的令牌`} />
      <AccountList />
    </>
  );
}
