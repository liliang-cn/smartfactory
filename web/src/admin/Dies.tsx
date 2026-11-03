// 模具: every die one by one — which press it is on, the strokes it has struck of its design life
// (counted from the press as it works), re-sinking and scrapping — and the presses with the die on each.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import { HistoryIcon, PlusIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import type * as api from './api';
import * as S from './storesApi';
import { Panel } from './Panel';
import { Empty, Field, FormDialog, LifeBar, Pick, Text } from './StoreForm';

const PLACE_TONE: Record<S.DiePlace, string> = {
  press: 'bg-brand-soft text-primary', store: 'bg-success-soft text-success', repair: 'bg-warning-soft text-warning', trial: 'bg-surface text-foreground', scrap: 'bg-surface text-muted-foreground',
};

export function DiesPanel({ c }: { c: api.Company }) {
  const [data, setData] = useState<S.DiesOverview | null>(null);
  const [error, setError] = useState<string>();
  const [newOpen, setNewOpen] = useState(false);
  const [mount, setMount] = useState<S.PressNow | null>(null);
  const [act, setAct] = useState<{ die: S.Die; do: 'unmount' | 'repair' | 'scrap' } | null>(null);
  const [history, setHistory] = useState<string | null>(null);
  const load = useCallback(() => S.dies(c.id).then((d) => { setData(d); setError(undefined); }, (e) => setError((e as Error).message)), [c.id]);
  useEffect(() => { load(); const t = setInterval(load, 10_000); return () => clearInterval(t); }, [load]);
  const kinds = useMemo(() => [...new Set((data?.presses ?? []).map((p) => p.kind))], [data]);
  if (error) return <Panel><p className="text-[13px] text-destructive">模具台账读不出来：{error}</p></Panel>;
  if (!data) return null;
  const near = data.dies.filter((d) => d.place === 'press' && d.strokes >= d.life * 0.9);

  return (
    <div className="grid gap-5">
      <Panel title="设备上的模具" aside={<span className="text-[12.5px] text-muted-foreground">已打次数跟着设备的冲次走，到 90% 设备上会报警</span>}>
        {data.presses.length === 0 ? (
          <Empty title="这里的设备都不用模具">这个工厂的设备不用换模具，所以没有模具要管。</Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="text-[12px]"><TableHead>设备</TableHead><TableHead className="max-md:hidden">产线</TableHead><TableHead>模具</TableHead><TableHead className="w-[34%] max-sm:hidden">还能打</TableHead>{data.operator && <TableHead />}</TableRow>
            </TableHeader>
            <TableBody>
              {data.presses.map((p) => {
                const d = p.die;
                return (
                  <TableRow key={p.plant + p.id}>
                    <TableCell className="whitespace-normal"><b className="text-[13px]">{p.id}</b><small className="block text-muted-foreground">{p.name}，用{p.kind}</small></TableCell>
                    <TableCell className="text-[12.5px] max-md:hidden">{p.plantName} {p.lineName}</TableCell>
                    <TableCell className="text-[12.5px]">
                      {d ? <b>{d.no}</b> : <span className="text-muted-foreground">没登记</span>}
                      {d && <small className={cn('block tabular-nums sm:hidden', d.strokes >= d.life * 0.9 ? 'font-bold text-destructive' : 'text-muted-foreground')}>还能打 {S.num(d.left)} 次</small>}
                    </TableCell>
                    <TableCell className="max-sm:hidden">
                      {d && (
                        <div className="grid gap-1 tabular-nums">
                          <span className={cn('text-[12.5px]', d.strokes >= d.life * 0.9 && 'font-bold text-destructive')}>{S.num(d.left)} 次<small className="ml-1 text-muted-foreground">已打 {S.num(d.strokes)} / {S.num(d.life)}</small></span>
                          <LifeBar share={d.strokes / Math.max(1, d.life)} />
                        </div>
                      )}
                    </TableCell>
                    {data.operator && (
                      <TableCell className="text-right">
                        {d
                          ? <Button size="sm" variant="ghost" className="h-8 rounded-lg font-bold" onClick={() => setAct({ die: d, do: 'unmount' })}>卸模</Button>
                          : <Button size="sm" variant="ghost" className="h-8 rounded-lg font-bold" onClick={() => setMount(p)}>装模</Button>}
                      </TableCell>
                    )}
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
        {near.length > 0 && <p className="mt-3 rounded-xl bg-warning-soft px-3 py-2 text-[12.5px] text-warning">{near.map((d) => `${d.asset} 上的 ${d.no} 只剩 ${S.num(d.left)} 次`).join('；')}，备好替换模。</p>}
      </Panel>

      <Panel title="模具台账" aside={data.operator && kinds.length > 0 && <Button className="h-9 rounded-lg font-bold" onClick={() => setNewOpen(true)}><PlusIcon />模具建档</Button>}>
        {data.dies.length === 0 ? (
          <Empty title="还没有登记模具">
            {kinds.length > 0 ? '先给每副模具建档（模具号、什么模、设计寿命），再在上面「装模」到设备上；之后设备每打一次，这副模具的已打次数就加一次。' : '设备不用模具时这里不用管。'}
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="text-[12px]"><TableHead>模具号</TableHead><TableHead className="max-md:hidden">零件</TableHead><TableHead>在哪</TableHead><TableHead className="w-[28%] max-sm:hidden">寿命</TableHead><TableHead /></TableRow>
            </TableHeader>
            <TableBody>
              {data.dies.map((d) => (
                <TableRow key={d.no}>
                  <TableCell className="whitespace-normal"><b className="text-[13px]">{d.no}</b><small className="block text-muted-foreground">{d.kind}{d.drawing ? `，图 ${d.drawing}` : ''}{d.resinks ? `，翻新 ${d.resinks} 次` : ''}</small></TableCell>
                  <TableCell className="text-[12.5px] max-md:hidden">{d.product}</TableCell>
                  <TableCell><span className={cn('rounded-md px-2 py-0.5 text-[12px] font-bold', PLACE_TONE[d.place])}>{data.places[d.place]}</span>{d.place === 'press' && <small className="block text-muted-foreground">{d.asset}</small>}</TableCell>
                  <TableCell className="max-sm:hidden">
                    <div className="grid gap-1 tabular-nums"><span className="text-[12px] text-muted-foreground">{S.num(d.strokes)} / {S.num(d.life)} 次</span><LifeBar share={d.strokes / Math.max(1, d.life)} /></div>
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      {data.operator && d.place === 'repair' && <Button size="sm" variant="ghost" className="h-8 rounded-lg font-bold" onClick={() => setAct({ die: d, do: 'repair' })}>修好入库</Button>}
                      {data.operator && (d.place === 'store' || d.place === 'repair') && <Button size="sm" variant="ghost" className="h-8 rounded-lg font-bold text-muted-foreground max-sm:hidden" onClick={() => setAct({ die: d, do: 'scrap' })}>报废</Button>}
                      <Button size="icon-sm" variant="ghost" className="rounded-lg" title="履历" aria-label={`${d.no} 履历`} onClick={() => setHistory(d.no)}><HistoryIcon /></Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>
      <NewDie c={c} open={newOpen} onOpenChange={setNewOpen} kinds={kinds} presses={data.presses} onSaved={load} />
      {mount && <MountDialog c={c} press={mount} dies={data.dies.filter((d) => (d.place === 'store' || d.place === 'trial') && d.kind === mount.kind)} onClose={() => setMount(null)} onSaved={load} />}
      {act && <ActDialog c={c} die={act.die} what={act.do} onClose={() => setAct(null)} onSaved={load} />}
      {history && <HistoryDialog c={c} no={history} onClose={() => setHistory(null)} />}
    </div>
  );
}

function NewDie({ c, open, onOpenChange, kinds, presses, onSaved }: { c: api.Company; open: boolean; onOpenChange(v: boolean): void; kinds: string[]; presses: S.PressNow[]; onSaved(): void }) {
  const [d, setD] = useState<Partial<S.Die>>({});
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [projects, setProjects] = useState<S.Project[]>([]);
  useEffect(() => {
    if (!open) return;
    setD({ kind: kinds[0], life: presses.find((p) => p.kind === kinds[0])?.twinLife });
    setProblems([]);
    S.rnd(c.id).then((r) => setProjects(r.projects.filter((p) => p.stage !== '量产')), () => setProjects([]));
  }, [open, kinds, presses, c.id]);
  const project = projects.find((p) => p.id === d.project);
  const products = [...new Set([...presses.filter((p) => p.kind === d.kind).map((p) => p.product), ...(project ? [project.name] : [])])];
  const save = async () => {
    setBusy(true);
    try {
      const saved = await S.createDie(c.id, { ...d, life: Number(d.life) || 0, strokes: Number(d.strokes) || 0 });
      toast.success(`已建档：${saved.no}`);
      onOpenChange(false);
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open={open} onOpenChange={onOpenChange} title="模具建档" problems={problems} busy={busy} onSave={save} save="建档">
      <Field label="模具号"><Text value={d.no} onChange={(v) => setD({ ...d, no: v })} placeholder="M-HP101-4" /></Field>
      <Field label="什么模"><Pick value={d.kind ?? ''} onChange={(v) => setD({ ...d, kind: v, life: d.life || presses.find((p) => p.kind === v)?.twinLife })} options={kinds.map((k) => [k, k])} /></Field>
      <Field label="锻什么零件"><Pick value={d.product ?? ''} onChange={(v) => setD({ ...d, product: v })} options={products.map((p) => [p, p])} placeholder="选零件" /></Field>
      <Field label="模具图版本"><Text value={d.drawing} onChange={(v) => setD({ ...d, drawing: v })} placeholder="A1" /></Field>
      <Field label="设计寿命（次）"><Text type="number" value={d.life} onChange={(v) => setD({ ...d, life: v as unknown as number })} /></Field>
      <Field label="已打次数" hint="旧模具建档时填，新模具是 0"><Text type="number" value={d.strokes} onChange={(v) => setD({ ...d, strokes: v as unknown as number })} /></Field>
      {projects.length > 0 && (
        <Field label="新品试制模具" hint="是给研发项目做的就选上，批准量产前它算试制模具" wide>
          <Pick value={d.project ?? 'none'} onChange={(v) => {
            const pr = projects.find((p) => p.id === v);
            setD({ ...d, project: pr?.id, place: pr ? 'trial' : undefined, product: pr ? pr.name : d.product });
          }} options={[['none', '不是，量产模具'], ...projects.map((p): [string, string] => [p.id, `${p.part} ${p.name}`])]} />
        </Field>
      )}
      <Field label="备注" wide><Text value={d.note} onChange={(v) => setD({ ...d, note: v })} /></Field>
    </FormDialog>
  );
}

function MountDialog({ c, press, dies, onClose, onSaved }: { c: api.Company; press: S.PressNow; dies: S.Die[]; onClose(): void; onSaved(): void }) {
  const [no, setNo] = useState(dies[0]?.no ?? '');
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      await S.dieAct(c.id, no, { do: 'mount', plant: press.plant, asset: press.id });
      toast.success(`${no} 已装上 ${press.id}`);
      onClose();
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open onOpenChange={(v) => !v && onClose()} title={`装模：${press.id} ${press.name}`} problems={problems} busy={busy} onSave={save} save="装上">
      {dies.length === 0
        ? <p className="col-span-2 text-[12.5px] text-muted-foreground max-sm:col-span-1">库里没有{press.kind}。先在「模具台账」给它建档。</p>
        : <Field label={`选一副${press.kind}`} wide><Pick value={no} onChange={setNo} options={dies.map((d) => [d.no, `${d.no}（${d.product ?? ''}，还能打 ${S.num(d.left)} 次）`])} /></Field>}
    </FormDialog>
  );
}

function ActDialog({ c, die, what, onClose, onSaved }: { c: api.Company; die: S.Die; what: 'unmount' | 'repair' | 'scrap'; onClose(): void; onSaved(): void }) {
  const [to, setTo] = useState(die.strokes >= die.life * 0.9 ? 'repair' : 'store');
  const [resunk, setResunk] = useState(true);
  const [life, setLife] = useState<string>('');
  const [note, setNote] = useState('');
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const title = { unmount: `卸模：${die.no}（${die.asset}）`, repair: `修好入库：${die.no}`, scrap: `报废：${die.no}` }[what];
  const save = async () => {
    setBusy(true);
    try {
      await S.dieAct(c.id, die.no, { do: what, to, resunk, life: Number(life) || 0, note });
      toast.success({ unmount: '已卸下', repair: '已入库', scrap: '已报废' }[what]);
      onClose();
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open onOpenChange={(v) => !v && onClose()} title={title} problems={problems} busy={busy} onSave={save} save={{ unmount: '卸下', repair: '入库', scrap: '报废' }[what]}>
      {what === 'unmount' && <Field label="卸下去哪" wide><Pick value={to} onChange={setTo} options={[['store', '回模具库'], ['repair', '送去翻新、修理']]} /></Field>}
      {what === 'repair' && (
        <>
          <label className="col-span-2 flex items-center gap-2 text-[12.5px] font-bold max-sm:col-span-1"><Checkbox checked={resunk} onCheckedChange={(v) => setResunk(v === true)} />是翻新（重新开型腔），翻新次数加一</label>
          <Field label="新的设计寿命（次）" hint={`不填就还是 ${S.num(die.life)}；已打 ${S.num(die.strokes)} 次`}><Text type="number" value={life} onChange={setLife} /></Field>
        </>
      )}
      <Field label="说明" wide><Text value={note} onChange={setNote} placeholder={what === 'scrap' ? '型腔裂纹，不能再修' : ''} /></Field>
    </FormDialog>
  );
}

function HistoryDialog({ c, no, onClose }: { c: api.Company; no: string; onClose(): void }) {
  const [h, setH] = useState<S.DieEvent[] | null>(null);
  useEffect(() => { S.dieDetail(c.id, no).then((d) => setH(d.history), () => setH([])); }, [c.id, no]);
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-[520px]">
        <DialogHeader><DialogTitle>{no} 的履历</DialogTitle></DialogHeader>
        <ul className="grid gap-1.5 text-[12.5px]">
          {(h ?? []).map((e, i) => (
            <li key={i} className="grid grid-cols-[84px_1fr] gap-2 tabular-nums"><span className="text-muted-foreground">{S.day(e.at)}</span><span>{e.text}<small className="ml-1 text-muted-foreground">（{e.who}，已打 {S.num(e.strokes)} 次）</small></span></li>
          ))}
          {h?.length === 0 && <li className="text-muted-foreground">还没有记录</li>}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
