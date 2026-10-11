// 资料库: a company's own documents — PDF work instructions, Word process cards, Excel parameter
// sheets, text — dropped in, read into passages for the knowledge base, kept with every version.
import { Fragment, useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { CheckIcon, CircleAlertIcon, DownloadIcon, FileTextIcon, LoaderCircleIcon, Trash2Icon, UploadIcon, XIcon } from 'lucide-react';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import * as api from './api';
import { Panel } from './Panel';

const KIND: Record<api.Doc['kind'], { label: string; tone: string }> = {
  pdf: { label: 'PDF', tone: 'bg-danger-soft text-destructive' },
  docx: { label: 'Word', tone: 'bg-brand-soft text-primary' },
  xlsx: { label: 'Excel', tone: 'bg-success-soft text-success' },
  csv: { label: 'CSV', tone: 'bg-success-soft text-success' },
  markdown: { label: '文本', tone: 'bg-surface text-muted-foreground' },
  text: { label: '文本', tone: 'bg-surface text-muted-foreground' },
};

const bytes = (n?: number) => (!n ? '' : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : n >= 1024 ? `${Math.round(n / 1024)} KB` : `${n} B`);
const day = (iso: string) => new Date(iso).toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' });

interface Sending { key: string; file: File; progress: number; result?: api.UploadResult; error?: string }

/** What an upload came to, in a line. */
function outcome(r: api.UploadResult) {
  const d = r.doc!;
  const what = [d.pages ? `${d.pages} 页` : '', `${d.parts} 段`].filter(Boolean).join('，');
  return r.outcome === 'updated' ? `已更新为第 ${d.version} 版，${what}` : `已收下，${what}`;
}

function Versions({ company, doc }: { company: string; doc: api.Doc }) {
  const [list, setList] = useState<api.DocVersion[] | null>(null);
  useEffect(() => { api.docVersions(company, doc.id).then(setList, () => setList([])); }, [company, doc.id, doc.version]);
  if (!list) return <div className="py-2 pl-4"><LoaderCircleIcon className="size-4 animate-spin text-muted-foreground" /></div>;
  return (
    <ul className="grid gap-1 py-1 pl-4">
      {list.map((v) => (
        <li key={v.version} className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[12.5px]">
          <b className={cn('w-12', v.current && 'text-primary')}>第 {v.version} 版</b>
          <span className="text-muted-foreground">{v.by}，{day(v.created)}</span>
          <span className="text-muted-foreground tabular-nums">{[v.pages ? `${v.pages} 页` : '', `${v.parts} 段`, bytes(v.bytes)].filter(Boolean).join('，')}</span>
          {v.current && <span className="rounded-md bg-brand-soft px-1.5 py-px text-[11px] font-bold text-primary">知识库在用</span>}
          {v.download && (
            <button type="button" className="ml-auto inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-bold text-muted-foreground hover:bg-surface hover:text-foreground"
              onClick={() => api.downloadDoc(company, doc.id, v.file || `${doc.title}.md`, v.version).catch((e: Error) => toast.error(e.message))}>
              <DownloadIcon className="size-3.5" />下载
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

export function Library({ company, admin, onChange }: { company: string; admin: boolean; onChange?(): void }) {
  const [docs, setDocs] = useState<api.Doc[] | null>(null);
  const [error, setError] = useState<string>();
  const [sending, setSending] = useState<Sending[]>([]);
  const [over, setOver] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const [removing, setRemoving] = useState<api.Doc | null>(null);
  const pick = useRef<HTMLInputElement>(null);
  const busy = useRef(false);
  const load = useCallback(() => { api.docs(company).then(setDocs, (e: Error) => setError(e.message)); }, [company]);
  useEffect(load, [load]);

  // one file at a time: each is read and indexed before the next goes
  const run = useCallback(async (queue: Sending[]) => {
    if (busy.current) return;
    busy.current = true;
    for (const s of queue) {
      try {
        const r = await api.uploadDoc(company, s.file, (p) => setSending((xs) => xs.map((x) => (x.key === s.key ? { ...x, progress: p } : x))));
        setSending((xs) => xs.map((x) => (x.key === s.key ? { ...x, progress: 1, result: r, error: r.ok ? undefined : r.error } : x)));
      } catch (e) {
        setSending((xs) => xs.map((x) => (x.key === s.key ? { ...x, error: (e as Error).message } : x)));
      }
      load();
    }
    busy.current = false;
    onChange?.();
  }, [company, load, onChange]);

  const add = (files: FileList | File[] | null) => {
    if (!files || !admin) return;
    const queue = [...files].map((file) => ({ key: `${file.name}-${file.size}-${Math.random()}`, file, progress: 0 }));
    if (!queue.length) return;
    setSending((xs) => [...xs.filter((x) => !x.result && !x.error), ...queue]);
    run(queue);
  };
  const done = sending.length > 0 && sending.every((x) => x.result || x.error);

  return (
    <Panel
      title={<span className="flex items-center gap-2">资料库{docs && <span className="text-[12.5px] font-semibold text-muted-foreground tabular-nums">{docs.length} 篇</span>}</span>}
      aside={admin && <Button size="sm" className="rounded-lg font-bold" onClick={() => pick.current?.click()}><UploadIcon />上传资料</Button>}
      className={cn('relative transition-shadow', over && 'ring-2 ring-primary')}
    >
      <div
        onDragOver={(e) => { if (admin && e.dataTransfer.types.includes('Files')) { e.preventDefault(); setOver(true); } }}
        onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(false); }}
        onDrop={(e) => { e.preventDefault(); setOver(false); add(e.dataTransfer.files); }}
        className="grid gap-4"
      >
        <input ref={pick} type="file" accept={api.DOC_ACCEPT} multiple hidden onChange={(e) => { add(e.target.files); e.target.value = ''; }} />
        {over && (
          <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center rounded-[18px] bg-brand-soft/80 backdrop-blur-[1px]">
            <span className="flex items-center gap-2 text-[15px] font-extrabold text-primary"><UploadIcon className="size-5" />松开上传</span>
          </div>
        )}

        {sending.length > 0 && (
          <ul className="grid gap-1.5 rounded-xl bg-surface/70 p-3">
            {sending.map((s) => (
              <li key={s.key} className="grid gap-1">
                <div className="flex items-center gap-2 text-[13px]">
                  {s.error ? <CircleAlertIcon className="size-4 shrink-0 text-destructive" />
                    : s.result ? <CheckIcon className="size-4 shrink-0 text-success" />
                      : <LoaderCircleIcon className="size-4 shrink-0 animate-spin text-primary" />}
                  <b className="min-w-0 truncate">{s.file.name}</b>
                  <span className="shrink-0 text-[12px] text-muted-foreground tabular-nums">{bytes(s.file.size)}</span>
                  <span className={cn('ml-auto shrink-0 text-[12.5px]', s.error ? 'text-destructive' : 'text-muted-foreground')}>
                    {s.error ?? (s.result ? outcome(s.result) : s.progress < 1 ? `${Math.round(s.progress * 100)}%` : '在读取')}
                  </span>
                </div>
                {!s.result && !s.error && (
                  <div className="h-1 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${Math.max(4, s.progress * 100)}%` }} /></div>
                )}
                {s.result?.doc?.problems?.map((p) => <p key={p} className="pl-6 text-[12px] text-warning">{p}</p>)}
              </li>
            ))}
            {done && (
              <li><button type="button" className="inline-flex items-center gap-1 text-[12.5px] font-bold text-muted-foreground hover:text-foreground" onClick={() => setSending([])}><XIcon className="size-3.5" />清除</button></li>
            )}
          </ul>
        )}

        {error && <p className="text-[13px] text-destructive">{error}</p>}
        {docs && !docs.length && (
          admin ? (
            <button type="button" onClick={() => pick.current?.click()}
              className="grid place-items-center gap-2 rounded-xl border-2 border-dashed px-4 py-10 text-center hover:border-primary/50 hover:bg-brand-soft/30">
              <UploadIcon className="size-6 text-muted-foreground" />
              <b className="text-[14px]">把作业指导书、工艺卡、参数表拖到这里</b>
              <span className="text-[12.5px] text-muted-foreground">PDF、Word、Excel、CSV、文本（UTF-8 或 GBK），每个不超过 50 MB</span>
            </button>
          ) : <p className="py-6 text-center text-[13px] text-muted-foreground">还没有资料</p>
        )}
        {docs && docs.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow className="text-[12px]">
                <TableHead>资料</TableHead><TableHead className="text-right">段落</TableHead><TableHead className="text-right max-sm:hidden">大小</TableHead>
                <TableHead className="max-md:hidden">版本</TableHead><TableHead className="max-md:hidden">上传</TableHead><TableHead className="w-20" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {docs.map((d) => (
                <Fragment key={d.id}>
                  <TableRow className="text-[13px]">
                    <TableCell className="max-w-[360px]">
                      <div className="flex items-center gap-2">
                        <span className={cn('shrink-0 rounded-md px-1.5 py-px text-[11px] font-bold', KIND[d.kind].tone)}>{KIND[d.kind].label}</span>
                        <b className="truncate">{d.title}</b>
                      </div>
                      {d.file && d.file.replace(/\.[^.]+$/, '') !== d.title && <div className="truncate pl-0.5 text-[12px] text-muted-foreground">{d.file}</div>}
                      {d.problems?.map((p) => <div key={p} className="text-[12px] text-warning">{p}</div>)}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{d.parts}{d.pages ? <div className="text-[12px] text-muted-foreground">{d.pages} 页</div> : null}</TableCell>
                    <TableCell className="text-right text-muted-foreground tabular-nums max-sm:hidden">{bytes(d.bytes) || `${d.size} 字`}</TableCell>
                    <TableCell className="max-md:hidden">
                      <button type="button" onClick={() => setOpen(open === d.id ? null : d.id)}
                        className={cn('rounded-md px-1.5 py-0.5 text-[12.5px] font-bold hover:bg-surface', d.version > 1 ? 'text-primary' : 'text-muted-foreground')}>
                        第 {d.version} 版
                      </button>
                    </TableCell>
                    <TableCell className="text-muted-foreground max-md:hidden">{d.by}，{day(d.updated)}</TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-0.5">
                        <Button variant="ghost" size="icon" className="size-8 rounded-lg text-muted-foreground" title="下载" aria-label={`下载 ${d.title}`}
                          onClick={() => api.downloadDoc(company, d.id, d.file || `${d.title}.md`).catch((e: Error) => toast.error(e.message))}><DownloadIcon /></Button>
                        {admin && (
                          <Button variant="ghost" size="icon" className="size-8 rounded-lg text-muted-foreground hover:bg-danger-soft hover:text-destructive" title="删除" aria-label={`删除 ${d.title}`}
                            onClick={() => setRemoving(d)}><Trash2Icon /></Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                  {open === d.id && (
                    <TableRow className="hover:bg-transparent"><TableCell colSpan={6} className="bg-surface/50"><Versions company={company} doc={d} /></TableCell></TableRow>
                  )}
                </Fragment>
              ))}
            </TableBody>
          </Table>
        )}
        {docs && docs.length > 0 && admin && (
          <p className="flex items-center gap-1.5 text-[12px] text-muted-foreground"><FileTextIcon className="size-3.5" />拖文件到这里也能上传。同名的资料再传一次就是新版本，旧版本留着可以下载。</p>
        )}
      </div>

      <AlertDialog open={!!removing} onOpenChange={(o) => !o && setRemoving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除「{removing?.title}」？</AlertDialogTitle>
            <AlertDialogDescription>它的 {removing?.version} 个版本和原文件一起删除，知识库里也不再有它的段落。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>不删</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={async () => {
              const d = removing!;
              try {
                await api.deleteDoc(company, d.id);
                toast.success(`已删除「${d.title}」`);
                load();
                onChange?.();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}>删除</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Panel>
  );
}
