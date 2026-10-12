// 点检: the forms operators fill in by hand at the machines — patrol rounds, first-article checks,
// lab results — set up here; the rounds that are overdue; the code printed on each machine that
// opens its page on a phone; and what was written, to read and export.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import {
  ArrowDownIcon, ArrowUpIcon, ChevronDownIcon, DownloadIcon, LoaderCircleIcon, PencilIcon, PlusIcon, PrinterIcon, Trash2Icon, XIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import * as api from './api';
import { useCompany } from './layouts';
import { Panel } from './Panel';

const KINDS: Record<api.CheckField['kind'], string> = { number: '数字', choice: '选择', yesno: '是 / 否', text: '文字', photo: '照片' };
const EVERY: Record<string, string> = { none: '不定期', shift: '每班', day: '每天', hours: '每几小时' };
const ALL = '*';

const when = (iso?: string) => (iso ? new Date(iso).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '');
function span(iso?: string) {
  if (!iso) return '';
  const m = Math.max(1, Math.round((Date.now() - Date.parse(iso)) / 60000));
  return m < 60 ? `${m} 分钟` : m < 2880 ? `${Math.floor(m / 60)} 小时` : `${Math.floor(m / 1440)} 天`;
}
function everyText(s: api.CheckForm['schedule']) {
  if (!s.every) return '不定期';
  const at = s.at?.length ? `（${s.at.join('、')} 起）` : '';
  const head = s.every === 'hours' ? `每 ${s.hours} 小时` : EVERY[s.every];
  return `${head}${at}${s.within ? `，${s.within} 分钟内做完` : ''}`;
}
function valueText(v: api.CheckSubmission['values'][number]) {
  if (v.num !== undefined) return `${v.num}${v.unit ? ` ${v.unit}` : ''}`;
  return v.text ?? '';
}

// ---------- inputs that keep what is being typed ----------

/** A number that may be left empty; what is typed stays as typed until it reads as a number. */
function NumInput({ value, onChange, ...rest }: { value?: number; onChange(v?: number): void } & Omit<React.ComponentProps<typeof Input>, 'value' | 'onChange'>) {
  const [text, setText] = useState(value === undefined ? '' : String(value));
  useEffect(() => { if (value === undefined ? text !== '' : Number(text) !== value) setText(value === undefined ? '' : String(value)); }, [value]); // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <Input {...rest} inputMode="decimal" value={text} onChange={(e) => {
      setText(e.target.value);
      const t = e.target.value.trim();
      if (t === '') onChange(undefined);
      else if (Number.isFinite(Number(t))) onChange(Number(t));
    }} />
  );
}

/** A list typed as words separated by 、 , or spaces. */
function ListInput({ value, onChange, ...rest }: { value?: string[]; onChange(v: string[]): void } & Omit<React.ComponentProps<typeof Input>, 'value' | 'onChange'>) {
  const [text, setText] = useState((value ?? []).join('、'));
  return <Input {...rest} value={text} onChange={(e) => { setText(e.target.value); onChange(e.target.value.split(/[,，、\s]+/).map((x) => x.trim()).filter(Boolean)); }} />;
}

function Chip({ on, onClick, children, tone, disabled }: { on: boolean; onClick(): void; children: React.ReactNode; tone?: 'bad'; disabled?: boolean }) {
  return (
    <button type="button" aria-pressed={on} onClick={onClick} disabled={disabled}
      className={cn('h-8 rounded-lg border px-2.5 text-[12.5px] font-bold disabled:opacity-60',
        on ? (tone === 'bad' ? 'border-destructive bg-danger-soft text-destructive' : 'border-primary bg-brand-soft text-primary') : 'text-muted-foreground hover:bg-surface')}>
      {children}
    </button>
  );
}

// ---------- a form being set up ----------

function FieldRow({ f, i, n, twinFields, set, move, remove }: {
  f: api.CheckField; i: number; n: number; twinFields: Record<string, string>;
  set(p: Partial<api.CheckField>): void; move(d: -1 | 1): void; remove(): void;
}) {
  return (
    <li className="grid gap-2.5 rounded-xl border p-3">
      <div className="grid grid-cols-[minmax(0,1fr)_120px_110px_auto] gap-2 max-md:grid-cols-2">
        <Input value={f.label} onChange={(e) => set({ label: e.target.value })} placeholder="名称，如 油位" aria-label="名称" className="h-9 rounded-lg text-[13px] max-md:col-span-2" />
        <Input value={f.key} onChange={(e) => set({ key: e.target.value })} placeholder="标识 oil" aria-label="标识" className="h-9 rounded-lg font-mono text-[12.5px]" />
        <Select value={f.kind} onValueChange={(v) => set({ kind: v as api.CheckField['kind'], ...(v === 'yesno' && { options: ['是', '否'] }) })}>
          <SelectTrigger className="h-9 w-full rounded-lg text-[12.5px]" aria-label="类型"><SelectValue /></SelectTrigger>
          <SelectContent>{Object.entries(KINDS).map(([k, l]) => <SelectItem key={k} value={k}>{l}</SelectItem>)}</SelectContent>
        </Select>
        <div className="flex items-center gap-0.5 max-md:col-span-2 max-md:justify-end">
          <Button variant="ghost" size="icon" className="size-8 rounded-lg" aria-label="上移" disabled={i === 0} onClick={() => move(-1)}><ArrowUpIcon /></Button>
          <Button variant="ghost" size="icon" className="size-8 rounded-lg" aria-label="下移" disabled={i === n - 1} onClick={() => move(1)}><ArrowDownIcon /></Button>
          <Button variant="ghost" size="icon" className="size-8 rounded-lg text-muted-foreground" aria-label="删掉这一项" onClick={remove}><Trash2Icon /></Button>
        </div>
      </div>
      {f.kind === 'number' && (
        <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
          <Input value={f.unit ?? ''} onChange={(e) => set({ unit: e.target.value })} placeholder="单位" aria-label="单位" className="h-8 w-20 rounded-lg text-[12.5px]" />
          <span className="text-muted-foreground">下限</span><NumInput value={f.lo} onChange={(lo) => set({ lo })} aria-label="下限" className="h-8 w-20 rounded-lg text-[12.5px] tabular-nums" />
          <span className="text-muted-foreground">上限</span><NumInput value={f.hi} onChange={(hi) => set({ hi })} aria-label="上限" className="h-8 w-20 rounded-lg text-[12.5px] tabular-nums" />
          <span className="text-muted-foreground">目标</span><NumInput value={f.target} onChange={(target) => set({ target })} aria-label="目标值" className="h-8 w-20 rounded-lg text-[12.5px] tabular-nums" />
          <Select value={f.twin ?? 'none'} onValueChange={(v) => set({ twin: v === 'none' ? undefined : v as api.CheckField['twin'] })}>
            <SelectTrigger className="h-8 w-[150px] rounded-lg text-[12.5px]" aria-label="对应设备字段"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="none">不对应设备字段</SelectItem>
              {Object.entries(twinFields).map(([k, l]) => <SelectItem key={k} value={k}>对应{l}（{k}）</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
      )}
      {f.kind === 'choice' && (
        <div className="grid gap-2 text-[12.5px]">
          <ListInput value={f.options} onChange={(options) => set({ options, bad: (f.bad ?? []).filter((b) => options.includes(b)) })} placeholder="选项，用顿号隔开：正常、异常" aria-label="选项" className="h-8 rounded-lg text-[12.5px]" />
          {(f.options?.length ?? 0) > 0 && (
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-muted-foreground">算异常的：</span>
              {f.options!.map((o) => <Chip key={o} tone="bad" on={!!f.bad?.includes(o)} onClick={() => set({ bad: f.bad?.includes(o) ? f.bad.filter((x) => x !== o) : [...(f.bad ?? []), o] })}>{o}</Chip>)}
            </div>
          )}
        </div>
      )}
      {f.kind === 'yesno' && (
        <div className="flex items-center gap-1.5 text-[12.5px]">
          <span className="text-muted-foreground">算异常的回答：</span>
          {['是', '否'].map((o) => <Chip key={o} tone="bad" on={!!f.bad?.includes(o)} onClick={() => set({ bad: f.bad?.includes(o) ? [] : [o] })}>{o}</Chip>)}
        </div>
      )}
      <label className="flex w-fit items-center gap-2 text-[12.5px]">
        <Checkbox checked={!!f.required} onCheckedChange={(v) => set({ required: v === true })} />必填
      </label>
    </li>
  );
}

function FormEditor({ c, ov, draft: start, onDone }: { c: api.Company; ov: api.ChecksOverview; draft: api.CheckForm; onDone(saved?: api.CheckForm): void }) {
  const [f, setF] = useState(start);
  // each row keeps its own id while rows move, so what is typed in it stays with it
  const [ids, setIds] = useState(() => start.fields.map((_, i) => i));
  const [nextID, setNextID] = useState(start.fields.length);
  const [problems, setProblems] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const { data } = useCompany(c.id);
  const plant = data?.plants.find((p) => p.id === f.plant);
  const set = (p: Partial<api.CheckForm>) => setF((x) => ({ ...x, ...p }));
  const sched = (p: Partial<api.CheckForm['schedule']>) => setF((x) => ({ ...x, schedule: { ...x.schedule, ...p } }));
  const field = (i: number, p: Partial<api.CheckField>) => set({ fields: f.fields.map((x, j) => (j === i ? { ...x, ...p } : x)) });
  const toggle = (k: 'lines' | 'assets', id: string) => set({ [k]: (f[k] ?? []).includes(id) ? f[k]!.filter((x) => x !== id) : [...(f[k] ?? []), id] });
  const nextKey = () => { let n = f.fields.length + 1; while (f.fields.some((x) => x.key === `f${n}`)) n++; return `f${n}`; };
  const save = async () => {
    setBusy(true);
    setProblems([]);
    try {
      const saved = await api.saveCheckForm(c.id, f);
      toast.success(`「${saved.name}」已保存`);
      onDone(saved);
    } catch (e) {
      const err = e as api.ApiError;
      setProblems(err.problems?.length ? err.problems : [err.message]);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel title={f.id ? `改「${start.name}」` : '新表单'} aside={<Button variant="ghost" size="icon" className="rounded-lg" aria-label="关闭" onClick={() => onDone()}><XIcon /></Button>}>
      <div className="grid gap-5">
        <div className="grid grid-cols-[minmax(0,1fr)_160px] gap-3 max-sm:grid-cols-1">
          <label className="grid gap-1.5 text-[12.5px] font-bold">名称<Input value={f.name} onChange={(e) => set({ name: e.target.value })} className="h-10 rounded-xl text-[14px] font-normal" /></label>
          <label className="grid gap-1.5 text-[12.5px] font-bold">类型
            <Select value={f.kind} onValueChange={(v) => set({ kind: v as api.CheckForm['kind'] })}>
              <SelectTrigger className="h-10 w-full rounded-xl text-[14px] font-normal"><SelectValue /></SelectTrigger>
              <SelectContent>{Object.entries(ov.kinds).map(([k, l]) => <SelectItem key={k} value={k}>{l}</SelectItem>)}</SelectContent>
            </Select>
          </label>
        </div>

        <div className="grid gap-2">
          <h3 className="text-[13.5px] font-extrabold">用于哪些设备</h3>
          <Select value={f.plant || ALL} onValueChange={(v) => set({ plant: v === ALL ? '' : v, lines: [], assets: [] })}>
            <SelectTrigger className="h-9 w-[220px] rounded-lg text-[13px]" aria-label="厂"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>所有厂的所有工位</SelectItem>
              {data?.plants.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}
            </SelectContent>
          </Select>
          {plant && (
            <div className="grid gap-2.5">
              <p className="text-[12px] text-muted-foreground">选产线就是线上所有工位，也可以单选设备；都不选就是这个厂的所有工位。</p>
              {plant.lines.map((l) => (
                <div key={l.id} className="flex flex-wrap items-center gap-1.5">
                  <Chip on={!!f.lines?.includes(l.id)} onClick={() => toggle('lines', l.id)}>{l.name}</Chip>
                  <span className="mx-1 h-4 w-px bg-border" />
                  {l.stations.map((s) => (
                    <Chip key={s.id} on={!!f.assets?.includes(s.id) || !!f.lines?.includes(l.id)} disabled={f.lines?.includes(l.id)} onClick={() => toggle('assets', s.id)}>
                      <span className="font-mono">{s.id}</span> <span className="font-normal">{s.name}</span>
                    </Chip>
                  ))}
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="grid gap-2">
          <h3 className="text-[13.5px] font-extrabold">多久一轮</h3>
          <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
            <Select value={f.schedule.every || 'none'} onValueChange={(v) => sched({ every: v === 'none' ? '' : v as 'shift' | 'day' | 'hours', ...(v === 'hours' && !f.schedule.hours && { hours: 4 }) })}>
              <SelectTrigger className="h-9 w-[130px] rounded-lg text-[13px]" aria-label="周期"><SelectValue /></SelectTrigger>
              <SelectContent>{Object.entries(EVERY).map(([k, l]) => <SelectItem key={k} value={k}>{l}</SelectItem>)}</SelectContent>
            </Select>
            {f.schedule.every === 'hours' && <><span>每</span><NumInput value={f.schedule.hours} onChange={(hours) => sched({ hours })} aria-label="小时" className="h-9 w-16 rounded-lg text-center" /><span>小时</span></>}
            {f.schedule.every && (
              <>
                <span className="ml-2 text-muted-foreground">{f.schedule.every === 'shift' ? '各班开始' : '从'}</span>
                <ListInput key={f.schedule.every} value={f.schedule.at} onChange={(at) => sched({ at })} placeholder={f.schedule.every === 'shift' ? '08:00、20:00' : f.schedule.every === 'day' ? '08:00' : '00:00'} aria-label="开始时间" className="h-9 w-[150px] rounded-lg" />
                <span className="ml-2 text-muted-foreground">开始后</span>
                <NumInput value={f.schedule.within || undefined} onChange={(within) => sched({ within: within ?? 0 })} placeholder="不限" aria-label="几分钟内做完" className="h-9 w-16 rounded-lg text-center" />
                <span className="text-muted-foreground">分钟内做完</span>
              </>
            )}
          </div>
          <p className="text-[12px] text-muted-foreground">一轮过了时间没人填，或者上一轮整轮没人填，就算逾期。不填分钟数，就是下一轮开始前做完。</p>
        </div>

        <label className="grid gap-1.5 text-[12.5px] font-bold">填表须知
          <Textarea value={f.note ?? ''} onChange={(e) => set({ note: e.target.value })} rows={2} placeholder="手机上填之前先看到的话，可不填" className="rounded-xl text-[13px] font-normal" />
        </label>

        <div className="grid gap-2">
          <h3 className="text-[13.5px] font-extrabold">要填的项目</h3>
          <ul className="grid gap-2">
            {f.fields.map((x, i) => (
              <FieldRow key={ids[i]} f={x} i={i} n={f.fields.length} twinFields={ov.twinFields} set={(p) => field(i, p)}
                move={(d) => {
                  const a = [...f.fields], b = [...ids];
                  [a[i], a[i + d]] = [a[i + d], a[i]];
                  [b[i], b[i + d]] = [b[i + d], b[i]];
                  set({ fields: a });
                  setIds(b);
                }}
                remove={() => { set({ fields: f.fields.filter((_, j) => j !== i) }); setIds(ids.filter((_, j) => j !== i)); }} />
            ))}
          </ul>
          <Button variant="ghost" size="sm" className="w-fit rounded-lg text-[12.5px]" onClick={() => { set({ fields: [...f.fields, { key: nextKey(), label: '', kind: 'number' }] }); setIds([...ids, nextID]); setNextID(nextID + 1); }}><PlusIcon />加一项</Button>
          <p className="text-[12px] leading-relaxed text-muted-foreground">
            数字按填写时间存进设备读数，来源记为 manual。设了「对应设备字段」的和设备上报的同一字段放在一起；对应质量检测值（measure）的还会进孪生的质量图，不写上下限时按设备的公差判。超出上下限、选了算异常的答案，孪生里这台设备会报一条质量报警。
          </p>
        </div>

        {problems.length > 0 && <ul className="grid gap-1 rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive">{problems.map((p, i) => <li key={i}>{p}</li>)}</ul>}
        <div className="flex items-center gap-2 border-t pt-4">
          <Button variant="ghost" className="rounded-xl" onClick={() => onDone()}>不改了</Button>
          <Button className="ml-auto h-10 rounded-xl font-bold" disabled={busy} onClick={save}>{busy && <LoaderCircleIcon className="animate-spin" />}保存</Button>
        </div>
      </div>
    </Panel>
  );
}

// ---------- the forms, the rounds, the codes ----------

function Forms({ c, ov, onEdit, reload }: { c: api.Company; ov: api.ChecksOverview; onEdit(f: api.CheckForm): void; reload(): void }) {
  const [templates, setTemplates] = useState<api.CheckForm[]>([]);
  const [removing, setRemoving] = useState<string | null>(null);
  useEffect(() => { api.checkTemplates(c.id).then(setTemplates, () => {}); }, [c.id]);
  const { data } = useCompany(c.id);
  const scope = (f: api.CheckForm) => {
    const p = data?.plants.find((x) => x.id === f.plant);
    if (!f.plant) return '所有厂';
    const parts = [...(f.lines ?? []).map((l) => p?.lines.find((x) => x.id === l)?.name ?? l), ...(f.assets ?? [])];
    return `${p?.name ?? f.plant}${parts.length ? `：${parts.join('、')}` : ''}`;
  };
  const blank: api.CheckForm = { id: '', name: '', kind: 'patrol', fields: [{ key: 'f1', label: '', kind: 'number' }], schedule: { every: 'shift' }, created: '', updated: '' };
  const remove = async (f: api.CheckForm) => {
    try {
      await api.deleteCheckForm(c.id, f.id);
      toast.success(`「${f.name}」已停用，填过的记录还在`);
      reload();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setRemoving(null);
    }
  };
  return (
    <Panel title="表单" aside={ov.admin && (
      <Select value="" onValueChange={(v) => { const t = templates.find((x) => x.name === v); onEdit(t ? { ...t, id: '' } : blank); }}>
        <SelectTrigger className="h-9 rounded-xl text-[13px] font-bold" aria-label="新建表单"><PlusIcon className="size-4" /><SelectValue placeholder="新建表单" /></SelectTrigger>
        <SelectContent>
          {templates.map((t) => <SelectItem key={t.name} value={t.name}>照模板：{t.name}</SelectItem>)}
          <SelectItem value="空白">空白表单</SelectItem>
        </SelectContent>
      </Select>
    )}>
      {ov.forms.length ? (
        <Table>
          <TableHeader>
            <TableRow className="text-[12px]"><TableHead>名称</TableHead><TableHead className="max-md:hidden">用于</TableHead><TableHead>周期</TableHead><TableHead className="text-right">设备</TableHead><TableHead className="text-right">逾期</TableHead><TableHead /></TableRow>
          </TableHeader>
          <TableBody>
            {ov.forms.map((f) => {
              const rounds = ov.machines.flatMap((m) => m.rounds.filter((r) => r.form === f.id));
              const late = rounds.filter((r) => r.status === 'overdue').length;
              return (
                <TableRow key={f.id} className="text-[13px]">
                  <TableCell><b>{f.name}</b><span className="ml-1.5 text-[12px] text-muted-foreground">{f.name.includes(ov.kinds[f.kind]) ? '' : `${ov.kinds[f.kind]}，`}{f.fields.length} 项</span></TableCell>
                  <TableCell className="max-w-[280px] truncate text-muted-foreground max-md:hidden">{scope(f)}</TableCell>
                  <TableCell className="text-muted-foreground">{everyText(f.schedule)}</TableCell>
                  <TableCell className="text-right tabular-nums">{rounds.length}</TableCell>
                  <TableCell className={cn('text-right tabular-nums', late > 0 ? 'font-bold text-destructive' : 'text-muted-foreground')}>{late}</TableCell>
                  <TableCell className="text-right whitespace-nowrap">
                    {ov.admin && (removing === f.id ? (
                      <>
                        <Button variant="ghost" size="sm" className="rounded-lg" onClick={() => setRemoving(null)}>不停用</Button>
                        <Button variant="destructive" size="sm" className="rounded-lg font-bold" onClick={() => remove(f)}>停用</Button>
                      </>
                    ) : (
                      <>
                        <Button variant="ghost" size="icon" className="size-8 rounded-lg" aria-label="改" title="改" onClick={() => onEdit(f)}><PencilIcon /></Button>
                        <Button variant="ghost" size="icon" className="size-8 rounded-lg text-muted-foreground" aria-label="停用" title="停用" onClick={() => setRemoving(f.id)}><Trash2Icon /></Button>
                      </>
                    ))}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      ) : (
        <p className="text-[13px] text-muted-foreground">还没有表单。照模板新建一张，选好用于哪些设备，操作员就能在手机上填了。</p>
      )}
    </Panel>
  );
}

function Overdue({ ov }: { ov: api.ChecksOverview }) {
  const rounds = ov.machines.flatMap((m) => m.rounds);
  const late = rounds.filter((r) => r.status === 'overdue').sort((a, b) => Date.parse(a.since ?? '') - Date.parse(b.since ?? ''));
  const due = rounds.filter((r) => r.status === 'due').length;
  const done = rounds.filter((r) => r.status === 'done').length;
  return (
    <Panel title={<span className="flex items-center gap-2">逾期{late.length > 0 && <span className="rounded-md bg-danger-soft px-1.5 py-0.5 text-[12px] font-bold text-destructive tabular-nums">{late.length}</span>}</span>}
      aside={<span className="text-[12.5px] text-muted-foreground tabular-nums">本轮已做 {done}，待做 {due}</span>}>
      {late.length ? (
        <ul className="grid max-h-[560px] gap-1.5 overflow-y-auto">
          {late.slice(0, 50).map((r) => (
            <li key={`${r.form}/${r.plant}/${r.asset}`} className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-2 rounded-xl bg-surface px-3 py-2 text-[13px]">
              <span className="truncate"><b className="font-mono">{r.asset}</b> {r.assetName} · <b>{r.formName}</b></span>
              <span className="text-[12.5px] font-bold text-destructive tabular-nums">逾期 {span(r.since)}</span>
              <span className="col-span-2 truncate text-[12px] text-muted-foreground">{r.plantName} {r.lineName} · {r.last ? `上次 ${when(r.last)} ${r.lastBy ?? ''}` : '从没填过'}</span>
            </li>
          ))}
          {late.length > 50 && <li className="text-[12.5px] text-muted-foreground">还有 {late.length - 50} 轮</li>}
        </ul>
      ) : <p className="text-[13px] text-muted-foreground">{rounds.length ? '没有逾期的。' : '还没有要按时做的表单。'}</p>}
    </Panel>
  );
}

const pageLink = (company: string, plant: string, asset: string) =>
  `${location.origin}/check?${new URLSearchParams({ company, plant, asset })}`;
const qr = (text: string) => `/api/qr?text=${encodeURIComponent(text)}`;

/** A sheet of codes to cut out and stick on the machines, in a window of its own to print. */
function printCodes(company: api.Company, ms: api.ChecksOverview['machines']) {
  const w = window.open('', '_blank');
  if (!w) { toast.error('浏览器拦住了新窗口，允许弹出窗口后再点'); return; }
  const esc = (s: string) => s.replace(/[&<>"]/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[ch]!);
  const cards = ms.map((m) => `<figure><img src="${location.origin}${qr(pageLink(company.id, m.plant, m.asset))}" alt=""><figcaption><b>${esc(m.name)}</b><span>${esc(m.asset)} · ${esc(m.plantName)} ${esc(m.lineName)}</span><small>扫码填点检、首件、化验</small></figcaption></figure>`).join('');
  w.document.write(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>${esc(company.name)} 设备二维码</title><style>
    @page { size: A4; margin: 10mm }
    body { margin: 0; font-family: system-ui, 'PingFang SC', 'Microsoft YaHei', sans-serif; color: #1d2540 }
    main { display: grid; grid-template-columns: repeat(3, 1fr); gap: 6mm }
    figure { margin: 0; border: 1px dashed #b8c0d8; border-radius: 3mm; padding: 4mm; text-align: center; break-inside: avoid }
    img { width: 42mm; height: 42mm }
    figcaption { display: grid; gap: 1mm; font-size: 10pt }
    figcaption b { font-size: 12pt } figcaption small { color: #5d6684; font-size: 8pt }
  </style></head><body><main>${cards}</main></body></html>`);
  w.document.close();
  const imgs = [...w.document.images];
  Promise.all(imgs.map((i) => (i.complete ? null : new Promise((r) => { i.onload = i.onerror = r; })))).then(() => w.print());
}

function Codes({ c, ov }: { c: api.Company; ov: api.ChecksOverview }) {
  const plants = [...new Map(ov.machines.map((m) => [m.plant, m.plantName])).entries()];
  const [plant, setPlant] = useState(ALL);
  const ms = ov.machines.filter((m) => plant === ALL || m.plant === plant);
  if (!ov.machines.length) return null;
  return (
    <Panel title="设备二维码" aside={
      <>
        {plants.length > 1 && (
          <Select value={plant} onValueChange={setPlant}>
            <SelectTrigger className="h-9 rounded-xl text-[13px]" aria-label="厂"><SelectValue /></SelectTrigger>
            <SelectContent><SelectItem value={ALL}>所有厂</SelectItem>{plants.map(([id, name]) => <SelectItem key={id} value={id}>{name}</SelectItem>)}</SelectContent>
          </Select>
        )}
        <Button variant="outline" className="h-9 rounded-xl font-bold" onClick={() => printCodes(c, ms)}><PrinterIcon />打印 {ms.length} 张</Button>
      </>
    }>
      <p className="mb-3 text-[12.5px] text-muted-foreground">贴在设备上。操作员用手机扫码，直接打开这台设备要填的表（第一次要登录操作员令牌）。</p>
      <ul className="grid grid-cols-[repeat(auto-fill,minmax(132px,1fr))] gap-2.5">
        {ms.slice(0, 24).map((m) => (
          <li key={`${m.plant}/${m.asset}`} className="grid justify-items-center gap-1 rounded-xl border p-2.5 text-center">
            <img src={qr(pageLink(c.id, m.plant, m.asset))} alt={`${m.asset} 的二维码`} className="size-24" loading="lazy" />
            <b className="max-w-full truncate text-[12.5px]">{m.name}</b>
            <a href={pageLink(c.id, m.plant, m.asset)} target="_blank" rel="noreferrer" className="font-mono text-[11.5px] text-primary hover:underline">{m.asset}</a>
          </li>
        ))}
      </ul>
      {ms.length > 24 && <p className="mt-2 text-[12.5px] text-muted-foreground">只显示前 24 台，打印时全部都有。</p>}
    </Panel>
  );
}

// ---------- what was written ----------

function Photo({ company, id }: { company: string; id: string }) {
  const [url, setUrl] = useState<string>();
  useEffect(() => {
    let u: string | undefined;
    api.checkPhoto(company, id).then((x) => { u = x; setUrl(x); }, () => {});
    return () => { if (u) URL.revokeObjectURL(u); };
  }, [company, id]);
  return url ? <a href={url} target="_blank" rel="noreferrer"><img src={url} alt="照片" className="h-28 rounded-lg object-cover" /></a> : <span className="grid h-28 w-36 place-items-center rounded-lg bg-surface text-[12px] text-muted-foreground">照片</span>;
}

function Submissions({ c, ov }: { c: api.Company; ov: api.ChecksOverview }) {
  const [form, setForm] = useState(ALL);
  const [asset, setAsset] = useState(ALL);
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [bad, setBad] = useState(false);
  const [list, setList] = useState<api.CheckSubmission[] | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const query = useMemo(() => {
    const q = new URLSearchParams({ limit: '200' });
    if (form !== ALL) q.set('form', form);
    if (asset !== ALL) { const [p, a] = asset.split('/'); q.set('plant', p); q.set('asset', a); }
    if (from) q.set('from', String(new Date(`${from}T00:00`).getTime()));
    if (to) q.set('to', String(new Date(`${to}T00:00`).getTime() + 86400_000));
    if (bad) q.set('bad', '1');
    return q;
  }, [form, asset, from, to, bad]);
  useEffect(() => {
    let live = true;
    api.checkSubmissions(c.id, query).then((l) => live && setList(l), (e: Error) => { toast.error(e.message); setList([]); });
    return () => { live = false; };
  }, [c.id, query]);
  const forms = useMemo(() => [...new Map([...ov.forms.map((f) => [f.id, f.name] as const), ...(list ?? []).map((s) => [s.form, s.formName] as const)]).entries()], [ov.forms, list]);
  const exportCSV = async () => {
    setBusy(true);
    try { await api.downloadChecks(c.id, query, `${c.short || c.id}-人工记录-${new Date().toLocaleDateString('sv')}.csv`); } catch (e) { toast.error((e as Error).message); } finally { setBusy(false); }
  };
  return (
    <Panel title="填过的记录" aside={<Button variant="outline" className="h-9 rounded-xl font-bold" disabled={busy || !list?.length} onClick={exportCSV}>{busy ? <LoaderCircleIcon className="animate-spin" /> : <DownloadIcon />}导出 CSV</Button>}>
      <div className="mb-3 flex flex-wrap items-center gap-2 text-[12.5px]">
        <Select value={form} onValueChange={setForm}>
          <SelectTrigger className="h-9 w-[150px] rounded-lg text-[13px]" aria-label="表单"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value={ALL}>所有表单</SelectItem>{forms.map(([id, name]) => <SelectItem key={id} value={id}>{name}</SelectItem>)}</SelectContent>
        </Select>
        <Select value={asset} onValueChange={setAsset}>
          <SelectTrigger className="h-9 w-[200px] rounded-lg text-[13px]" aria-label="设备"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>所有设备</SelectItem>
            {ov.machines.map((m) => <SelectItem key={`${m.plant}/${m.asset}`} value={`${m.plant}/${m.asset}`}>{m.asset} {m.name}</SelectItem>)}
          </SelectContent>
        </Select>
        <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} aria-label="从" className="h-9 w-[150px] rounded-lg text-[13px]" />
        <span className="text-muted-foreground">到</span>
        <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} aria-label="到" className="h-9 w-[150px] rounded-lg text-[13px]" />
        <label className="ml-1 flex items-center gap-2"><Checkbox checked={bad} onCheckedChange={(v) => setBad(v === true)} />只看有异常的</label>
      </div>
      {list === null ? <div className="h-16" /> : list.length === 0 ? <p className="text-[13px] text-muted-foreground">没有这样的记录。</p> : (
        <ul className="grid gap-1.5">
          {list.map((s) => {
            const expanded = open === s.id;
            return (
              <li key={s.id} className="rounded-xl border">
                <button type="button" onClick={() => setOpen(expanded ? null : s.id)} aria-expanded={expanded} className="flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5 text-left text-[13px]">
                  <ChevronDownIcon className={cn('size-4 shrink-0 text-muted-foreground transition-transform', expanded && 'rotate-180')} />
                  <span className="w-[92px] tabular-nums">{when(s.taken)}</span>
                  <span className="min-w-0"><b className="font-mono">{s.asset}</b> {s.assetName}</span>
                  <span className="font-bold">{s.formName}</span>
                  <span className="text-muted-foreground">{s.by}</span>
                  {s.late && <span className="rounded-md bg-surface px-1.5 py-0.5 text-[11.5px] font-bold text-muted-foreground" title={`${when(s.received)} 收到`}>离线补发</span>}
                  <span className={cn('ml-auto rounded-md px-1.5 py-0.5 text-[12px] font-bold', s.bad ? 'bg-danger-soft text-destructive' : 'bg-success-soft text-success')}>{s.bad ? `${s.bad} 项异常` : '正常'}</span>
                </button>
                {expanded && (
                  <div className="grid gap-2 border-t px-3 py-2.5 text-[13px]">
                    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
                      {s.values.filter((v) => v.kind !== 'photo').map((v) => (
                        <div key={v.key} className="contents">
                          <dt className="text-muted-foreground">{v.label}</dt>
                          <dd className={cn(v.bad && 'font-bold text-destructive')}>
                            {valueText(v)}
                            {v.bad && v.num !== undefined && <span className="ml-1.5 font-normal">{v.why}</span>}
                            {(v.lo !== undefined || v.hi !== undefined) && <span className="ml-1.5 font-normal text-muted-foreground">标准 {v.lo ?? '…'}～{v.hi ?? '…'}</span>}
                            {v.flag && <span className="ml-1.5 font-normal text-warning">读数不可信：{v.flag}</span>}
                          </dd>
                        </div>
                      ))}
                    </dl>
                    {s.values.some((v) => v.kind === 'photo') && (
                      <div className="flex flex-wrap gap-2">{s.values.filter((v) => v.kind === 'photo' && v.text).map((v) => <Photo key={v.key} company={c.id} id={v.text!} />)}</div>
                    )}
                    <p className="text-[12px] text-muted-foreground">{s.id}，{when(s.taken)} 记，{when(s.received)} 收到，{s.plantName}</p>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

export function ChecksPanel({ c }: { c: api.Company }) {
  const [ov, setOv] = useState<api.ChecksOverview | null>(null);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<api.CheckForm | null>(null);
  const load = useCallback(() => { api.checks(c.id).then(setOv, (e: Error) => setError(e.message)); }, [c.id]);
  useEffect(() => {
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, 30_000);
    return () => clearInterval(t);
  }, [load]);
  if (error) return <Panel title="点检"><p className="text-[13px] text-destructive">{error}</p></Panel>;
  if (!ov) return <Panel title="点检"><div className="h-24" /></Panel>;
  if (editing) return <FormEditor c={c} ov={ov} draft={editing} onDone={(saved) => { setEditing(null); if (saved) load(); }} />;
  return (
    <div className="grid gap-5">
      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_420px]">
        <Forms c={c} ov={ov} onEdit={setEditing} reload={load} />
        <Overdue ov={ov} />
      </div>
      <Codes c={c} ov={ov} />
      <Submissions c={c} ov={ov} />
      <p className="text-[12px] text-muted-foreground">
        手机上打开 <a className="font-mono text-primary hover:underline" href={`/check?company=${encodeURIComponent(c.id)}`} target="_blank" rel="noreferrer">/check?company={c.id}</a> 也能挑设备来填。
      </p>
    </div>
  );
}
