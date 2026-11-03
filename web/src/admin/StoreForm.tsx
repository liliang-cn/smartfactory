// Small pieces the stores, dies and development pages build their forms from.
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { cn } from '@/lib/utils';

export function Field({ label, hint, children, wide }: { label: string; hint?: string; children: React.ReactNode; wide?: boolean }) {
  return (
    <label className={cn('grid gap-1 text-[12.5px] font-bold', wide && 'col-span-2 max-sm:col-span-1')}>
      {label}
      {children}
      {hint && <small className="font-normal text-muted-foreground">{hint}</small>}
    </label>
  );
}

export function Text({ value, onChange, placeholder, type }: { value: string | number | undefined; onChange(v: string): void; placeholder?: string; type?: string }) {
  return <Input className="h-9 rounded-lg" value={value ?? ''} type={type} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />;
}

export function Pick({ value, onChange, options, placeholder }: { value: string; onChange(v: string): void; options: [string, string][]; placeholder?: string }) {
  return (
    <Select value={value || undefined} onValueChange={onChange}>
      <SelectTrigger className="h-9 w-full rounded-lg"><SelectValue placeholder={placeholder ?? '选一个'} /></SelectTrigger>
      <SelectContent>{options.map(([v, l]) => <SelectItem key={v} value={v}>{l}</SelectItem>)}</SelectContent>
    </Select>
  );
}

/** A dialog with a form: its fields in two columns (one on a phone), what went wrong, and a save button. */
export function FormDialog({ open, onOpenChange, title, problems, busy, onSave, save, children }: {
  open: boolean; onOpenChange(v: boolean): void; title: string; problems: string[]; busy: boolean; onSave(): void; save: string; children: React.ReactNode;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader><DialogTitle>{title}</DialogTitle></DialogHeader>
        <form className="grid grid-cols-2 gap-3 max-sm:grid-cols-1" onSubmit={(e) => { e.preventDefault(); onSave(); }}>
          {children}
          {problems.length > 0 && (
            <ul className="col-span-2 grid gap-1 rounded-xl bg-danger-soft px-3 py-2 text-[12.5px] text-destructive max-sm:col-span-1">{problems.map((p, i) => <li key={i}>{p}</li>)}</ul>
          )}
          <DialogFooter className="col-span-2 max-sm:col-span-1">
            <Button type="submit" disabled={busy} className="h-9 rounded-lg font-bold">{save}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** What a page says when there is nothing yet, and what to do first. */
export function Empty({ title, children }: { title: string; children?: React.ReactNode }) {
  return (
    <div className="grid justify-items-center gap-2 rounded-xl border-2 border-dashed px-4 py-8 text-center">
      <b className="text-[14px]">{title}</b>
      {children && <div className="max-w-[460px] text-[12.5px] text-muted-foreground">{children}</div>}
    </div>
  );
}

/** A bar of what is left of something (a die's life), green to red as it runs out. */
export function LifeBar({ share }: { share: number }) {
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-[#e2e7f4]">
      <i className={cn('block h-full rounded-full', share >= 1 ? 'bg-destructive' : share >= 0.9 ? 'bg-amber-500' : 'bg-success')} style={{ width: `${Math.max(0, Math.min(1, 1 - share)) * 100}%` }} />
    </div>
  );
}
