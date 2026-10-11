// 行业包: what a kind of site is made of — its products, its machines and the routes between
// them. Every company is built from one; a company writes its own (by hand or with the AI) when
// none of the built-in ones fits, or copies a built-in one to change it.
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { CheckIcon, CircleAlertIcon, CopyIcon, FootprintsIcon, LoaderCircleIcon, PencilIcon, SparklesIcon, UploadIcon, WaypointsIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import type { IndustryCfg } from '@/config/schema';
import { stageOrder } from '@/core/layout';
import * as api from './api';
import { link, PageHead, useConsole } from './Console';
import { go } from './router';

/** Paste a pack, or edit one: the same id again replaces it. */
function Import({ open, onOpenChange, initial = '', title = '导入行业包' }: { open: boolean; onOpenChange(o: boolean): void; initial?: string; title?: string }) {
  const { reload } = useConsole();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [problems, setProblems] = useState<string[]>([]);
  useEffect(() => { if (open) { setText(initial); setProblems([]); } }, [open, initial]);
  const submit = async () => {
    let cfg: unknown;
    try { cfg = JSON.parse(text); } catch (e) { setProblems([`不是合法的 JSON：${(e as Error).message}`]); return; }
    setBusy(true);
    setProblems([]);
    try {
      await api.createIndustry(cfg);
      await reload();
      toast.success(`行业包 ${(cfg as { name?: string }).name ?? ''} 已保存`);
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
          <DialogTitle className="text-[17px] font-extrabold">{title}</DialogTitle>
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
          <Button className="rounded-xl font-bold" disabled={!text.trim() || busy} onClick={submit}>{busy && <LoaderCircleIcon className="animate-spin" />}保存</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The AI writes a pack from a description (or changes base); it is saved only when confirmed. */
function Writer({ open, onOpenChange, base }: { open: boolean; onOpenChange(o: boolean): void; base?: IndustryCfg }) {
  const { reload } = useConsole();
  const [brief, setBrief] = useState('');
  const [steps, setSteps] = useState<{ tool: string; done: boolean; failed?: string }[]>([]);
  const [answer, setAnswer] = useState('');
  const [error, setError] = useState<string>();
  const [pack, setPack] = useState<Record<string, unknown> | null>(null);
  const [ctrl, setCtrl] = useState<AbortController | null>(null);
  const [saving, setSaving] = useState(false);
  useEffect(() => { if (open) { setBrief(''); setSteps([]); setAnswer(''); setError(undefined); setPack(null); } else ctrl?.abort(); }, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  const run = async () => {
    if (!brief.trim() || ctrl) return;
    const c = new AbortController();
    setCtrl(c);
    setSteps([]); setAnswer(''); setError(undefined); setPack(null);
    try {
      await api.compose(brief.trim(), base, c.signal, (ev) => {
        switch (ev.kind) {
          case 'tool_call': setSteps((x) => [...x, { tool: ev.tool, done: false }]); break;
          case 'tool_result': setSteps((x) => {
            const out = [...x];
            const i = out.findLastIndex((st) => st.tool === ev.tool && !st.done);
            const r = typeof ev.result === 'string' ? ev.result : '';
            if (i >= 0) out[i] = { ...out[i], done: true, failed: /没通过|已经有|没有/.test(r) ? r : undefined };
            return out;
          }); break;
          case 'industry': setPack(ev.result); break;
          case 'delta': setAnswer((a) => a + ev.text); break;
          case 'reset': setAnswer(''); break;
          case 'complete': if (ev.text) setAnswer(ev.text); break;
          case 'error': setError(ev.text); break;
        }
      }, 'industry');
    } catch (e) {
      if (!c.signal.aborted) setError((e as Error).message);
    } finally {
      setCtrl(null);
    }
  };
  const save = async () => {
    if (!pack) return;
    setSaving(true);
    try {
      await api.createIndustry(pack);
      await reload();
      toast.success(`行业包 ${String(pack.name)} 已保存`);
      onOpenChange(false);
      go(`/admin/industries/${encodeURIComponent(String(pack.id))}`);
    } catch (e) {
      const err = e as api.ApiError;
      setError(err.problems.length ? err.problems.join('；') : err.message);
    } finally {
      setSaving(false);
    }
  };
  const LABEL: Record<string, string> = { list_industries: '看有哪些行业包', get_industry: '参考一个行业包', propose_industry: '检查写好的行业包' };
  const products = pack ? Object.values(pack.products as Record<string, { name: string }>).map((p) => p.name) : [];
  const machines = pack ? Object.values(pack.machines as Record<string, { label: string }>).map((m) => m.label) : [];
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[620px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle className="text-[17px] font-extrabold">{base ? `让 AI 改 ${base.name}` : '用 AI 写一个行业包'}</DialogTitle>
          <DialogDescription>{base ? '说要加什么、改什么。保存后用它的工厂按新版本重建。' : '说这类工厂做什么产品、经过哪些工序和设备。写好先给你看，保存了才能用来建工厂。'}</DialogDescription>
        </DialogHeader>
        <Textarea value={brief} onChange={(e) => setBrief(e.target.value)} rows={4} disabled={!!ctrl}
          onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); run(); } }}
          placeholder={base ? '加一道去毛刺工序，放在精车后面；质检加圆度测量' : '注塑件工厂：做汽车门板和仪表台骨架。工序是干燥、注塑、去浇口、视觉检测、包装。注塑机 800 吨，节拍 60 秒左右'}
          className="min-h-[96px] resize-none rounded-xl text-[14px] leading-relaxed" aria-label="描述" />
        {(steps.length > 0 || ctrl || answer || error) && (
          <div className="grid gap-2 rounded-xl bg-surface/60 px-4 py-3">
            <ol className="grid gap-1">
              {steps.map((st, i) => (
                <li key={i} className="flex items-start gap-2 text-[12.5px]">
                  {!st.done ? <LoaderCircleIcon className="mt-0.5 size-3.5 shrink-0 animate-spin text-primary" />
                    : st.failed ? <CircleAlertIcon className="mt-0.5 size-3.5 shrink-0 text-destructive" /> : <CheckIcon className="mt-0.5 size-3.5 shrink-0 text-success" />}
                  <span className="min-w-0">
                    <span className={cn('font-bold', st.failed && 'text-destructive')}>{LABEL[st.tool] ?? st.tool}{st.tool === 'propose_industry' && st.done && !st.failed ? '：通过' : ''}</span>
                    {st.failed && <span className="block whitespace-pre-line text-muted-foreground">{st.failed.replace(/^没通过：\n?/, '').slice(0, 300)}</span>}
                  </span>
                </li>
              ))}
              {ctrl && !steps.some((st) => !st.done) && <li className="flex items-center gap-2 text-[12.5px] text-muted-foreground"><LoaderCircleIcon className="size-3.5 animate-spin" />在想</li>}
            </ol>
            {answer && <p className="text-[13.5px] leading-relaxed whitespace-pre-line">{answer}</p>}
            {error && <p className="rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
          </div>
        )}
        {pack && !ctrl && (
          <div className="grid gap-1 rounded-xl border px-4 py-3 text-[13px]">
            <b className="text-[14px]">{String(pack.name)} <span className="font-mono text-[12px] font-normal text-muted-foreground">{String(pack.id)}</span></b>
            <span><span className="text-muted-foreground">产品：</span>{products.join('、')}</span>
            <span><span className="text-muted-foreground">设备：</span>{machines.join('、')}</span>
          </div>
        )}
        <DialogFooter>
          <Button variant="ghost" className="rounded-xl" onClick={() => onOpenChange(false)}>{pack ? '不要' : '取消'}</Button>
          {pack && !ctrl
            ? <Button className="rounded-xl font-bold" disabled={saving} onClick={save}>{saving && <LoaderCircleIcon className="animate-spin" />}保存行业包</Button>
            : <Button className="rounded-xl font-bold" disabled={!brief.trim() || !!ctrl} onClick={run}>{ctrl ? <LoaderCircleIcon className="animate-spin" /> : <SparklesIcon />}{base ? '让 AI 改' : '让 AI 写'}</Button>}
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
  const [editing, setEditing] = useState<'json' | 'copy' | 'ai' | null>(null);
  useEffect(() => { setCfg(null); api.industry(id).then(setCfg, (e: Error) => setError(e.message)); }, [id, ov]);
  const summary = ov.industries.find((i) => i.id === id);
  const users = ov.companies.filter((c) => c.industry === id);
  if (error) return <PageHead title="找不到这个行业包" sub={error} back={{ to: '/admin/industries', label: '行业包' }} />;
  return (
    <>
      <PageHead back={{ to: '/admin/industries', label: '行业包' }} title={summary?.name ?? id}
        sub={`${id}，${summary?.flow === 'walk' ? '人或车沿步道走' : '传送带'}，单位「${summary?.unit ?? ''}」${summary?.builtin ? '，随应用内置' : ''}${users.length ? `，${users.map((u) => u.name).join('、')} 在用` : ''}`}>
        {cfg && ov.me.everywhere && (summary?.builtin ? (
          <Button variant="outline" className="h-10 rounded-xl font-bold" onClick={() => setEditing('copy')}><CopyIcon />复制成新行业包</Button>
        ) : (
          <>
            {ov.assistants && <Button variant="outline" className="h-10 rounded-xl font-bold" onClick={() => setEditing('ai')}><SparklesIcon />让 AI 改</Button>}
            <Button variant="outline" className="h-10 rounded-xl font-bold" onClick={() => setEditing('json')}><PencilIcon />改 JSON</Button>
          </>
        ))}
      </PageHead>
      {cfg && (
        <>
          <Import open={editing === 'json' || editing === 'copy'} onOpenChange={(o) => !o && setEditing(null)}
            title={editing === 'copy' ? `复制 ${cfg.name}` : `改 ${cfg.name}`}
            initial={JSON.stringify(editing === 'copy' ? { ...cfg, id: `${cfg.id}-2`, name: `${cfg.name}（自定义）` } : cfg, null, 2)} />
          <Writer open={editing === 'ai'} onOpenChange={(o) => !o && setEditing(null)} base={cfg} />
        </>
      )}
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
                    <TableCell className="text-muted-foreground">{stageOrder(cfg, k).join(' › ')}</TableCell>
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
  const [writing, setWriting] = useState(false);
  if (open) return <Detail id={open} />;
  return (
    <>
      <PageHead title="行业包" sub="一类工厂由什么组成：产品、设备、工艺路线。建工厂时从这里选一个起步">
        {ov.me.everywhere && <Button variant="outline" className="h-10 rounded-xl font-bold" onClick={() => setImporting(true)}><UploadIcon />导入</Button>}
        {ov.me.everywhere && ov.assistants && <Button className="h-10 rounded-xl font-bold" onClick={() => setWriting(true)}><SparklesIcon />用 AI 写一个</Button>}
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
      <Writer open={writing} onOpenChange={setWriting} />
    </>
  );
}
