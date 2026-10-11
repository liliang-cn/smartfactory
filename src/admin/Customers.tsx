// 客户: every company the token covers, each with its first plant drawn as a plan.
import { useMemo, useState } from 'react';
import { PlusIcon, RadioTowerIcon, SearchIcon, TrendingUpIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import type * as api from './api';
import { Blueprint } from './Blueprint';
import { link, PageHead, useConsole } from './Console';
import { useCompany } from './layouts';

export function Badge({ text, className }: { text: string; className?: string }) {
  return <span className={cn('grid h-9 min-w-10 shrink-0 place-items-center rounded-[10px] bg-foreground px-1.5 text-[13px] font-extrabold text-background', className)}>{text}</span>;
}

export const totals = (c: api.Company) => ({
  plants: c.plants.length,
  lines: c.plants.reduce((n, p) => n + p.lines, 0),
  machines: c.plants.reduce((n, p) => n + p.machines, 0),
});

function Plan({ id }: { id: string }) {
  const { data, error } = useCompany(id);
  const plants = data?.plants ?? [];
  return (
    <div className="relative flex h-[148px] items-center gap-2 overflow-hidden rounded-t-[18px] border-b bg-[color-mix(in_oklab,var(--primary)_4%,var(--card))] px-4 py-3">
      {error && <p className="w-full text-center text-[12px] text-destructive">配置读不出来：{error}</p>}
      {plants.slice(0, 3).map((p) => (
        <Blueprint key={p.id} plant={p} labels={false} className="max-h-full min-w-0 flex-1" />
      ))}
      {plants.length > 3 && <span className="shrink-0 text-[12px] font-bold text-muted-foreground">+{plants.length - 3}</span>}
    </div>
  );
}

function Card({ c, industry }: { c: api.Company; industry?: api.IndustrySummary }) {
  const t = totals(c);
  return (
    <a {...link(`/admin/customers/${encodeURIComponent(c.id)}`)}
      className="group flex flex-col rounded-[18px] bg-card shadow-float outline-none ring-primary/40 transition-shadow hover:ring-2 focus-visible:ring-2">
      <Plan id={c.id} />
      <div className="flex items-center gap-3 p-4 pb-3">
        <Badge text={c.short} />
        <span className="grid min-w-0 flex-1 leading-tight">
          <b className="truncate text-[15.5px] font-extrabold group-hover:text-primary">{c.name}</b>
          <small className="truncate text-[12.5px] text-muted-foreground">{industry?.name ?? c.industry}，{c.id}</small>
        </span>
        {!c.builtin && <span className="shrink-0 rounded-md bg-brand-soft px-1.5 py-0.5 text-[11px] font-bold text-primary">自建</span>}
      </div>
      <dl className="mx-4 grid grid-cols-3 border-t py-3 text-center">
        {([['工厂', t.plants], ['产线', t.lines], ['设备', t.machines]] as const).map(([k, v]) => (
          <div key={k}>
            <dd className="text-[18px] leading-none font-extrabold tabular-nums">{v}</dd>
            <dt className="mt-1 text-[11.5px] text-muted-foreground">{k}</dt>
          </div>
        ))}
      </dl>
      {(c.liveTags > 0 || c.advisor) && (
        <div className="mx-4 mb-4 flex flex-wrap gap-1.5">
          {c.liveTags > 0 && <span className="inline-flex items-center gap-1 rounded-md bg-success-soft px-1.5 py-0.5 text-[11.5px] font-bold text-success"><RadioTowerIcon className="size-3" />真实数据 {c.liveTags} 个点</span>}
          {c.advisor && <span className="inline-flex items-center gap-1 rounded-md bg-warning-soft px-1.5 py-0.5 text-[11.5px] font-bold text-warning"><TrendingUpIcon className="size-3" />经营顾问</span>}
        </div>
      )}
    </a>
  );
}

export default function Customers() {
  const { ov } = useConsole();
  const [q, setQ] = useState('');
  const industries = useMemo(() => new Map(ov.industries.map((i) => [i.id, i])), [ov.industries]);
  const shown = ov.companies.filter((c) => !q || `${c.name}${c.short}${c.id}${industries.get(c.industry)?.name ?? ''}`.toLowerCase().includes(q.toLowerCase()));
  const all = ov.companies.reduce((a, c) => { const t = totals(c); return { lines: a.lines + t.lines, machines: a.machines + t.machines }; }, { lines: 0, machines: 0 });
  return (
    <>
      <PageHead title="客户" sub={`${ov.companies.length} 家公司，${all.lines} 条产线，${all.machines} 台设备在孪生里运行`}>
        {ov.companies.length > 6 && (
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="找客户" className="h-10 w-56 rounded-xl pl-9" aria-label="找客户" />
          </div>
        )}
        {ov.me.everywhere && (
          <Button asChild className="h-10 rounded-xl px-4 font-bold"><a {...link('/admin/new')}><PlusIcon />新建客户</a></Button>
        )}
      </PageHead>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-5 max-sm:grid-cols-1 max-sm:gap-3">
        {shown.map((c) => <Card key={c.id} c={c} industry={industries.get(c.industry)} />)}
        {ov.me.everywhere && !q && (
          <a {...link('/admin/new')}
            className="grid min-h-[300px] place-items-center rounded-[18px] border-2 border-dashed border-border text-muted-foreground transition-colors hover:border-primary/50 hover:bg-brand-soft/40 hover:text-primary max-sm:min-h-[120px]">
            <span className="grid justify-items-center gap-2">
              <span className="grid size-11 place-items-center rounded-full bg-card shadow-float"><PlusIcon className="size-5" /></span>
              <b className="text-[14px]">新建客户</b>
              <small className="text-[12px]">从现有的工厂起步，改成客户的样子</small>
            </span>
          </a>
        )}
      </div>
      {!shown.length && q && <p className="mt-10 text-center text-[13px] text-muted-foreground">没有叫「{q}」的客户</p>}
    </>
  );
}
