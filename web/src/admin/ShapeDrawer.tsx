// 让 AI 画外形: the administrator says how a machine kind or a product looks, the AI draws it by
// writing a program the server checks against the twin (fits its envelope, keeps the belt
// clear, not too many parts), and the drawing is shown as the twin will build it. Saved into the
// industry pack only when confirmed.
import { lazy, Suspense, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { CheckIcon, CircleAlertIcon, LoaderCircleIcon, ShapesIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import type { IndustryCfg } from '@/config/schema';
import { stageOrder } from '@/core/layout';
import * as api from './api';
import { useConsole } from './Console';
import { shapeLines, type ShapeStep } from './steps';

const ShapePreview = lazy(() => import('./ShapePreview'));

export interface ShapeTarget { target: 'machine' | 'product'; key: string }

export default function ShapeDrawer({ cfg, what, onClose }: { cfg: IndustryCfg; what: ShapeTarget | null; onClose(): void }) {
  const { reload } = useConsole();
  const [brief, setBrief] = useState('');
  const [stage, setStage] = useState('');
  const [attempts, setAttempts] = useState<ShapeStep[]>([]);
  const [drawing, setDrawing] = useState<api.ShapeDrawing | null>(null);
  const [answer, setAnswer] = useState('');
  const [error, setError] = useState<string>();
  const [ctrl, setCtrl] = useState<AbortController | null>(null);
  const [saving, setSaving] = useState(false);
  const stages = what?.target === 'product' ? stageOrder(cfg, what.key) : [];
  useEffect(() => {
    if (what) {
      setBrief(''); setAttempts([]); setDrawing(null); setAnswer(''); setError(undefined);
      setStage(what.target === 'product' ? stageOrder(cfg, what.key).at(-1) ?? '' : '');
    } else ctrl?.abort();
  }, [what]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!what) return null;
  const machine = what.target === 'machine';
  const name = machine ? cfg.machines[what.key]?.label : cfg.products[what.key]?.name;

  const run = async () => {
    if (!brief.trim() || ctrl) return;
    const c = new AbortController();
    setCtrl(c);
    setAttempts([]); setDrawing(null); setAnswer(''); setError(undefined);
    try {
      await api.drawShape({ industry: cfg.id, target: what.target, key: what.key, ...(machine ? {} : { stage }), brief: brief.trim() }, c.signal, (ev) => {
        switch (ev.kind) {
          case 'attempt': setAttempts((x) => [...x, ev.result]); break;
          case 'shape': setDrawing(ev.result); break;
          case 'complete': if (ev.text) setAnswer(ev.text); break;
          case 'error': setError(ev.text); break;
        }
      });
    } catch (e) {
      if (!c.signal.aborted) setError((e as Error).message);
    } finally {
      setCtrl(null);
    }
  };
  const save = async () => {
    if (!drawing) return;
    setSaving(true);
    try {
      await api.saveShape(cfg.id, {
        target: what.target, key: what.key, parts: drawing.drawing.parts,
        ...(machine ? { size: drawing.drawing.size } : { stage }),
      });
      await reload();
      toast.success(`${name} 的外形已保存到 ${cfg.name}`);
      onClose();
    } catch (e) {
      const err = e as api.ApiError;
      setError(err.problems?.length ? err.problems.join('；') : err.message);
    } finally {
      setSaving(false);
    }
  };
  const d = drawing?.drawing;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[94vh] overflow-y-auto rounded-[20px] sm:max-w-[760px]">
        <DialogHeader>
          <DialogTitle className="text-[17px] font-extrabold">让 AI 画外形：{name}</DialogTitle>
          <DialogDescription>
            {machine ? '说这台设备长什么样。画好先给你看，放得进它的位置、不挡传送带才算画好；保存后用这个行业包的工厂按新外形重建。'
              : '说这个产品长什么样。画好先给你看，保存后线上的每一件都按这个样子画。'}
          </DialogDescription>
        </DialogHeader>
        {stages.length > 1 && (
          <div className="flex flex-wrap items-center gap-1.5 text-[12.5px]">
            <span className="text-muted-foreground">画哪个工序状态：</span>
            {stages.map((s) => (
              <button key={s} type="button" disabled={!!ctrl} onClick={() => setStage(s)}
                className={cn('rounded-lg px-2 py-1 font-mono font-bold', s === stage ? 'bg-primary text-primary-foreground' : 'bg-surface hover:bg-surface/70')}>{s}</button>
            ))}
          </div>
        )}
        <Textarea value={brief} onChange={(e) => setBrief(e.target.value)} rows={3} disabled={!!ctrl}
          onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); run(); } }}
          placeholder={machine ? '带顶冠的开式冲床，C 形机身，左边有飞轮' : '一根四拐曲轴，平放，两头有法兰'}
          className="min-h-[76px] resize-none rounded-xl text-[14px] leading-relaxed" aria-label="外形描述" />
        {(attempts.length > 0 || ctrl || answer || error) && (
          <div className="grid gap-2 rounded-xl bg-surface/60 px-4 py-3">
            <ol className="grid gap-1">
              {shapeLines(attempts, !!ctrl).map((st, i) => (
                <li key={i} className="flex items-start gap-2 text-[12.5px]">
                  {st.state === 'doing' ? <LoaderCircleIcon className="mt-0.5 size-3.5 shrink-0 animate-spin text-primary" />
                    : st.state === 'failed' ? <CircleAlertIcon className="mt-0.5 size-3.5 shrink-0 text-destructive" /> : <CheckIcon className="mt-0.5 size-3.5 shrink-0 text-success" />}
                  <span className="min-w-0">
                    <span className={cn('font-bold', st.state === 'failed' && 'text-destructive')}>{st.text}</span>
                    {st.detail && <span className="block whitespace-pre-line text-muted-foreground">{st.detail}</span>}
                  </span>
                </li>
              ))}
            </ol>
            {answer && <p className="text-[13.5px] leading-relaxed whitespace-pre-line">{answer}</p>}
            {error && <p className="rounded-xl bg-danger-soft px-3 py-2 text-[13px] text-destructive">{error}</p>}
          </div>
        )}
        {d && (
          <div className="grid gap-1.5">
            <Suspense fallback={<div className="h-[340px] rounded-xl bg-surface" />}>
              <ShapePreview parts={d.parts} spec={drawing!.spec} color={machine ? cfg.machines[what.key]?.look.color : undefined}
                className="h-[340px] overflow-hidden rounded-xl border" />
            </Suspense>
            <p className="text-[12px] text-muted-foreground">
              {d.parts.length} 个零件，{machine ? `占地 ${d.size[0]} × ${d.size[1]} 米，高 ${d.size[2]} 米；蓝色是传送带要留出的通道` : `长 ${d.size[0]}、宽 ${d.size[1]}、高 ${d.size[2]} 米`}
              {d.attempts > 1 ? `，改了 ${d.attempts - 1} 次` : ''}。拖动可以转着看。
            </p>
          </div>
        )}
        <DialogFooter>
          <Button variant="ghost" className="rounded-xl" onClick={onClose}>{d ? '不要' : '取消'}</Button>
          {d && !ctrl && <Button variant="outline" className="rounded-xl font-bold" disabled={!brief.trim()} onClick={run}><ShapesIcon />再画一次</Button>}
          {d && !ctrl
            ? <Button className="rounded-xl font-bold" disabled={saving} onClick={save}>{saving && <LoaderCircleIcon className="animate-spin" />}保存到行业包</Button>
            : <Button className="rounded-xl font-bold" disabled={!brief.trim() || !!ctrl} onClick={run}>{ctrl ? <LoaderCircleIcon className="animate-spin" /> : <ShapesIcon />}让 AI 画</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
