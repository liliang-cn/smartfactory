// Whether the business view is open, and where in it the viewer is.
import { create } from 'zustand';

export type BizTab = 'data' | 'ask' | 'board';
export type BizSection = 'prod' | 'quality' | 'sales' | 'logistics' | 'purchase' | 'cost';

interface BizState {
  open: boolean;
  tab: BizTab;
  section: BizSection;
  /** plant name as the history database spells it (一厂), '' for every plant */
  plant: string;
  days: 7 | 30;
  setOpen(open: boolean): void;
  setTab(tab: BizTab): void;
  setSection(s: BizSection): void;
  setPlant(p: string): void;
  setDays(d: 7 | 30): void;
}

const remembered = (() => {
  try { return JSON.parse(localStorage.getItem('sf-biz') ?? '{}') as Partial<BizState>; } catch { return {}; }
})();

export const useBiz = create<BizState>()((set) => ({
  open: new URLSearchParams(location.search).get('view') === 'biz',
  tab: remembered.tab ?? 'data',
  section: remembered.section ?? 'prod',
  plant: '',
  days: 30,
  setOpen(open) {
    set({ open });
    const url = new URL(location.href);
    if (open) url.searchParams.set('view', 'biz');
    else url.searchParams.delete('view');
    history.replaceState(null, '', url);
  },
  setTab: (tab) => set({ tab }),
  setSection: (section) => set({ section }),
  setPlant: (plant) => set({ plant }),
  setDays: (days) => set({ days }),
}));

useBiz.subscribe((s) => {
  try { localStorage.setItem('sf-biz', JSON.stringify({ tab: s.tab, section: s.section })); } catch { /* storage blocked */ }
});
