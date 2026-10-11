// How the audit log reads: each entry as a sentence, where it came from, the day it falls on.
// Shared by the twin's activity sheet and the console.
import type { z } from 'zod';
import { BotIcon, MousePointerClickIcon, PlugIcon, RadioTowerIcon, type LucideIcon } from 'lucide-react';
import { CMD_LABEL } from '@/live/client';
import type * as P from '@/protocol/sfp';
import type { Command } from '@/sim/types';

export type Entry = z.infer<typeof P.AuditEntry>;
export type Via = Entry['via'];

export const VIA: Record<Via, { label: string; icon: LucideIcon }> = {
  page: { label: '页面', icon: MousePointerClickIcon },
  live: { label: '实时', icon: RadioTowerIcon },
  assistant: { label: '助手', icon: BotIcon },
  mcp: { label: 'MCP', icon: PlugIcon },
};
export const ROLE: Record<Entry['role'], string> = { visitor: '访客', member: '成员', operator: '操作员', admin: '管理员' };
const STATUS_ADVISOR: Record<string, string> = {
  adopt: '采纳计划', reject: '否决计划', measure: '开始验收', accept: '验收通过', review: '运行例行诊断',
};

/** "id title" as the server keeps a target: the title, or the id when there is none. */
const titleOf = (target: string) => {
  const i = target.indexOf(' ');
  return i < 0 ? target : target.slice(i + 1);
};
const idOf = (target: string) => target.split(' ')[0];

function commandText(detail: string) {
  const [cmd, arg] = detail.split(' ') as [Command, string | undefined];
  const label = CMD_LABEL[cmd] ?? cmd;
  if (arg == null) return label;
  const pct = `${Math.round(Number(arg) * 100)}%`;
  return cmd === 'speed' || cmd === 'toollimit' ? `${label} ${pct}` : `${label} ${arg}`;
}

/** What an entry did, as a sentence, and the machine it touched if any. */
export function describe(e: Entry): { text: string; asset?: string; plant?: string } {
  switch (e.action) {
    case 'command': {
      const [plant, asset] = e.target.split('/');
      return { text: commandText(e.detail ?? ''), plant, asset };
    }
    case 'clock': return { text: `全厂倍速改为 ${e.detail}` };
    case 'save_note': return { text: `记笔记「${titleOf(e.target)}」` };
    case 'update_note': return { text: `更新笔记「${titleOf(e.target)}」` };
    case 'create_ticket': return { text: `登记工单 ${idOf(e.target)}「${titleOf(e.target)}」` };
    case 'update_ticket': return { text: `更新工单 ${idOf(e.target)}` };
    case 'ticket.status': return { text: `工单 ${idOf(e.target)} 改为「${e.detail}」` };
    case 'doc.add': return { text: `上传资料「${titleOf(e.target)}」` };
    case 'doc.delete': return { text: `删除资料 ${e.target}` };
    case 'note.delete': return { text: `删除笔记 ${e.target}` };
    case 'assistant.create': return { text: `新建助手「${titleOf(e.target)}」` };
    case 'assistant.update': return { text: `修改助手「${titleOf(e.target)}」` };
    case 'assistant.delete': return { text: `删除助手 ${e.target}` };
    case 'company.create': return { text: `建立或替换公司 ${e.target}` };
    case 'company.delete': return { text: `删除公司 ${e.target}` };
    case 'industry.create': return { text: `建立或替换行业包 ${e.target}` };
    case 'token.issue': return { text: `发令牌给${titleOf(e.target)}（${ROLE[e.detail as Entry['role']] ?? e.detail}）` };
    case 'token.revoke': return { text: `撤销${titleOf(e.target)}的令牌` };
  }
  if (e.action.startsWith('advisor.')) {
    const verb = e.action.slice('advisor.'.length);
    return { text: `${STATUS_ADVISOR[verb] ?? verb}${e.target ? ` ${e.target}` : ''}` };
  }
  return { text: [e.action, e.target, e.detail].filter(Boolean).join(' ') };
}

export function dayLabel(d: Date) {
  const today = new Date();
  const y = new Date(today);
  y.setDate(today.getDate() - 1);
  const same = (a: Date, b: Date) => a.toDateString() === b.toDateString();
  if (same(d, today)) return '今天';
  if (same(d, y)) return '昨天';
  return d.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'short', ...(d.getFullYear() !== today.getFullYear() && { year: 'numeric' }) });
}
