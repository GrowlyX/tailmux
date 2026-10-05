// State for the main window: peers, config, logs, forms and actions.
// Live status and throughput come from the shared Store. Mirrors
// Manager.swift.
import { get, send, type ConfigView, type PeerInfo, type TailnetStatus } from "./api";
import { invoke } from "./bridge";
import { store } from "./store.svelte";

export const pages = ["overview", "tailnets", "devices", "exit-node", "settings", "logs"] as const;
export type Page = (typeof pages)[number];

export function isPage(s: string | null | undefined): s is Page {
  return pages.includes(s as Page);
}

export class Manager {
  page = $state<Page>("overview");
  peers = $state<PeerInfo[]>([]);
  config = $state<ConfigView | null>(null);
  logs = $state<string[]>([]);
  query = $state("");
  busy = $state(false);
  message = $state<string | null>(null);
  error = $state<string | null>(null);
  restartPending = $state(false);

  // Add-tailnet dialog.
  showAdd = $state(false);
  newName = $state("");
  newControl = $state("");
  newKey = $state("");
  addedLoginURL = $state<string | null>(null);

  // Settings form.
  hostname = $state("");
  tun = $state(false);
  autoUpdate = $state(true);
  loginItem = $state(false);

  private timer: ReturnType<typeof setInterval> | null = null;

  filteredPeers = $derived.by(() => {
    const q = this.query.trim().toLowerCase();
    const sorted = [...this.peers].sort((a, b) =>
      (a.online ? 0 : 1) - (b.online ? 0 : 1) || a.alias.localeCompare(b.alias),
    );
    if (!q) return sorted;
    return sorted.filter((p) => p.alias.toLowerCase().includes(q) || (p.ips ?? []).some((ip) => ip.includes(q)));
  });

  async start() {
    void this.load(true);
    invoke<boolean>("autostart_enabled").then((on) => (this.loginItem = on));
    if (this.timer) clearInterval(this.timer);
    this.timer = setInterval(() => {
      if (!document.hidden) void this.load(false);
    }, 2000);
  }

  stop() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  }

  async load(resetForm: boolean) {
    try {
      const [peers, config, logs] = await Promise.all([
        get<PeerInfo[] | null>("peers"),
        get<ConfigView>("config"),
        get<string[] | null>("logs?n=300"),
      ]);
      this.peers = peers ?? [];
      this.config = config;
      this.logs = logs ?? [];
      if (resetForm) this.fillForm(config);
    } catch {
      // The Store reports the daemon being down; nothing to add.
    }
  }

  private fillForm(c: ConfigView) {
    this.hostname = c.config.hostname ?? "";
    this.tun = c.config.tun?.enabled ?? false;
    this.autoUpdate = c.config.updates?.auto ?? true;
  }

  private async run(done: string | null, body: () => Promise<void>) {
    this.busy = true;
    this.error = null;
    try {
      await body();
      if (done) this.message = done;
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
    this.busy = false;
    await store.refresh();
    await this.load(false);
  }

  addTailnet() {
    const name = this.newName.trim().toLowerCase();
    const body: Record<string, string> = { name };
    if (this.newControl) body.control_url = this.newControl;
    if (this.newKey) body.auth_key = this.newKey;
    return this.run(null, async () => {
      const st = await send<TailnetStatus>("POST", "tailnets", body);
      if (st?.auth_url) {
        this.addedLoginURL = st.auth_url;
      } else {
        this.showAdd = false;
        this.message = `Joined ${name}.`;
      }
      this.newName = "";
      this.newControl = "";
      this.newKey = "";
    });
  }

  remove(name: string) {
    return this.run(`Removed ${name}. Its login is kept if you add it back.`, async () => {
      await send("DELETE", `tailnets/${encodeURIComponent(name)}`);
    });
  }

  saveSettings() {
    return this.run("Saved. Restart the service to apply.", async () => {
      await send("PUT", "settings", { tun: this.tun, auto_update: this.autoUpdate, hostname: this.hostname });
      this.restartPending = true;
    });
  }

  restartService() {
    return this.run("Restarting the service…", async () => {
      await send("POST", "restart");
      this.restartPending = false;
    });
  }

  repair() {
    return this.run("Routes and DNS re-applied, DNS cache flushed.", async () => {
      await send("POST", "repair");
    });
  }

  checkForUpdates() {
    return this.run("Checking for updates…", async () => {
      await send("POST", "update");
    });
  }

  installService() {
    return this.run(null, async () => {
      this.message = "Installing the service…";
      this.message = await invoke<string>("install_service");
    });
  }

  async setLoginItem(on: boolean) {
    try {
      this.loginItem = await invoke<boolean>("set_autostart", { on });
    } catch (e) {
      this.error = `Start at login: ${e}`;
    }
  }

  openLogin(url: string) {
    void invoke("open_url", { url });
  }

  async copy(s: string) {
    await invoke("copy_text", { text: s });
    this.message = `Copied ${s}`;
  }
}

export const manager = new Manager();
