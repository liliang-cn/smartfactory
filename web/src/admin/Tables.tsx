// In 接入, for a company with restaurant tables: which table is reported by what (a camera box,
// the POS, a waiter's phone) and when it last was, today's figures with where they come from, a
// reporter token to issue for an integration (shown once), and how to send a report.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import { CopyIcon, KeyRoundIcon, SmartphoneIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';
import * as api from './api';
import { ago } from './Accounts';
import { Panel } from './Panel';

const STATE: Record<api.TableNow['state'], { label: string; tone: string }> = {
  free: { label: '空闲', tone: 'bg-success-soft text-success' },
  occupied: { label: '就餐中', tone: 'bg-brand-soft text-primary' },
  reserved: { label: '已预订', tone: 'bg-warning-soft text-warning' },
  cleaning: { label: '清台中', tone: 'bg-maint-soft text-maint' },
};
const SOURCE: Record<api.TableKpis['source'], string> = { measured: '实测', simulated: '仿真', mixed: '混合' };

const pct = (v: number) => `${Math.round(v * 100)}%`;
const iso = (ms?: number) => (ms ? new Date(ms).toISOString() : undefined);

function copy(text: string) {
  navigator.clipboard.writeText(text).then(() => toast.success('已复制'), () => toast.error('复制不了，手动选中复制'));
}

function Code({ text }: { text: string }) {
  return (
    <div className="relative">
      <pre className="overflow-x-auto rounded-xl bg-surface p-3 pr-10 font-mono text-[12px] leading-relaxed whitespace-pre">{text}</pre>
      <Button variant="ghost" size="icon" className="absolute top-1.5 right-1.5 size-7 rounded-lg" aria-label="复制" onClick={() => copy(text)}><CopyIcon /></Button>
    </div>
  );
}

function Figures({ k }: { k: api.TableKpis }) {
  const items: [string, string][] = [
    ['上座率', pct(k.occupancy)], ['座位利用率', pct(k.seatUse)], ['翻台率', k.turnover.toFixed(1)],
    ['平均用餐', k.avgMinutes == null ? '—' : `${Math.round(k.avgMinutes)} 分钟`], ['今日开台', `${k.sessions}`],
  ];
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px]">
      {items.map(([l, v]) => <span key={l}>{l} <b className="tabular-nums">{v}</b></span>)}
      <span className={cn('rounded-md px-1.5 py-px text-[11.5px] font-bold', k.source === 'simulated' ? 'bg-surface text-muted-foreground' : 'bg-success-soft text-success')}>
        {SOURCE[k.source]}{k.source === 'mixed' ? ` ${pct(k.measuredShare)}` : ''}
      </span>
    </div>
  );
}

export function TablePanel({ c }: { c: api.Company }) {
  const [ov, setOv] = useState<api.TablesOverview | null>(null);
  const [prefix, setPrefix] = useState('sf');
  const [issuing, setIssuing] = useState(false);
  const [issued, setIssued] = useState<string | null>(null);
  const [name, setName] = useState('前厅摄像头');
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => api.tables(c.id).then(setOv, () => setOv(null)), [c.id]);
  useEffect(() => {
    load();
    api.devices(c.id).then((d) => d.broker.prefix && setPrefix(d.broker.prefix), () => {});
    const t = setInterval(load, 10_000);
    return () => clearInterval(t);
  }, [load, c.id]);
  const plants = useMemo(() => {
    const by = new Map<string, api.TableNow[]>();
    for (const t of ov?.tables ?? []) by.set(t.plant, [...(by.get(t.plant) ?? []), t]);
    return [...by.entries()];
  }, [ov]);
  if (!ov || !ov.tables.length) return null;

  const first = ov.tables[0];
  const origin = location.origin;
  const curl = `curl -X POST ${origin}/api/report/${c.id}/tables \\
  -H "Authorization: Bearer $SF_TOKEN" -H "Content-Type: application/json" \\
  -d '[{"plant":"${first.plant}","table":"${first.id}","state":"occupied","people":3,"since":"${iso(Date.now())}","source":"cam-1"},
       {"plant":"${first.plant}","table":"${first.id}","state":"cleaning","since":"${iso(Date.now() + 3_600_000)}","source":"cam-1"}]'`;
  const pos = `curl -X POST ${origin}/api/report/${c.id}/sessions \\
  -H "Authorization: Bearer $SF_TOKEN" -H "Content-Type: application/json" \\
  -d '[{"plant":"${first.plant}","table":"${first.id}","event":"seated","people":4,"source":"pos"},
       {"plant":"${first.plant}","table":"${first.id}","event":"paid","source":"pos"}]'`;
  const mqtt = `mosquitto_pub -h ${location.hostname} -u 设备名 -P "$SF_DEVICE_TOKEN" \\
  -t ${prefix}/${c.id}/${first.plant}/${first.id}/table \\
  -m '{"state":"occupied","people":3,"source":"cam-1"}'`;

  return (
    <Panel
      title="桌态上报"
      aside={(
        <>
          <Button variant="outline" size="sm" className="rounded-lg font-bold" asChild>
            <a href={`/waiter?company=${encodeURIComponent(c.id)}`} target="_blank" rel="noreferrer"><SmartphoneIcon />服务员手机页</a>
          </Button>
          <Button size="sm" className="rounded-lg font-bold" onClick={() => { setIssued(null); setIssuing(true); }}><KeyRoundIcon />签发上报令牌</Button>
        </>
      )}
    >
      <div className="grid gap-4">
        <p className="text-[12.5px] leading-relaxed text-muted-foreground">
          摄像头盒子、收银系统、桌边传感器、服务员手机报上来的桌态，孪生和指标都按它算；三个小时没人报的桌，回到仿真。每张桌下面写着最后是谁报的。
        </p>
        {ov.plants.map((k) => (
          <div key={k.plant} className="grid gap-2">
            <div className="flex flex-wrap items-baseline gap-2">
              <b className="text-[13.5px]">{k.name}</b>
              <small className="text-[12px] text-muted-foreground">{k.tables} 张桌，{k.seats} 个座位，今天 {k.day}</small>
            </div>
            <Figures k={k} />
            <ul className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-2">
              {(plants.find(([p]) => p === k.plant)?.[1] ?? []).map((t) => (
                <li key={t.id} className="rounded-xl border px-2.5 py-2 text-[12.5px]">
                  <div className="flex items-center gap-1.5">
                    <b className="font-mono text-[13.5px]">{t.id}</b>
                    <span className={cn('rounded-md px-1.5 py-px text-[11.5px] font-bold', STATE[t.state].tone)}>{STATE[t.state].label}</span>
                    <small className="ml-auto text-muted-foreground">{t.seats} 座</small>
                  </div>
                  <p className="mt-0.5 text-muted-foreground tabular-nums">
                    {t.state === 'occupied' ? `${t.people} 人，` : ''}{Math.round(t.minutes)} 分钟 · 今天 {t.sessions} 台
                  </p>
                  <p className={cn('mt-0.5 truncate', t.reported ? 'text-success' : 'text-muted-foreground')}>
                    {t.lastSource ? `${t.reported ? '实测' : '已超时'} · ${t.lastSource} ${ago(iso(t.lastAt))}` : '仿真 · 没人报过'}
                  </p>
                </li>
              ))}
            </ul>
          </div>
        ))}
        <details className="rounded-xl bg-surface/60 px-3 py-2 text-[12.5px]">
          <summary className="cursor-pointer font-bold">怎么上报</summary>
          <div className="mt-2 grid gap-3">
            <p className="text-muted-foreground">桌态：free 空闲、occupied 就餐中、reserved 已预订、cleaning 清台中；since 是进入这个状态的时间（毫秒或 RFC 3339，不写就是现在）。同一条报两次只记一次；晚到的旧报告照样记下，按时间放进当天的用餐记录。</p>
            <Code text={curl} />
            <p className="text-muted-foreground">收银系统可以报用餐事件：seated 入座、ordered 点单、paid 结账、left 离开。</p>
            <Code text={pos} />
            <p className="text-muted-foreground">店里的设备也可以发 MQTT（在上面「设备身份」里登记，资产填桌号）：</p>
            <Code text={mqtt} />
          </div>
        </details>
      </div>
      <Dialog open={issuing} onOpenChange={setIssuing}>
        <DialogContent className="sm:max-w-[480px]">
          <DialogHeader>
            <DialogTitle>{issued ? '上报令牌' : '签发上报令牌'}</DialogTitle>
            <DialogDescription>
              {issued ? '只显示这一次，关掉就看不到了。配到摄像头盒子或收银系统里，用 Authorization: Bearer 带上。' : `给摄像头盒子、收银系统或桌边传感器用：只能上报 ${c.name} 的桌态和用餐记录，别的都做不了。`}
            </DialogDescription>
          </DialogHeader>
          {issued ? (
            <div className="flex items-center gap-2">
              <Input readOnly value={issued} className="font-mono text-[13px]" onFocus={(e) => e.currentTarget.select()} />
              <Button variant="outline" size="icon" aria-label="复制令牌" onClick={() => copy(issued)}><CopyIcon /></Button>
            </div>
          ) : (
            <div className="grid gap-1.5">
              <Label htmlFor="reporter-name">谁用它</Label>
              <Input id="reporter-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="如 春熙店前厅摄像头" />
            </div>
          )}
          <DialogFooter>
            {issued ? <Button onClick={() => setIssuing(false)}>记下了</Button> : (
              <Button disabled={busy || !name.trim()} onClick={async () => {
                setBusy(true);
                try {
                  const r = await api.issue({ name: name.trim(), role: 'reporter', companies: [c.id] });
                  setIssued(r.token);
                } catch (e) {
                  toast.error((e as Error).message);
                } finally {
                  setBusy(false);
                }
              }}>签发</Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Panel>
  );
}
