// BOM（物料清单）: what goes into each part number — the forging and the bar it is cut from, the
// bought-in and standard parts, the consumables — level by level, in revisions with one in effect;
// the change records (ECO) of every revision put in effect; a part exploded for a quantity against
// what the stores hold; where a component is used; and importing a BOM from Excel or CSV.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import {
  ArrowDownIcon, ArrowLeftIcon, ArrowRightIcon, ArrowUpIcon, BadgeCheckIcon, CopyIcon, FileDownIcon, FileUpIcon, ListTreeIcon, PencilIcon, PlusIcon,
  SearchIcon, Trash2Icon, XIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import type * as api from './api';
import * as S from './storesApi';
import { Panel } from './Panel';
import { go, query, usePath } from './router';
import { Empty, Field, FormDialog, Pick, Text } from './StoreForm';

const KINDS: [S.LineKind, string][] = [['make', '自制件'], ['buy', '外购件'], ['std', '标准件'], ['raw', '原材料'], ['aux', '辅料']];
const kindLabel = (k: S.LineKind) => KINDS.find((x) => x[0] === k)?.[1] ?? k;

function KindChip({ k }: { k: S.LineKind }) {
  return <span className={cn('rounded-md px-1.5 py-0.5 text-[11.5px] font-bold whitespace-nowrap', S.KIND_TONE[k])}>{kindLabel(k)}</span>;
}
function StatusChip({ s }: { s: S.BOMStatus }) {
  return <span className={cn('rounded-md px-1.5 py-0.5 text-[11.5px] font-bold whitespace-nowrap', S.STATUS_TONE[s])}>{s}</span>;
}
const label = (l: { name: string; spec?: string; grade?: string }) =>
  [l.grade && !l.name.includes(l.grade) ? l.grade : '', l.name, l.spec && !l.name.includes(l.spec) ? l.spec : ''].filter(Boolean).join(' ');

/** Depth of each line in a revision's tree (1: straight into the part). */
function depths(lines: S.BOMLine[]) {
  const at = new Map<string, number>();
  for (const l of lines) at.set(l.id, l.parent ? (at.get(l.parent) ?? 0) + 1 : 1);
  return at;
}

export function BOMPanel({ c }: { c: api.Company }) {
  const path = usePath();
  const part = query(path).get('part') ?? '';
  const pick = (p: string) => {
    const q = new URLSearchParams(location.search);
    if (p) q.set('part', p); else q.delete('part');
    go(`${location.pathname}?${q}`, true);
    // the part opens below the list: bring it up, which matters most on a phone
    if (p) setTimeout(() => document.getElementById('bom-part')?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 300);
  };
  const [data, setData] = useState<S.BOMOverview | null>(null);
  const [error, setError] = useState<string>();
  const [q, setQ] = useState('');
  const [edit, setEdit] = useState<Partial<S.BOM> | null>(null);
  const [importing, setImporting] = useState(false);
  // bumped when a save or import lands, so the open part reads its revisions again
  const [saved, setSaved] = useState(0);
  const load = useCallback(() => S.bomOverview(c.id).then((d) => { setData(d); setError(undefined); }, (e) => setError((e as Error).message)), [c.id]);
  useEffect(() => { load(); }, [load]);
  const shown = useMemo(() => (data?.parts ?? []).filter((p) => !q || `${p.part}${p.name}${p.family ?? ''}`.toLowerCase().includes(q.toLowerCase())), [data, q]);
  if (error) return <Panel><p className="text-[13px] text-destructive">BOM 读不出来：{error}</p></Panel>;
  if (!data) return null;
  const without = data.parts.filter((p) => p.listed && !p.active).length;
  return (
    <div className="grid gap-5">
      <Panel
        title="BOM"
        aside={data.operator && (
          <>
            <Button variant="outline" className="h-9 rounded-lg font-bold" onClick={() => setImporting(true)}><FileUpIcon />导入</Button>
            <Button className="h-9 rounded-lg font-bold" onClick={() => setEdit({ lines: [] })}><PlusIcon />新建 BOM</Button>
          </>
        )}
      >
        {data.parts.length === 0 ? (
          <Empty title="还没有 BOM">
            BOM 记的是一个零件用到哪些料：锻件和它下料的棒料、外购件、标准件、辅料，每件用多少、损耗多少、在哪道工序用。可以一行一行录，也可以把 Excel 或 CSV 里现成的 BOM 导进来。有了 BOM，工单就能算出物料齐不齐套。
          </Empty>
        ) : (
          <>
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <p className="text-[12.5px] text-muted-foreground">
                {data.parts.length} 个零件号{without > 0 && <>，<b className="text-warning">{without} 个还没有生效的 BOM</b></>}
              </p>
              <div className="relative ml-auto max-sm:ml-0 max-sm:w-full">
                <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="零件号、名称" className="h-9 w-60 rounded-lg pl-9 max-sm:w-full" />
              </div>
            </div>
            <Table>
              <TableHeader>
                <TableRow className="text-[12px]">
                  <TableHead>零件</TableHead><TableHead>生效版本</TableHead><TableHead className="max-md:hidden">图纸</TableHead>
                  <TableHead className="text-right max-sm:hidden">物料</TableHead><TableHead className="max-lg:hidden">草稿</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((p) => (
                  <TableRow key={p.part} className={cn('cursor-pointer text-[13px]', p.part === part && 'bg-brand-soft/50')} onClick={() => pick(p.part === part ? '' : p.part)}>
                    <TableCell>
                      <span className="font-mono text-[12.5px] font-bold">{p.part}</span>
                      <div className="text-[12px] text-muted-foreground">{p.name}{p.family && p.family !== p.name ? `，${p.family}` : ''}</div>
                    </TableCell>
                    <TableCell>
                      {p.active ? <span className="font-bold">{p.active} 版<small className="ml-1.5 font-normal text-muted-foreground">{p.effective} 起</small></span>
                        : <span className="text-[12.5px] text-warning">{p.revisions ? '没有生效的版本' : '还没建'}</span>}
                    </TableCell>
                    <TableCell className="text-muted-foreground max-md:hidden">{p.drawing ? `${p.drawing} 版` : ''}</TableCell>
                    <TableCell className="text-right tabular-nums max-sm:hidden">{p.active ? p.lines : ''}</TableCell>
                    <TableCell className="text-muted-foreground max-lg:hidden">{p.drafts > 0 ? `${p.drafts} 份` : ''}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </>
        )}
      </Panel>
      {part && <PartPanel c={c} part={part} saved={saved} row={data.parts.find((p) => p.part === part)} operator={data.operator} onEdit={setEdit} onChanged={load} onClose={() => pick('')} />}
      {!part && <WherePanel c={c} />}
      {!part && data.ecos.length > 0 && <Panel title="最近的变更"><ECOList ecos={data.ecos} showPart onPick={pick} /></Panel>}
      {edit && <BOMEditor c={c} start={edit} parts={data.parts} onClose={() => setEdit(null)} onSaved={(b) => { load(); setSaved((n) => n + 1); pick(b.part); }} />}
      {importing && <ImportDialog c={c} parts={data.parts} preset={part} onClose={() => setImporting(false)} onDone={(b) => { load(); setSaved((n) => n + 1); pick(b.part); }} />}
    </div>
  );
}

function PartPanel({ c, part, saved, row, operator, onEdit, onChanged, onClose }: {
  c: api.Company; part: string; saved: number; row?: S.PartRow; operator: boolean; onEdit(b: Partial<S.BOM>): void; onChanged(): void; onClose(): void;
}) {
  const [data, setData] = useState<S.BOMPart | null>(null);
  const [rev, setRev] = useState('');
  const [releasing, setReleasing] = useState<S.BOM | null>(null);
  const load = useCallback(() => S.bomPart(c.id, part).then(setData, (e) => toast.error((e as Error).message)), [c.id, part]);
  useEffect(() => { setRev(''); load(); }, [load]);
  useEffect(() => { if (saved) load(); }, [saved, load]);
  if (!data) return null;
  const revs = data.revisions;
  const cur = revs.find((b) => b.id === rev) ?? revs.find((b) => b.status === '生效') ?? revs[revs.length - 1];
  const after = () => { load(); onChanged(); };
  const act = async (what: () => Promise<unknown>, done: string) => {
    try { await what(); toast.success(done); after(); } catch (e) { toast.error(S.problemsOf(e).join('；')); }
  };
  return (
    <>
      <Panel
        id="bom-part"
        title={<span className="flex flex-wrap items-baseline gap-2"><span className="font-mono">{part}</span><small className="font-semibold text-muted-foreground">{cur?.name ?? row?.name}</small></span>}
        aside={<Button variant="ghost" size="icon" className="size-8 rounded-lg" aria-label="收起" onClick={onClose}><XIcon /></Button>}
      >
        {revs.length === 0 ? (
          <Empty title="这个零件还没有 BOM">
            {row?.material && <>它用 {row.material}{row.input ? `，下料 ${row.input} kg` : ''}。</>}点「新建 BOM」录入，或者把 Excel 里的 BOM 导进来。
            {operator && <div className="mt-3"><Button className="h-9 rounded-lg font-bold" onClick={() => onEdit(starter(row ?? { part, name: part } as S.PartRow))}><PlusIcon />新建 BOM</Button></div>}
          </Empty>
        ) : (
          <>
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <div className="flex gap-1 overflow-x-auto rounded-lg bg-muted p-[3px]">
                {revs.map((b) => (
                  <button key={b.id} type="button" onClick={() => setRev(b.id)}
                    className={cn('flex h-8 shrink-0 items-center gap-1.5 rounded-md px-3 text-[12.5px] font-bold text-muted-foreground', cur?.id === b.id && 'bg-background text-foreground shadow-sm')}>
                    {b.rev} 版<StatusChip s={b.status} />
                  </button>
                ))}
              </div>
              {operator && cur && (
                <div className="ml-auto flex flex-wrap gap-1 max-sm:ml-0">
                  {cur.status === '草稿' && <>
                    <Button size="sm" variant="ghost" className="h-8 rounded-lg font-bold" onClick={() => onEdit(cur)}><PencilIcon />改</Button>
                    <Button size="sm" variant="ghost" className="h-8 rounded-lg font-bold text-destructive" onClick={() => act(() => S.deleteBOM(c.id, cur.id), `已删除 ${cur.rev} 版草稿`)}><Trash2Icon />删</Button>
                    <Button size="sm" className="h-8 rounded-lg font-bold" onClick={() => setReleasing(cur)}><BadgeCheckIcon />生效</Button>
                  </>}
                  {cur.status !== '草稿' && <Button size="sm" variant="outline" className="h-8 rounded-lg font-bold" onClick={() => act(async () => { const n = await S.copyBOM(c.id, cur.id); setRev(n.id); }, '已复制成新版本草稿，改好再生效')}><CopyIcon />出新版本</Button>}
                  {cur.status === '生效' && <Button size="sm" variant="ghost" className="h-8 rounded-lg font-bold" onClick={() => act(() => S.retireBOM(c.id, cur.id), `${cur.rev} 版已停用`)}>停用</Button>}
                </div>
              )}
            </div>
            {cur && <>
              <p className="mb-3 text-[12.5px] text-muted-foreground">
                {[cur.drawing && `图纸 ${cur.drawing} 版`, cur.status === '生效' && cur.effective && `${cur.effective} 起生效`, cur.releasedBy && `${cur.releasedBy} 批准`,
                  cur.source && cur.source !== '手工' && `来自${cur.source}`, cur.by && `${cur.by} 建`].filter(Boolean).join('，')}
                {cur.note && <span className="block">{cur.note}</span>}
              </p>
              <Tree lines={cur.lines} />
            </>}
          </>
        )}
      </Panel>
      {cur && <ExplodePanel c={c} b={cur} />}
      {data.usedIn.length > 0 && (
        <Panel title="用在">
          <ul className="grid gap-1 text-[13px]">
            {data.usedIn.map((u, i) => <li key={i}><b className="font-mono">{u.part}</b> {u.name}（{u.rev} 版）每件用 {S.qty(u.per, u.unit)}{u.via ? `，在${u.via}里` : ''}</li>)}
          </ul>
        </Panel>
      )}
      {data.ecos.length > 0 && <Panel title="变更记录"><ECOList ecos={data.ecos} /></Panel>}
      {releasing && <ReleaseDialog c={c} b={releasing} first={!revs.some((b) => b.status === '生效')} onClose={() => setReleasing(null)} onDone={after} />}
    </>
  );
}

/** A new BOM for a part number its config knows: the forging over the bar it is cut from. */
function starter(p: S.PartRow): Partial<S.BOM> {
  const lines: S.BOMLine[] = [];
  if (p.material) {
    lines.push({ id: 'n1', code: `${p.part}-DJ`, name: `${p.name}锻件`, kind: 'make', qty: 1, unit: '件', op: '热模锻' });
    lines.push({ id: 'n2', parent: 'n1', code: '', name: `${p.material} 圆钢`, grade: p.material, kind: 'raw', qty: p.input ?? p.weight ?? 1, unit: 'kg', scrap: 1.5, op: '下料' });
  }
  return { part: p.part, name: p.name, lines };
}

function Tree({ lines }: { lines: S.BOMLine[] }) {
  const at = depths(lines);
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-[13px]">
        <thead>
          <tr className="border-b text-left text-[12px] text-muted-foreground">
            <th className="py-2 font-semibold">物料</th><th className="py-2 font-semibold max-sm:hidden">类型</th>
            <th className="py-2 text-right font-semibold">每件用量</th><th className="py-2 pl-3 text-right font-semibold max-md:hidden">损耗</th><th className="py-2 pl-3 font-semibold max-md:hidden">工序</th>
          </tr>
        </thead>
        <tbody>
          {lines.map((l) => {
            const d = at.get(l.id) ?? 1;
            return (
              <tr key={l.id} className="border-b last:border-0">
                <td className="py-2" style={{ paddingLeft: `${(d - 1) * 20}px` }}>
                  <div className="flex items-start gap-1.5">
                    {d > 1 && <span className="text-muted-foreground">└</span>}
                    <div className="min-w-0">
                      <b>{label(l)}</b> <span className="sm:hidden"><KindChip k={l.kind} /></span>
                      <div className="font-mono text-[11.5px] text-muted-foreground">{l.code}</div>
                      {l.note && <div className="text-[11.5px] text-muted-foreground">{l.note}</div>}
                    </div>
                  </div>
                </td>
                <td className="py-2 max-sm:hidden"><KindChip k={l.kind} /></td>
                <td className="py-2 text-right whitespace-nowrap tabular-nums">{S.qty(l.qty, l.unit)}</td>
                <td className="py-2 pl-3 text-right text-muted-foreground tabular-nums max-md:hidden">{l.scrap ? `${l.scrap}%` : ''}</td>
                <td className="py-2 pl-3 text-muted-foreground max-md:hidden">{l.op}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/** A revision exploded for a quantity: every level, sub-assemblies' own BOMs included, and what of each leaf the stores hold. */
function ExplodePanel({ c, b }: { c: api.Company; b: S.BOM }) {
  const [n, setN] = useState('100');
  const [x, setX] = useState<S.Explosion | null>(null);
  const [err, setErr] = useState<string>();
  const [rows, setRows] = useState(false);
  useEffect(() => {
    const qty = Number(n);
    if (!(qty > 0)) return;
    const t = setTimeout(() => S.explodeBOM(c.id, b.part, qty, b.id).then((v) => { setX(v); setErr(undefined); }, (e) => setErr((e as Error).message)), 250);
    return () => clearTimeout(t);
  }, [c.id, b.part, b.id, n]);
  return (
    <Panel
      title={<span className="flex items-center gap-2"><ListTreeIcon className="size-4" />按数量算料</span>}
      aside={<label className="flex items-center gap-2 text-[12.5px] font-bold">做<Input className="h-8 w-24 rounded-lg" type="number" min={1} value={n} onChange={(e) => setN(e.target.value)} />{b.unit ?? '件'}</label>}
    >
      {err && <p className="text-[13px] text-destructive">{err}</p>}
      {x && (
        <>
          <p className="mb-3 text-[12.5px]">
            {x.short === 0 && x.unkept === 0 ? <b className="text-success">库房的料够做 {S.num(x.qty)} {x.unit}</b>
              : <b className="text-destructive">做 {S.num(x.qty)} {x.unit}，{x.short} 样料不够{x.unkept > 0 ? `（其中 ${x.unkept} 样库房没有建档）` : ''}</b>}
            {b.status !== '生效' && <span className="ml-2 text-muted-foreground">（按 {b.rev} 版{b.status}算的）</span>}
          </p>
          <Needs needs={x.needs} />
          <button type="button" className="mt-3 text-[12.5px] font-bold text-primary" onClick={() => setRows(!rows)}>{rows ? '收起逐层用量' : '看逐层用量'}</button>
          {rows && (
            <ul className="mt-2 grid gap-1 text-[12.5px]">
              {x.rows.map((r, i) => (
                <li key={i} className="flex flex-wrap gap-x-2" style={{ paddingLeft: `${(r.level - 1) * 18}px` }}>
                  <KindChip k={r.kind} /><span>{label(r)}</span>
                  <span className="text-muted-foreground tabular-nums">每件 {S.qty(r.per, r.unit)}，共 {S.qty(r.total, r.unit)}</span>
                  {r.sub && <span className="text-primary">（展开 {r.sub} {r.rev} 版）</span>}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </Panel>
  );
}

/** What is needed of each leaf, against the stores. */
export function Needs({ needs, given }: { needs: S.Need[]; given?: boolean }) {
  return (
    <Table>
      <TableHeader>
        <TableRow className="text-[12px]">
          <TableHead>物料</TableHead><TableHead className="text-right">要</TableHead><TableHead className="text-right max-sm:hidden">{given ? '分到' : '库存'}</TableHead><TableHead className="text-right">缺</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {needs.map((n) => (
          <TableRow key={n.code + n.unit} className="text-[13px]">
            <TableCell>
              <span className="flex flex-wrap items-center gap-1.5"><b>{label(n)}</b><KindChip k={n.kind} /></span>
              <div className="font-mono text-[11.5px] text-muted-foreground">{n.code}{n.ops?.length ? `，${n.ops.join('、')}` : ''}</div>
            </TableCell>
            <TableCell className="text-right whitespace-nowrap tabular-nums">{S.qty(n.need, n.unit)}</TableCell>
            <TableCell className="text-right whitespace-nowrap text-muted-foreground tabular-nums max-sm:hidden">
              {n.problem ? <span className="text-warning">{n.problem}</span> : S.qty(given ? n.have : n.stock, n.unit)}
              {n.storeUnit && !n.problem && <small className="block">库房按 {n.storeUnit} 记</small>}
            </TableCell>
            <TableCell className={cn('text-right whitespace-nowrap font-bold tabular-nums', n.short > 0 ? 'text-destructive' : 'text-success')}>
              {n.short > 0 ? S.qty(n.short, n.unit) : '够'}
              {n.problem && <small className="block font-normal text-warning sm:hidden">{n.problem}</small>}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function ECOList({ ecos, showPart, onPick }: { ecos: S.ECO[]; showPart?: boolean; onPick?(part: string): void }) {
  return (
    <ul className="grid gap-3">
      {ecos.map((e) => (
        <li key={e.id} className="rounded-xl border p-3 text-[12.5px]">
          <div className="flex flex-wrap items-baseline gap-x-2">
            {showPart && <button type="button" className="font-mono font-bold text-primary" onClick={() => onPick?.(e.part)}>{e.part}</button>}
            <b>{e.from ? `${e.from} 版 → ${e.to} 版` : `${e.to} 版首次发布`}</b>
            <span className="text-muted-foreground">{S.day(e.at)}，{e.by} 批准{e.project ? '，随研发项目批准量产' : ''}</span>
          </div>
          {(e.from || e.why !== '首次发布') && <p className="mt-1">为什么：{e.why}</p>}
          <ul className="mt-1 list-disc pl-5 text-muted-foreground">{e.changes.map((x, i) => <li key={i}>{x}</li>)}</ul>
        </li>
      ))}
    </ul>
  );
}

function WherePanel({ c }: { c: api.Company }) {
  const [q, setQ] = useState('');
  const [list, setList] = useState<S.Usage[] | null>(null);
  useEffect(() => {
    if (!q.trim()) { setList(null); return; }
    const t = setTimeout(() => S.whereUsed(c.id, q).then(setList, () => setList([])), 300);
    return () => clearTimeout(t);
  }, [c.id, q]);
  return (
    <Panel title="查物料用在哪">
      <div className="relative mb-3">
        <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="物料编码、名称、牌号或规格，比如 40Cr、螺塞、M10" className="h-9 rounded-lg pl-9" />
      </div>
      {list && (list.length === 0 ? <p className="text-[12.5px] text-muted-foreground">生效的 BOM 里没有用到它的</p> : (
        <ul className="grid gap-1 text-[13px]">
          {list.map((u, i) => (
            <li key={i}><b className="font-mono">{u.part}</b> {u.name}（{u.rev} 版）：{u.item} 每件 {S.qty(u.per, u.unit)}{u.via ? `，在${u.via}里` : ''}{u.op ? `，${u.op}` : ''}</li>
          ))}
        </ul>
      ))}
    </Panel>
  );
}

function ReleaseDialog({ c, b, first, onClose, onDone }: { c: api.Company; b: S.BOM; first: boolean; onClose(): void; onDone(): void }) {
  const [why, setWhy] = useState(first ? '首次发布' : '');
  const [drawing, setDrawing] = useState(b.drawing ?? '');
  const [effective, setEffective] = useState(new Date(Date.now() + 8 * 3600_000).toISOString().slice(0, 10));
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      const r = await S.releaseBOM(c.id, b.id, { why, drawing, effective });
      toast.success(`${b.part} ${r.bom.rev} 版已生效${r.eco.from ? `，${r.eco.from} 版停用` : ''}`);
      onClose();
      onDone();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open onOpenChange={(v) => !v && onClose()} title={`${b.part} ${b.rev} 版生效`} problems={problems} busy={busy} onSave={save} save="生效">
      <p className="col-span-2 text-[12.5px] text-muted-foreground max-sm:col-span-1">
        {first ? '这是这个零件第一个生效的版本。' : '生效后原来生效的版本停用，工单齐套按新版本算。'}会记一条变更记录：改了什么、为什么、谁批准的。
      </p>
      <Field label="为什么改" wide><Text value={why} onChange={setWhy} placeholder="客户图纸改版、工艺优化、换供应商 …" /></Field>
      <Field label="图纸版本"><Text value={drawing} onChange={setDrawing} placeholder="C" /></Field>
      <Field label="生效日期"><Text type="date" value={effective} onChange={setEffective} /></Field>
    </FormDialog>
  );
}

// ---------- editing ----------

interface Draft extends S.BOMLine { depth: number }

function toDrafts(lines: S.BOMLine[]): Draft[] {
  const at = depths(lines);
  return lines.map((l) => ({ ...l, depth: at.get(l.id) ?? 1 }));
}

/** The editor's lines back as a tree: each line's parent is the nearest line above it one level up. */
function fromDrafts(ds: Draft[]): S.BOMLine[] {
  const stack: string[] = [];
  return ds.map(({ depth, ...l }) => {
    const d = Math.max(1, Math.min(depth, stack.length + 1));
    stack.length = d - 1;
    const parent = stack[d - 2];
    stack.push(l.id);
    return { ...l, parent: parent || undefined, qty: Number(l.qty) || 0, scrap: Number(l.scrap) || 0 };
  });
}

let seq = 0;
const blank = (depth = 1): Draft => ({ id: `n${Date.now()}${seq++}`, code: '', name: '', kind: 'std', qty: 1, unit: '件', depth });

function BOMEditor({ c, start, parts, onClose, onSaved }: { c: api.Company; start: Partial<S.BOM>; parts: S.PartRow[]; onClose(): void; onSaved(b: S.BOM): void }) {
  const [b, setB] = useState<Partial<S.BOM>>(start);
  const [lines, setLines] = useState<Draft[]>(() => (start.lines?.length ? toDrafts(start.lines) : [blank()]));
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const set = (i: number, v: Partial<Draft>) => setLines(lines.map((l, j) => (j === i ? { ...l, ...v } : l)));
  const move = (i: number, by: number) => {
    const j = i + by;
    if (j < 0 || j >= lines.length) return;
    const next = [...lines];
    [next[i], next[j]] = [next[j], next[i]];
    setLines(next);
  };
  const pickPart = (v: string) => {
    const p = parts.find((x) => x.part === v);
    setB({ ...b, part: v, name: b.name || p?.name });
    // a part its config knows the grade of: start from its forging and bar
    if (p?.material && lines.every((l) => !l.name.trim() && !l.code.trim())) setLines(toDrafts(starter(p).lines ?? []));
  };
  const save = async () => {
    setBusy(true);
    try {
      const saved = await S.saveBOM(c.id, { ...b, lines: fromDrafts(lines.filter((l) => l.name.trim() || l.code.trim())) });
      toast.success(start.id ? `已保存 ${saved.part} ${saved.rev} 版草稿` : `已建 ${saved.part} ${saved.rev} 版草稿，看好了再点「生效」`);
      onClose();
      onSaved(saved);
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-[1040px]">
        <DialogHeader>
          <DialogTitle>{start.id ? `改 ${start.part} ${start.rev} 版草稿` : '新建 BOM'}</DialogTitle>
          <DialogDescription>每行一样料，用量按上一级一件算。往右缩进就是放进上一行里（比如棒料放在锻件下面）。</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); save(); }}>
          <div className="grid grid-cols-4 gap-3 max-md:grid-cols-2 max-sm:grid-cols-1">
            <Field label="零件号">
              <Input className="h-9 rounded-lg" list="bom-parts" value={b.part ?? ''} disabled={!!start.id} onChange={(e) => pickPart(e.target.value)} placeholder="QZ-B20-4" />
              <datalist id="bom-parts">{parts.map((p) => <option key={p.part} value={p.part}>{p.name}</option>)}</datalist>
            </Field>
            <Field label="零件名称"><Text value={b.name} onChange={(v) => setB({ ...b, name: v })} placeholder="2.0T 四缸曲轴" /></Field>
            <Field label="版本" hint={start.id ? undefined : '不填就按 A、B、C 往下排'}><Text value={b.rev} onChange={(v) => setB({ ...b, rev: v })} placeholder="A" /></Field>
            <Field label="图纸版本"><Text value={b.drawing} onChange={(v) => setB({ ...b, drawing: v })} placeholder="B" /></Field>
          </div>
          <div className="grid gap-2">
            {lines.map((l, i) => (
              <div key={l.id} className="grid gap-2 rounded-xl border p-2.5" style={{ marginLeft: `${Math.min(l.depth - 1, 5) * 18}px` }}>
                <div className="grid grid-cols-[1.1fr_1.4fr_110px_1fr_0.9fr] gap-2 max-lg:grid-cols-2 max-sm:grid-cols-1">
                  <Input className="h-9 rounded-lg" value={l.code} onChange={(e) => set(i, { code: e.target.value })} placeholder="物料编码" aria-label="物料编码" />
                  <Input className="h-9 rounded-lg" value={l.name} onChange={(e) => set(i, { name: e.target.value })} placeholder="名称（如 油道螺塞）" aria-label="名称" />
                  <Pick value={l.kind} onChange={(v) => set(i, { kind: v as S.LineKind })} options={KINDS} />
                  <Input className="h-9 rounded-lg" value={l.spec ?? ''} onChange={(e) => set(i, { spec: e.target.value })} placeholder="规格（φ80、M10×1）" aria-label="规格" />
                  <Input className="h-9 rounded-lg" value={l.grade ?? ''} onChange={(e) => set(i, { grade: e.target.value })} placeholder="牌号（42CrMo）" aria-label="牌号" />
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <label className="flex items-center gap-1 text-[12px] font-bold">用量<Input className="h-8 w-24 rounded-lg" type="number" step="any" value={l.qty} onChange={(e) => set(i, { qty: e.target.value as unknown as number })} /></label>
                  <Input className="h-8 w-16 rounded-lg" value={l.unit} onChange={(e) => set(i, { unit: e.target.value })} aria-label="单位" />
                  <label className="flex items-center gap-1 text-[12px] font-bold">损耗 %<Input className="h-8 w-16 rounded-lg" type="number" step="any" value={l.scrap ?? ''} onChange={(e) => set(i, { scrap: e.target.value as unknown as number })} /></label>
                  <Input className="h-8 w-28 rounded-lg" value={l.op ?? ''} onChange={(e) => set(i, { op: e.target.value })} placeholder="工序" aria-label="工序" />
                  <div className="ml-auto flex gap-0.5">
                    <Button type="button" size="icon" variant="ghost" className="size-8" title="往左（提一级）" aria-label="往左" disabled={l.depth <= 1} onClick={() => set(i, { depth: l.depth - 1 })}><ArrowLeftIcon /></Button>
                    <Button type="button" size="icon" variant="ghost" className="size-8" title="往右（放进上一行）" aria-label="往右" disabled={i === 0 || l.depth > lines[i - 1].depth} onClick={() => set(i, { depth: l.depth + 1 })}><ArrowRightIcon /></Button>
                    <Button type="button" size="icon" variant="ghost" className="size-8" title="上移" aria-label="上移" disabled={i === 0} onClick={() => move(i, -1)}><ArrowUpIcon /></Button>
                    <Button type="button" size="icon" variant="ghost" className="size-8" title="下移" aria-label="下移" disabled={i === lines.length - 1} onClick={() => move(i, 1)}><ArrowDownIcon /></Button>
                    <Button type="button" size="icon" variant="ghost" className="size-8 text-destructive" title="删掉这行" aria-label="删掉这行" onClick={() => setLines(lines.filter((_, j) => j !== i))}><Trash2Icon /></Button>
                  </div>
                </div>
              </div>
            ))}
            <Button type="button" variant="outline" className="h-9 justify-self-start rounded-lg font-bold" onClick={() => setLines([...lines, blank(lines[lines.length - 1]?.depth ?? 1)])}><PlusIcon />加一行</Button>
          </div>
          <Field label="备注"><Text value={b.note} onChange={(v) => setB({ ...b, note: v })} placeholder="调质 + 高频淬火；客户图纸 C 版" /></Field>
          {problems.length > 0 && <ul className="grid gap-1 rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive">{problems.map((p, i) => <li key={i}>{p}</li>)}</ul>}
          <DialogFooter><Button type="submit" disabled={busy} className="h-9 rounded-lg font-bold">保存草稿</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ---------- importing ----------

const TEMPLATE = `层级,物料编码,物料名称,物料类型,规格,牌号,单件用量,单位,损耗率,工序,备注
1,QZ-B20-4-DJ,曲轴锻件,自制件,,,1,件,,热模锻,
2,GB-42CrMo-80,42CrMo 圆钢,原材料,φ80,42CrMo,27.4,kg,1.5%,下料,
1,BZ-PLUG-M10,油道螺塞,标准件,M10×1 GB/T 3,,3,件,,装配,
1,FL-S330,抛丸钢丸,辅料,S330,,0.11,kg,,抛丸,
`;

function downloadTemplate() {
  const url = URL.createObjectURL(new Blob(['﻿' + TEMPLATE], { type: 'text/csv;charset=utf-8' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = 'BOM导入模板.csv';
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

function ImportDialog({ c, parts, preset, onClose, onDone }: { c: api.Company; parts: S.PartRow[]; preset: string; onClose(): void; onDone(b: S.BOM): void }) {
  const [file, setFile] = useState<File | null>(null);
  const [part, setPart] = useState(preset);
  const [name, setName] = useState(parts.find((p) => p.part === preset)?.name ?? '');
  const [drawing, setDrawing] = useState('');
  const [busy, setBusy] = useState(false);
  const [problems, setProblems] = useState<string[]>([]);
  const [done, setDone] = useState<S.BOMImported | null>(null);
  const send = async () => {
    if (!file) { setProblems(['选一个 Excel（.xlsx）或 CSV 文件']); return; }
    setBusy(true);
    setProblems([]);
    try {
      const r = await S.importBOM(c.id, file, { part, name, drawing });
      setDone(r);
      onDone(r.bom);
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>导入 BOM</DialogTitle>
          <DialogDescription>
            Excel 或 CSV 都行。要有一行表头，至少有「用量」和「物料编码」或「名称」；层级写 1、2、3（或 .1、..2），也可以用「上级编码」一列。导进来是草稿，看过没问题再生效。
          </DialogDescription>
        </DialogHeader>
        {done ? (
          <div className="grid gap-2 text-[13px]">
            <p className="font-bold text-success">已导入 {done.bom.part} {done.bom.rev} 版草稿，{done.lines} 行物料</p>
            <p className="text-[12.5px] text-muted-foreground">认出的列：{done.columns.join('、')}</p>
            {done.problems.length > 0 && (
              <ul className="grid gap-1 rounded-xl bg-warning-soft px-3 py-2 text-[12.5px] text-warning">{done.problems.map((p, i) => <li key={i}>{p}</li>)}</ul>
            )}
            <DialogFooter><Button className="h-9 rounded-lg font-bold" onClick={onClose}>去看看</Button></DialogFooter>
          </div>
        ) : (
          <form className="grid grid-cols-2 gap-3 max-sm:grid-cols-1" onSubmit={(e) => { e.preventDefault(); send(); }}>
            <Field label="文件" wide>
              <Input className="h-9 rounded-lg" type="file" accept=".xlsx,.xlsm,.csv" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
            </Field>
            <Field label="零件号" hint="表里第 0 层写了零件本身的话可以不填">
              <Input className="h-9 rounded-lg" list="bom-import-parts" value={part} onChange={(e) => { setPart(e.target.value); setName(parts.find((p) => p.part === e.target.value)?.name ?? name); }} />
              <datalist id="bom-import-parts">{parts.map((p) => <option key={p.part} value={p.part}>{p.name}</option>)}</datalist>
            </Field>
            <Field label="零件名称"><Text value={name} onChange={setName} /></Field>
            <Field label="图纸版本"><Text value={drawing} onChange={setDrawing} placeholder="B" /></Field>
            <div className="flex items-end"><Button type="button" variant="ghost" className="h-9 rounded-lg font-bold" onClick={downloadTemplate}><FileDownIcon />下载模板</Button></div>
            {problems.length > 0 && <ul className="col-span-2 grid gap-1 rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive max-sm:col-span-1">{problems.map((p, i) => <li key={i}>{p}</li>)}</ul>}
            <DialogFooter className="col-span-2 max-sm:col-span-1"><Button type="submit" disabled={busy} className="h-9 rounded-lg font-bold">{busy ? '正在导入…' : '导入'}</Button></DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
