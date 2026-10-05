<script lang="ts">
  import { store } from "../lib/store.svelte";
  import { exitLabel } from "../lib/exit";
  import PageHeader from "../ui/PageHeader.svelte";
  import ExitList from "../ui/ExitList.svelte";
  import Icon from "../ui/Icon.svelte";

  let query = $state("");
  let e = $derived(store.exit);
</script>

<PageHeader title="Exit node" subtitle="Send internet traffic through a device in one of your tailnets.">
  <input class="field search" bind:value={query} placeholder="Search devices, countries and cities" type="search" autocomplete="off" />
</PageHeader>
<div class="scroll">
  <div class="body">
    <div class="current card" class:broken={e !== null && !e.active}>
      <span class="ic" class:on={e?.active} class:warn={e !== null && !e.active}><Icon name={e && !e.active ? "warning" : "globe"} size={18} /></span>
      <div class="text">
        {#if !e}
          <div class="title">No exit node</div>
          <div class="sub secondary">Internet traffic goes out through this network directly.</div>
        {:else}
          <div class="title">{exitLabel(e)} <span class="tn secondary">· {e.tailnet}</span></div>
          {#if e.active}
            <div class="sub secondary">Everything no tailnet claims leaves through {e.fqdn || e.node}. The local network stays direct.</div>
          {:else}
            <div class="sub warn">{e.error ?? "Not usable right now."}</div>
            <div class="sub secondary">Until it works again, traffic no tailnet claims is blocked rather than going out directly.</div>
          {/if}
        {/if}
      </div>
      <span class="spacer"></span>
      {#if e}<span class="state" style:color={e.active ? "var(--green)" : "var(--orange)"}>{e.active ? "Active" : "Not active"}</span>{/if}
    </div>
    {#if !store.status?.tun}
      <div class="note secondary">Proxy mode: only apps using the SOCKS5 or HTTP proxy go through the exit node. Turn on TUN mode in Settings to cover every app.</div>
    {/if}
    <ExitList {query} />
  </div>
</div>

<style>
  .search { width: 260px; }
  .scroll { flex: 1; overflow-y: auto; min-height: 0; }
  .body { display: flex; flex-direction: column; gap: 12px; padding: 0 24px 24px; max-width: 720px; }
  .current { display: flex; align-items: center; gap: 12px; padding: 14px; }
  .ic { display: flex; color: var(--fg-2); flex: none; }
  .ic.on { color: var(--accent); }
  .ic.warn { color: var(--orange); }
  .text { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
  .title { font-size: 14px; font-weight: 600; }
  .tn { font-weight: 400; font-size: 12px; }
  .sub { font-size: 12px; }
  .warn { color: var(--orange); }
  .spacer { flex: 1; }
  .state { font-size: 12px; white-space: nowrap; }
  .note { font-size: 11px; padding: 0 4px; }
</style>
