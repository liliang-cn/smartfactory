// 行业包: what a kind of site is made of — its products, its machines and the routes between
// them. Every company is built from one.
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { FootprintsIcon, LoaderCircleIcon, UploadIcon, WaypointsIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import type { IndustryCfg } from '@/config/schema';
import * as api from './api';
import { link, PageHead, useConsole } from './Console';

function Import({ open, onOpenChange }: { open: boolean; onOpenChange(o: boolean): void }) {
  const { reload } = useConsole();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [problems, setProblems] = useState<string[]>([]);
  useEffect(() => { if (open) { setText(''); setProblems([]); } }, [open]);
  const submit = async () => {
    let cfg: unknown;
    try { cfg = JSON.parse(text); } catch (e) { setProblems([`不是合法的 JSON：${(e as Error).message}`]); return; }
    setBusy(true);
    setProblems([]);
    try {
      await api.createIndustry(cfg);
      await reload();
      toast.success('行业包已导入');
      onOpenChange(false);
    } catch (e) {
      const err = e as api.ApiError;
      setProblems(err.problems.length ? err.problems : [err.message]);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[640px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle className="text-[17px] font-extrabold">导入行业包</DialogTitle>
          <DialogDescription>粘贴行业包 JSON。同一个标识再导入一次就是替换，用它的公司会按新的重建。</DialogDescription>
        </DialogHeader>
        <Textarea value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} placeholder='{ "id": "bakery", "name": "烘焙", "products": { … }, "machines": { … }, "routes": { … } }'
          className="h-[320px] resize-none rounded-xl font-mono text-[12.5px]" />
        {problems.length > 0 && (
          <ul className="grid max-h-36 gap-1 overflow-y-auto rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive">
            {problems.map((p, i) => <li key={i}>{p}</li>)}
          </ul>
        )}
        <DialogFooter>
          <Button variant="ghost" className="rounded-xl" onClick={() => onOpenChange(false)}>取消</Button>
          <Button className="rounded-xl font-bold" disabled={!text.trim() || busy} onClick={submit}>{busy && <LoaderCircleIcon className="animate-spin" />}导入</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const stepKey = (s: IndustryCfg['routes'][string][number]) => (typeof s === 'string' ? s : s.machine);

function Detail({ id }: { id: string }) {
  const { ov } = useConsole();
  const [cfg, setCfg] = useState<IndustryCfg | null>(null);
  const [error, setError] = useState<string>();
  useEffect(() => { setCfg(null); api.industry(id).then(setCfg, (e: Error) => setError(e.message)); }, [id]);
  const summary = ov.industries.find((i) => i.id === id);
  const users = ov.companies.filter((c) => c.industry === id);
  if (error) return <PageHead title="找不到这个行业包" sub={error} back={{ to: '/admin/industries', label: '行业包' }} />;
  return (
    <>
      <PageHead back={{ to: '/admin/industries', label: '行业包' }} title={summary?.name ?? id}
        sub={`${id}，${summary?.flow === 'walk' ? '人或车沿步道走' : '传送带'}，单位「${summary?.unit ?? ''}」${summary?.builtin ? '，随应用内置' : ''}${users.length ? `，${users.map((u) => u.name).join('、')} 在用` : ''}`} />
      {cfg && (
        <div className="grid gap-5 xl:grid-cols-2">
          <section className="rounded-[18px] bg-card p-5 shadow-float xl:col-span-2">
            <h2 className="mb-3 text-[15px] font-extrabold">工艺路线</h2>
            <ul className="grid gap-3">
              {Object.entries(cfg.routes).map(([k, steps]) => (
                <li key={k} className="grid gap-1.5">
                  <span className="font-mono text-[12px] font-bold text-muted-foreground">{k}</span>
                  <ol className="flex flex-wrap items-center gap-1">
                    {steps.map((s, i) => {
                      const m = cfg.machines[stepKey(s)];
                      const name = (typeof s !== 'string' && s.name) || m?.label || stepKey(s);
                      return (
                        <li key={i} className="flex items-center gap-1">
                          {i > 0 && <span className="text-muted-foreground/60">›</span>}
                          <span className="rounded-lg bg-surface px-2 py-1 text-[12.5px] font-bold">{name}</span>
                        </li>
                      );
                    })}
                  </ol>
                </li>
              ))}
            </ul>
          </section>
          <section className="rounded-[18px] bg-card shadow-float">
            <h2 className="p-5 pb-2 text-[15px] font-extrabold">产品</h2>
            <Table>
              <TableHeader><TableRow className="text-[12px]"><TableHead className="pl-5">名称</TableHead><TableHead>工序状态</TableHead><TableHead className="pr-5 text-right">客户</TableHead></TableRow></TableHeader>
              <TableBody>
                {Object.entries(cfg.products).map(([k, p]) => (
                  <TableRow key={k} className="text-[13px]">
                    <TableCell className="pl-5"><b>{p.name}</b><div className="font-mono text-[11px] text-muted-foreground">{k}</div></TableCell>
                    <TableCell className="text-muted-foreground">{Object.keys(p.stages).join(' › ')}</TableCell>
                    <TableCell className="pr-5 text-right text-muted-foreground">{p.customers?.map((c) => c.name).join('、') ?? ''}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </section>
          <section className="rounded-[18px] bg-card shadow-float">
            <h2 className="p-5 pb-2 text-[15px] font-extrabold">设备</h2>
            <Table>
              <TableHeader><TableRow className="text-[12px]"><TableHead className="pl-5">名称</TableHead><TableHead>编号</TableHead><TableHead className="text-right">节拍</TableHead><TableHead className="pr-5 text-right">功率</TableHead></TableRow></TableHeader>
              <TableBody>
                {Object.entries(cfg.machines).map(([k, m]) => (
                  <TableRow key={k} className="text-[13px]">
                    <TableCell className="pl-5"><b>{m.label}</b><div className="font-mono text-[11px] text-muted-foreground">{k}</div></TableCell>
                    <TableCell className="font-mono text-[12px]">{m.code}</TableCell>
                    <TableCell className="text-right tabular-nums">{m.cycle} 秒</TableCell>
                    <TableCell className="pr-5 text-right tabular-nums text-muted-foreground">{m.power ? `${m.power.run} kW` : ''}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </section>
        </div>
      )}
    </>
  );
}

export default function Industries({ open }: { open?: string }) {
  const { ov } = useConsole();
  const [importing, setImporting] = useState(false);
  if (open) return <Detail id={open} />;
  return (
    <>
      <PageHead title="行业包" sub="一类工厂由什么组成：产品、设备、工艺路线。建工厂时从这里选一个起步">
        {ov.me.everywhere && <Button className="h-10 rounded-xl font-bold" onClick={() => setImporting(true)}><UploadIcon />导入行业包</Button>}
      </PageHead>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-5 max-sm:grid-cols-1 max-sm:gap-3">
        {ov.industries.map((i) => {
          const users = ov.companies.filter((c) => c.industry === i.id);
          const machines = Object.values(i.machines).map((m) => m.replace(/\s*\(.*\)$/, ''));
          return (
            <a key={i.id} {...link(`/admin/industries/${encodeURIComponent(i.id)}`)}
              className="group flex flex-col gap-3 rounded-[18px] bg-card p-5 shadow-float outline-none ring-primary/40 hover:ring-2 focus-visible:ring-2">
              <div className="flex items-center gap-3">
                <span className={cn('grid size-10 place-items-center rounded-xl', i.flow === 'walk' ? 'bg-success-soft text-success' : 'bg-brand-soft text-primary')}>
                  {i.flow === 'walk' ? <FootprintsIcon className="size-5" /> : <WaypointsIcon className="size-5" />}
                </span>
                <span className="grid min-w-0 flex-1 leading-tight">
                  <b className="truncate text-[15.5px] font-extrabold group-hover:text-primary">{i.name}</b>
                  <small className="font-mono text-[12px] text-muted-foreground">{i.id}</small>
                </span>
                {!i.builtin && <span className="rounded-md bg-brand-soft px-1.5 py-0.5 text-[11px] font-bold text-primary">导入</span>}
              </div>
              <p className="text-[13px] leading-relaxed text-muted-foreground">
                {i.products.length} 种产品，{machines.length} 种设备：{machines.slice(0, 5).join('、')}{machines.length > 5 ? ' 等' : ''}
              </p>
              <p className="mt-auto border-t pt-3 text-[12.5px]">
                {users.length ? <>在用：<b>{users.map((u) => u.name).join('、')}</b></> : <span className="text-muted-foreground">还没有公司在用</span>}
              </p>
            </a>
          );
        })}
      </div>
      <Import open={importing} onOpenChange={setImporting} />
    </>
  );
}
