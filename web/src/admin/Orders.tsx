// 工单: the production orders the company's ERP sends, which line runs each, how far they are;
// and in 接入, how the ERP is connected — orders pushed to the order API, pulled from the ERP's
// own, and a signed callback when a line starts or finishes one.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import {
  ArrowDownToLineIcon, CircleAlertIcon, ClipboardListIcon, CopyIcon, KeyRoundIcon, LoaderCircleIcon, PlusIcon, RefreshCwIcon,
  SearchIcon, Trash2Icon, UndoIcon,
} from 'lucide-react';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import * as api from './api';
import { ago, Issue } from './Accounts';
import { useConsole } from './Console';
import { useCompany } from './layouts';
import { Panel } from './Panel';
import { go } from './router';

type Status = api.Order['status'];
const STATUS: Record<Status, { label: string; tone: string }> = {
  running: { label: '生产中', tone: 'bg-brand-soft text-primary' },
  queued: { label: '排队', tone: 'bg-surface text-foreground' },
  done: { label: '完成', tone: 'bg-success-soft text-success' },
  cancelled: { label: '已撤回', tone: 'bg-surface text-muted-foreground' },
};
const SOURCE: Record<api.Order['source'], string> = { push: 'ERP 推送', pull: 'ERP 拉取', console: '后台录入' };

const fmt = (iso?: string) => {
  if (!iso) return '';
  const d = new Date(iso);
  const day = d.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' });
  return d.getHours() || d.getMinutes() ? `${day} ${d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}` : day;
};
const late = (o: api.Order) => !!o.due && (o.status === 'queued' || o.status === 'running') && Date.parse(o.due) < Date.now();

function Chip({ status, problem }: { status: Status; problem?: string }) {
  if (status === 'queued' && problem) return <span className="rounded-md bg-danger-soft px-1.5 py-0.5 text-[12px] font-bold whitespace-nowrap text-destructive" title={problem}>排不上</span>;
  return <span className={cn('rounded-md px-1.5 py-0.5 text-[12px] font-bold whitespace-nowrap', STATUS[status].tone)}>{STATUS[status].label}</span>;
}

/** good of qty, scrap on top, as a bar. */
function Progress({ o }: { o: api.Order }) {
  const pct = Math.min(100, (o.good / Math.max(1, o.qty)) * 100);
  return (
    <div className="grid gap-1">
      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div className={cn('h-full rounded-full', o.status === 'done' ? 'bg-success' : 'bg-primary')} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-[12px] text-muted-foreground tabular-nums">
        <b className="text-foreground">{o.good}</b> / {o.qty}{o.scrap > 0 && <span className="text-destructive">，报废 {o.scrap}</span>}
      </span>
    </div>
  );
}

/** The orders each line is running, as the line sees them. */
function Running({ list, onCancel }: { list: api.Order[]; onCancel?(o: api.Order): void }) {
  if (!list.length) return null;
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3 max-sm:grid-cols-1">
      {list.map((o) => (
        <div key={o.no} className="grid gap-2.5 rounded-xl border bg-[color-mix(in_oklab,var(--primary)_4%,var(--card))] p-3.5">
          <div className="flex items-center gap-2">
            <span className="relative flex size-2"><span className="absolute inline-flex size-full animate-ping rounded-full bg-primary/50" /><span className="relative inline-flex size-2 rounded-full bg-primary" /></span>
            <b className="text-[13.5px]">{o.lineName}</b>
            <small className="text-[12px] text-muted-foreground">{o.plantName}</small>
            {onCancel && (
              <Button variant="ghost" size="icon" className="-my-1 ml-auto size-7 rounded-lg text-muted-foreground hover:bg-danger-soft hover:text-destructive" title="撤回" aria-label={`撤回 ${o.no}`} onClick={() => onCancel(o)}><UndoIcon /></Button>
            )}
          </div>
          <div className="grid leading-tight">
            <span className="font-mono text-[13px] font-bold">{o.no}</span>
            <small className="truncate text-[12px] text-muted-foreground">{[o.partNo, o.name, o.customer].filter(Boolean).join('，')}</small>
          </div>
          <Progress o={o} />
          {o.due && <small className={cn('text-[12px]', late(o) ? 'font-bold text-destructive' : 'text-muted-foreground')}>交期 {fmt(o.due)}{late(o) ? '，已过' : ''}</small>}
        </div>
      ))}
    </div>
  );
}

function Enter({ c, open, onOpenChange, onSent }: { c: api.Company; open: boolean; onOpenChange(o: boolean): void; onSent(): void }) {
  const { data } = useCompany(open ? c.id : null);
  // every part number a line here can run, with the customers of its product
  const parts = useMemo(() => {
    const out = new Map<string, { label: string; customers: string[] }>();
    for (const p of data?.plants ?? []) {
      for (const l of p.lines) {
        const pr = l.product;
        const vs = pr.variants?.length ? pr.variants : [{ partNo: pr.partNo ?? pr.key, name: pr.name, customers: undefined }];
        for (const v of vs) {
          if (!out.has(v.partNo)) out.set(v.partNo, { label: v.name, customers: v.customers ?? (pr.customers ?? []).map((x) => x.name) });
        }
      }
    }
    return out;
  }, [data]);
  const [no, setNo] = useState('');
  const [part, setPart] = useState('');
  const [qty, setQty] = useState('');
  const [due, setDue] = useState('');
  const [customer, setCustomer] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  useEffect(() => {
    if (!open) return;
    const d = new Date(Date.now() + 8 * 3600_000).toISOString();
    setNo(`MO-${d.slice(2, 4)}${d.slice(5, 7)}${d.slice(8, 10)}-${String(Math.floor(Math.random() * 900) + 100)}`);
    setPart(''); setQty(''); setDue(''); setCustomer(''); setError(undefined);
  }, [open]);
  const ready = no.trim() && part && Number(qty) > 0;
  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const [r] = await api.sendOrders(c.id, [{ no: no.trim(), partNo: part, qty: Number(qty), ...(due && { due: due.replace('T', ' ') }), ...(customer && { customer }) }]);
      if (!r.ok) {
        setError(r.error);
        return;
      }
      toast.success(`${r.no} 排到了${r.lineName}`);
      onSent();
      onOpenChange(false);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[480px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle className="text-[17px] font-extrabold">录入工单</DialogTitle>
          <DialogDescription>ERP 没接上或者要插一张急单时用。工单排到做这个料号、手上活最少的那条线。</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); if (ready) submit(); }}>
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="o-no" className="text-[13px] font-bold">工单号</Label>
              <Input id="o-no" value={no} onChange={(e) => setNo(e.target.value)} className="h-10 rounded-xl font-mono" maxLength={64} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="o-qty" className="text-[13px] font-bold">数量</Label>
              <Input id="o-qty" type="number" min={1} value={qty} onChange={(e) => setQty(e.target.value)} className="h-10 rounded-xl tabular-nums" />
            </div>
          </div>
          <div className="grid gap-1.5">
            <span className="text-[13px] font-bold">料号</span>
            <Select value={part} onValueChange={(v) => { setPart(v); setCustomer(''); }}>
              <SelectTrigger className="h-10 w-full rounded-xl" aria-label="料号"><SelectValue placeholder={data ? '选一个料号' : '读取产线…'} /></SelectTrigger>
              <SelectContent>
                {[...parts].map(([k, v]) => <SelectItem key={k} value={k}><span className="font-mono">{k}</span><span className="text-muted-foreground">{v.label}</span></SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
            <div className="grid gap-1.5">
              <Label htmlFor="o-due" className="text-[13px] font-bold">交期 <small className="font-normal text-muted-foreground">可不填</small></Label>
              <Input id="o-due" type="datetime-local" value={due} onChange={(e) => setDue(e.target.value)} className="h-10 rounded-xl" />
            </div>
            <div className="grid gap-1.5">
              <span className="text-[13px] font-bold">客户 <small className="font-normal text-muted-foreground">可不填</small></span>
              <Select value={customer} onValueChange={setCustomer} disabled={!part}>
                <SelectTrigger className="h-10 w-full rounded-xl" aria-label="客户"><SelectValue placeholder="—" /></SelectTrigger>
                <SelectContent>{(parts.get(part)?.customers ?? []).map((x) => <SelectItem key={x} value={x}>{x}</SelectItem>)}</SelectContent>
              </Select>
            </div>
          </div>
          {error && <p className="rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="ghost" className="rounded-xl" onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" className="rounded-xl font-bold" disabled={!ready || busy}>{busy && <LoaderCircleIcon className="animate-spin" />}下达</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const VIEWS = [
  { id: 'open', label: '在做和排队', status: ['running', 'queued'] as Status[] },
  { id: 'done', label: '完成', status: ['done'] as Status[] },
  { id: 'cancelled', label: '撤回', status: ['cancelled'] as Status[] },
] as const;

export function OrderBoard({ c }: { c: api.Company }) {
  const { ov } = useConsole();
  const [erp, setERP] = useState<api.ERPOverview | null>(null);
  const [list, setList] = useState<api.Order[] | null>(null);
  const [error, setError] = useState<string>();
  const [view, setView] = useState<(typeof VIEWS)[number]['id']>('open');
  const [q, setQ] = useState('');
  const [entering, setEntering] = useState(false);
  const [cancelling, setCancelling] = useState<api.Order | null>(null);
  const [pulling, setPulling] = useState(false);
  const status = VIEWS.find((v) => v.id === view)!.status;
  const load = useCallback(() => {
    api.orders(c.id, [...status], q.trim()).then((l) => { setList(l); setError(undefined); }, (e: Error) => setError(e.message));
    api.erp(c.id).then(setERP, () => {});
  }, [c.id, status, q]);
  useEffect(() => {
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, 3000);
    return () => clearInterval(t);
  }, [load]);
  const running = (list ?? []).filter((o) => o.status === 'running');
  const rest = (list ?? []).filter((o) => o.status !== 'running');
  // a queued order's place in its line's queue
  const place = new Map<string, number>();
  const seen = new Map<string, number>();
  for (const o of rest) {
    if (o.status !== 'queued' || !o.line) continue;
    const k = `${o.plant}/${o.line}`;
    seen.set(k, (seen.get(k) ?? 0) + 1);
    place.set(o.no, seen.get(k)!);
  }
  const counts = erp?.counts;
  const pull = async () => {
    setPulling(true);
    try {
      const r = await api.pullERP(c.id, false);
      toast.success(`读到 ${r.fetched} 张：新增 ${r.created}，更新 ${r.updated}${r.failed ? `，${r.failed} 张没收下` : ''}`);
      load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setPulling(false);
    }
  };
  const operator = ov.me.role === 'admin' || ov.me.role === 'operator';
  return (
    <div className="grid gap-5">
      {erp && !erp.config.enabled && (
        <div className="flex flex-wrap items-center gap-3 rounded-[18px] border border-dashed border-primary/40 bg-brand-soft/40 px-5 py-4">
          <ClipboardListIcon className="size-5 text-primary" />
          <p className="min-w-0 flex-1 text-[13.5px]"><b>还没接 ERP。</b>产线现在自己排工单（仿真）。接上以后，ERP 下达的工单排到产线上做，做完回传给 ERP。</p>
          <Button className="h-9 rounded-xl font-bold" onClick={() => go(`${location.pathname}?tab=connect`, true)}>接 ERP</Button>
        </div>
      )}
      <Panel
        title="生产工单"
        aside={
          <>
            {erp?.config.pull && <Button variant="outline" className="h-9 rounded-xl font-bold" disabled={pulling} onClick={pull}>{pulling ? <LoaderCircleIcon className="animate-spin" /> : <ArrowDownToLineIcon />}从 ERP 拉取</Button>}
            {operator && <Button className="h-9 rounded-xl font-bold" onClick={() => setEntering(true)}><PlusIcon />录入工单</Button>}
          </>
        }
      >
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <div className="flex rounded-xl bg-surface p-1">
            {VIEWS.map((v) => {
              const n = counts ? v.status.reduce((s, st) => s + (counts[st] ?? 0), 0) : undefined;
              return (
                <button key={v.id} type="button" onClick={() => setView(v.id)}
                  className={cn('h-8 rounded-lg px-3 text-[13px] font-bold', view === v.id ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>
                  {v.label}{n !== undefined && <span className="ml-1.5 tabular-nums opacity-60">{n}</span>}
                </button>
              );
            })}
          </div>
          <div className="relative ml-auto w-64 max-sm:w-full">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="工单号、料号、客户、产线" className="h-9 rounded-xl pl-9" aria-label="找工单" />
          </div>
        </div>
        {error && <p className="text-[13px] text-destructive">{error}</p>}
        {list && !list.length && (
          <p className="py-10 text-center text-[13px] text-muted-foreground">
            {q ? '没有找到' : view === 'open' ? (erp?.config.enabled ? '没有在做或排队的工单。ERP 下达的工单会出现在这里。' : '没有工单') : '还没有'}
          </p>
        )}
        <div className="grid gap-4">
          <Running list={running} onCancel={operator ? setCancelling : undefined} />
          {rest.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow className="text-[12px]">
                  <TableHead>工单</TableHead><TableHead>做什么</TableHead><TableHead>产线</TableHead><TableHead className="w-40">完成</TableHead>
                  <TableHead className="max-md:hidden">交期</TableHead><TableHead className="max-lg:hidden">来自</TableHead><TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rest.map((o) => (
                  <TableRow key={o.no} className="text-[13px]">
                    <TableCell><div className="flex items-center gap-2"><span className="font-mono font-bold">{o.no}</span><Chip status={o.status} problem={o.problem} /></div></TableCell>
                    <TableCell className="max-w-[220px]"><span className="font-mono text-[12.5px]">{o.partNo || o.product}</span><div className="truncate text-[12px] text-muted-foreground">{[o.name, o.customer].filter(Boolean).join('，')}</div></TableCell>
                    <TableCell>
                      {o.problem && o.status === 'queued' ? <span className="text-[12.5px] text-destructive">{o.problem}</span> : (
                        <><span className="font-bold">{o.lineName}</span>{place.has(o.no) && <div className="text-[12px] text-muted-foreground">排第 {place.get(o.no)}</div>}</>
                      )}
                    </TableCell>
                    <TableCell>{o.status === 'queued' ? <span className="text-muted-foreground tabular-nums">{o.qty} 件</span> : <Progress o={o} />}</TableCell>
                    <TableCell className={cn('max-md:hidden', late(o) ? 'font-bold text-destructive' : 'text-muted-foreground')}>{fmt(o.due)}</TableCell>
                    <TableCell className="text-[12px] text-muted-foreground max-lg:hidden">{SOURCE[o.source]}<div>{o.finished ? `${fmt(o.finished)} 完成` : ago(o.created)}</div></TableCell>
                    <TableCell className="text-right">
                      {operator && (o.status === 'queued' || o.status === 'running') && (
                        <Button variant="ghost" size="icon" className="size-8 rounded-lg text-muted-foreground hover:bg-danger-soft hover:text-destructive" title="撤回" aria-label={`撤回 ${o.no}`} onClick={() => setCancelling(o)}><UndoIcon /></Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      </Panel>
      <Enter c={c} open={entering} onOpenChange={setEntering} onSent={load} />
      <AlertDialog open={!!cancelling} onOpenChange={(v) => !v && setCancelling(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>撤回工单 {cancelling?.no}？</AlertDialogTitle>
            <AlertDialogDescription>
              {cancelling?.status === 'running' ? `${cancelling.lineName}停下它，转做下一张。已做的 ${cancelling.good} 件留在记录里。` : '它不再排队。'}ERP 再发一次同号工单会重新排队。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>不撤</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={async () => {
              const o = cancelling!;
              try {
                await api.cancelOrder(c.id, o.no);
                toast.success(`${o.no} 已撤回`);
                load();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}>撤回</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

// ---------- the connection (接入) ----------

const FIELD: Record<string, { label: string; hint: string }> = {
  no: { label: '工单号', hint: 'FBillNo' }, partNo: { label: '料号', hint: 'FMaterialId.FNumber' }, product: { label: '产品', hint: '' },
  name: { label: '品名', hint: '' }, qty: { label: '数量', hint: 'FQty' }, due: { label: '交期', hint: 'FPlanFinishDate' },
  customer: { label: '客户', hint: '' }, price: { label: '单价', hint: '' }, plant: { label: '工厂', hint: '' }, line: { label: '产线', hint: '' },
};

type PullForm = { url: string; method: 'GET' | 'POST'; body: string; headers: [string, string, boolean][]; items: string; fields: Record<string, string>; every: string };

function pullForm(p?: api.ERPOverview['config']['pull']): PullForm {
  return {
    url: p?.url ?? '', method: p?.method ?? 'GET', body: p?.body ?? '',
    headers: Object.keys(p?.headers ?? {}).map((k) => [k, '', true]),
    items: p?.items ?? '', fields: { ...(p?.fields ?? {}) }, every: p?.every ? String(p.every) : '',
  };
}

function Secret({ value, onClose }: { value: string | null; onClose(): void }) {
  return (
    <Dialog open={!!value} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-[520px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle className="text-[17px] font-extrabold">回调签名密钥</DialogTitle>
          <DialogDescription>交给 ERP 那边验签用。关掉这个窗口就再也看不到了，丢了只能换一个。</DialogDescription>
        </DialogHeader>
        <button type="button" onClick={() => navigator.clipboard.writeText(value ?? '').then(() => toast.success('密钥已复制'))}
          className="flex items-center gap-2 rounded-xl border-2 border-dashed border-primary/40 bg-brand-soft/40 px-3 py-3 text-left hover:bg-brand-soft">
          <KeyRoundIcon className="size-4 shrink-0 text-primary" />
          <code className="min-w-0 flex-1 font-mono text-[13px] break-all">{value}</code>
          <CopyIcon className="size-4 shrink-0 text-muted-foreground" />
        </button>
        <p className="text-[12.5px] leading-relaxed text-muted-foreground">
          每次回调的请求头 <code className="font-mono">X-SF-Signature</code> 是 <code className="font-mono">sha256=</code> 加上用这个密钥对请求体做的 HMAC-SHA256（十六进制）。算出来对不上就不是这台服务器发的。
        </p>
        <DialogFooter><Button className="rounded-xl font-bold" onClick={onClose}>复制好了</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Copyable({ text, label }: { text: string; label?: string }) {
  return (
    <button type="button" onClick={() => navigator.clipboard.writeText(text).then(() => toast.success(`${label ?? ''}已复制`))}
      className="flex w-full items-center gap-2 rounded-xl bg-surface px-3 py-2 text-left font-mono text-[12.5px] hover:bg-brand-soft">
      <span className="min-w-0 flex-1 truncate">{text}</span><CopyIcon className="size-3.5 shrink-0 text-muted-foreground" />
    </button>
  );
}

export function ERPPanel({ c }: { c: api.Company }) {
  const [ov, setOv] = useState<api.ERPOverview | null>(null);
  const [error, setError] = useState<string>();
  const [enabled, setEnabled] = useState(false);
  const [whenEmpty, setWhenEmpty] = useState<'idle' | 'simulate'>('idle');
  const [usePull, setUsePull] = useState(false);
  const [pull, setPull] = useState<PullForm>(pullForm());
  const [notify, setNotify] = useState('');
  const [busy, setBusy] = useState(false);
  const [problems, setProblems] = useState<string[]>([]);
  const [secret, setSecret] = useState<string | null>(null);
  const [dry, setDry] = useState<api.PullResult | null>(null);
  const [trying, setTrying] = useState(false);
  const [issuing, setIssuing] = useState(false);
  const fill = useCallback((o: api.ERPOverview) => {
    setOv(o);
    setEnabled(o.config.enabled);
    setWhenEmpty(o.config.whenEmpty);
    setUsePull(!!o.config.pull);
    setPull(pullForm(o.config.pull));
    setNotify(o.config.notify?.url ?? '');
  }, []);
  useEffect(() => { api.erp(c.id).then(fill, (e: Error) => setError(e.message)); }, [c.id, fill]);
  const config = (): api.ERPConfig => ({
    enabled, whenEmpty,
    ...(usePull && {
      pull: {
        url: pull.url.trim(), method: pull.method, ...(pull.method === 'POST' && pull.body.trim() && { body: pull.body }),
        headers: Object.fromEntries(pull.headers.filter(([k]) => k.trim()).map(([k, v]) => [k.trim(), v])),
        items: pull.items.trim(), fields: Object.fromEntries(Object.entries(pull.fields).filter(([, v]) => v.trim())),
        ...(Number(pull.every) > 0 && { every: Number(pull.every) }),
      },
    }),
    ...(notify.trim() && { notify: { url: notify.trim() } }),
  });
  const save = async () => {
    setBusy(true);
    setProblems([]);
    try {
      const r = await api.setERP(c.id, config());
      if (r.secret) setSecret(r.secret);
      fill(await api.erp(c.id));
      toast.success(enabled ? 'ERP 对接已保存' : 'ERP 对接已停用，产线自己排工单');
    } catch (e) {
      const err = e as api.ApiError;
      setProblems(err.problems?.length ? err.problems : [err.message]);
    } finally {
      setBusy(false);
    }
  };
  const tryPull = async () => {
    setTrying(true);
    setDry(null);
    setProblems([]);
    try {
      // the dry run reads the saved settings: save the form first
      await api.setERP(c.id, config()).then((r) => r.secret && setSecret(r.secret));
      fill(await api.erp(c.id));
      setDry(await api.pullERP(c.id, true));
    } catch (e) {
      const err = e as api.ApiError;
      setProblems(err.problems?.length ? err.problems : [err.message]);
    } finally {
      setTrying(false);
    }
  };
  const rotate = async () => {
    try {
      setSecret((await api.rotateERPSecret(c.id)).secret);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  if (error) return <Panel title="ERP"><p className="text-[13px] text-destructive">{error}</p></Panel>;
  if (!ov) return <Panel title="ERP"><div className="h-24" /></Panel>;
  const endpoint = `${location.origin}/api/erp/${c.id}/orders`;
  const st = ov.status;
  const curl = `curl -X POST ${endpoint} \\\n  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \\\n  -d '{"no":"MO-1001","partNo":"…","qty":200,"due":"2026-10-20 17:00","customer":"…"}'`;
  const set = (patch: Partial<PullForm>) => setPull((p) => ({ ...p, ...patch }));
  return (
    <Panel
      title={<span className="flex items-center gap-2">ERP 生产工单{ov.config.enabled ? <span className="rounded-md bg-success-soft px-1.5 py-0.5 text-[11.5px] font-bold text-success">已接</span> : <span className="rounded-md bg-surface px-1.5 py-0.5 text-[11.5px] font-bold text-muted-foreground">没接</span>}</span>}
      aside={<Button variant="outline" size="sm" className="rounded-lg font-bold" onClick={() => go(`${location.pathname}?tab=orders`, true)}><ClipboardListIcon />看工单</Button>}
      className="xl:col-span-2"
    >
      <div className="grid gap-6">
        <div className="grid gap-2 sm:grid-cols-[1fr_1fr]">
          {([[true, '接 ERP', 'ERP 下达的工单排到产线上做，开工和完工回传给 ERP'], [false, '不接', '产线自己排工单，孪生照常仿真']] as const).map(([v, title, sub]) => (
            <button key={title} type="button" onClick={() => setEnabled(v)}
              className={cn('grid gap-0.5 rounded-xl border p-3 text-left transition-colors', enabled === v ? 'border-primary bg-brand-soft/50' : 'hover:bg-surface')}>
              <b className="text-[13.5px]">{title}</b><small className="text-[12px] text-muted-foreground">{sub}</small>
            </button>
          ))}
        </div>
        {enabled && (
          <>
            <section className="grid gap-2">
              <h3 className="text-[13.5px] font-extrabold">没有工单的产线</h3>
              <div className="flex flex-wrap gap-2">
                {([['idle', '停线等单', '和真实工厂一样：没单就不开'], ['simulate', '照常仿真', '演示用：没单时自己排一张']] as const).map(([v, title, sub]) => (
                  <button key={v} type="button" onClick={() => setWhenEmpty(v)}
                    className={cn('grid rounded-xl border px-3 py-2 text-left', whenEmpty === v ? 'border-primary bg-brand-soft/50' : 'hover:bg-surface')}>
                    <b className="text-[13px]">{title}</b><small className="text-[12px] text-muted-foreground">{sub}</small>
                  </button>
                ))}
              </div>
            </section>

            <section className="grid gap-2">
              <h3 className="text-[13.5px] font-extrabold">ERP 推给孪生</h3>
              <p className="text-[12.5px] text-muted-foreground">ERP（或中间件）带一个操作员令牌，把工单 POST 到这里。同一个工单号再发一次就是改；DELETE 这个地址加 /工单号 就是撤回。</p>
              <Copyable text={endpoint} label="地址" />
              <pre className="overflow-x-auto rounded-xl bg-surface px-3 py-2 font-mono text-[12px] leading-relaxed text-muted-foreground">{curl}</pre>
              <Button variant="outline" size="sm" className="w-fit rounded-lg font-bold" onClick={() => setIssuing(true)}><KeyRoundIcon />给 ERP 发一个令牌</Button>
            </section>

            <section className="grid gap-3">
              <div className="flex items-center gap-2">
                <h3 className="text-[13.5px] font-extrabold">孪生去 ERP 拉</h3>
                <Label className="ml-auto flex cursor-pointer items-center gap-2 text-[12.5px] font-bold">
                  <Checkbox checked={usePull} onCheckedChange={(v) => setUsePull(v === true)} />开启
                </Label>
              </div>
              {!usePull && <p className="text-[12.5px] text-muted-foreground">ERP 推不了（比如只有查询接口）时，由孪生定时去 ERP 的接口取下达的工单。</p>}
              {usePull && (
                <div className="grid gap-3 rounded-xl border p-4">
                  <div className="grid grid-cols-[100px_minmax(0,1fr)] gap-2 max-sm:grid-cols-1">
                    <Select value={pull.method} onValueChange={(v) => set({ method: v as 'GET' | 'POST' })}>
                      <SelectTrigger className="h-10 w-full rounded-xl" aria-label="方法"><SelectValue /></SelectTrigger>
                      <SelectContent><SelectItem value="GET">GET</SelectItem><SelectItem value="POST">POST</SelectItem></SelectContent>
                    </Select>
                    <Input value={pull.url} onChange={(e) => set({ url: e.target.value })} placeholder="ERP 的工单接口，如 https://erp.example.com/api/production-orders?status=released" className="h-10 rounded-xl font-mono text-[12.5px]" aria-label="ERP 接口地址" />
                  </div>
                  {pull.method === 'POST' && (
                    <Textarea value={pull.body} onChange={(e) => set({ body: e.target.value })} placeholder='请求体，比如金蝶 ExecuteBillQuery 的 {"FormId":"PRD_MO", "FieldKeys":"FBillNo,FMaterialId.FNumber,FQty,FPlanFinishDate", …}'
                      className="h-24 resize-none rounded-xl font-mono text-[12px]" spellCheck={false} />
                  )}
                  <div className="grid gap-1.5">
                    <span className="text-[12.5px] font-bold">请求头 <small className="font-normal text-muted-foreground">密钥存在服务器上，这里不再显示；留空就不改</small></span>
                    {pull.headers.map(([k, v, saved], i) => (
                      <div key={i} className="grid grid-cols-[minmax(0,180px)_minmax(0,1fr)_32px] gap-2">
                        <Input value={k} onChange={(e) => set({ headers: pull.headers.map((h, j) => (j === i ? [e.target.value, h[1], h[2]] : h)) })} placeholder="Authorization" className="h-9 rounded-lg font-mono text-[12.5px]" aria-label="请求头名" />
                        <Input value={v} type="password" autoComplete="off" onChange={(e) => set({ headers: pull.headers.map((h, j) => (j === i ? [h[0], e.target.value, h[2]] : h)) })} placeholder={saved ? '已设置，留空不变' : 'Bearer …'} className="h-9 rounded-lg font-mono text-[12.5px]" aria-label="请求头值" />
                        <Button variant="ghost" size="icon" className="size-9 rounded-lg text-muted-foreground" aria-label="删掉这个请求头" onClick={() => set({ headers: pull.headers.filter((_, j) => j !== i) })}><Trash2Icon /></Button>
                      </div>
                    ))}
                    <Button variant="ghost" size="sm" className="w-fit rounded-lg text-[12.5px]" onClick={() => set({ headers: [...pull.headers, ['', '', false]] })}><PlusIcon />加一个请求头</Button>
                  </div>
                  <div className="grid gap-1.5">
                    <Label htmlFor="items" className="text-[12.5px] font-bold">列表在哪 <small className="font-normal text-muted-foreground">返回 JSON 里工单列表的路径，比如 data.rows；本身就是列表留空</small></Label>
                    <Input id="items" value={pull.items} onChange={(e) => set({ items: e.target.value })} placeholder="如 data.rows" className="h-9 rounded-lg font-mono text-[12.5px]" />
                  </div>
                  <div className="grid gap-1.5">
                    <span className="text-[12.5px] font-bold">字段对应 <small className="font-normal text-muted-foreground">每张工单里的路径；ERP 按数组返回一行时写下标 0、1、2</small></span>
                    <div className="grid grid-cols-2 gap-x-3 gap-y-2 max-sm:grid-cols-1">
                      {ov.fields.map((f) => (
                        <label key={f} className="grid grid-cols-[64px_minmax(0,1fr)] items-center gap-2">
                          <span className={cn('text-[12.5px]', f === 'no' || f === 'qty' ? 'font-bold' : 'text-muted-foreground')}>{FIELD[f]?.label ?? f}</span>
                          <Input value={pull.fields[f] ?? ''} onChange={(e) => set({ fields: { ...pull.fields, [f]: e.target.value } })} placeholder={FIELD[f]?.hint ? `如 ${FIELD[f].hint}` : ''}
                            className="h-9 rounded-lg font-mono text-[12.5px]" aria-label={`${FIELD[f]?.label ?? f}的路径`} />
                        </label>
                      ))}
                    </div>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-[12.5px] font-bold">每</span>
                    <Input value={pull.every} onChange={(e) => set({ every: e.target.value.replace(/\D/g, '') })} placeholder="—" className="h-9 w-20 rounded-lg text-center tabular-nums" aria-label="拉取间隔（分钟）" />
                    <span className="text-[12.5px] text-muted-foreground">分钟拉一次，不填只在点「从 ERP 拉取」时拉</span>
                    <Button variant="outline" size="sm" className="ml-auto rounded-lg font-bold" disabled={trying || !pull.url} onClick={tryPull}>
                      {trying ? <LoaderCircleIcon className="animate-spin" /> : <RefreshCwIcon />}试拉一次
                    </Button>
                  </div>
                  {dry && (
                    <div className="grid gap-2 rounded-xl bg-surface p-3">
                      <p className="text-[12.5px] font-bold">读到 {dry.fetched} 张（试拉，没有存）</p>
                      <ul className="grid max-h-56 gap-1 overflow-y-auto text-[12.5px]">
                        {dry.items.map((it, i) => {
                          const o = it.order as Record<string, unknown>;
                          return (
                            <li key={i} className="flex flex-wrap items-baseline gap-x-2">
                              <span className="font-mono font-bold">{String(o.no || '（没有工单号）')}</span>
                              <span className="font-mono text-muted-foreground">{String(o.partNo || o.product || '')} × {String(o.qty ?? '')}</span>
                              {it.error ? <span className="text-destructive">{it.error}</span> : <span className="text-success">→ {it.lineName}</span>}
                            </li>
                          );
                        })}
                      </ul>
                    </div>
                  )}
                  {st.lastPull && (
                    <p className="text-[12px] text-muted-foreground">
                      上次 {ago(st.lastPull)}，读到 {st.pullFetched} 张{st.pullError && <span className="text-destructive">，{st.pullError}</span>}
                    </p>
                  )}
                  {(st.pullProblems ?? []).length > 0 && (
                    <ul className="grid gap-0.5 text-[12px] text-destructive">{st.pullProblems!.map((p) => <li key={p}>{p}</li>)}</ul>
                  )}
                </div>
              )}
            </section>

            <section className="grid gap-2">
              <h3 className="text-[13.5px] font-extrabold">回传给 ERP</h3>
              <p className="text-[12.5px] text-muted-foreground">产线开工、完工时，把这张工单（含合格数、报废数）POST 到这个地址，带签名。ERP 也可以随时 GET 工单地址查进度。</p>
              <Input value={notify} onChange={(e) => setNotify(e.target.value)} placeholder="https://erp.example.com/hooks/smartfactory（不回传就留空）" className="h-10 rounded-xl font-mono text-[12.5px]" aria-label="回调地址" />
              {ov.config.notify && (
                <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
                  <span className="text-muted-foreground">签名密钥{ov.config.secretSet ? '已生成' : '还没有'}，成功 {st.delivered} 次，失败 {st.failed} 次{st.lastNotify ? `，上次 ${ago(st.lastNotify)}` : ''}</span>
                  {st.notifyError && <span className="flex items-center gap-1 text-destructive"><CircleAlertIcon className="size-3.5" />{st.notifyError}</span>}
                  <Button variant="ghost" size="sm" className="ml-auto h-7 rounded-lg text-[12.5px]" onClick={rotate}>换一个密钥</Button>
                </div>
              )}
            </section>
          </>
        )}
        {problems.length > 0 && (
          <ul className="grid gap-1 rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive">{problems.map((p, i) => <li key={i}>{p}</li>)}</ul>
        )}
        <div className="flex items-center gap-3 border-t pt-4">
          <span className="text-[12.5px] text-muted-foreground tabular-nums">
            排队 {ov.counts.queued}，生产中 {ov.counts.running}，完成 {ov.counts.done}
          </span>
          <Button className="ml-auto h-10 rounded-xl font-bold" disabled={busy} onClick={save}>{busy && <LoaderCircleIcon className="animate-spin" />}保存</Button>
        </div>
      </div>
      <Secret value={secret} onClose={() => setSecret(null)} />
      <Issue open={issuing} onOpenChange={setIssuing} company={c.id} onIssued={() => {}} holder="ERP 接口"
        note={<p className="text-[12.5px] leading-relaxed text-muted-foreground">在 ERP（或中间件）里把它配成请求头 <code className="font-mono">Authorization: Bearer 令牌</code>。它是这家公司的操作员令牌：能发、撤、查工单，也能给设备下指令，进不了后台。只给 ERP 用，不用了就在账号里撤销。</p>} />
    </Panel>
  );
}
