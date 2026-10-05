<script lang="ts">
  // The full app: sidebar with Overview, Tailnets, Devices, Exit node,
  // Settings, Logs. Mirrors MainWindow.swift.
  import { onMount, untrack } from "svelte";
  import { store } from "./lib/store.svelte";
  import { manager, isPage } from "./lib/manager.svelte";
  import { listen } from "./lib/bridge";
  import Sidebar from "./ui/Sidebar.svelte";
  import StatusLine from "./ui/StatusLine.svelte";
  import OfflineView from "./ui/OfflineView.svelte";
  import Overview from "./pages/Overview.svelte";
  import Tailnets from "./pages/Tailnets.svelte";
  import Devices from "./pages/Devices.svelte";
  import ExitNode from "./pages/ExitNode.svelte";
  import Settings from "./pages/Settings.svelte";
  import Logs from "./pages/Logs.svelte";

  let { startPage }: { startPage: string | null } = $props();
  // Only the initial value matters: the hash is read once at mount.
  const initial = untrack(() => startPage);
  if (isPage(initial)) manager.page = initial;

  onMount(() => {
    void store.start().then(() => {
      const p = store.info?.start_page;
      if (isPage(p)) manager.page = p;
    });
    void manager.start();
    const un = listen<string>("page", (p) => { if (isPage(p)) manager.page = p; });
    return () => { store.stop(); manager.stop(); void un.then((f) => f()); };
  });
</script>

<div class="main">
  <Sidebar />
  <div class="content">
    <div class="page">
      {#if store.offline !== null}
        <div class="offline"><OfflineView message={store.offline} /></div>
      {:else if manager.page === "overview"}
        <Overview />
      {:else if manager.page === "tailnets"}
        <Tailnets />
      {:else if manager.page === "devices"}
        <Devices />
      {:else if manager.page === "exit-node"}
        <ExitNode />
      {:else if manager.page === "settings"}
        <Settings />
      {:else}
        <Logs />
      {/if}
    </div>
    <StatusLine />
  </div>
</div>

<style>
  .main { display: flex; height: 100%; }
  .content { flex: 1; min-width: 560px; display: flex; flex-direction: column; min-height: 0; background: var(--bg); }
  .page { flex: 1; min-height: 0; display: flex; flex-direction: column; }
  .offline { padding: 20px; }
</style>
