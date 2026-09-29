<script lang="ts">
  // The tray dropdown: header, live throughput chart and one row per
  // tailnet with an on/off switch. Mirrors PanelView.swift.
  import { onMount } from "svelte";
  import { store } from "./lib/store.svelte";
  import { invoke, inTauri } from "./lib/bridge";
  import DotsIcon from "./ui/DotsIcon.svelte";
  import Wordmark from "./ui/Wordmark.svelte";
  import Icon from "./ui/Icon.svelte";
  import ThroughputChart from "./ui/ThroughputChart.svelte";
  import TailnetRow from "./ui/TailnetRow.svelte";
  import UpdateBanner from "./ui/UpdateBanner.svelte";
  import OfflineView from "./ui/OfflineView.svelte";

  let { startPage: _startPage }: { startPage: string | null } = $props();
  let root = $state<HTMLElement>();

  onMount(() => {
    void store.start();
    // The window is as tall as the content; tell Rust after each layout.
    const ro = new ResizeObserver(() => {
      if (root && inTauri) void invoke("resize_panel", { height: root.offsetHeight });
    });
    if (root) ro.observe(root);
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") void invoke("hide_panel"); };
    window.addEventListener("keydown", onKey);
    return () => { ro.disconnect(); window.removeEventListener("keydown", onKey); store.stop(); };
  });
</script>

<div class="panel" bind:this={root}>
  <div class="header">
    <span class="dots"><DotsIcon lit={store.runningCount} reachable={store.offline === null} size={26} /></span>
    <div class="brand">
      <Wordmark size={17} />
      {#if store.offline !== null}<span class="sub secondary">Not running</span>{/if}
    </div>
    <span class="spacer"></span>
    <button class="hbtn" title="Open tailmux" aria-label="Open tailmux" onclick={() => invoke("show_main")}><Icon name="gear-fill" size={15} /></button>
    <button class="hbtn" title="Quit" aria-label="Quit" onclick={() => invoke("quit")}><Icon name="door" size={15} /></button>
  </div>

  {#if store.update}
    <UpdateBanner info={store.update} />
  {/if}

  {#if store.offline !== null}
    <OfflineView message={store.offline} />
  {:else}
    <ThroughputChart height={96} />
    <div class="rows">
      {#each store.tailnets as t, i (t.name)}
        <TailnetRow tailnet={t} index={i} />
      {/each}
    </div>
  {/if}
</div>

<style>
  .panel { width: 372px; padding: 14px; display: flex; flex-direction: column; gap: 12px; }
  .header { display: flex; align-items: center; gap: 10px; }
  .dots { display: flex; color: var(--fg); }
  .brand { display: flex; flex-direction: column; gap: 1px; }
  .sub { font-size: 11px; }
  .spacer { flex: 1; }
  .hbtn {
    width: 28px; height: 28px; border-radius: 50%; border: none; background: none; padding: 0;
    display: inline-flex; align-items: center; justify-content: center; color: var(--fg-2);
  }
  .hbtn + .hbtn { margin-left: -8px; }
  .hbtn:hover { color: var(--fg); background: rgb(var(--fg-rgb) / 0.1); }
  .rows { display: flex; flex-direction: column; gap: 2px; }
</style>
