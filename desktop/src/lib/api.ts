// Mirrors of the daemon's JSON (internal/mux/http.go, stats.go, exit.go).
import { invoke } from "./bridge";

export interface TailnetStatus {
  name: string;
  enabled: boolean;
  state?: string;
  auth_url?: string;
  suffix?: string;
  self_ips?: string[];
  peers: number;
  online: number;
  routes?: string[];
  error?: string;
}

export interface UpdateInfo {
  current: string;
  latest?: string;
  available: boolean;
  url?: string;
  state: string;
  error?: string;
  auto: boolean;
  method?: string;
}

export interface StatusResponse {
  version?: string;
  tailnets: TailnetStatus[];
  tun?: { interface: string; fake_range: string; dns: boolean };
  update?: UpdateInfo;
  exit_node?: ExitStatus;
}

export interface TailnetStats {
  name: string;
  enabled: boolean;
  state?: string;
  conns: number;
  rx_total: number;
  tx_total: number;
  rx?: number[] | null;
  tx?: number[] | null;
}

export interface StatsResponse {
  interval_ms: number;
  tailnets: TailnetStats[];
}

export interface PeerInfo {
  tailnet: string;
  name: string;
  alias: string;
  fqdn: string;
  ips?: string[];
  online: boolean;
  routes?: string[];
}

/// Where a located exit node (Mullvad) is.
export interface ExitLocation {
  country?: string;
  country_code?: string;
  city?: string;
  city_code?: string;
  /// Ranks nodes in the same place; higher is better.
  priority?: number;
}

/// The chosen exit node. `active` false means it can't be used right
/// now (`error` says why) and non-tailnet traffic is blocked.
export interface ExitStatus {
  tailnet: string;
  node: string;
  name?: string;
  fqdn?: string;
  online: boolean;
  active: boolean;
  location?: ExitLocation;
  error?: string;
}

/// One device offering itself as an exit node. The daemon sorts them:
/// unlocated (your own) first, then by country, city and priority.
export interface ExitNodeInfo {
  tailnet: string;
  id: string;
  name: string;
  fqdn: string;
  ips?: string[];
  online: boolean;
  mullvad?: boolean;
  location?: ExitLocation;
  selected?: boolean;
}

export interface ExitNodesResponse {
  current: ExitStatus | null;
  nodes: ExitNodeInfo[] | null;
}

export interface ConfigView {
  path: string;
  state_dir: string;
  config: {
    hostname?: string;
    tailnets?: { name: string; control_url?: string }[];
    tun?: { enabled?: boolean };
    updates?: { check?: boolean; auto?: boolean };
  };
}

export function running(t: TailnetStatus): boolean {
  return t.enabled && t.state === "Running";
}

export function get<T>(path: string): Promise<T> {
  return invoke<T>("api", { method: "GET", path });
}

/// A changing request; Rust adds the X-Tailmux header.
export function send<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  return invoke<T>("api", { method, path, body: body ?? null });
}
