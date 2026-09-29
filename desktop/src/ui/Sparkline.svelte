<script lang="ts">
  let { values, color, width = 40, height = 16 }: { values: number[]; color: string; width?: number; height?: number } = $props();
  let d = $derived.by(() => {
    if (values.length < 2) return "";
    const peak = Math.max(...values, 1);
    return values
      .map((v, i) => {
        const x = (width * i) / (values.length - 1);
        const y = height * (1 - v / peak) * 0.9 + height * 0.05;
        return `${i === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`;
      })
      .join("");
  });
</script>

<svg {width} {height} viewBox="0 0 {width} {height}" aria-hidden="true">
  <path {d} fill="none" stroke={color} stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round" />
</svg>
