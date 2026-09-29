<script lang="ts">
  import { manager } from "../lib/manager.svelte";
  import PageHeader from "../ui/PageHeader.svelte";

  let box = $state<HTMLElement>();
  let stick = true;
  // Follow new lines unless the user scrolled up to read.
  $effect(() => {
    manager.logs.length;
    if (box && stick) box.scrollTop = box.scrollHeight;
  });
  function onscroll() {
    if (box) stick = box.scrollHeight - box.scrollTop - box.clientHeight < 24;
  }
</script>

<PageHeader title="Logs" subtitle="The service's recent activity, newest at the bottom.">
  <button class="btn" onclick={() => manager.copy(manager.logs.join("\n"))}>Copy all</button>
</PageHeader>
<div class="log mono selectable" bind:this={box} {onscroll}>
  {#each manager.logs as line}
    <div class="line" class:bad={line.includes("fail") || line.includes("error")}>{line}</div>
  {/each}
</div>

<style>
  .log { flex: 1; overflow: auto; min-height: 0; padding: 0 24px 16px; font-size: 11px; }
  .line { white-space: pre-wrap; word-break: break-all; line-height: 1.45; padding: 0.5px 0; }
  .bad { color: var(--orange); }
</style>
