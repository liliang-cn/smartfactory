// 修改这里: for administrators, a link from a part of the twin (a machine, a line, an area) to the
// console's editor of just that part.
import { PencilIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useLive } from '@/live/client';
import { runtime } from '@/state/store';

export interface EditNode { kind: 'plant' | 'area' | 'line' | 'machine'; plant: string; area?: string; line?: string; asset?: string }

export function EditHere({ node, label = '修改这里', className }: { node: EditNode; label?: string; className?: string }) {
  const role = useLive((s) => s.role);
  if (role !== 'admin') return null;
  const company = runtime().bundle.company.id;
  const q = new URLSearchParams(Object.entries(node).filter(([, v]) => v) as [string, string][]);
  return (
    <a href={`/admin/customers/${encodeURIComponent(company)}/scope?${q}`} title="在后台只改这一处"
      className={cn('inline-flex h-7 shrink-0 items-center gap-1 rounded-lg px-2 text-[12px] font-bold text-primary hover:bg-brand-soft', className)}>
      <PencilIcon className="size-3.5" />{label}
    </a>
  );
}
