// A company's pages: the sidebar lists them in these groups, the company page shows the one picked.
import {
  BookOpenTextIcon, BotIcon, BoxIcon, ClipboardCheckIcon, ClipboardListIcon, KeyRoundIcon, PlugZapIcon, ScrollTextIcon, ShieldCheckIcon,
  SlidersHorizontalIcon, TrendingUpIcon,
} from 'lucide-react';

export const SECTIONS = [
  { group: '生产', tabs: [
    { id: 'scene', label: '场景', icon: BoxIcon },
    { id: 'orders', label: '工单', icon: ClipboardListIcon },
    { id: 'checks', label: '点检', icon: ClipboardCheckIcon },
    { id: 'guard', label: '指令', icon: ShieldCheckIcon },
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
  ] },
] as const;

/** Where a company's page is: the console's home on a one-company server, its own page otherwise. */
export const tabHref = (company: string, home: boolean, tab: string) =>
  `${home ? '/admin' : `/admin/customers/${encodeURIComponent(company)}`}${tab === 'scene' ? '' : `?tab=${tab}`}`;
