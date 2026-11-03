// 研发: new parts being developed for customers — the drawing and die design revisions, the trial
// die, every trial forging with its settings and what inspection found — up to the sign-off that
// lets the line make it — and with it, the part's BOM revision and drawing revision going in
// effect, recorded as an engineering change.
import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { BadgeCheckIcon, FlaskConicalIcon, ListTreeIcon, PlusIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type * as api from './api';
import * as S from './storesApi';
import { Panel } from './Panel';
import { go } from './router';
import { Empty, Field, FormDialog, Pick, Text } from './StoreForm';

const RESULT_TONE: Record<string, string> = { 合格: 'text-success', 需改模: 'text-warning', 需改工艺: 'text-warning', 不合格: 'text-destructive' };

export function RnDPanel({ c }: { c: api.Company }) {
  const [data, setData] = useState<S.RnDOverview | null>(null);
  const [error, setError] = useState<string>();
  const [edit, setEdit] = useState<Partial<S.Project> | null>(null);
  const [trial, setTrial] = useState<S.Project | null>(null);
  const [signing, setSigning] = useState<S.Project | null>(null);
  // each part's BOM, as the BOM page has it
  const [parts, setParts] = useState<S.PartRow[]>([]);
  const load = useCallback(() => {
    S.bomOverview(c.id).then((d) => setParts(d.parts), () => {});
    return S.rnd(c.id).then((d) => { setData(d); setError(undefined); }, (e) => setError((e as Error).message));
  }, [c.id]);
  useEffect(() => { load(); }, [load]);
  if (error) return <Panel><p className="text-[13px] text-destructive">研发项目读不出来：{error}</p></Panel>;
  if (!data) return null;
  const draft = async (p: S.Project) => {
    try {
      const b = await S.draftProjectBOM(c.id, p.id);
      toast.success(`已按项目起了 ${b.part} ${b.rev} 版 BOM 草稿，去 BOM 页把料补全`);
      load();
    } catch (e) { toast.error(S.problemsOf(e).join('；')); }
  };
  const bomOf = (p: S.Project) => parts.find((x) => x.part.toUpperCase() === p.part.toUpperCase());
  const toBOM = (p: S.Project) => go(`${location.pathname}?tab=bom&part=${encodeURIComponent(p.part)}`);
  return (
    <div className="grid gap-5">
      <Panel title="新品开发" aside={data.operator && <Button className="h-9 rounded-lg font-bold" onClick={() => setEdit({ stage: data.stages[0] })}><PlusIcon />新项目</Button>}>
        {data.projects.length === 0 ? (
          <Empty title="还没有研发项目">
            客户来了新零件，就在这里立项：记下客户图纸版本、模具设计版本和交样日期；试制模具在「模具台账」建档时选这个项目，每次试锻记下参数、件数和检验发现，有一次合格就能批准量产。
          </Empty>
        ) : (
          <div className="grid gap-4">
            {data.projects.map((p) => (
              <article key={p.id} className="rounded-2xl border p-4 max-sm:p-3">
                <div className="flex flex-wrap items-start gap-2">
                  <div className="min-w-0">
                    <h3 className="text-[15px] font-extrabold">{p.name} <small className="font-semibold text-muted-foreground">{p.part}</small></h3>
                    <p className="text-[12.5px] text-muted-foreground">
                      {[p.customer && `客户 ${p.customer}`, p.grade && `${p.grade}${p.weight ? `，${p.weight} kg` : ''}`, p.drawing && `图纸 ${p.drawing} 版`, p.dieDesign && `模具设计 ${p.dieDesign} 版`, p.due && `${p.due} 交样`, p.owner].filter(Boolean).join('，')}
                    </p>
                  </div>
                  {data.operator && p.stage !== '量产' && (
                    <div className="ml-auto flex gap-1">
                      <Button size="sm" variant="outline" className="h-8 rounded-lg font-bold" onClick={() => setTrial(p)}><FlaskConicalIcon />记试锻</Button>
                      <Button size="sm" variant="ghost" className="h-8 rounded-lg font-bold" onClick={() => setEdit(p)}>改</Button>
                      {(p.trials ?? []).some((t) => t.result === '合格') && <Button size="sm" className="h-8 rounded-lg font-bold" onClick={() => setSigning(p)}><BadgeCheckIcon />批准量产</Button>}
                    </div>
                  )}
                </div>
                <ol className="mt-3 flex flex-wrap gap-1 text-[12px] font-bold">
                  {data.stages.filter((s) => s !== '暂停' || p.stage === '暂停').map((s, i) => {
                    const at = data.stages.indexOf(p.stage);
                    return <li key={s} className={cn('rounded-md px-2 py-0.5', s === p.stage ? 'bg-primary text-primary-foreground' : i < at && p.stage !== '暂停' ? 'bg-brand-soft text-primary' : 'bg-surface text-muted-foreground')}>{s}</li>;
                  })}
                </ol>
                {(p.dies ?? []).length > 0 && <p className="mt-2 text-[12.5px]">模具：{p.dies!.map((d) => `${d.no}（${{ press: '在机', store: '在库', repair: '返修中', trial: '试制中', scrap: '已报废' }[d.place]}）`).join('、')}</p>}
                {p.note && <p className="mt-1 text-[12.5px] text-muted-foreground">{p.note}</p>}
                <div className="mt-2 flex flex-wrap items-center gap-2 text-[12.5px]">
                  <ListTreeIcon className="size-3.5 text-muted-foreground" />
                  {(() => {
                    const b = bomOf(p);
                    if (b?.active) return <span>BOM {b.active} 版生效{b.drawing ? `（图纸 ${b.drawing} 版）` : ''}{b.drafts ? `，另有 ${b.drafts} 份草稿` : ''}</span>;
                    if (b?.drafts) return <span>BOM 有 {b.drafts} 份草稿，批准量产时选一份生效</span>;
                    return <span className="text-muted-foreground">还没有 BOM</span>;
                  })()}
                  {data.operator && !bomOf(p)?.revisions && <Button size="sm" variant="outline" className="h-7 rounded-lg px-2 text-[12px] font-bold" onClick={() => draft(p)}>按项目起草 BOM</Button>}
                  {bomOf(p)?.revisions ? <button type="button" className="font-bold text-primary" onClick={() => toBOM(p)}>去 BOM 页</button> : null}
                </div>
                {(p.trials ?? []).length > 0 && (
                  <ul className="mt-3 grid gap-1.5 border-t pt-3 text-[12.5px]">
                    {p.trials!.map((t) => (
                      <li key={t.id} className="grid grid-cols-[84px_1fr] gap-2 max-sm:grid-cols-1">
                        <span className="text-muted-foreground tabular-nums">{S.day(t.at)}</span>
                        <span>
                          第 {p.trials!.length - p.trials!.indexOf(t)} 次试锻{t.die ? `（${t.die}）` : ''}：{t.qty} 件合格 {t.good} 件，<b className={RESULT_TONE[t.result]}>{t.result}</b>
                          {t.params && <small className="block text-muted-foreground">参数：{t.params}</small>}
                          {t.findings && <small className="block text-muted-foreground">发现：{t.findings}</small>}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
                {p.released && <p className="mt-2 text-[12.5px] text-success">{S.day(p.released)} 由 {p.releasedBy} 批准量产</p>}
              </article>
            ))}
          </div>
        )}
      </Panel>
      {edit && <ProjectDialog c={c} p={edit} stages={data.stages} onClose={() => setEdit(null)} onSaved={load} />}
      {trial && <TrialDialog c={c} p={trial} results={data.results} onClose={() => setTrial(null)} onSaved={load} />}
      {signing && <SignOffDialog c={c} p={signing} onClose={() => setSigning(null)} onSaved={load} />}
    </div>
  );
}

function ProjectDialog({ c, p: start, stages, onClose, onSaved }: { c: api.Company; p: Partial<S.Project>; stages: string[]; onClose(): void; onSaved(): void }) {
  const [p, setP] = useState<Partial<S.Project>>(start);
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      const saved = await S.saveProject(c.id, { ...p, weight: Number(p.weight) || 0 });
      toast.success(start.id ? `已改：${saved.name}` : `已立项：${saved.name}`);
      onClose();
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open onOpenChange={(v) => !v && onClose()} title={start.id ? `改项目：${start.name}` : '新项目'} problems={problems} busy={busy} onSave={save} save={start.id ? '保存' : '立项'}>
      <Field label="零件名称"><Text value={p.name} onChange={(v) => setP({ ...p, name: v })} placeholder="新能源车转向节" /></Field>
      <Field label="零件号"><Text value={p.part} onChange={(v) => setP({ ...p, part: v })} placeholder="ZXJ-2201" /></Field>
      <Field label="客户"><Text value={p.customer} onChange={(v) => setP({ ...p, customer: v })} /></Field>
      <Field label="交样日期"><Text type="date" value={p.due} onChange={(v) => setP({ ...p, due: v })} /></Field>
      <Field label="牌号"><Text value={p.grade} onChange={(v) => setP({ ...p, grade: v })} placeholder="42CrMo" /></Field>
      <Field label="锻件重量（kg）"><Text type="number" value={p.weight} onChange={(v) => setP({ ...p, weight: v as unknown as number })} /></Field>
      <Field label="客户图纸版本"><Text value={p.drawing} onChange={(v) => setP({ ...p, drawing: v })} placeholder="B" /></Field>
      <Field label="模具设计版本"><Text value={p.dieDesign} onChange={(v) => setP({ ...p, dieDesign: v })} placeholder="A2" /></Field>
      <Field label="阶段"><Pick value={p.stage ?? ''} onChange={(v) => setP({ ...p, stage: v })} options={stages.filter((s) => s !== '量产').map((s) => [s, s])} /></Field>
      <Field label="负责人"><Text value={p.owner} onChange={(v) => setP({ ...p, owner: v })} /></Field>
      <Field label="要注意的" wide><Text value={p.note} onChange={(v) => setP({ ...p, note: v })} placeholder="客户要求 PPAP 3 级；R 角充不满是难点" /></Field>
    </FormDialog>
  );
}

function TrialDialog({ c, p, results, onClose, onSaved }: { c: api.Company; p: S.Project; results: string[]; onClose(): void; onSaved(): void }) {
  const [t, setT] = useState<Partial<S.Trial>>({ die: p.dies?.[0]?.no, result: results[0] });
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      await S.addTrial(c.id, p.id, { ...t, qty: Number(t.qty) || 0, good: Number(t.good) || 0 });
      toast.success('试锻已记下');
      onClose();
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open onOpenChange={(v) => !v && onClose()} title={`记一次试锻：${p.name}`} problems={problems} busy={busy} onSave={save} save="记下">
      <Field label="用的模具">
        {(p.dies ?? []).length ? <Pick value={t.die ?? ''} onChange={(v) => setT({ ...t, die: v })} options={p.dies!.map((d) => [d.no, d.no])} /> : <Text value={t.die} onChange={(v) => setT({ ...t, die: v })} placeholder="先在模具台账给试制模具建档" />}
      </Field>
      <Field label="结论"><Pick value={t.result ?? ''} onChange={(v) => setT({ ...t, result: v })} options={results.map((r) => [r, r])} /></Field>
      <Field label="试锻件数"><Text type="number" value={t.qty} onChange={(v) => setT({ ...t, qty: v as unknown as number })} /></Field>
      <Field label="合格件数"><Text type="number" value={t.good} onChange={(v) => setT({ ...t, good: v as unknown as number })} /></Field>
      <Field label="参数" wide><Text value={t.params} onChange={(v) => setT({ ...t, params: v })} placeholder="加热 1180°C，始锻 1150°C，预锻一次 + 终锻一次" /></Field>
      <Field label="检验发现" wide><Text value={t.findings} onChange={(v) => setT({ ...t, findings: v })} placeholder="R 角充不满 3 件；飞边偏大" /></Field>
    </FormDialog>
  );
}

/** Signing a part off: its trial dies go into the die store, and a BOM draft goes in effect with the drawing revision, as a change record. */
function SignOffDialog({ c, p, onClose, onSaved }: { c: api.Company; p: S.Project; onClose(): void; onSaved(): void }) {
  const [drafts, setDrafts] = useState<S.BOM[] | null>(null);
  const [bom, setBom] = useState('');
  const [drawing, setDrawing] = useState(p.drawing ?? '');
  const [why, setWhy] = useState('');
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    S.bomPart(c.id, p.part).then((d) => {
      const ds = d.revisions.filter((b) => b.status === '草稿');
      setDrafts(ds);
      setBom(ds[ds.length - 1]?.id ?? '');
    }, () => setDrafts([]));
  }, [c.id, p.part]);
  const save = async () => {
    setBusy(true);
    try {
      await S.releaseProject(c.id, p.id, { bom: bom || undefined, drawing, why });
      const b = drafts?.find((x) => x.id === bom);
      toast.success(`${p.part} 已批准量产，试制模具转入模具库${b ? `，BOM ${b.rev} 版生效` : ''}`);
      onClose();
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open onOpenChange={(v) => !v && onClose()} title={`批准量产：${p.name}`} problems={problems} busy={busy} onSave={save} save="批准量产">
      <p className="col-span-2 text-[12.5px] text-muted-foreground max-sm:col-span-1">
        批准后阶段改为量产，试制模具转入模具库。选了 BOM 的话，那份草稿随之生效（原来生效的版本停用），并记一条变更记录，写明改了什么、为什么、谁批准的。
      </p>
      <Field label="随之生效的 BOM" wide>
        {drafts === null ? <p className="text-[12.5px] font-normal text-muted-foreground">正在读 BOM…</p> : drafts.length === 0
          ? <p className="text-[12.5px] font-normal text-muted-foreground">这个零件还没有 BOM 草稿，可以先批准，BOM 以后在 BOM 页生效。</p>
          : <Pick value={bom || 'none'} onChange={(v) => setBom(v === 'none' ? '' : v)} options={[...drafts.map((b): [string, string] => [b.id, `${b.rev} 版草稿（${b.lines.length} 行${b.source && b.source !== '手工' ? `，${b.source}` : ''}）`]), ['none', '这次不动 BOM']]} />}
      </Field>
      <Field label="图纸版本"><Text value={drawing} onChange={setDrawing} placeholder="B" /></Field>
      <Field label="为什么（写进变更记录）"><Text value={why} onChange={setWhy} placeholder={`新品 ${p.part} 试锻合格，批准量产`} /></Field>
    </FormDialog>
  );
}
