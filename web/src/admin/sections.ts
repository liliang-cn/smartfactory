// A company's pages: the sidebar lists them in these groups, the company page shows the one picked.
import {
  BookOpenTextIcon, BotIcon, BoxIcon, CalendarRangeIcon, ClipboardCheckIcon, ClipboardListIcon, KeyRoundIcon, PlugZapIcon, ScrollTextIcon, ShieldCheckIcon,
  FlaskConicalIcon, GaugeIcon, HammerIcon, ListTreeIcon, SlidersHorizontalIcon, TrendingUpIcon, WarehouseIcon,
} from 'lucide-react';

export const SECTIONS = [
  { group: '生产', tabs: [
    { id: 'scene', label: '场景', icon: BoxIcon },
    { id: 'orders', label: '工单', icon: ClipboardListIcon },
    { id: 'plan', label: '生产计划', icon: CalendarRangeIcon },
    { id: 'checks', label: '点检', icon: ClipboardCheckIcon },
    { id: 'guard', label: '指令', icon: ShieldCheckIcon },
  ] },
  { group: '物料与研发', tabs: [
    { id: 'bom', label: 'BOM', icon: ListTreeIcon },
    { id: 'stores', label: '库房', icon: WarehouseIcon },
    { id: 'dies', label: '模具', icon: HammerIcon },
    { id: 'rnd', label: '研发', icon: FlaskConicalIcon },
  ] },
  { group: '数据', tabs: [
    { id: 'connect', label: '接入', icon: PlugZapIcon },
    { id: 'calib', label: '校准', icon: SlidersHorizontalIcon },
    { id: 'value', label: '成效', icon: TrendingUpIcon },
  ] },
  { group: 'AI', tabs: [
    { id: 'ai', label: '助手', icon: BotIcon },
    { id: 'knowledge', label: '知识', icon: BookOpenTextIcon },
  ] },
  { group: '管理', tabs: [
    { id: 'accounts', label: '账号', icon: KeyRoundIcon },
    { id: 'activity', label: '操作记录', icon: ScrollTextIcon },
    { id: 'health', label: '运行状况', icon: GaugeIcon },
  ] },
] as const;

/** Where a company's page is: the console's home on a one-company server, its own page otherwise. */
export const tabHref = (company: string, home: boolean, tab: string) =>
  `${home ? '/admin' : `/admin/customers/${encodeURIComponent(company)}`}${tab === 'scene' ? '' : `?tab=${tab}`}`;
