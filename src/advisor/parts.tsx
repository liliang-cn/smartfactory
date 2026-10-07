// Pieces shared by the business pages: cached queries, the card every chart sits in, result tables.
import { useEffect, useState } from 'react';
import { ChevronRightIcon, CodeXmlIcon } from 'lucide-react';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { query, type Answer, type Query } from './api';
import { toNumber, type Model } from './model';

const cache = new Map<string, Promise<Answer>>();

export function runQuery(company: string, q: Query) {
  const key = `${company}|${JSON.stringify(q)}`;
  let p = cache.get(key);
  if (!p) {
    p = query(company, q);
    cache.set(key, p);
    p.catch(() => cache.delete(key));
  }
  return p;
}

export interface Loaded { data?: Answer; error?: string; loading: boolean }

export function useAnswer(company: string, q: Query | null): Loaded {
  const key = q ? JSON.stringify(q) : '';
  const [state, setState] = useState<Loaded & { key: string }>({ loading: !!q, key });
  useEffect(() => {
    if (!q) return;
    let live = true;
    setState((s) => ({ ...s, loading: true, error: undefined, key }));
    runQuery(company, q).then(
      (data) => live && setState({ data, loading: false, key }),
      (e: Error) => live && setState({ error: e.message, loading: false, key }),
    );
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [company, key]);
  return state.key === key ? state : { loading: true };
}

export interface Merged { rows: Record<string, unknown>[]; sql: string[]; loading: boolean; failed: boolean }

/**
 * One query per metric, joined on the grouping columns: a metric the semantic layer refuses
 * (or does not have yet) leaves its column empty instead of failing the whole table.
 */
export function useMerged(company: string, metrics: string[], q: Omit<Query, 'metrics'> | null): Merged {
  const key = q ? JSON.stringify([metrics, q]) : '';
  const [state, setState] = useState<Merged & { key: string }>({ rows: [], sql: [], loading: true, failed: false, key });
  useEffect(() => {
    if (!q) return;
    let live = true;
    setState((s) => ({ ...s, loading: true, key }));
    Promise.allSettled(metrics.map((m) => runQuery(company, { ...q, metrics: [m] }))).then((rs) => {
      if (!live) return;
      const by = q.group_by ?? [];
      const rows = new Map<string, Record<string, unknown>>();
      const sql: string[] = [];
      rs.forEach((r) => {
        if (r.status !== 'fulfilled') return;
        sql.push(r.value.sql);
        for (const o of records(r.value)) {
          const k = by.map((d) => String(o[d])).join('\u0000');
          rows.set(k, { ...rows.get(k), ...o });
        }
      });
      setState({ rows: [...rows.values()], sql, loading: false, failed: rs.every((r) => r.status === 'rejected'), key });
    });
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [company, key]);
  return state.key === key ? state : { rows: [], sql: [], loading: true, failed: false };
}

/** Rows as objects with numbers parsed. */
export function records(a?: Answer): Record<string, unknown>[] {
  if (!a) return [];
  return a.rows.map((r) => {
    const o: Record<string, unknown> = {};
    a.columns.forEach((c, i) => {
      const n = toNumber(r[i]);
      o[c] = n ?? r[i];
    });
    return o;
  });
}

export const num = (v: unknown) => toNumber(v) ?? 0;

export function Empty({ className }: { className?: string }) {
  return <div className={cn('grid min-h-24 place-items-center text-[13px] text-muted-foreground', className)}>暂无数据</div>;
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded-xl bg-muted/70', className)} />;
}

/** A dashboard card: title, an optional figure on the right, a tiny SQL switch, and the chart. */
export function ChartCard({
  title, aside, sql, loading, error, empty, className, bodyClass, children,
}: {
  title: string; aside?: React.ReactNode; sql?: string | string[]; loading?: boolean; error?: string; empty?: boolean;
  className?: string; bodyClass?: string; children?: React.ReactNode;
}) {
  const sqls = (Array.isArray(sql) ? sql : sql ? [sql] : []).filter(Boolean);
  return (
    <Collapsible asChild>
      <section className={cn('flex min-w-0 flex-col rounded-2xl bg-card p-4 shadow-float max-md:p-3.5', className)}>
        <header className="flex min-h-7 items-center gap-2">
          <h3 className="min-w-0 truncate text-[14.5px] font-extrabold">{title}</h3>
          <div className="ml-auto flex shrink-0 items-center gap-2">
            {aside}
            {sqls.length > 0 && (
              <CollapsibleTrigger className="group flex h-6 items-center gap-1 rounded-md px-1.5 text-[11px] font-bold text-muted-foreground/80 outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/40 data-[state=open]:bg-brand-soft data-[state=open]:text-primary">
                <CodeXmlIcon className="size-3.5" />SQL
              </CollapsibleTrigger>
            )}
          </div>
        </header>
        {sqls.length > 0 && (
          <CollapsibleContent>
            {sqls.map((s, i) => <Sql key={i} sql={s} className="mt-2" />)}
          </CollapsibleContent>
        )}
        <div className={cn('mt-3 min-h-0 flex-1', bodyClass)}>
          {error ? <div className="grid min-h-24 place-items-center text-lg font-bold text-muted-foreground/50">—</div>
            : loading ? <Skeleton className="h-full min-h-40" />
              : empty ? <Empty /> : children}
        </div>
      </section>
    </Collapsible>
  );
}

export function Sql({ sql, className }: { sql: string; className?: string }) {
  return (
    <pre className={cn('max-h-64 overflow-auto rounded-xl bg-surface p-3 font-mono text-[11px] leading-relaxed whitespace-pre text-foreground/80', className)}>{sql}</pre>
  );
}

/** A small collapsible SQL line, for places that are not cards. */
export function SqlFold({ sql }: { sql: string }) {
  return (
    <Collapsible>
      <CollapsibleTrigger className="group flex items-center gap-1 rounded-md py-0.5 text-[11.5px] font-bold text-muted-foreground outline-none hover:text-foreground">
        <ChevronRightIcon className="size-3.5 transition-transform group-data-[state=open]:rotate-90" />
        <CodeXmlIcon className="size-3.5" />SQL
      </CollapsibleTrigger>
      <CollapsibleContent><Sql sql={sql} className="mt-1.5" /></CollapsibleContent>
    </Collapsible>
  );
}

/** Query rows as a compact table, labelled and formatted through the model. */
export function ResultTable({ columns, rows, model, max = 10, className }: { columns: string[]; rows: unknown[][]; model: Model; max?: number; className?: string }) {
  if (!rows.length) return <Empty className="min-h-14 rounded-xl bg-surface" />;
  const numeric = columns.map((c, i) => model.isMetric(c) || rows.every((r) => r[i] == null || (toNumber(r[i]) !== null && !model.has(c))));
  return (
    <div className={cn('overflow-hidden rounded-xl border bg-card', className)}>
      <Table className="text-[12.5px]">
        <TableHeader className="bg-surface">
          <TableRow className="hover:bg-transparent">
            {columns.map((c, i) => (
              <TableHead key={i} className={cn('h-8 px-2.5 text-[11.5px] font-bold whitespace-nowrap text-muted-foreground', numeric[i] && 'text-right')}>{model.label(c)}</TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.slice(0, max).map((r, ri) => (
            <TableRow key={ri}>
              {r.map((v, ci) => (
                <TableCell key={ci} className={cn('px-2.5 py-1.5 whitespace-nowrap', numeric[ci] && 'text-right tabular-nums')}>{model.cell(columns[ci], v)}</TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
