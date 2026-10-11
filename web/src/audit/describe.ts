// How the audit log reads: each entry as a sentence, where it came from, the day it falls on.
// Shared by the twin's activity sheet and the console.
import type { z } from 'zod';
import { BotIcon, MousePointerClickIcon, PlugIcon, RadioTowerIcon, ShieldCheckIcon, SquareTerminalIcon, UnplugIcon, type LucideIcon } from 'lucide-react';
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
  cli: { label: '命令行', icon: SquareTerminalIcon },
  api: { label: '接口', icon: UnplugIcon },
  guard: { label: '指令把关', icon: ShieldCheckIcon },
};
export const ROLE: Record<Entry['role'], string> = { visitor: '访客', member: '成员', operator: '操作员', admin: '管理员', erp: 'ERP 接口' };
/** How each guard level reads. */
export const GUARD_LEVEL: Record<string, string> = { advisory: '只提议', guarded: '有护栏', autonomous: '自主' };
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
    case 'command.refused': case 'command.pending': case 'command.approved': case 'command.denied': case 'command.expired': {
      const [where, id] = e.target.split(' ');
      const [plant, asset] = where.split('/');
      const what = `「${commandText(e.detail ?? '')}」`;
      const text = {
        'command.refused': `按规则没有执行${what}`,
        'command.pending': `提出${what}，等操作员批准${id ? `（${id}）` : ''}`,
        'command.approved': e.ok ? `批准并执行${what}${id ? `（${id}）` : ''}` : `批准${what}，但没有执行`,
        'command.denied': `拒绝${what}${id ? `（${id}）` : ''}`,
        'command.expired': `${what}没人批准，过期作废${id ? `（${id}）` : ''}`,
      }[e.action]!;
      return { text, plant, asset };
    }
    case 'guard.config': return { text: `把指令把关设为「${GUARD_LEVEL[e.detail ?? ''] ?? e.detail}」` };
    case 'clock': return { text: `全厂倍速改为 ${e.detail}` };
    case 'save_note': return { text: `记笔记「${titleOf(e.target)}」` };
    case 'update_note': return { text: `更新笔记「${titleOf(e.target)}」` };
    case 'create_ticket': return { text: `登记工单 ${idOf(e.target)}「${titleOf(e.target)}」` };
    case 'update_ticket': return { text: `更新工单 ${idOf(e.target)}` };
    case 'ticket.status': return { text: `工单 ${idOf(e.target)} 改为「${e.detail}」` };
    case 'doc.add': return { text: `上传资料「${titleOf(e.target)}」` };
    case 'doc.update': return { text: `更新资料「${titleOf(e.target)}」${e.detail ? `（${e.detail}）` : ''}` };
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
    case 'knowledge.rebuild': return { text: `重建 ${e.target} 的知识库` };
    case 'erp.config': return { text: `设置 ERP 对接：${e.detail}` };
    case 'erp.secret': return { text: '换 ERP 回调的签名密钥' };
    case 'erp.pull': return { text: `从 ERP 拉取工单：${e.detail}` };
    case 'erp.orders': return { text: `收到工单 ${e.target}（${e.detail}）` };
    case 'erp.cancel': return { text: `撤回工单 ${e.target}` };
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
