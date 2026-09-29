// Polls the daemon once a second while the window is visible and holds
// what both windows show. Mirrors Store.swift.
import { get, send, running, type StatusResponse, type TailnetStats, type TailnetStatus } from "./api";
import { invoke, type Info } from "./bridge";

export class Store {
  status = $state<StatusResponse | null>(null);
  stats = $state<Record<string, TailnetStats>>({});
  offline = $state<string | null>(null);
  pending = $state<Set<string>>(new Set());
  info = $state<Info | null>(null);
  loaded = $state(false);

  private timer: ReturnType<typeof setInterval> | null = null;
  private inflight = false;

  tailnets = $derived<TailnetStatus[]>(this.status?.tailnets ?? []);
  runningCount = $derived(this.tailnets.filter(running).length);
  totalRate = $derived.by(() => {
    let rx = 0, tx = 0;
    for (const t of this.tailnets) {
      const r = this.rate(t.name);
      rx += r.rx;
      tx += r.tx;
    }
    return { rx, tx };
  });
  update = $derived.by(() => {
    const u = this.status?.update;
    if (!u) return null;
    return u.available || u.state === "installing" || u.state === "failed" ? u : null;
  });

  async start() {
    if (this.timer) return;
    this.info = await invoke<Info>("info");
    await this.refresh();
    this.timer = setInterval(() => {
      // Hidden windows keep the webview alive; no point polling then.
      if (!document.hidden) void this.refresh();
    }, 1000);
  }

  stop() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  }

  async refresh() {
    if (this.inflight) return;
    this.inflight = true;
    try {
      const [s, x] = await Promise.all([get<StatusResponse>("status"), get<{ tailnets: TailnetStats[] | null }>("stats")]);
      this.status = s;
      this.stats = Object.fromEntries((x.tailnets ?? []).map((t) => [t.name, t]));
      this.offline = null;
    } catch (e) {
      this.status = null;
      this.stats = {};
      this.offline = String(e instanceof Error ? e.message : e);
    } finally {
      this.inflight = false;
      this.loaded = true;
    }
  }

  async startUpdate() {
    try {
      await send("POST", "update");
    } catch (e) {
      this.offline = String(e);
    }
    await this.refresh();
  }

  async setEnabled(name: string, on: boolean) {
    this.pending = new Set([...this.pending, name]);
    // Show the switch flip right away; the next poll confirms it.
    const t = this.status?.tailnets.find((t) => t.name === name);
    if (t) t.enabled = on;
    try {
      await send("POST", `tailnets/${encodeURIComponent(name)}/${on ? "enable" : "disable"}`);
    } catch (e) {
      this.offline = String(e);
    }
    const next = new Set(this.pending);
    next.delete(name);
    this.pending = next;
    await this.refresh();
  }

  /// Latest bytes/s for a tailnet, both directions.
  rate(name: string): { rx: number; tx: number } {
    const s = this.stats[name];
    if (!s) return { rx: 0, tx: 0 };
    return { rx: s.rx?.at(-1) ?? 0, tx: s.tx?.at(-1) ?? 0 };
  }

  /// Per-tailnet throughput series for the chart, oldest first.
  series(name: string): number[] {
    const s = this.stats[name];
    if (!s) return [];
    const rx = s.rx ?? [], tx = s.tx ?? [];
    const n = Math.min(rx.length, tx.length);
    const out = new Array<number>(n);
    for (let i = 0; i < n; i++) out[i] = rx[i] + tx[i];
    return out;
  }

  colorIndex(name: string): number {
    const i = this.tailnets.findIndex((t) => t.name === name);
    return i < 0 ? 0 : i;
  }
}

export const store = new Store();
