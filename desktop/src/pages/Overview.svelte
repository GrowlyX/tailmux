<script lang="ts">
  import { store } from "../lib/store.svelte";
  import { manager } from "../lib/manager.svelte";
  import { formatRate } from "../lib/format";
  import PageHeader from "../ui/PageHeader.svelte";
  import UpdateBanner from "../ui/UpdateBanner.svelte";
  import ThroughputChart from "../ui/ThroughputChart.svelte";
  import TailnetRow from "../ui/TailnetRow.svelte";

  let subtitle = $derived.by(() => {
    const tun = store.status?.tun;
    return `tailmux ${store.status?.version ?? ""} · ${tun ? `TUN on ${tun.interface}` : "proxy mode"}`;
  });
  let online = $derived(manager.peers.filter((p) => p.online).length);
</script>

<div class="scroll">
  <PageHeader title="Overview" {subtitle} />
  <div class="body">
    {#if store.update}<UpdateBanner info={store.update} />{/if}
    <div class="cards">
      {#each [
        ["Tailnets connected", `${store.runningCount} of ${store.tailnets.length}`],
        ["Devices online", `${online} of ${manager.peers.length}`],
        ["Downloading", formatRate(store.totalRate.rx)],
        ["Uploading", formatRate(store.totalRate.tx)],
      ] as [label, value]}
        <div class="stat card">
          <div class="label secondary">{label}</div>
          <div class="value num">{value}</div>
        </div>
      {/each}
    </div>
    <ThroughputChart height={200} />
    <div class="rows">
      {#each store.tailnets as t, i (t.name)}
        <TailnetRow tailnet={t} index={i} />
      {/each}
    </div>
  </div>
</div>

<style>
  .scroll { flex: 1; overflow-y: auto; min-height: 0; }
  .body { display: flex; flex-direction: column; gap: 16px; padding: 0 24px 24px; }
  .cards { display: flex; gap: 12px; }
  .stat { flex: 1; padding: 14px; display: flex; flex-direction: column; gap: 4px; min-width: 0; }
  .label { font-size: 11px; }
  .value { font-size: 20px; font-weight: 600; white-space: nowrap; }
  .rows { display: flex; flex-direction: column; gap: 2px; }
</style>
