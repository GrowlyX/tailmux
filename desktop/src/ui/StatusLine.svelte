<script lang="ts">
  import { manager } from "../lib/manager.svelte";
  import Icon from "./Icon.svelte";
</script>

{#if manager.busy || manager.error || manager.message}
  <div class="status">
    {#if manager.busy}<span class="spinner"></span>{/if}
    {#if manager.error}
      <span class="warn"><Icon name="warning" size={13} /></span>
      <span class="text">{manager.error}</span>
    {:else if manager.message}
      <span class="text secondary">{manager.message}</span>
    {/if}
  </div>
{/if}

<style>
  .status { display: flex; align-items: center; gap: 8px; padding: 8px 20px; font-size: 12px; background: rgb(var(--fg-rgb) / 0.03); border-top: 1px solid rgb(var(--fg-rgb) / 0.06); flex: none; }
  .warn { color: var(--orange); display: flex; }
  .text { display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .spinner { width: 12px; height: 12px; border-radius: 50%; border: 2px solid rgb(var(--fg-rgb) / 0.15); border-top-color: var(--fg-2); animation: spin 0.8s linear infinite; flex: none; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
