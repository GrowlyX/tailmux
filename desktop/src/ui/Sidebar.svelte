<script lang="ts">
  import { store } from "../lib/store.svelte";
  import { manager, pages, type Page } from "../lib/manager.svelte";
  import DotsIcon from "./DotsIcon.svelte";
  import Wordmark from "./Wordmark.svelte";
  import Icon from "./Icon.svelte";

  const icons: Record<Page, string> = { overview: "gauge", tailnets: "grid", devices: "computer", "exit-node": "globe", settings: "gear", logs: "lines" };
  const title = (p: Page) => (p[0].toUpperCase() + p.slice(1)).replace("-", " ");
</script>

<nav class="sidebar">
  <div class="brand">
    <span class="dots"><DotsIcon lit={store.runningCount} reachable={store.offline === null} size={22} /></span>
    <Wordmark size={20} />
  </div>
  {#each pages as p}
    <button class="item" class:selected={manager.page === p} onclick={() => (manager.page = p)}>
      <span class="icon"><Icon name={icons[p]} size={15} /></span>
      <span>{title(p)}</span>
    </button>
  {/each}
</nav>

<style>
  /* No divider: the sidebar is told apart by its background alone. */
  .sidebar { width: 210px; flex: none; padding: 0 10px; background: var(--sidebar); display: flex; flex-direction: column; gap: 2px; }
  .brand { display: flex; align-items: center; gap: 9px; padding: 40px 14px 18px; }
  .dots { display: flex; color: var(--fg); }
  .item {
    display: flex; align-items: center; gap: 10px; padding: 7px 10px; border-radius: 7px; border: none;
    background: none; color: var(--fg); font-size: 14px; text-align: left; width: 100%;
  }
  .item:hover { background: rgb(var(--fg-rgb) / 0.05); }
  .item.selected { background: rgb(var(--fg-rgb) / 0.12); font-weight: 600; }
  .icon { width: 20px; display: flex; justify-content: center; }
</style>
