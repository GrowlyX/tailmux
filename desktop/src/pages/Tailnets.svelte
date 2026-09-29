<script lang="ts">
  import { store } from "../lib/store.svelte";
  import { manager } from "../lib/manager.svelte";
  import { running } from "../lib/api";
  import { color, formatBytes, stateWord } from "../lib/format";
  import PageHeader from "../ui/PageHeader.svelte";
  import PillSwitch from "../ui/PillSwitch.svelte";
  import Icon from "../ui/Icon.svelte";

  let confirmName = $state<string | null>(null);
  let menuFor = $state<string | null>(null);
  let addDialog = $state<HTMLDialogElement>();
  let confirmDialog = $state<HTMLDialogElement>();

  $effect(() => {
    if (!addDialog) return;
    if (manager.showAdd && !addDialog.open) addDialog.showModal();
    if (!manager.showAdd && addDialog.open) addDialog.close();
  });
  $effect(() => {
    if (!confirmDialog) return;
    if (confirmName && !confirmDialog.open) confirmDialog.showModal();
    if (!confirmName && confirmDialog.open) confirmDialog.close();
  });

  function closeAdd() {
    manager.showAdd = false;
    manager.addedLoginURL = null;
    manager.error = null;
  }
  function stateColor(t: (typeof store.tailnets)[number]) {
    if (t.auth_url && t.enabled) return "var(--orange)";
    return running(t) ? "var(--green)" : "var(--fg-2)";
  }
</script>

<svelte:window onclick={() => (menuFor = null)} />

<PageHeader title="Tailnets" subtitle="Each one is joined as its own device, all at once.">
  <button class="btn prominent" onclick={() => (manager.showAdd = true)}><Icon name="plus" size={12} stroke={2.5} />Add tailnet</button>
</PageHeader>
<div class="scroll">
  <div class="list">
    {#each store.tailnets as t, i (t.name)}
      {@const c = color(i)}
      {@const s = store.stats[t.name]}
      <div class="tcard">
        <div class="top">
          <span class="dot" style:background={running(t) ? c : "rgb(var(--fg-rgb) / 0.2)"}></span>
          <span class="name">{t.name}</span>
          <span class="state" style:color={stateColor(t)}>{stateWord(t)}</span>
          <span class="spacer"></span>
          {#if t.auth_url}
            <button class="btn prominent" onclick={() => manager.openLogin(t.auth_url!)}>Log in…</button>
          {/if}
          <PillSwitch on={t.enabled} tint={c} onchange={(on) => store.setEnabled(t.name, on)} />
          <span class="menu-wrap">
            <button class="more" title="More" onclick={(e) => { e.stopPropagation(); menuFor = menuFor === t.name ? null : t.name; }}>
              <Icon name="ellipsis" size={16} stroke={1.6} />
            </button>
            {#if menuFor === t.name}
              <div class="menu" role="menu">
                {#if t.suffix}<button onclick={() => manager.copy(t.suffix!)}>Copy MagicDNS suffix</button>{/if}
                {#if t.self_ips?.[0]}<button onclick={() => manager.copy(t.self_ips![0])}>Copy this device's IP</button>{/if}
                <hr />
                <button class="danger" onclick={() => (confirmName = t.name)}>Remove…</button>
              </div>
            {/if}
          </span>
        </div>
        <div class="details">
          {#each [
            ["MagicDNS", t.suffix ?? "–"],
            ["This device", t.self_ips?.[0] ?? "–"],
            ["Devices", `${t.online}/${t.peers} online`],
            ["Routes", String(t.routes?.length ?? 0)],
            ...(s ? [["Transferred", formatBytes(s.rx_total + s.tx_total)]] : []),
          ] as [label, value]}
            <div class="detail">
              <div class="dl secondary">{label}</div>
              <div class="dv num selectable">{value}</div>
            </div>
          {/each}
        </div>
        {#if t.routes?.length}
          <div class="routes mono secondary">{t.routes.join("   ")}</div>
        {/if}
      </div>
    {/each}
    {#if store.tailnets.length === 0}
      <div class="empty secondary">No tailnets yet. Add one to get started.</div>
    {/if}
  </div>
</div>

<dialog bind:this={addDialog} onclose={closeAdd} oncancel={closeAdd}>
  <div class="sheet">
    <h2>Add a tailnet</h2>
    {#if manager.addedLoginURL}
      <p>Almost there: sign in to this tailnet with the account that owns it.</p>
      <div class="url mono secondary selectable">{manager.addedLoginURL}</div>
      <div class="actions">
        <button class="btn" onclick={closeAdd}>Done</button>
        <button class="btn prominent" onclick={() => manager.openLogin(manager.addedLoginURL!)}>Open login page</button>
      </div>
    {:else}
      <form onsubmit={(e) => { e.preventDefault(); void manager.addTailnet(); }}>
        <label><span>Name</span><input class="field" bind:value={manager.newName} placeholder="work" autocomplete="off" /></label>
        <label><span>Control server</span><input class="field" bind:value={manager.newControl} placeholder="Tailscale (leave empty) or a Headscale URL" autocomplete="off" /></label>
        <label><span>Auth key</span><input class="field" type="password" bind:value={manager.newKey} placeholder="optional; empty logs in through the browser" autocomplete="off" /></label>
        <div class="note secondary">The name is also a DNS suffix: devices become host.name.</div>
        {#if manager.error}<div class="err">{manager.error}</div>{/if}
        <div class="actions">
          <button type="button" class="btn" onclick={closeAdd}>Cancel</button>
          <button type="submit" class="btn prominent" disabled={!manager.newName.trim() || manager.busy}>Add</button>
        </div>
      </form>
    {/if}
  </div>
</dialog>

<dialog bind:this={confirmDialog} onclose={() => (confirmName = null)} oncancel={() => (confirmName = null)}>
  <div class="sheet confirm">
    <h2>Remove {confirmName}?</h2>
    <p class="secondary">tailmux leaves this tailnet and forgets it. Its login is kept, so adding it back needs no new login.</p>
    <div class="actions">
      <button class="btn" onclick={() => (confirmName = null)}>Cancel</button>
      <button class="btn danger" onclick={() => { const n = confirmName!; confirmName = null; void manager.remove(n); }}>Remove</button>
    </div>
  </div>
</dialog>

<style>
  .scroll { flex: 1; overflow-y: auto; min-height: 0; }
  .list { display: flex; flex-direction: column; gap: 10px; padding: 0 24px 24px; }
  .tcard { padding: 16px; border-radius: 12px; background: rgb(var(--fg-rgb) / 0.04); display: flex; flex-direction: column; gap: 10px; }
  .top { display: flex; align-items: center; gap: 10px; }
  .dot { width: 10px; height: 10px; border-radius: 50%; flex: none; }
  .name { font-size: 15px; font-weight: 600; }
  .state { font-size: 12px; }
  .spacer { flex: 1; }
  .menu-wrap { position: relative; display: flex; }
  .more { border: none; background: none; padding: 2px; color: var(--fg-2); display: flex; border-radius: 6px; }
  .more:hover { color: var(--fg); background: rgb(var(--fg-rgb) / 0.08); }
  .menu { position: absolute; right: 0; top: 24px; z-index: 5; min-width: 200px; padding: 4px; border-radius: 8px; background: var(--bg); border: 1px solid var(--field-border); box-shadow: 0 8px 24px rgb(0 0 0 / 0.2); display: flex; flex-direction: column; }
  .menu button { text-align: left; border: none; background: none; padding: 5px 10px; border-radius: 5px; font-size: 13px; }
  .menu button:hover { background: var(--accent); color: #fff; }
  .menu button.danger { color: var(--red); }
  .menu button.danger:hover { color: #fff; }
  .menu hr { border: none; border-top: 1px solid var(--field-border); margin: 4px 6px; }
  .details { display: flex; gap: 18px; flex-wrap: wrap; }
  .detail { display: flex; flex-direction: column; gap: 2px; }
  .dl { font-size: 10px; }
  .dv { font-size: 12px; }
  .routes { font-size: 11px; white-space: pre-wrap; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .empty { padding: 40px; text-align: center; }

  .sheet { width: 460px; padding: 24px; border-radius: 12px; background: var(--bg); box-shadow: 0 20px 60px rgb(0 0 0 / 0.3); display: flex; flex-direction: column; gap: 16px; }
  .sheet.confirm { width: 380px; }
  h2 { margin: 0; font-size: 18px; font-weight: 700; }
  p { margin: 0; }
  form { display: flex; flex-direction: column; gap: 10px; }
  label { display: grid; grid-template-columns: 110px 1fr; align-items: center; gap: 10px; font-size: 13px; }
  .note { font-size: 11px; }
  .err { font-size: 12px; color: var(--orange); }
  .url { font-size: 11px; word-break: break-all; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
