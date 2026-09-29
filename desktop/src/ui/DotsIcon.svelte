<script lang="ts">
  // Tailscale's 3x3 dot grid, one dot lit per connected tailnet, lit in
  // the order that draws the "T". Same geometry as Icon.swift.
  let { lit = 0, reachable = true, size = 26 }: { lit?: number; reachable?: boolean; size?: number } = $props();
  const order = [0, 1, 2, 4, 7, 3, 5, 6, 8];
  const d = 0.235, gap = 0.1;
  const origin = (1 - (3 * d + 2 * gap)) / 2;
  let litSet = $derived(new Set(order.slice(0, Math.max(0, Math.min(lit, 9)))));
</script>

<svg width={size} height={size} viewBox="0 0 1 1" aria-hidden="true">
  {#each Array(9) as _, i}
    <circle
      cx={origin + (i % 3) * (d + gap) + d / 2}
      cy={origin + Math.floor(i / 3) * (d + gap) + d / 2}
      r={d / 2}
      fill="currentColor"
      opacity={!reachable ? 0.18 : litSet.has(i) ? 1 : 0.3}
    />
  {/each}
</svg>
