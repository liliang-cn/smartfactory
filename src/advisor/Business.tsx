// 经营: the company's business view over the twin — figures, the advisor agent, the consulting record.
import { BarChart3Icon, ClipboardCheckIcon, SparklesIcon, XIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { runtime } from '@/state/store';
import { Board } from './Board';
import { Chat, useAsking } from './Chat';
import { Dashboard, Filters, SECTIONS, Seg } from './Dashboard';
import { useModel } from './model';
import { useBiz, type BizTab } from './store';

const TABS: [BizTab, string, React.ReactNode][] = [
  ['data', '经营数据', <BarChart3Icon key="d" />],
  ['ask', '顾问', <SparklesIcon key="a" />],
  ['board', '咨询档案', <ClipboardCheckIcon key="b" />],
];

export default function Business() {
  const open = useBiz((s) => s.open);
  const tab = useBiz((s) => s.tab);
  const section = useBiz((s) => s.section);
  const company = runtime().bundle.company.id;
  const model = useModel(company);
  const asking = useAsking();
  if (!open) return null;
  return (
    <div className="absolute inset-x-0 top-16 bottom-0 z-[25] flex flex-col bg-stage max-md:top-[calc(54px+env(safe-area-inset-top))]">
      <div data-biz-scroll className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <div className="sticky top-0 z-20 bg-stage/90 backdrop-blur-md">
          <div className="mx-auto flex max-w-[1480px] flex-wrap items-center gap-2.5 px-5 pt-4 pb-3 max-md:gap-2 max-md:px-2.5 max-md:pt-2.5 max-md:pb-2">
            <div role="tablist" className="flex gap-1 rounded-[14px] bg-card p-1 shadow-float max-md:flex-1">
              {TABS.map(([id, label, icon]) => (
                <button key={id} type="button" role="tab" aria-selected={tab === id} onClick={() => useBiz.getState().setTab(id)}
                  className={cn('relative flex h-9 items-center gap-1.5 rounded-[10px] px-3.5 text-[13.5px] font-extrabold text-muted-foreground transition-colors hover:text-foreground max-md:h-[34px] max-md:flex-1 max-md:justify-center max-md:px-2 max-md:text-[13px] [&>svg]:size-4',
                    tab === id && 'bg-primary text-primary-foreground shadow-sm hover:text-primary-foreground')}>
                  {icon}{label}
                  {id === 'ask' && asking && <span className={cn('absolute top-1.5 right-1.5 size-2 animate-pulse rounded-full', tab === id ? 'bg-white' : 'bg-primary')} />}
                </button>
              ))}
            </div>
            {tab === 'data' && (
              <div className="order-last flex w-full min-w-0 flex-wrap items-center gap-2 md:order-none md:ml-auto md:w-auto md:flex-nowrap max-md:gap-1.5">
                <Seg value={section} items={SECTIONS} onChange={(v) => useBiz.getState().setSection(v)} className="min-w-0 overflow-x-auto [scrollbar-width:none] max-md:w-full" />
                <Filters className="max-md:hidden" />
              </div>
            )}
            <Button variant="outline" size="icon" className={cn('size-10 shrink-0 rounded-xl bg-card shadow-float max-md:size-[42px]', tab !== 'data' && 'ml-auto')} aria-label="返回现场" title="返回现场"
              onClick={() => useBiz.getState().setOpen(false)}>
              <XIcon className="size-[18px]" />
            </Button>
          </div>
        </div>
        <div className={cn('mx-auto max-w-[1480px] px-5 pb-6 max-md:px-2.5 max-md:pb-4', tab === 'ask' && 'pb-0 max-md:pb-0')}>
          {tab === 'data' && <Dashboard company={company} model={model} />}
          {tab === 'ask' && <Chat company={company} model={model} />}
          {tab === 'board' && <Board company={company} model={model} />}
        </div>
      </div>
    </div>
  );
}
