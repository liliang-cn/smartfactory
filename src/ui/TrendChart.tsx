// Small canvas chart: telemetry trends, or an SPC chart (one dot per unit, limits, Cpk).
import { useEffect, useRef } from 'react';
import type { Asset } from '@/sim/types';

export type Metric = 'meas' | 'temp' | 'power' | 'vib' | 'level';

export function spcStats(a: Asset) {
  const m = a.measure!;
  const data = a.hist.meas;
  const mean = data.reduce((x, y) => x + y, 0) / Math.max(1, data.length);
  const sd = Math.sqrt(data.reduce((x, y) => x + (y - mean) ** 2, 0) / Math.max(1, data.length - 1)) || 1e-9;
  const cpk = Math.min(m.hi !== undefined ? (m.hi - mean) / (3 * sd) : Infinity, m.lo !== undefined ? (mean - m.lo) / (3 * sd) : Infinity);
  return { n: data.length, mean, cpk };
}

export function TrendChart({ asset: a, metric, tick }: { asset: Asset; metric: Metric; tick: number }) {
  const ref = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const cv = ref.current;
    if (!cv) return;
    const data = a.hist[metric] ?? [];
    const slots = metric === 'meas' ? 60 : 240;
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    const W = cv.clientWidth;
    const H = cv.clientHeight;
    if (!W) return;
    if (cv.width !== W * dpr) { cv.width = W * dpr; cv.height = H * dpr; }
    const g = cv.getContext('2d')!;
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, W, H);
    if (data.length < 2) {
      g.fillStyle = '#8f97b2';
      g.font = '12px "Plus Jakarta Sans Variable", sans-serif';
      g.fillText('正在采集数据', 4, H / 2);
      return;
    }
    const m = a.measure;
    const lines: [number, string][] = [];
    if (metric === 'temp' && a.spec?.warn) lines.push([a.spec.warn, '#f2a516'], [a.spec.trip!, '#e5383b']);
    if (metric === 'meas' && m) {
      lines.push([m.target, '#9fb2f0']);
      if (m.lo !== undefined) lines.push([m.lo, '#e5383b']);
      if (m.hi !== undefined) lines.push([m.hi, '#e5383b']);
    }
    let lo = Math.min(...data);
    let hi = Math.max(...data);
    for (const [v] of lines) { lo = Math.min(lo, v); hi = Math.max(hi, v); }
    if (metric === 'level') { lo = 0; hi = Math.max(hi, a.type === 'agv' ? 100 : a.cap || 1); }
    if (metric === 'meas' && m?.abs) lo = Math.min(lo, 0);
    const pad = (hi - lo) * 0.12 || 1;
    lo -= pad;
    hi += pad;
    const off = slots - data.length;
    const X = (i: number) => ((i + off) / (slots - 1)) * (W - 6) + 3;
    const Y = (v: number) => H - 4 - ((v - lo) / (hi - lo)) * (H - 8);

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
    const path = () => { g.beginPath(); data.forEach((v, i) => (i ? g.lineTo(X(i), Y(v)) : g.moveTo(X(i), Y(v)))); };
    if (metric !== 'meas') {
      const grad = g.createLinearGradient(0, 0, 0, H);
      grad.addColorStop(0, 'rgba(47,91,234,0.28)');
      grad.addColorStop(1, 'rgba(47,91,234,0)');
      path();
      g.lineTo(X(data.length - 1), H);
      g.lineTo(X(0), H);
      g.closePath();
      g.fillStyle = grad;
      g.fill();
    }
    path();
    g.strokeStyle = metric === 'meas' ? 'rgba(47,91,234,0.45)' : '#2f5bea';
    g.lineWidth = metric === 'meas' ? 1.2 : 2;
    g.lineJoin = 'round';
    g.stroke();
    if (metric === 'meas' && m) {
      data.forEach((v, i) => {
        const bad = (m.lo !== undefined && v < m.lo) || (m.hi !== undefined && v > m.hi);
        g.fillStyle = bad ? '#e5383b' : '#2f5bea';
        g.beginPath();
        g.arc(X(i), Y(v), bad ? 3.2 : 2.2, 0, Math.PI * 2);
        g.fill();
      });
    } else {
      const last = data[data.length - 1];
      g.fillStyle = '#2f5bea';
      g.beginPath();
      g.arc(X(data.length - 1), Y(last), 3.2, 0, Math.PI * 2);
      g.fill();
    }
  }, [a, metric, tick]);

  return <canvas ref={ref} className="block h-[86px] w-full" />;
}
