<script lang="ts">
  // Last 120 s of throughput, one line per enabled tailnet with a faint
  // area under it. Newest sample at the right edge; the plot is clipped
  // so lines never spill over the axis labels.
  import { store } from "../lib/store.svelte";
  import { color, formatRate, timeLabel } from "../lib/format";

  let { height = 96 }: { height?: number } = $props();
  const HISTORY = 120;
  const AXIS_W = 52;
  const AXIS_H = 14;

  let width = $state(300);
  let plotW = $derived(Math.max(10, width - AXIS_W));
  let plotH = $derived(height - AXIS_H);

  let lines = $derived.by(() =>
    store.tailnets
      .filter((t) => t.enabled)
      .map((t) => ({ name: t.name, color: color(store.colorIndex(t.name)), values: store.series(t.name) })),
  );
  let peak = $derived(Math.max(0, ...lines.flatMap((l) => l.values)));
  let yMax = $derived(Math.max(peak * 1.15, 2048));
  let ticks = $derived(niceTicks(yMax, 3));

  function niceTicks(max: number, count: number): number[] {
    const raw = max / count;
    const mag = Math.pow(10, Math.floor(Math.log10(raw)));
    // Nearest nice step, so the axis gets about `count` labels.
    const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).reduce((a, b) => (Math.abs(b - raw) < Math.abs(a - raw) ? b : a));
    const out: number[] = [];
    for (let v = step; v <= max; v += step) out.push(v);
    return out;
  }

  function x(t: number) { return plotW * (1 + t / HISTORY); }
  function y(v: number) { return plotH * (1 - v / yMax); }

  /// Monotone cubic (Fritsch-Carlson), like SwiftUI's .monotone.
  function path(values: number[]): string {
    const n = values.length;
    if (n === 0) return "";
    const px = values.map((_, i) => x(i - (n - 1)));
    const py = values.map(y);
    if (n === 1) return `M${px[0]} ${py[0]}`;
    const dx: number[] = [], m: number[] = [];
    for (let i = 0; i < n - 1; i++) {
      dx.push(px[i + 1] - px[i]);
      m.push((py[i + 1] - py[i]) / (dx[i] || 1));
    }
    const tg = [m[0]];
    for (let i = 1; i < n - 1; i++) tg.push(m[i - 1] * m[i] <= 0 ? 0 : (m[i - 1] + m[i]) / 2);
    tg.push(m[n - 2]);
    for (let i = 0; i < n - 1; i++) {
      if (m[i] === 0) { tg[i] = 0; tg[i + 1] = 0; continue; }
      const a = tg[i] / m[i], b = tg[i + 1] / m[i], s = a * a + b * b;
      if (s > 9) { const t = 3 / Math.sqrt(s); tg[i] = t * a * m[i]; tg[i + 1] = t * b * m[i]; }
    }
    let d = `M${px[0].toFixed(1)} ${py[0].toFixed(1)}`;
    for (let i = 0; i < n - 1; i++) {
      const h = dx[i] / 3;
      d += `C${(px[i] + h).toFixed(1)} ${(py[i] + h * tg[i]).toFixed(1)} ${(px[i + 1] - h).toFixed(1)} ${(py[i + 1] - h * tg[i + 1]).toFixed(1)} ${px[i + 1].toFixed(1)} ${py[i + 1].toFixed(1)}`;
    }
    return d;
  }

  function area(values: number[]): string {
    const p = path(values);
    if (!p) return "";
    const n = values.length;
    return `${p}L${x(0).toFixed(1)} ${plotH}L${x(-(n - 1)).toFixed(1)} ${plotH}Z`;
  }

  const xTicks = [-120, -90, -60, -30, 0];
  const clipId = `clip-${Math.random().toString(36).slice(2)}`;
</script>

<div class="chart card">
  <div class="title secondary">Throughput</div>
  <div class="plot" bind:clientWidth={width} style:height="{height}px">
    <svg {width} {height}>
      <defs><clipPath id={clipId}><rect x="0" y="0" width={plotW} height={plotH} /></clipPath></defs>
      {#each ticks as v}
        <line x1="0" x2={plotW} y1={y(v)} y2={y(v)} class="grid" />
        <text x={plotW + 6} y={y(v)} class="label" dominant-baseline="middle">{formatRate(v)}</text>
      {/each}
      {#each xTicks as t, i}
        <line x1={x(t)} x2={x(t)} y1="0" y2={plotH} class="grid" />
        <text
          x={x(t)}
          y={plotH + 11}
          class="label"
          text-anchor={i === 0 ? "start" : i === xTicks.length - 1 ? "end" : "middle"}
        >{timeLabel(t)}</text>
      {/each}
      <g clip-path="url(#{clipId})">
        {#each lines as l (l.name)}
          <path d={area(l.values)} fill={l.color} fill-opacity="0.08" />
          <path d={path(l.values)} fill="none" stroke={l.color} stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
        {/each}
      </g>
    </svg>
  </div>
</div>

<style>
  .chart { padding: 10px; display: flex; flex-direction: column; gap: 4px; }
  .title { font-size: 11px; font-weight: 500; }
  .plot { width: 100%; }
  svg { display: block; overflow: visible; }
  .grid { stroke: rgb(var(--fg-rgb) / 0.18); stroke-width: 0.5; stroke-dasharray: 2 3; }
  .label { font-size: 9px; fill: var(--fg-2); }
</style>
