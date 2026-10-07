import { lazy, Suspense, useEffect, useRef } from 'react';
import { toast } from 'sonner';
import { Toaster } from '@/components/ui/sonner';
import { TooltipProvider } from '@/components/ui/tooltip';
import { CampusScene } from '@/scene/CampusScene';
import { Scene } from '@/scene/Scene';
import { runtime, useCampus, usePlant, useTick, useUI } from '@/state/store';
import { Inspector } from '@/ui/Inspector';
import { Labels } from '@/ui/Labels';
import { GroupKpis, HoverTip, Kpis, MapControls, ViewSwitch } from '@/ui/Overlay';
import { Fleet, GroupFleet, Tracker } from '@/ui/Panels';
import { TopBar } from '@/ui/TopBar';
import { useBiz } from '@/advisor/store';

const Business = lazy(() => import('@/advisor/Business'));

/** The business view loads its charts and markdown only when first opened. */
function BusinessView() {
  const open = useBiz((s) => s.open);
  if (!open) return null;
  return <Suspense fallback={null}><Business /></Suspense>;
}

// Raise a toast for every new fault in any plant of the company; the action jumps there.
function FaultToasts() {
  useTick();
  const current = usePlant();
  const campus = useCampus();
  const seen = useRef(new WeakMap<object, number>());
  useEffect(() => {
    for (const p of runtime().plants) {
      const { sim } = p;
      // A browser following the server has no alarms of its own until the first frame.
      if (!sim.synced) continue;
      const last = seen.current.get(sim) ?? sim.alarms[0]?.id ?? 0;
      for (const al of sim.alarms) {
        if (al.id <= last) break;
        if (al.prio !== 1) continue;
        const elsewhere = campus || p !== current;
        toast.error(`${elsewhere ? `${p.layout.name} ` : ''}${al.asset}：${al.text}`, {
          duration: 5000,
          action: {
            label: '查看',
            onClick: () => {
              const ui = useUI.getState();
              if (elsewhere) ui.setPlant(p.layout.id);
              // Give the scene a moment to mount the plant before framing the machine.
              setTimeout(() => useUI.getState().select(al.asset, true), elsewhere ? 400 : 0);
            },
          },
        });
      }
      seen.current.set(sim, sim.alarms[0]?.id ?? last);
    }
  });
  return null;
}

function useShortcuts() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest('input, textarea, [role="dialog"]')) return;
      const s = useUI.getState();
      if (e.key === 'Escape') s.select(null);
      else if (e.key === ' ') { e.preventDefault(); s.setTimeScale(s.timeScale === 0 ? 1 : 0); }
      else if (e.key === 'f' || e.key === 'F') { if (s.selected) s.camera('focus', s.selected); }
      else if (e.key === 'h' || e.key === 'H') s.camera('home');
      else if (e.key === 'l' || e.key === 'L') s.toggleLabels();
      else if (e.key === '1') s.setMode('live');
      else if (e.key === '2') s.setMode('heat');
      else if (e.key === '3') s.setMode('util');
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
}

export default function App() {
  useShortcuts();
  const plant = usePlant();
  const campus = useCampus();
  return (
    <TooltipProvider delayDuration={300}>
      <div className="fixed inset-0 overflow-hidden bg-stage">
        {campus ? <CampusScene /> : <Scene key={plant.layout.id} plant={plant} />}
        {!campus && <Labels key={`labels-${plant.layout.id}`} />}
        <TopBar />
        {campus ? <GroupKpis /> : <Kpis />}
        <ViewSwitch />
        <MapControls />
        {!campus && <Tracker />}
        {campus ? <GroupFleet /> : <Fleet />}
        <Inspector />
        <HoverTip />
        <BusinessView />
        <FaultToasts />
        <Toaster theme="light" position="top-center" offset={{ top: 84 }} mobileOffset={{ top: 'calc(176px + env(safe-area-inset-top))' }} />
      </div>
    </TooltipProvider>
  );
}
