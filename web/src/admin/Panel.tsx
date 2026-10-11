// A card on a company's pages: a title, things on its right, and what it holds.
import { cn } from '@/lib/utils';

export function Panel({ title, aside, children, className }: { title?: React.ReactNode; aside?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <section className={cn('rounded-[18px] bg-card p-5 shadow-float max-sm:p-4', className)}>
      {(title || aside) && (
        <div className="mb-4 flex flex-wrap items-center gap-2">
          {title && <h2 className="text-[15px] font-extrabold">{title}</h2>}
          {aside && <div className="ml-auto flex items-center gap-2">{aside}</div>}
        </div>
      )}
      {children}
    </section>
  );
}
