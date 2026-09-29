<script lang="ts">
  import { manager } from "../lib/manager.svelte";
  import PageHeader from "../ui/PageHeader.svelte";
</script>

<PageHeader title="Devices" subtitle="Every device in every tailnet, by the name to reach it.">
  <input class="field search" bind:value={manager.query} placeholder="Search names and IPs" type="search" autocomplete="off" />
</PageHeader>
<div class="scroll">
  <div class="list">
    {#each manager.filteredPeers as p (p.tailnet + "/" + p.name)}
      <div class="row" title="Click to copy {p.alias}" onclick={() => manager.copy(p.alias)} role="button" tabindex="-1" onkeydown={() => {}}>
        <span class="dot" style:background={p.online ? "var(--green)" : "rgb(var(--fg-rgb) / 0.2)"}></span>
        <span class="alias mono">{p.alias}</span>
        <span class="spacer"></span>
        {#if p.routes?.length}
          <span class="routes secondary">routes {p.routes.join(", ")}</span>
        {/if}
        <span class="ip mono secondary" title="Click to copy" onclick={(e) => { e.stopPropagation(); if (p.ips?.[0]) void manager.copy(p.ips[0]); }} role="button" tabindex="-1" onkeydown={() => {}}>{p.ips?.[0] ?? ""}</span>
        <span class="online" style:color={p.online ? "var(--green)" : "var(--fg-2)"}>{p.online ? "online" : "offline"}</span>
      </div>
    {/each}
    {#if manager.filteredPeers.length === 0}
      <div class="empty secondary">{manager.peers.length === 0 ? "No devices yet." : "Nothing matches."}</div>
    {/if}
  </div>
</div>

<style>
  .search { width: 240px; }
  .scroll { flex: 1; overflow-y: auto; min-height: 0; }
  .list { padding: 0 24px 24px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 8px 6px; border-bottom: 1px solid rgb(var(--fg-rgb) / 0.08); }
  .row:hover { background: rgb(var(--fg-rgb) / 0.04); }
  .dot { width: 7px; height: 7px; border-radius: 50%; flex: none; }
  .alias { font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .spacer { flex: 1; min-width: 12px; }
  .routes { font-size: 11px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .ip { font-size: 12px; width: 120px; text-align: right; flex: none; }
  .ip:hover { color: var(--fg); }
  .online { font-size: 11px; width: 50px; text-align: right; flex: none; }
  .empty { padding: 40px; text-align: center; }
</style>
