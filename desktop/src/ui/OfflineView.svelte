<script lang="ts">
  import { store } from "../lib/store.svelte";
  import { manager } from "../lib/manager.svelte";

  let { message, install = true }: { message: string; install?: boolean } = $props();
  let hint = $derived(
    store.info?.os === "windows"
      ? "Start it with tailmux up, or install the service so it runs in the background and TUN mode works."
      : "Start it with tailmux up, or install the service (runs as root) for TUN mode.",
  );
</script>

<div class="offline card">
  <div class="title">The tailmux daemon isn't reachable.</div>
  <div class="hint secondary">{hint}</div>
  <div class="msg tertiary">{message}</div>
  {#if install}
    <div class="actions">
      <button class="btn" onclick={() => manager.installService()} disabled={manager.busy}>Install service…</button>
      {#if manager.message && !manager.error}<span class="secondary small">{manager.message}</span>{/if}
      {#if manager.error}<span class="err small">{manager.error}</span>{/if}
    </div>
  {/if}
</div>

<style>
  .offline { padding: 10px; display: flex; flex-direction: column; gap: 6px; }
  .title { font-size: 12px; font-weight: 500; }
  .hint { font-size: 11px; }
  .msg { font-size: 10px; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .actions { display: flex; align-items: center; gap: 10px; margin-top: 4px; flex-wrap: wrap; }
  .small { font-size: 11px; }
  .err { color: var(--orange); }
</style>
