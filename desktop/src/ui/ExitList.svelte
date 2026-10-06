<script lang="ts">
  // The exit node choices, used by the panel's picker and the Exit node
  // page: None, your own exit nodes per tailnet, then Mullvad by country
  // and city with a "Best available" pick in each. Mirrors the official
  // client's Exit Node menu.
  import { onMount } from "svelte";
  import type { ExitNodeInfo } from "../lib/api";
  import { store } from "../lib/store.svelte";
  import { best, flag, groupExitNodes, nodeRef, shortName, type ExitCountry } from "../lib/exit";
  import { color } from "../lib/format";
  import Icon from "./Icon.svelte";

  let { query = "" }: { query?: string } = $props();

  let groups = $derived(groupExitNodes(store.exitNodes ?? [], query));
  let searching = $derived(query.trim() !== "");
  let open = $state<Set<string>>(new Set());
  let total = $derived(store.exitNodes?.length ?? 0);

  onMount(() => store.watchExitNodes());

  // While searching every matching group is open; otherwise the user's.
  const isOpen = (key: string) => searching || open.has(key);
  function toggle(key: string) {
    const next = new Set(open);
    if (!next.delete(key)) next.add(key);
    open = next;
  }
  function pick(n: ExitNodeInfo | undefined, key: string) {
    if (n && !store.exitPending) void store.setExitNode({ tailnet: n.tailnet, node: nodeRef(n) }, key);
  }
  const has = (nodes: ExitNodeInfo[]) => nodes.some((n) => n.selected);
  const servers = (n: number) => `${n} server${n === 1 ? "" : "s"}`;
  // A country's trailing text: its cities, or the one city's servers.
  const countryTrail = (c: ExitCountry) =>
    c.cities.length > 1 ? `${c.cities.length} cities` : c.cities[0].name === c.name ? servers(c.nodes.length) : `${c.cities[0].name} · ${servers(c.nodes.length)}`;
</script>

{#snippet check(on: boolean)}
  <span class="check">{#if on}<Icon name="check" size={13} stroke={2.4} />{/if}</span>
{/snippet}

{#snippet node(n: ExitNodeInfo, depth: number, label: string)}
  {@const key = "n:" + n.tailnet + "/" + n.id}
  <button class="opt" style:padding-left="{8 + depth * 18}px" class:pending={store.exitPending === key} onclick={() => pick(n, key)} title={n.fqdn}>
    {@render check(Boolean(n.selected))}
    <!-- Mullvad nodes often report no presence, so grey would mislead. -->
    <span class="dot" style:background={n.online ? "var(--green)" : n.mullvad ? "transparent" : "rgb(var(--fg-rgb) / 0.2)"}></span>
    <span class="label">{label}</span>
    <span class="spacer"></span>
    {#if !n.online && !n.mullvad}<span class="trail secondary">offline</span>{/if}
  </button>
{/snippet}

{#snippet bestRow(nodes: ExitNodeInfo[], key: string, depth: number)}
  {@const b = best(nodes)}
  <button class="opt" style:padding-left="{8 + depth * 18}px" class:pending={store.exitPending === key} onclick={() => pick(b, key)} title={b?.fqdn}>
    {@render check(false)}
    <span class="label">Best available</span>
    <span class="spacer"></span>
    {#if b}<span class="trail secondary">{shortName(b)}</span>{/if}
  </button>
{/snippet}

<div class="list">
  {#if store.exitError}<div class="err">{store.exitError}</div>{/if}

  {#if !searching}
    <button class="opt" class:pending={store.exitPending === "none"} onclick={() => { if (!store.exitPending) void store.setExitNode(null, "none"); }}>
      {@render check(!store.exit)}
      <span class="label">None</span>
      <span class="spacer"></span>
      <span class="trail secondary">use this network directly</span>
    </button>
  {/if}

  {#each groups.own as g (g.tailnet)}
    <div class="sh"><span class="tdot" style:background={color(store.colorIndex(g.tailnet))}></span>{g.tailnet}</div>
    {#each g.nodes as n (n.id)}
      {@render node(n, 0, shortName(n))}
    {/each}
  {/each}

  {#if groups.countries.length}
    <div class="sh">Mullvad</div>
    {#each groups.countries as c (c.name)}
      {@const ck = "c:" + c.name}
      {#if c.nodes.length === 1}
        {@const n = c.nodes[0]}
        {@const key = "n:" + n.tailnet + "/" + n.id}
        <button class="opt" class:pending={store.exitPending === key} onclick={() => pick(n, key)} title={n.fqdn}>
          {@render check(Boolean(n.selected))}
          <span class="flag">{flag(c.code)}</span>
          <span class="label">{c.name}</span>
          <span class="spacer"></span>
          <span class="trail secondary">{c.cities[0].name === c.name ? shortName(n) : c.cities[0].name}</span>
        </button>
      {:else}
        <button class="opt" onclick={() => toggle(ck)}>
          {@render check(has(c.nodes))}
          <span class="flag">{flag(c.code)}</span>
          <span class="label">{c.name}</span>
          <span class="spacer"></span>
          <span class="trail secondary">{countryTrail(c)}</span>
          <span class="chev"><Icon name={isOpen(ck) ? "chevron-down" : "chevron-right"} size={11} stroke={2.2} /></span>
        </button>
        {#if isOpen(ck)}
          {@render bestRow(c.nodes, "b:" + c.name, 1)}
          {#if c.cities.length === 1}
            {#each c.nodes as n (n.tailnet + n.id)}
              {@render node(n, 1, shortName(n))}
            {/each}
          {:else}
            {#each c.cities as city (city.name)}
              {@const yk = "y:" + c.name + "/" + city.name}
              {#if city.nodes.length === 1}
                {@render node(city.nodes[0], 1, city.name)}
              {:else}
                <button class="opt" style:padding-left="26px" onclick={() => toggle(yk)}>
                  {@render check(has(city.nodes))}
                  <span class="label">{city.name}</span>
                  <span class="spacer"></span>
                  <span class="trail secondary">{servers(city.nodes.length)}</span>
                  <span class="chev"><Icon name={isOpen(yk) ? "chevron-down" : "chevron-right"} size={11} stroke={2.2} /></span>
                </button>
                {#if isOpen(yk)}
                  {@render bestRow(city.nodes, "b:" + c.name + "/" + city.name, 2)}
                  {#each city.nodes as n (n.tailnet + n.id)}
                    {@render node(n, 2, shortName(n))}
                  {/each}
                {/if}
              {/if}
            {/each}
          {/if}
        {/if}
      {/if}
    {/each}
  {/if}

  {#if store.exitNodes === null}
    <div class="empty secondary">Loading…</div>
  {:else if total === 0}
    <div class="empty secondary">No exit nodes yet. Devices in your tailnets that offer to be an exit node, and Mullvad locations, show up here.</div>
  {:else if searching && !groups.own.length && !groups.countries.length}
    <div class="empty secondary">Nothing matches.</div>
  {/if}
</div>

<style>
  .list { display: flex; flex-direction: column; gap: 1px; }
  .opt {
    display: flex; align-items: center; gap: 8px; width: 100%; padding: 5px 8px; border: none; border-radius: 6px;
    background: none; font-size: 13px; text-align: left; min-height: 28px;
  }
  .opt:hover { background: rgb(var(--fg-rgb) / 0.06); }
  .opt.pending { opacity: 0.5; }
  .check { width: 14px; flex: none; display: flex; color: var(--accent); }
  .dot { width: 7px; height: 7px; border-radius: 50%; flex: none; }
  .flag { width: 18px; flex: none; font-size: 14px; line-height: 1; text-align: center; }
  .label { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
  .spacer { flex: 1; min-width: 8px; }
  .trail { font-size: 11px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
  .chev { color: var(--fg-2); display: flex; flex: none; }
  .sh { display: flex; align-items: center; gap: 6px; margin: 10px 0 2px 8px; font-size: 11px; font-weight: 600; color: var(--fg-2); }
  .tdot { width: 7px; height: 7px; border-radius: 50%; }
  .err { font-size: 12px; color: var(--orange); padding: 4px 8px 6px; }
  .empty { padding: 24px 12px; text-align: center; font-size: 12px; }
</style>
