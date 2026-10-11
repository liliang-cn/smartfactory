// 模板: factories to start from — built in, or saved here (from a factory, or imported from a file
// another installation exported). A template is structure only: plants, lines, machines; never a
// company's data.
import { useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import { useRef } from 'react';
import { ArrowRightIcon, DownloadIcon, FileUpIcon, LoaderCircleIcon, PackageIcon, SaveIcon, Trash2Icon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { cn } from '@/lib/utils';
import { companyConfig, industriesOf, industryConfig, layoutCompany, validateCompany, validateIndustry } from '@/config/loader';
import type { IndustryCfg } from '@/config/schema';
import type { PlantLayout } from '@/core/layout';
import * as api from './api';
import { Blueprint } from './Blueprint';
import { link, PageHead, useConsole } from './Console';

const SOURCE: Record<api.TemplateSummary['source'], { label: string; tone: string }> = {
  builtin: { label: '内置', tone: 'bg-surface text-muted-foreground' },
  local: { label: '这里的', tone: 'bg-brand-soft text-primary' },
};

/** A template as a file, for another installation to import. */
async function download(t: api.TemplateSummary) {
  const full = await api.template(t.source, t.id);
  const url = URL.createObjectURL(new Blob([JSON.stringify(full, null, 2)], { type: 'application/json' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = `${t.id}.smartfactory-template.json`;
  a.click();
  URL.revokeObjectURL(url);
}

const plans = new Map<string, Promise<PlantLayout[]>>();

/** A template laid out, for its picture. */
function usePlan(t: api.TemplateSummary) {
  const key = `${t.source}:${t.id}`;
  const [plants, setPlants] = useState<PlantLayout[] | null>(null);
  useEffect(() => {
    let p = plans.get(key);
    if (!p) {
      p = (async () => {
        if (t.source === 'builtin') {
          const { company, industries } = await companyConfig(t.id, api.industry);
          return layoutCompany(company, industries);
        }
        const full = await api.template(t.source, t.id);
        const inds = new Map<string, IndustryCfg>();
        for (const [id, raw] of Object.entries(full.industries ?? {})) inds.set(id, validateIndustry(raw, id));
        const company = validateCompany(full.company, t.name);
        for (const id of industriesOf(company)) if (!inds.has(id)) inds.set(id, await industryConfig(id, api.industry));
        return layoutCompany(company, inds);
      })();
      p.catch(() => plans.delete(key));
      plans.set(key, p);
    }
    let live = true;
    p.then((x) => live && setPlants(x), () => live && setPlants([]));
    return () => { live = false; };
  }, [key, t]);
  return plants;
}

function TemplateCard({ t, canUse, onChanged }: { t: api.TemplateSummary; canUse: boolean; onChanged(): void }) {
  const plants = usePlan(t);
  const [busy, setBusy] = useState(false);
  const lines = t.plants.reduce((n, p) => n + p.lines.length, 0);
  const machines = t.plants.reduce((n, p) => n + p.machines, 0);
  const remove = async () => {
    setBusy(true);
    try {
      await api.deleteTemplate(t.id);
      toast.success(`${t.name} 已删除`);
      onChanged();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <article className="flex flex-col rounded-[18px] bg-card shadow-float">
      <div className="flex h-[140px] items-center gap-2 overflow-hidden rounded-t-[18px] border-b bg-[color-mix(in_oklab,var(--primary)_4%,var(--card))] px-4 py-3">
        {!plants && <LoaderCircleIcon className="mx-auto size-5 animate-spin text-muted-foreground" />}
        {plants?.slice(0, 3).map((p) => <Blueprint key={p.id} plant={p} labels={false} className="max-h-full min-w-0 flex-1" />)}
      </div>
      <div className="grid flex-1 gap-2 p-4">
        <div className="flex items-start gap-2">
          <span className="grid min-w-0 flex-1 leading-tight">
            <b className="truncate text-[15px] font-extrabold">{t.name}</b>
            <small className="truncate text-[12.5px] text-muted-foreground">{t.industryName}，{t.plants.length} 个工厂，{lines} 条线，{machines} 台设备</small>
          </span>
          <span className={cn('shrink-0 rounded-md px-1.5 py-0.5 text-[11px] font-bold', SOURCE[t.source].tone)}>{SOURCE[t.source].label}</span>
        </div>
        {t.description && <p className="line-clamp-2 text-[12.5px] leading-relaxed text-muted-foreground">{t.description}</p>}
        {t.author && t.source !== 'builtin' && <p className="text-[12px] text-muted-foreground">{t.author}</p>}
        <div className="mt-auto flex flex-wrap items-center gap-2 pt-2">
          {canUse && (
            <Button asChild className="h-9 rounded-xl font-bold"><a {...link(`/admin/new?template=${t.source}:${encodeURIComponent(t.id)}`)}>用它建工厂<ArrowRightIcon /></a></Button>
          )}
          <Button variant="outline" className="h-9 rounded-xl font-bold" title="存成文件，给别的工厂导入" onClick={() => download(t).catch((e: Error) => toast.error(e.message))}>
            <DownloadIcon />导出
          </Button>
          {t.source === 'local' && (
            <Button variant="ghost" size="icon" className="ml-auto size-9 rounded-xl text-muted-foreground hover:bg-danger-soft hover:text-destructive" aria-label="删除模板" title="删除模板" disabled={busy} onClick={remove}>
              <Trash2Icon />
            </Button>
          )}
        </div>
      </div>
    </article>
  );
}

function SaveDialog({ open, onOpenChange, onSaved }: { open: boolean; onOpenChange(o: boolean): void; onSaved(): void }) {
  const { ov } = useConsole();
  const [from, setFrom] = useState(ov.companies[0]?.id ?? '');
  const [id, setId] = useState('');
  const [name, setName] = useState('');
  const [desc, setDesc] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  useEffect(() => { if (open) { setFrom(ov.companies[0]?.id ?? ''); setId(''); setName(''); setDesc(''); setError(undefined); } }, [open, ov.companies]);
  const ok = /^[a-z0-9][a-z0-9-]{1,47}$/.test(id) && name.trim() && from;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[480px] rounded-[20px]">
        <DialogHeader>
          <DialogTitle className="text-[17px] font-extrabold">存成模板</DialogTitle>
          <DialogDescription>只存工厂的结构：工厂、产线、设备和它们用的行业包。公司名字换成模板名，数据、资料、记录都不带。</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={async (e) => {
          e.preventDefault();
          if (!ok) return;
          setBusy(true);
          setError(undefined);
          try {
            await api.saveTemplate({ id, name: name.trim(), description: desc.trim() || undefined, from });
            toast.success(`${name.trim()} 已存成模板`);
            onSaved();
            onOpenChange(false);
          } catch (err) {
            setError((err as Error).message);
          } finally {
            setBusy(false);
          }
        }}>
          {ov.companies.length > 1 && (
            <div className="grid gap-1.5">
              <span className="text-[13px] font-bold">哪家的工厂</span>
              <ToggleGroup type="single" value={from} onValueChange={(v) => v && setFrom(v)} className="flex-wrap justify-start gap-1.5">
                {ov.companies.map((c) => (
                  <ToggleGroupItem key={c.id} value={c.id} className="h-8 rounded-lg border px-2.5 text-[12.5px] font-bold data-[state=on]:border-primary data-[state=on]:bg-brand-soft data-[state=on]:text-primary">{c.name}</ToggleGroupItem>
                ))}
              </ToggleGroup>
            </div>
          )}
          <div className="grid grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)] gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="tname" className="text-[13px] font-bold">模板名称</Label>
              <Input id="tname" value={name} onChange={(e) => setName(e.target.value)} placeholder="三厂锻造：曲轴、法兰、主轴" className="h-10 rounded-xl" autoFocus />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="tid" className="text-[13px] font-bold">标识</Label>
              <Input id="tid" value={id} onChange={(e) => setId(e.target.value.toLowerCase())} placeholder="forge-3plants" className="h-10 rounded-xl font-mono" spellCheck={false} />
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="tdesc" className="text-[13px] font-bold">说明 <small className="font-normal text-muted-foreground">适合什么样的工厂</small></Label>
            <Textarea id="tdesc" value={desc} onChange={(e) => setDesc(e.target.value)} rows={3} className="resize-none rounded-xl" />
          </div>
          {error && <p className="rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="ghost" className="rounded-xl" onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" className="rounded-xl font-bold" disabled={!ok || busy}>{busy && <LoaderCircleIcon className="animate-spin" />}存成模板</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** The templates, filtered by where they come from; embedded is the gallery a new instance starts from. */
export function TemplateGallery({ embedded = false }: { embedded?: boolean }) {
  const { ov } = useConsole();
  const [list, setList] = useState<api.TemplateSummary[] | null>(null);
  const [src, setSrc] = useState<'all' | api.TemplateSummary['source']>('all');
  const [saving, setSaving] = useState(false);
  const load = () => { api.templates().then(setList, (e: Error) => { toast.error(e.message); setList([]); }); };
  useEffect(load, []);
  const canUse = ov.me.everywhere && (ov.single ? ov.companies.length === 0 : !ov.runs);
  const counts = useMemo(() => Object.fromEntries((['builtin', 'local'] as const).map((k) => [k, (list ?? []).filter((t) => t.source === k).length])), [list]);
  const file = useRef<HTMLInputElement>(null);
  const importFile = async (f: File) => {
    try {
      const t = await api.importTemplate(JSON.parse(await f.text()));
      toast.success(`${t.name} 已导入`);
      load();
    } catch (e) {
      toast.error(e instanceof SyntaxError ? '这不是模板文件（不是 JSON）' : (e as Error).message);
    }
  };
  const shown = (list ?? []).filter((t) => src === 'all' || t.source === src);
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <ToggleGroup type="single" value={src} onValueChange={(v) => v && setSrc(v as typeof src)} className="rounded-xl bg-card p-0.5 shadow-float">
          <ToggleGroupItem value="all" className="h-8 rounded-lg px-3 text-[12.5px] font-bold data-[state=on]:bg-brand-soft data-[state=on]:text-primary">全部</ToggleGroupItem>
          {(['builtin', 'local'] as const).filter((k) => counts[k] > 0).map((k) => (
            <ToggleGroupItem key={k} value={k} className="h-8 gap-1.5 rounded-lg px-3 text-[12.5px] font-bold data-[state=on]:bg-brand-soft data-[state=on]:text-primary">
              {k === 'local' ? <SaveIcon className="size-3.5" /> : <PackageIcon className="size-3.5" />}
              {SOURCE[k].label} <span className="tabular-nums opacity-60">{counts[k]}</span>
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <div className="ml-auto flex gap-2">
          <input ref={file} type="file" accept=".json,application/json" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) importFile(f); e.target.value = ''; }} />
          <Button variant="outline" className="h-9 rounded-xl font-bold" onClick={() => file.current?.click()}><FileUpIcon />导入模板</Button>
          {!embedded && ov.companies.length > 0 && (
            <Button className="h-9 rounded-xl font-bold" onClick={() => setSaving(true)}><SaveIcon />把工厂存成模板</Button>
          )}
        </div>
      </div>
      {!list && <div className="grid h-40 place-items-center"><LoaderCircleIcon className="animate-spin text-muted-foreground" /></div>}
      <div className="grid grid-cols-[repeat(auto-fill,minmax(290px,1fr))] gap-5 max-sm:grid-cols-1 max-sm:gap-3">
        {shown.map((t) => <TemplateCard key={`${t.source}:${t.id}`} t={t} canUse={canUse} onChanged={load} />)}
      </div>
      <SaveDialog open={saving} onOpenChange={setSaving} onSaved={load} />
    </div>
  );
}

export default function Templates() {
  return (
    <>
      <PageHead title="模板" sub="建工厂的起点：内置的，和这里存的、导入的。模板只有结构（工厂、产线、设备），不带任何公司的数据，导出成文件就能给别的工厂用" />
      <TemplateGallery />
    </>
  );
}
