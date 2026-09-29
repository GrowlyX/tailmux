<script lang="ts">
  import type { Snippet } from "svelte";
  let { title, subtitle, children }: { title: string; subtitle: string; children?: Snippet } = $props();
</script>

<div class="header">
  <div class="titles">
    <h1>{title}</h1>
    <div class="sub secondary">{subtitle}</div>
  </div>
  <span class="spacer"></span>
  {@render children?.()}
</div>

<style>
  .header { display: flex; align-items: flex-start; gap: 12px; padding: 18px 24px 12px; flex: none; }
  .titles { display: flex; flex-direction: column; gap: 2px; }
  h1 { margin: 0; font-size: 22px; font-weight: 700; line-height: 1.2; }
  .sub { font-size: 12px; }
  .spacer { flex: 1; }
</style>
