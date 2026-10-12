// In 接入: the devices that report to the embedded broker under identities of their own. Each
// is registered for some of a plant's assets and gets a token or a certificate, shown once; it
// may publish only those assets' readings and hear only their commands. The broker's shared
// users, from the config, are listed apart: they are held to nothing.
import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { CircleAlertIcon, CopyIcon, DownloadIcon, KeyRoundIcon, LoaderCircleIcon, PlusIcon, RefreshCwIcon, ShieldCheckIcon, Trash2Icon, UsersIcon } from 'lucide-react';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { cn } from '@/lib/utils';
import * as api from './api';
import { ago } from './Accounts';
import { useCompany } from './layouts';
import { Panel } from './Panel';

type Kind = api.BrokerDevice['kind'];
const KIND: Record<Kind, { label: string; note: string }> = {
  token: { label: '令牌', note: '设备连 MQTT 时用令牌当密码。大多数设备用这个' },
  cert: { label: '证书', note: '设备用自己的证书连 TLS 端口，不用密码。设备支持双向 TLS 时用' },
};

const port = (addr?: string) => addr?.slice(addr.lastIndexOf(':') + 1) ?? '';

export function DevicePanel({ c }: { c: api.Company }) {
  const [ov, setOv] = useState<api.BrokerDevices | null>(null);
  const [error, setError] = useState<string>();
  const [adding, setAdding] = useState(false);
  const [issued, setIssued] = useState<api.DeviceCredential | null>(null);
  const [revoking, setRevoking] = useState<api.BrokerDevice | null>(null);
  const [renewing, setRenewing] = useState<api.BrokerDevice | null>(null);
  const [showRevoked, setShowRevoked] = useState(false);
  const load = useCallback(() => api.devices(c.id).then((o) => { setOv(o); setError(undefined); }, (e: Error) => setError(e.message)), [c.id]);
  useEffect(() => {
    load();
    const t = setInterval(load, 10_000);
    return () => clearInterval(t);
  }, [load]);

  if (error && !ov) return <Panel title="设备身份"><p className="text-[13px] text-muted-foreground">{error}</p></Panel>;
  if (!ov) return <Panel title="设备身份"><div className="h-24" /></Panel>;
  const live = ov.devices.filter((d) => !d.revoked);
  const revoked = ov.devices.filter((d) => d.revoked);
  const b = ov.broker;
  return (
    <Panel
      title="设备身份"
      aside={b.listen && <Button size="sm" className="rounded-lg font-bold" onClick={() => setAdding(true)}><PlusIcon />登记设备</Button>}
    >
      <div className="grid gap-4">
        <p className="text-[12.5px] leading-relaxed text-muted-foreground">
          每台设备（或网关）一个自己的令牌或证书，只能发它负责的那几台设备的读数，只收它们的指令；发别的会被拒绝并记下来。撤销后立即断开。
        </p>
        {!b.listen ? (
          <p className="rounded-xl bg-surface px-3 py-2 text-[12.5px] text-muted-foreground">这台服务器没有开内置 MQTT broker（配置 mqtt.listen）。</p>
        ) : (
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px]">
            <span>MQTT 端口 <b className="font-mono">{port(b.listen)}</b></span>
            {b.tls ? <span>TLS 端口 <b className="font-mono">{port(b.tls)}</b></span> : <span className="text-muted-foreground">没开 TLS 端口（配置 mqtt.tls），证书设备连不上</span>}
            <span>主题 <code className="font-mono">{b.prefix}/{c.id}/厂/设备/字段</code></span>
            {b.local && <span className="flex items-center gap-1 text-warning"><CircleAlertIcon className="size-3.5" />现在只接受本机连接，登记第一台设备后对网络开放</span>}
          </div>
        )}

        {live.length === 0 && !ov.shared.length && <p className="text-[13px] text-muted-foreground">还没有登记设备。</p>}
        {live.length > 0 && (
          <ul className="grid gap-2">
            {live.map((d) => <DeviceRow key={d.id} d={d} c={c} onRenew={() => setRenewing(d)} onRevoke={() => setRevoking(d)} />)}
          </ul>
        )}

        {ov.shared.length > 0 && (
          <ul className="grid gap-2">
            {ov.shared.map((u) => (
              <li key={u} className="flex flex-wrap items-center gap-2.5 rounded-xl border border-dashed border-warning/50 bg-warning-soft/40 px-3 py-2.5">
                <UsersIcon className="size-4 shrink-0 text-warning" />
                <b className="font-mono text-[13.5px]">{u}</b>
                <span className="rounded-md bg-warning-soft px-1.5 py-px text-[11.5px] font-bold text-warning">共用账号</span>
                <span className="text-[12.5px] text-muted-foreground">配置文件 mqtt.users 里的，谁拿到密码都能发、能订阅任何主题。设备都登记好以后，建议从配置里删掉。</span>
              </li>
            ))}
          </ul>
        )}

        {revoked.length > 0 && (
          <div className="grid gap-2">
            <button type="button" className="w-fit text-[12.5px] text-muted-foreground hover:text-foreground" onClick={() => setShowRevoked((v) => !v)}>
              {showRevoked ? '收起' : '看看'}已撤销的 {revoked.length} 台
            </button>
            {showRevoked && (
              <ul className="grid gap-1.5">
                {revoked.map((d) => (
                  <li key={d.id} className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 rounded-xl bg-surface px-3 py-2 text-[12.5px] text-muted-foreground">
                    <span className="font-mono font-bold">{d.name}</span>
                    <span>{plantName(c, d.plant)}：{d.assets.join('、')}</span>
                    <span className="ml-auto">{d.revokedBy} {ago(d.revoked)}撤销</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>
      <Register c={c} open={adding} onOpenChange={setAdding} onDone={(r) => { setIssued(r); load(); }} />
      <Issued c={c} cred={issued} broker={b} onClose={() => setIssued(null)} />
      <AlertDialog open={!!revoking} onOpenChange={(o) => !o && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>撤销 {revoking?.name}？</AlertDialogTitle>
            <AlertDialogDescription>它立即断开，{revoking && KIND[revoking.kind].label}再也不能用。要再接，就重新登记一个。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>不撤销</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={async () => {
              const d = revoking!;
              try {
                await api.revokeDevice(c.id, d.id);
                toast.success(`${d.name} 已撤销`);
                load();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}>撤销</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog open={!!renewing} onOpenChange={(o) => !o && setRenewing(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>给 {renewing?.name} 换新{renewing && KIND[renewing.kind].label}？</AlertDialogTitle>
            <AlertDialogDescription>旧的立即失效，设备会断开，换上新的才能再连。{renewing && KIND[renewing.kind].label}丢了或泄露了就这么做。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>不换</AlertDialogCancel>
            <AlertDialogAction onClick={async () => {
              const d = renewing!;
              try {
                setIssued(await api.renewDevice(c.id, d.id));
                load();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}>换新</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Panel>
  );
}

const plantName = (c: api.Company, id: string) => c.plants.find((p) => p.id === id)?.name ?? id;

function DeviceRow({ d, c, onRenew, onRevoke }: { d: api.BrokerDevice; c: api.Company; onRenew(): void; onRevoke(): void }) {
  return (
    <li className="rounded-xl border px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-2.5">
        <span className={cn('size-2 shrink-0 rounded-full', d.connected ? 'bg-success' : 'bg-muted-foreground/40')} title={d.connected ? '在线' : '离线'} />
        <b className="font-mono text-[14px]">{d.name}</b>
        <span className="flex items-center gap-1 rounded-md bg-surface px-1.5 py-px text-[11.5px] font-bold text-muted-foreground">
          {d.kind === 'cert' ? <ShieldCheckIcon className="size-3" /> : <KeyRoundIcon className="size-3" />}{KIND[d.kind].label}
        </span>
        <small className="ml-auto text-[12px] text-muted-foreground tabular-nums">
          {d.connected ? '在线' : d.lastSeen ? `${ago(d.lastSeen)}来过` : '还没连过'}
          {d.expires && `，证书到 ${new Date(d.expires).toLocaleDateString('zh-CN')}`}
        </small>
        <Button variant="ghost" size="sm" className="h-7 rounded-lg text-[12.5px]" onClick={onRenew}><RefreshCwIcon />换新{KIND[d.kind].label}</Button>
        <Button variant="ghost" size="icon" className="size-7 rounded-lg text-muted-foreground hover:bg-danger-soft hover:text-destructive" aria-label={`撤销 ${d.name}`} title="撤销" onClick={onRevoke}><Trash2Icon /></Button>
      </div>
      <p className="mt-1 text-[12.5px]">{plantName(c, d.plant)}：<span className="font-mono">{d.assets.map((a) => (a === '*' ? '整个厂' : a)).join('、')}</span></p>
      {d.refused > 0 && (
        <p className="mt-1.5 rounded-lg bg-warning-soft px-2 py-1 text-[12px] text-warning">
          被拒绝 {d.refused} 次{d.lastRefusal ? `，最近一次（${ago(d.refusedAt)}）：${d.lastRefusal}` : ''}
        </p>
      )}
    </li>
  );
}

function Register({ c, open, onOpenChange, onDone }: { c: api.Company; open: boolean; onOpenChange(o: boolean): void; onDone(r: api.DeviceCredential): void }) {
  const { data } = useCompany(open ? c.id : null);
  const [name, setName] = useState('');
  const [plant, setPlant] = useState('');
  const [assets, setAssets] = useState('');
  const [kind, setKind] = useState<Kind>('token');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  useEffect(() => {
    if (open) { setName(''); setPlant(c.plants[0]?.id ?? ''); setAssets(''); setKind('token'); setError(undefined); }
  }, [open, c.plants]);
  const list = assets.split(/[,，、\s]+/).filter(Boolean);
  const machines = data?.plants.find((p) => p.id === plant)?.lines.flatMap((l) => l.stations.map((s) => s.id)) ?? [];
  const add = (a: string) => setAssets((v) => (list.includes(a) ? v : [...list, a].join(', ')));
  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const r = await api.addDevice(c.id, { name: name.trim(), plant, assets: list, kind });
      onOpenChange(false);
      onDone(r);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[540px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle className="text-[17px] font-extrabold">登记设备</DialogTitle>
          <DialogDescription>令牌或证书只在登记的这一刻给一次，服务器只存令牌的指纹，不存证书私钥。</DialogDescription>
        </DialogHeader>
        <form className="grid gap-5" onSubmit={(e) => { e.preventDefault(); if (name.trim() && plant && list.length) submit(); }}>
          <div className="grid gap-1.5">
            <Label htmlFor="dev-name" className="text-[13px] font-bold">设备名 <small className="font-normal text-muted-foreground">也是它连 MQTT 的 client id 和用户名</small></Label>
            <Input id="dev-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="如 rf-line1、plc-hp101" className="h-10 rounded-xl font-mono" autoFocus maxLength={64} />
          </div>
          <div className="grid gap-1.5">
            <span className="text-[13px] font-bold">哪个厂</span>
            <Select value={plant} onValueChange={setPlant}>
              <SelectTrigger className="h-10 w-full rounded-xl" aria-label="哪个厂"><SelectValue /></SelectTrigger>
              <SelectContent>{c.plants.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}</SelectContent>
            </Select>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="dev-assets" className="text-[13px] font-bold">负责哪些设备 <small className="font-normal text-muted-foreground">设备编号，逗号隔开；RF-* 是 RF- 开头的，* 是整个厂</small></Label>
            <Input id="dev-assets" value={assets} onChange={(e) => setAssets(e.target.value)} placeholder="如 HP-101, RF-*" className="h-10 rounded-xl font-mono" />
            <div className="flex flex-wrap gap-1.5">
              <button type="button" onClick={() => add('*')} className="rounded-md bg-surface px-1.5 py-0.5 text-[12px] font-bold hover:bg-brand-soft">整个厂</button>
              {machines.slice(0, 24).map((m) => (
                <button key={m} type="button" onClick={() => add(m)} className={cn('rounded-md px-1.5 py-0.5 font-mono text-[12px]', list.includes(m) ? 'bg-brand-soft text-primary' : 'bg-surface hover:bg-brand-soft')}>{m}</button>
              ))}
            </div>
          </div>
          <div className="grid gap-1.5">
            <span className="text-[13px] font-bold">凭据</span>
            <RadioGroup value={kind} onValueChange={(v) => setKind(v as Kind)} className="grid gap-2 sm:grid-cols-2">
              {(['token', 'cert'] as const).map((k) => (
                <Label key={k} htmlFor={`kind-${k}`} className={cn('flex cursor-pointer items-start gap-3 rounded-xl border p-3 font-normal transition-colors', kind === k && 'border-primary bg-brand-soft/50')}>
                  <RadioGroupItem id={`kind-${k}`} value={k} className="mt-0.5" />
                  <span className="grid gap-0.5"><b className="text-[13.5px]">{KIND[k].label}</b><small className="text-[12px] text-muted-foreground">{KIND[k].note}</small></span>
                </Label>
              ))}
            </RadioGroup>
          </div>
          {error && <p className="rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="ghost" className="rounded-xl" onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" className="rounded-xl font-bold" disabled={!name.trim() || !plant || !list.length || busy}>{busy && <LoaderCircleIcon className="animate-spin" />}登记</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function download(name: string, base64: string) {
  const bytes = Uint8Array.from(atob(base64), (ch) => ch.charCodeAt(0));
  const url = URL.createObjectURL(new Blob([bytes], { type: 'application/zip' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function Issued({ c, cred: open, broker, onClose }: { c: api.Company; cred: api.DeviceCredential | null; broker: api.BrokerDevices['broker']; onClose(): void }) {
  const [saved, setSaved] = useState(false);
  // what is shown stays while the dialog closes, then is forgotten
  const [cred, setCred] = useState(open);
  useEffect(() => {
    setSaved(false);
    if (open) {
      setCred(open);
      return;
    }
    const t = setTimeout(() => setCred(null), 400);
    return () => clearTimeout(t);
  }, [open]);
  const d = cred?.device;
  const copy = (text: string, what: string) => { navigator.clipboard.writeText(text).then(() => toast.success(`${what}已复制`)); };
  const host = location.hostname;
  const rows: [string, string][] = d ? (cred!.token
    ? [['地址', `${host}:${port(broker.listen)}`], ['client id', d.name], ['用户名', d.name]]
    : [['地址', broker.tls ? `${host}:${port(broker.tls)}（TLS）` : '没开 TLS 端口'], ['client id', d.name], ['密码', '不用']]) : [];
  return (
    <Dialog open={!!open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-[540px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle className="text-[17px] font-extrabold">{d?.name} 的{d && KIND[d.kind].label}</DialogTitle>
          <DialogDescription>只显示这一次，关掉就再也看不到了；丢了只能换新的。</DialogDescription>
        </DialogHeader>
        {cred?.token && (
          <button type="button" onClick={() => { copy(cred.token!, '令牌'); setSaved(true); }}
            className="flex items-center gap-2 rounded-xl border-2 border-dashed border-primary/40 bg-brand-soft/40 px-3 py-3 text-left hover:bg-brand-soft">
            <KeyRoundIcon className="size-4 shrink-0 text-primary" />
            <code className="min-w-0 flex-1 font-mono text-[13px] break-all">{cred.token}</code>
            <CopyIcon className="size-4 shrink-0 text-muted-foreground" />
          </button>
        )}
        {cred?.bundle && (
          <Button variant="outline" className="h-12 justify-start gap-3 rounded-xl border-2 border-dashed border-primary/40 bg-brand-soft/40 font-bold"
            onClick={() => { download(`${d!.name}.zip`, cred.bundle!); setSaved(true); }}>
            <DownloadIcon className="text-primary" />下载证书包 {d!.name}.zip
            <small className="ml-auto font-normal text-muted-foreground">证书、私钥、CA</small>
          </Button>
        )}
        <dl className="grid grid-cols-[80px_minmax(0,1fr)] gap-x-3 gap-y-1.5 rounded-xl bg-surface px-3 py-2.5 text-[12.5px]">
          {rows.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted-foreground">{k}</dt>
              <dd><button type="button" className="font-mono hover:text-primary" onClick={() => copy(v, k)}>{v}</button></dd>
            </div>
          ))}
          {d && (
            <>
              <dt className="text-muted-foreground">能发</dt>
              <dd className="font-mono break-all">{broker.prefix}/{c.id}/{d.plant}/设备/字段</dd>
              <dt className="text-muted-foreground">能收</dt>
              <dd className="font-mono break-all">{broker.prefix}/{c.id}/{d.plant}/设备/cmd</dd>
              <dt className="text-muted-foreground">设备</dt>
              <dd className="font-mono break-all">{d.assets.map((a) => (a === '*' ? '整个厂' : a)).join('、')}</dd>
            </>
          )}
        </dl>
        {cred?.bundle && (
          <p className="text-[12.5px] leading-relaxed text-muted-foreground">
            设备信任 ca.crt，用 {d!.name}.crt 和 {d!.name}.key 连 TLS 端口。边缘网关在配置的 mqtt 里写 <code className="font-mono">"url": "tls://{host}:{port(broker.tls)}", "cert", "key", "ca"</code> 三个文件的路径。
          </p>
        )}
        <DialogFooter><Button className="rounded-xl font-bold" variant={saved ? 'default' : 'outline'} onClick={onClose}>{cred?.token ? '复制好了' : '存好了'}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
