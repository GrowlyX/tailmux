<script lang="ts">
  import type { UpdateInfo } from "../lib/api";
  import { store } from "../lib/store.svelte";
  import Icon from "./Icon.svelte";

  let { info }: { info: UpdateInfo } = $props();
  let v = $derived(info.latest ?? "?");
  let icon = $derived(info.state === "installing" ? "refresh" : info.state === "failed" ? "warning" : "download");
  let title = $derived.by(() => {
    switch (info.state) {
      case "installing": return `Updating to ${v}…`;
      case "installed": return `Restarting on ${v}…`;
      case "failed": return `Update to ${v} failed`;
      default: return `tailmux ${v} is available`;
    }
  });
  let detail = $derived.by(() => {
    if (info.state === "failed") return info.error ?? "";
    if (info.state === "installing") return "Downloading and installing the new release.";
    return `You have ${info.current}.` + (info.auto ? " It installs itself automatically too." : "");
  });
</script>

<div class="banner">
  <span class="icon" class:failed={info.state === "failed"}><Icon name={icon} size={16} /></span>
  <div class="text">
    <div class="title">{title}</div>
    <div class="detail secondary">{detail}</div>
  </div>
  <span class="spacer"></span>
  {#if info.state !== "installing" && info.state !== "installed"}
    <button class="update" onclick={() => store.startUpdate()}>{info.state === "failed" ? "Retry" : "Update"}</button>
  {/if}
</div>

<style>
  .banner { display: flex; align-items: center; gap: 10px; padding: 10px; border-radius: 10px; background: rgb(10 122 255 / 0.08); }
  .icon { color: var(--accent); display: flex; }
  .icon.failed { color: var(--orange); }
  .text { min-width: 0; display: flex; flex-direction: column; gap: 1px; }
  .title { font-size: 12px; font-weight: 500; }
  .detail { font-size: 10px; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .spacer { flex: 1; min-width: 6px; }
  .update { border: none; border-radius: 999px; padding: 4px 10px; background: var(--accent); color: #fff; font-size: 11px; font-weight: 600; }
</style>
