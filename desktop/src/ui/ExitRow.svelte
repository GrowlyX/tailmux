<script lang="ts">
  // The "Exit node" row: what non-tailnet traffic leaves through, in a
  // warning color when the chosen node can't be used. Opens the picker.
  import { store } from "../lib/store.svelte";
  import { exitLabel } from "../lib/exit";
  import Icon from "./Icon.svelte";

  let { onclick }: { onclick: () => void } = $props();
  let e = $derived(store.exit);
  let broken = $derived(e !== null && !e.active);
  let detail = $derived.by(() => {
    if (!e) return "None";
    if (!e.active) return e.error ? `${exitLabel(e)}: ${e.error}` : `${exitLabel(e)} isn't usable`;
    return `${exitLabel(e)} · ${e.tailnet}`;
  });
  let help = $derived(
    !e
      ? "Internet traffic goes out directly. Pick an exit node to send it through a device in a tailnet."
      : broken
        ? "Traffic that no tailnet claims is blocked until the exit node works again (the local network stays direct)."
        : `Traffic that no tailnet claims leaves through ${e.fqdn || e.node} (the local network stays direct).`,
  );
</script>

<button class="row" title={help} {onclick}>
  <span class="ic" class:on={e !== null && !broken} class:warn={broken}><Icon name={broken ? "warning" : "globe"} size={14} /></span>
  <div class="text">
    <div class="name">Exit node</div>
    <div class="detail" class:warn={broken}>{detail}</div>
  </div>
  <span class="spacer"></span>
  <span class="chev"><Icon name="chevron-right" size={12} stroke={2.2} /></span>
</button>

<style>
  .row { display: flex; align-items: center; gap: 10px; padding: 6px 8px; border-radius: 7px; border: none; background: none; width: 100%; text-align: left; }
  .row:hover { background: rgb(var(--fg-rgb) / 0.06); }
  .ic { width: 16px; flex: none; display: flex; justify-content: center; margin-left: -4px; color: var(--fg-2); }
  .ic.on { color: var(--accent); }
  .ic.warn { color: var(--orange); }
  .text { min-width: 0; display: flex; flex-direction: column; gap: 1px; }
  .name { font-size: 13px; font-weight: 500; }
  .detail { font-size: 11px; color: var(--fg-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .detail.warn { color: var(--orange); }
  .spacer { flex: 1; min-width: 6px; }
  .chev { color: var(--fg-2); display: flex; flex: none; }
</style>
