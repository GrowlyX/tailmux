<script lang="ts">
  import { store } from "../lib/store.svelte";
  import { manager } from "../lib/manager.svelte";
  import { invoke } from "../lib/bridge";
  import PageHeader from "../ui/PageHeader.svelte";
  import PillSwitch from "../ui/PillSwitch.svelte";
  import Icon from "../ui/Icon.svelte";

  /// Middle truncation, like macOS, so the file name stays visible.
  const middle = (s: string, n: number) => (s.length <= n ? s : s.slice(0, Math.ceil(n / 2) - 1) + "…" + s.slice(-Math.floor(n / 2)));
  let os = $derived(store.info?.os ?? "linux");
  let device = $derived(os === "windows" ? "PC" : "computer");
  let updateLine = $derived.by(() => {
    const u = store.status?.update;
    if (!u) return `tailmux ${store.status?.version ?? ""}`;
    if (u.available) return `tailmux ${u.latest ?? ""} is available (you have ${u.current})`;
    return `tailmux ${u.current} is up to date`;
  });
  let tunHelp = $derived(
    os === "windows"
      ? "Needs the tailmux service (it runs as a Windows service). Off leaves only the SOCKS5 and HTTP proxies."
      : "Needs the service to run as root: install it below. Off leaves only the SOCKS5 and HTTP proxies.",
  );
  // Offer the installer when TUN is being turned on; it needs the
  // service. Offline is handled by the OfflineView instead.
  let configTun = $derived(manager.config?.config.tun?.enabled ?? false);
  let showService = $derived(manager.tun && !configTun);
</script>

<PageHeader title="Settings" subtitle="Changes to the service apply after a restart.">
  <button class="btn prominent" onclick={() => manager.saveSettings()} disabled={manager.busy}>Save</button>
</PageHeader>
<div class="scroll">
  <div class="form">
    {#if manager.restartPending}
      <div class="restart">
        <span class="ric"><Icon name="refresh" size={15} /></span>
        <span>Saved. Restart the service to apply your changes.</span>
        <span class="spacer"></span>
        <button class="btn" onclick={() => manager.restartService()}>Restart now</button>
      </div>
    {/if}

    <section>
      <h3>This device</h3>
      <div class="group">
        <label class="line"><span>Device name</span><input class="field" bind:value={manager.hostname} placeholder="tailmux-{store.info?.hostname ?? device}" autocomplete="off" /></label>
        <div class="help secondary">How this {device} shows up in every tailnet.</div>
      </div>
    </section>

    <section>
      <h3>Network</h3>
      <div class="group">
        <div class="line"><span>TUN mode: reach tailnets from every app</span><span class="spacer"></span><PillSwitch on={manager.tun} onchange={(on) => (manager.tun = on)} /></div>
        <div class="help secondary">{tunHelp}</div>
        <div class="line"><button class="btn" onclick={() => manager.repair()} disabled={manager.busy}>Repair network</button><span class="help secondary">Re-applies routes and DNS and flushes the DNS cache.</span></div>
      </div>
    </section>

    {#if showService}
      <section>
        <h3>Service</h3>
        <div class="group">
          <div class="line">
            <button class="btn" onclick={() => manager.installService()} disabled={manager.busy}>Install service…</button>
            <span class="help secondary">Installs tailmux as a system service{os === "windows" ? " (asks for administrator rights)" : " (asks for your password)"}.</span>
          </div>
        </div>
      </section>
    {/if}

    <section>
      <h3>Updates</h3>
      <div class="group">
        <div class="line"><span>Install updates automatically</span><span class="spacer"></span><PillSwitch on={manager.autoUpdate} onchange={(on) => (manager.autoUpdate = on)} /></div>
        <div class="line"><span class="secondary">{updateLine}</span><span class="spacer"></span><button class="btn" onclick={() => manager.checkForUpdates()} disabled={manager.busy}>Check now</button></div>
      </div>
    </section>

    <section>
      <h3>App</h3>
      <div class="group">
        <div class="line"><span>Start at login</span><span class="spacer"></span><PillSwitch on={manager.loginItem} onchange={(on) => manager.setLoginItem(on)} /></div>
        <div class="line">
          <button class="btn" onclick={() => manager.restartService()} disabled={manager.busy}>Restart service</button>
          <button class="btn" onclick={() => invoke("quit")}>Quit tailmux</button>
        </div>
      </div>
    </section>

    {#if manager.config}
      <section>
        <h3>Files</h3>
        <div class="group">
          {#each [["Config", manager.config.path], ["State", manager.config.state_dir]] as [label, path]}
            <div class="line">
              <span class="flabel">{label}</span>
              <span class="path mono selectable" title={path}>{middle(path, 64)}</span>
              <button class="btn plain" title="Copy path" onclick={() => manager.copy(path)}><Icon name="copy" size={13} /></button>
            </div>
          {/each}
        </div>
      </section>
    {/if}
  </div>
</div>

<style>
  .scroll { flex: 1; overflow-y: auto; min-height: 0; }
  .form { display: flex; flex-direction: column; gap: 18px; padding: 0 24px 24px; max-width: 720px; }
  .restart { display: flex; align-items: center; gap: 10px; padding: 12px; border-radius: 10px; background: rgb(10 122 255 / 0.08); }
  .ric { color: var(--accent); display: flex; }
  section { display: flex; flex-direction: column; gap: 6px; }
  h3 { margin: 0 0 2px 12px; font-size: 12px; font-weight: 600; color: var(--fg-2); text-transform: none; }
  .group { border-radius: 10px; background: rgb(var(--fg-rgb) / 0.04); padding: 4px 12px; }
  .line { display: flex; align-items: center; gap: 12px; min-height: 36px; padding: 4px 0; font-size: 13px; }
  .line + .line, .help + .line { border-top: 1px solid rgb(var(--fg-rgb) / 0.06); }
  .line input { max-width: 380px; margin-left: auto; }
  .help { font-size: 11px; padding: 0 0 8px; }
  .spacer { flex: 1; }
  .flabel { width: 60px; flex: none; }
  .path { font-size: 11px; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
