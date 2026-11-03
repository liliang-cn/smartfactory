// 库房: what the company keeps — steel bar by heat, standard parts, finished parts — with every
// receipt, issue and count; parts below their safety stock; and tracing a heat or an order.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import { ArrowDownToLineIcon, ArrowUpFromLineIcon, ClipboardCheckIcon, PlusIcon, SearchIcon, TriangleAlertIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import type * as api from './api';
import * as S from './storesApi';
import { Panel } from './Panel';
import { Empty, Field, FormDialog, Pick, Text } from './StoreForm';

const KIND_LABEL: Record<S.MoveInput['kind'], string> = { in: '入库', out: '出库', count: '盘点' };
const CAT_KINDS: [S.StoreCategory['kind'], string][] = [['bar', '棒料（按炉号收发，产线按重量自动扣）'], ['part', '备件、标准件'], ['product', '成品'], ['other', '其他']];

export function StoresPanel({ c }: { c: api.Company }) {
  const [data, setData] = useState<S.StoresOverview | null>(null);
  const [error, setError] = useState<string>();
  const [cat, setCat] = useState('');
  const [q, setQ] = useState('');
  const [itemOpen, setItemOpen] = useState(false);
  const [catOpen, setCatOpen] = useState(false);
  const [move, setMove] = useState<{ item: S.StoreItem; kind: S.MoveInput['kind'] } | null>(null);
  const load = useCallback(() => S.stores(c.id).then((d) => { setData(d); setError(undefined); }, (e) => setError((e as Error).message)), [c.id]);
  useEffect(() => { load(); const t = setInterval(load, 20_000); return () => clearInterval(t); }, [load]);

  const shown = useMemo(() => (data?.items ?? []).filter((it) => (!cat || it.cat === cat) && (!q || `${it.code}${it.name}${Object.values(it.attrs ?? {}).join('')}${(it.heats ?? []).map((h) => h.no).join('')}`.toLowerCase().includes(q.toLowerCase()))), [data, cat, q]);
  if (error) return <Panel><p className="text-[13px] text-destructive">库房读不出来：{error}</p></Panel>;
  if (!data) return null;
  const cats = data.categories;
  const catOf = (k: string) => cats.find((x) => x.key === k);
  const low = data.items.filter((it) => it.low);

  return (
    <div className="grid gap-5">
      {low.length > 0 && (
        <Panel title={<span className="flex items-center gap-2 text-destructive"><TriangleAlertIcon className="size-4" />{low.length} 样低于安全库存</span>}>
          <ul className="grid gap-1 text-[13px]">
            {low.map((it) => <li key={it.id}><b>{it.name}</b> {it.attrs?.['规格'] ?? ''}：库存 {S.num(it.stock)} {it.unit}，安全库存 {S.num(it.safety)}{it.attrs?.['用在'] ? `，用在${it.attrs['用在']}` : ''}</li>)}
          </ul>
        </Panel>
      )}
      <Panel
        title="库房"
        aside={data.operator && cats.length > 0 && (
          <>
            <Button variant="outline" className="h-9 rounded-lg font-bold" onClick={() => setCatOpen(true)}><PlusIcon />分类</Button>
            <Button className="h-9 rounded-lg font-bold" onClick={() => setItemOpen(true)}><PlusIcon />新建物料</Button>
          </>
        )}
      >
        {cats.length === 0 ? (
          <Empty title="还没有库房分类">
            库房里还没有分类。先加一个分类（比如 原材料、备件、成品），再建物料、记入库。
            {data.operator && <div className="mt-3"><Button className="h-9 rounded-lg font-bold" onClick={() => setCatOpen(true)}><PlusIcon />加分类</Button></div>}
          </Empty>
        ) : (
          <>
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <div className="flex gap-1 overflow-x-auto rounded-lg bg-muted p-[3px]">
                {[['', '全部'] as const, ...cats.map((x) => [x.key, x.label] as const)].map(([k, l]) => (
                  <button key={k} type="button" onClick={() => setCat(k)} className={cn('h-8 shrink-0 rounded-md px-3 text-[12.5px] font-bold text-muted-foreground', cat === k && 'bg-background text-primary shadow-sm')}>
                    {l}<span className="ml-1 tabular-nums opacity-60">{data.items.filter((it) => !k || it.cat === k).length}</span>
                  </button>
                ))}
              </div>
              <div className="relative ml-auto max-sm:ml-0 max-sm:w-full">
                <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="名称、牌号、规格、炉号" className="h-9 w-60 rounded-lg pl-9 max-sm:w-full" />
              </div>
            </div>
            {data.items.length === 0 ? (
              <Empty title="库房里还没有物料">
                先点「新建物料」建档（棒料要填牌号和规格，标准件填规格和安全库存），再记入库。棒料入库要填炉号、钢厂和质保书号，产线开工用到哪炉钢会自动记下来。
              </Empty>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow className="text-[12px]">
                    <TableHead>物料</TableHead><TableHead className="max-md:hidden">规格</TableHead><TableHead className="text-right">库存</TableHead>
                    <TableHead className="text-right max-sm:hidden">安全库存</TableHead><TableHead className="max-lg:hidden">库位</TableHead>{data.operator && <TableHead />}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((it) => (
                    <ItemRow key={it.id} it={it} bar={catOf(it.cat)?.kind === 'bar'} operator={data.operator} onMove={(kind) => setMove({ item: it, kind })} />
                  ))}
                </TableBody>
              </Table>
            )}
          </>
        )}
      </Panel>
      {cats.length > 0 && <TracePanel c={c} />}
      {data.moves.length > 0 && (
        <Panel title="最近的收发">
          <ul className="grid gap-1.5 text-[12.5px]">
            {data.moves.map((m) => (
              <li key={m.id} className="grid grid-cols-[88px_44px_1fr_auto] gap-2 tabular-nums max-sm:grid-cols-[70px_40px_1fr]">
                <span className="text-muted-foreground">{S.day(m.at)}</span>
                <b className={m.kind === 'in' ? 'text-success' : m.kind === 'out' ? 'text-primary' : ''}>{KIND_LABEL[m.kind]}</b>
                <span className="min-w-0 truncate">{m.itemName} {m.kind === 'count' ? `盘得 ${S.num(m.qty)}（${m.delta >= 0 ? '+' : ''}${S.num(m.delta)}）` : S.num(m.qty)}{m.heat ? ` 炉号 ${m.heat}` : ''}{m.ref ? `，${m.ref}` : ''}</span>
                <span className="text-muted-foreground max-sm:hidden">{m.who}</span>
              </li>
            ))}
          </ul>
        </Panel>
      )}
      <ItemDialog c={c} open={itemOpen} onOpenChange={setItemOpen} cats={cats} defaultCat={cat || cats[0]?.key} onSaved={load} />
      <CategoryDialog c={c} open={catOpen} onOpenChange={setCatOpen} onSaved={load} />
      {move && <MoveDialog c={c} item={move.item} kind={move.kind} bar={catOf(move.item.cat)?.kind === 'bar'} onClose={() => setMove(null)} onSaved={load} />}
    </div>
  );
}

function ItemRow({ it, bar, operator, onMove }: { it: S.StoreItem; bar: boolean; operator: boolean; onMove(k: S.MoveInput['kind']): void }) {
  const spec = Object.entries(it.attrs ?? {}).filter(([, v]) => v).map(([k, v]) => (k === '规格' || k === '牌号' ? v : `${k} ${v}`)).join('，');
  return (
    <>
      <TableRow>
        <TableCell className="whitespace-normal"><b className="text-[13px]">{it.name}</b><small className="block text-muted-foreground">{it.code}</small></TableCell>
        <TableCell className="max-md:hidden text-[12.5px]">{spec}</TableCell>
        <TableCell className={cn('text-right font-bold tabular-nums', it.low && 'text-destructive')}>{S.num(it.stock)} <small className="font-normal text-muted-foreground">{it.unit}</small></TableCell>
        <TableCell className="text-right tabular-nums text-muted-foreground max-sm:hidden">{it.safety ? S.num(it.safety) : '—'}</TableCell>
        <TableCell className="text-[12.5px] text-muted-foreground max-lg:hidden">{it.place}</TableCell>
        {operator && (
          <TableCell className="text-right">
            <div className="flex justify-end gap-1">
              <Button size="sm" variant="ghost" className="h-8 rounded-lg px-2 font-bold" onClick={() => onMove('in')} title="入库"><ArrowDownToLineIcon />入</Button>
              <Button size="sm" variant="ghost" className="h-8 rounded-lg px-2 font-bold" onClick={() => onMove('out')} title="出库"><ArrowUpFromLineIcon />出</Button>
              <Button size="sm" variant="ghost" className="h-8 rounded-lg px-2 font-bold max-sm:hidden" onClick={() => onMove('count')} title="盘点"><ClipboardCheckIcon />盘</Button>
            </div>
          </TableCell>
        )}
      </TableRow>
      {bar && (it.heats ?? []).map((h) => (
        <TableRow key={h.no} className="bg-surface/60 text-[12px]">
          <TableCell colSpan={6} className="whitespace-normal py-1.5 pl-6 text-muted-foreground tabular-nums">
            炉号 <b className="text-foreground">{h.no}</b>，{h.mill}{h.cert ? `，质保书 ${h.cert}` : ''}；入库 {S.num(h.received)} {h.unit}，产线用掉 {S.num(h.used)} {h.unit}，剩 <b className="text-foreground">{S.num(h.left)}</b> {h.unit}
          </TableCell>
        </TableRow>
      ))}
    </>
  );
}

function ItemDialog({ c, open, onOpenChange, cats, defaultCat, onSaved }: { c: api.Company; open: boolean; onOpenChange(v: boolean): void; cats: S.StoreCategory[]; defaultCat?: string; onSaved(): void }) {
  const [it, setIt] = useState<Partial<S.StoreItem>>({});
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (open) { setIt({ cat: defaultCat, attrs: {} }); setProblems([]); } }, [open, defaultCat]);
  const cat = cats.find((x) => x.key === it.cat);
  const save = async () => {
    setBusy(true);
    try {
      const saved = await S.saveStoreItem(c.id, { ...it, safety: Number(it.safety) || 0 });
      toast.success(`已建档：${saved.name}`);
      onOpenChange(false);
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open={open} onOpenChange={onOpenChange} title="新建物料" problems={problems} busy={busy} onSave={save} save="建档">
      <Field label="分类"><Pick value={it.cat ?? ''} onChange={(v) => setIt({ ...it, cat: v })} options={cats.map((x) => [x.key, x.label])} /></Field>
      <Field label="名称"><Text value={it.name} onChange={(v) => setIt({ ...it, name: v })} placeholder={cat?.kind === 'bar' ? '42CrMo 圆钢' : cat?.kind === 'part' ? '高强度螺栓' : ''} /></Field>
      <Field label="编码" hint="不填就用名称"><Text value={it.code} onChange={(v) => setIt({ ...it, code: v })} /></Field>
      <Field label="单位" hint={cat?.kind === 'bar' ? '棒料用 t 或 kg' : undefined}><Text value={it.unit} onChange={(v) => setIt({ ...it, unit: v })} placeholder={cat?.unit} /></Field>
      {(cat?.fields ?? []).map((f) => (
        <Field key={f} label={f}><Text value={it.attrs?.[f]} onChange={(v) => setIt({ ...it, attrs: { ...it.attrs, [f]: v } })} placeholder={f === '牌号' ? '42CrMo' : f === '规格' ? (cat?.kind === 'bar' ? 'φ80' : 'M24×120') : ''} /></Field>
      ))}
      <Field label="安全库存" hint="低于它会提醒补货，0 是不管"><Text type="number" value={it.safety} onChange={(v) => setIt({ ...it, safety: v as unknown as number })} /></Field>
      <Field label="库位"><Text value={it.place} onChange={(v) => setIt({ ...it, place: v })} placeholder="原料库 A1 区" /></Field>
    </FormDialog>
  );
}

function CategoryDialog({ c, open, onOpenChange, onSaved }: { c: api.Company; open: boolean; onOpenChange(v: boolean): void; onSaved(): void }) {
  const [cat, setCat] = useState<Partial<S.StoreCategory>>({ kind: 'part' });
  const [fields, setFields] = useState('规格');
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (open) { setCat({ kind: 'part', unit: '件' }); setFields('规格'); setProblems([]); } }, [open]);
  const save = async () => {
    setBusy(true);
    try {
      await S.saveStoreCategory(c.id, { ...cat, fields: fields.split(/[,，、\s]+/).filter(Boolean) });
      toast.success(`已加分类：${cat.label}`);
      onOpenChange(false);
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open={open} onOpenChange={onOpenChange} title="加分类" problems={problems} busy={busy} onSave={save} save="加上">
      <Field label="名称"><Text value={cat.label} onChange={(v) => setCat({ ...cat, label: v })} placeholder="备件" /></Field>
      <Field label="是什么"><Pick value={cat.kind ?? ''} onChange={(v) => setCat({ ...cat, kind: v as S.StoreCategory['kind'], unit: v === 'bar' ? 't' : cat.unit })} options={CAT_KINDS} /></Field>
      <Field label="单位"><Text value={cat.unit} onChange={(v) => setCat({ ...cat, unit: v })} /></Field>
      <Field label="要记的项" hint="用顿号分开，比如 牌号、规格"><Text value={fields} onChange={setFields} /></Field>
    </FormDialog>
  );
}

function MoveDialog({ c, item, kind, bar, onClose, onSaved }: { c: api.Company; item: S.StoreItem; kind: S.MoveInput['kind']; bar: boolean; onClose(): void; onSaved(): void }) {
  const [m, setM] = useState<Partial<S.MoveInput>>({ kind, heat: kind !== 'in' ? item.heats?.[0]?.no : undefined });
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      await S.recordMove(c.id, { ...m, item: item.id, kind, qty: Number(m.qty) } as S.MoveInput);
      toast.success(`已${KIND_LABEL[kind]}：${item.name}`);
      onClose();
      onSaved();
    } catch (e) { setProblems(S.problemsOf(e)); } finally { setBusy(false); }
  };
  return (
    <FormDialog open onOpenChange={(v) => !v && onClose()} title={`${KIND_LABEL[kind]}：${item.name}`} problems={problems} busy={busy} onSave={save} save={KIND_LABEL[kind]}>
      <Field label={kind === 'count' ? `盘得多少（${item.unit}）` : `数量（${item.unit}）`} hint={kind === 'count' ? `账上 ${S.num(bar && m.heat ? item.heats?.find((h) => h.no === m.heat)?.left ?? 0 : item.stock)} ${item.unit}` : `现有 ${S.num(item.stock)} ${item.unit}`}>
        <Text type="number" value={m.qty} onChange={(v) => setM({ ...m, qty: v as unknown as number })} />
      </Field>
      {bar && (kind === 'in' ? (
        <>
          <Field label="炉号"><Text value={m.heat} onChange={(v) => setM({ ...m, heat: v })} placeholder="26A10231" /></Field>
          <Field label="钢厂"><Text value={m.mill} onChange={(v) => setM({ ...m, mill: v })} /></Field>
          <Field label="质保书号"><Text value={m.cert} onChange={(v) => setM({ ...m, cert: v })} /></Field>
        </>
      ) : (
        <Field label="炉号"><Pick value={m.heat ?? ''} onChange={(v) => setM({ ...m, heat: v })} options={(item.heats ?? []).map((h) => [h.no, `${h.no}（${h.mill}，剩 ${S.num(h.left)} ${h.unit}）`])} placeholder="选炉号" /></Field>
      ))}
      <Field label={kind === 'in' ? '送货单 / 来源' : kind === 'out' ? '用在 / 去向' : '说明'}>
        <Text value={m.ref} onChange={(v) => setM({ ...m, ref: v })} placeholder={kind === 'out' ? (bar ? '退回钢厂、外协' : 'HP-101 维修') : ''} />
      </Field>
      <Field label="备注" wide><Text value={m.note} onChange={(v) => setM({ ...m, note: v })} placeholder={kind === 'in' && bar ? '进厂复检：低倍、超声合格' : ''} /></Field>
      {bar && kind === 'out' && <p className="col-span-2 text-[12px] text-muted-foreground max-sm:col-span-1">产线锻掉的钢不用在这里出库：开工用到哪炉、多少件，系统按件数和投料重量自动扣。</p>}
    </FormDialog>
  );
}

function TracePanel({ c }: { c: api.Company }) {
  const [q, setQ] = useState('');
  const [t, setT] = useState<S.HeatTrace | null>(null);
  const [msg, setMsg] = useState<string>();
  const run = async () => {
    if (!q.trim()) return;
    try { setT(await S.traceHeat(c.id, q.trim())); setMsg(undefined); } catch (e) { setT(null); setMsg((e as api.ApiError).status === 404 ? `没查到 ${q}：炉号要入库登记过，工单要开工后才有记录` : (e as Error).message); }
  };
  return (
    <Panel title="炉号追溯">
      <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); run(); }}>
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="填炉号，或工单号（MO-…）" className="h-9 max-w-80 rounded-lg" />
        <Button type="submit" variant="outline" className="h-9 rounded-lg font-bold"><SearchIcon />追溯</Button>
      </form>
      {msg && <p className="mt-3 text-[12.5px] text-muted-foreground">{msg}</p>}
      {t && (
        <div className="mt-3 grid gap-2 text-[12.5px]">
          {t.heat && (
            <p>炉号 <b>{t.heat.no}</b>：{t.heat.itemName}，{t.heat.mill}{t.heat.cert ? `，质保书 ${t.heat.cert}` : ''}；{S.day(t.heat.at)} 入库 {S.num(t.heat.received)} {t.heat.unit}，产线用掉 {S.num(t.heat.used)} {t.heat.unit}，剩 {S.num(t.heat.left)} {t.heat.unit}</p>
          )}
          {t.uses.length > 0 && (
            <ul className="grid gap-1">
              {t.uses.map((u, i) => <li key={i} className="tabular-nums">{u.lineName}{u.order ? ` 为工单 ${u.order} ` : ''}用炉号 {u.heat}（{u.mill}）锻了 {u.pieces} 件，{S.day(u.first)} 到 {S.day(u.last)}{u.stored ? '' : '（库房没登记这炉）'}</li>)}
            </ul>
          )}
          {t.note && <p className="text-amber-700">{t.note}</p>}
        </div>
      )}
    </Panel>
  );
}
