// What the AI is doing while it writes a factory or an industry pack, said the way a person would:
// "正在读取…" while a step runs, "已读取…" once it is done.

export interface Step { tool: string; args?: Record<string, unknown>; done: boolean; failed?: string }

const TEXT: Record<string, { doing: string; done: (n: number) => string; failed?: string }> = {
  list_companies: { doing: '正在查看现有的工厂', done: () => '已查看现有的工厂' },
  list_industries: { doing: '正在查看行业库', done: () => '已查看行业库' },
  get_industry: { doing: '正在读取行业的设备和工艺', done: (n) => (n > 1 ? `已读取 ${n} 个行业的设备和工艺` : '已读取行业的设备和工艺') },
  get_company: { doing: '正在读取参考配置', done: (n) => (n > 1 ? `已读取 ${n} 份参考配置` : '已读取参考配置') },
  propose_company: { doing: '正在检查工厂配置', done: () => '工厂配置检查通过，已填入下面的表单', failed: '工厂配置检查没通过' },
  propose_industry: { doing: '正在检查设备和工艺', done: () => '设备和工艺检查通过，建厂时一起使用', failed: '设备和工艺检查没通过' },
};

/** One line per step; finished steps of the same kind in a row become one line with a count. */
export function stepLines(steps: Step[]): { text: string; state: 'doing' | 'done' | 'failed'; detail?: string }[] {
  const out: { text: string; state: 'doing' | 'done' | 'failed'; detail?: string; tool: string; n: number }[] = [];
  for (const st of steps) {
    const t = TEXT[st.tool];
    const state = !st.done ? 'doing' : st.failed ? 'failed' : 'done';
    const last = out[out.length - 1];
    if (state === 'done' && last && last.state === 'done' && last.tool === st.tool) {
      last.n++;
      last.text = t ? t.done(last.n) : st.tool;
      continue;
    }
    const text = !t ? st.tool : state === 'doing' ? t.doing : state === 'failed' ? (t.failed ?? `${t.doing}，出错了`) : t.done(1);
    out.push({ text, state, tool: st.tool, n: 1, detail: st.failed?.replace(/^没通过：\n?/, '').slice(0, 300) });
  }
  return out;
}

export const THINKING = '正在思考…';
