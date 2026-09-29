<script lang="ts">
  import type { TailnetStatus } from "../lib/api";
  import { running } from "../lib/api";
  import { color as paletteColor, detail, formatBytes, formatRate } from "../lib/format";
  import { store } from "../lib/store.svelte";
  import { invoke } from "../lib/bridge";
  import PillSwitch from "./PillSwitch.svelte";
  import Sparkline from "./Sparkline.svelte";

  let { tailnet, index }: { tailnet: TailnetStatus; index: number } = $props();
  let color = $derived(paletteColor(index));
  let isRunning = $derived(running(tailnet));
  let series = $derived(store.series(tailnet.name).slice(-40));
  let rate = $derived.by(() => { const r = store.rate(tailnet.name); return r.rx + r.tx; });
  let needsLogin = $derived(Boolean(tailnet.auth_url) && tailnet.enabled);

  let help = $derived.by(() => {
    const lines = [tailnet.name + (tailnet.suffix ? ` (${tailnet.suffix})` : "")];
    if (tailnet.self_ips?.length) lines.push("this device: " + tailnet.self_ips.join(", "));
    lines.push(...(tailnet.routes ?? []));
    const s = store.stats[tailnet.name];
    if (s) lines.push(`${formatBytes(s.rx_total)} in, ${formatBytes(s.tx_total)} out, ${s.conns} open`);
    return lines.join("\n");
  });

  function click() {
    if (tailnet.auth_url) void invoke("open_url", { url: tailnet.auth_url });
  }
</script>

<div class="row" title={help} onclick={click} role="button" tabindex="-1" onkeydown={() => {}}>
  <span class="dot" style:background={isRunning ? color : "rgb(var(--fg-rgb) / 0.2)"}></span>
  <div class="text">
    <div class="name">{tailnet.name}</div>
    <div class="detail" class:login={needsLogin}>{detail(tailnet)}</div>
  </div>
  <span class="spacer"></span>
  {#if isRunning}
    <Sparkline values={series} {color} />
    <span class="rate num secondary">{formatRate(rate)}</span>
  {/if}
  <span style:opacity={store.pending.has(tailnet.name) ? 0.5 : 1}>
    <PillSwitch on={tailnet.enabled} tint={color} onchange={(on) => store.setEnabled(tailnet.name, on)} />
  </span>
</div>

<style>
  .row { display: flex; align-items: center; gap: 10px; padding: 6px 8px; border-radius: 7px; }
  .row:hover { background: rgb(var(--fg-rgb) / 0.06); }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex: none; }
  .text { min-width: 0; display: flex; flex-direction: column; gap: 1px; }
  .name { font-size: 13px; font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .detail { font-size: 11px; color: var(--fg-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .detail.login { color: var(--orange); }
  .spacer { flex: 1; min-width: 6px; }
  .rate { font-size: 10px; width: 52px; text-align: right; flex: none; }
</style>
