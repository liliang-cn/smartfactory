// A measured machine's stored readings over the last hour, day, week or month (GET /api/series),
// drawn like the live trend: the line (an average with its min–max band once the range is in
// minutes or hours), and flagged readings in red.
import { useEffect, useRef, useState } from 'react';
import type { z } from 'zod';
import { operatorToken } from '@/live/client';
import type * as P from '@/protocol/sfp';

type Answer = z.infer<typeof P.SeriesAnswer>;

export const RANGES = [
  { id: 'live', label: '实时', ms: 0 },
  { id: '1h', label: '1 小时', ms: 3600_000 },
  { id: '24h', label: '24 小时', ms: 86400_000 },
  { id: '7d', label: '7 天', ms: 7 * 86400_000 },
  { id: '30d', label: '30 天', ms: 30 * 86400_000 },
] as const;
export type Range = (typeof RANGES)[number]['id'];

export interface Summary { n: number; flagged: number; resolution: Answer['resolution'] }

type State = { status: 'loading' } | { status: 'error'; text: string } | { status: 'ok'; answer: Answer };

async function load(company: string, plant: string, asset: string, field: string, ms: number): Promise<Answer> {
  const t = operatorToken();
  const res = await fetch(`/api/series/${encodeURIComponent(company)}/${encodeURIComponent(plant)}/${encodeURIComponent(asset)}?field=${encodeURIComponent(field)}&from=${Date.now() - ms}`, {
    headers: t ? { Authorization: `Bearer ${t}` } : {},
  });
  if (res.status === 401 || res.status === 403) throw new Error('看历史要这家公司成员以上的令牌');
  if (res.status === 503) throw new Error('这台服务器不保存设备数据');
  if (!res.ok) throw new Error(`读取失败（${res.status}）`);
  return res.json();
}

export function HistoryChart({ company, plant, asset, field, range, lines, onSummary }: {
  company: string; plant: string; asset: string; field: string; range: Range;
  /** reference lines: [value, colour] */
  lines: [number, string][];
  onSummary?(s: Summary | null): void;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const [state, setState] = useState<State>({ status: 'loading' });
  const ms = RANGES.find((r) => r.id === range)!.ms;

  useEffect(() => {
    let live = true;
    const get = () => load(company, plant, asset, field, ms).then(
      (answer) => live && setState({ status: 'ok', answer }),
      (e: Error) => live && setState({ status: 'error', text: e.message }),
    );
    setState({ status: 'loading' });
    get();
    // the last hour moves; longer ranges barely do
    const every = setInterval(get, ms <= 3600_000 ? 15_000 : 120_000);
    return () => { live = false; clearInterval(every); };
  }, [company, plant, asset, field, ms]);

  useEffect(() => {
    if (state.status !== 'ok') { onSummary?.(null); return; }
    const a = state.answer;
    const pts = a.series[0]?.points ?? [];
    const raw = a.resolution === 'raw';
    onSummary?.({
      resolution: a.resolution,
      n: raw ? pts.length : pts.reduce((s, p) => s + (p[4] as number), 0),
      flagged: raw ? pts.filter((p) => p[2] !== 0).length : pts.reduce((s, p) => s + ((p[5] as number) ?? 0), 0),
    });
  }, [state, onSummary]);

  useEffect(() => {
    const cv = ref.current;
    if (!cv) return;
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    const W = cv.clientWidth;
    const H = cv.clientHeight;
    if (!W) return;
    if (cv.width !== W * dpr) { cv.width = W * dpr; cv.height = H * dpr; }
    const g = cv.getContext('2d')!;
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, W, H);
    const say = (text: string) => {
      g.fillStyle = '#8f97b2';
      g.font = '12px "Plus Jakarta Sans Variable", sans-serif';
      g.fillText(text, 4, H / 2);
    };
    if (state.status === 'loading') return say('读取中');
    if (state.status === 'error') return say(state.text);
    const a = state.answer;
    const pts = a.series[0]?.points ?? [];
    if (!pts.length) return say('这段时间没有收到数据');
    const raw = a.resolution === 'raw';
    // the scale is set by the believable readings; flagged ones are pinned to the edge
    const vals: number[] = [];
    for (const p of pts) {
      if (raw) { if (p[2] === 0) vals.push(p[1] as number); } else if (p[2] !== null) vals.push(p[2] as number, p[3] as number);
    }
    for (const [v] of lines) vals.push(v);
    if (!vals.length) vals.push(0);
    let lo = Math.min(...vals);
    let hi = Math.max(...vals);
    const pad = (hi - lo) * 0.12 || 1;
    lo -= pad;
    hi += pad;
    const X = (t: number) => ((t - a.from) / (a.to - a.from)) * (W - 6) + 3;
    const Y = (v: number) => Math.max(3, Math.min(H - 3, H - 4 - ((v - lo) / (hi - lo)) * (H - 8)));

    g.lineWidth = 1;
    g.setLineDash([4, 4]);
    for (const [v, c] of lines) {
      g.strokeStyle = c;
      g.beginPath();
      g.moveTo(0, Y(v));
      g.lineTo(W, Y(v));
      g.stroke();
    }
    g.setLineDash([]);

    // a gap of more than a few usual spacings is a gap in the data, not a straight line
    const ts = pts.map((p) => p[0] as number);
    const steps = ts.slice(1).map((t, i) => t - ts[i]).sort((x, y) => x - y);
    const usual = steps[Math.floor(steps.length / 2)] ?? 0;
    const gap = (i: number) => i > 0 && usual > 0 && ts[i] - ts[i - 1] > usual * 5;

    if (!raw) {
      // the min–max band: along the max edge, back along the min edge
      g.fillStyle = 'rgba(47,91,234,0.16)';
      let run: number[] = [];
      const flush = () => {
        if (run.length < 1) return;
        g.beginPath();
        run.forEach((i, k) => (k ? g.lineTo(X(ts[i]), Y(pts[i][3] as number)) : g.moveTo(X(ts[i]), Y(pts[i][3] as number))));
        for (let k = run.length - 1; k >= 0; k--) g.lineTo(X(ts[run[k]]), Y(pts[run[k]][2] as number));
        g.closePath();
        g.fill();
        run = [];
      };
      pts.forEach((p, i) => {
        if (p[1] === null || gap(i)) flush();
        if (p[1] !== null) run.push(i);
      });
      flush();
    }
    g.beginPath();
    let pen = false;
    pts.forEach((p, i) => {
      const v = raw ? (p[2] === 0 ? (p[1] as number) : null) : (p[1] as number | null);
      if (v === null || gap(i)) pen = false;
      if (v === null) return;
      if (pen) g.lineTo(X(ts[i]), Y(v));
      else g.moveTo(X(ts[i]), Y(v));
      pen = true;
    });
    g.strokeStyle = '#2f5bea';
    g.lineWidth = raw ? 1.6 : 1.8;
    g.lineJoin = 'round';
    g.stroke();

    g.fillStyle = '#e5383b';
    pts.forEach((p, i) => {
      const bad = raw ? p[2] !== 0 : ((p[5] as number) ?? 0) > 0;
      if (!bad) return;
      g.beginPath();
      if (raw) g.arc(X(ts[i]), Y(p[1] as number), 3, 0, Math.PI * 2);
      else g.rect(X(ts[i]) - 1, H - 6, 2, 5);
      g.fill();
    });
  }, [state, lines]);

  return <canvas ref={ref} className="block h-[86px] w-full" />;
}
