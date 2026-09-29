<script lang="ts">
  // "tail" light, "mux" bold.
  let { size = 17 }: { size?: number } = $props();
</script>

<span class="wordmark" style:font-size="{size}px"><span class="light">tail</span><span class="bold">mux</span></span>

<style>
  .wordmark { letter-spacing: -0.2px; line-height: 1.15; white-space: nowrap; }
  .light { font-weight: 300; }
  .bold { font-weight: 700; }
</style>
