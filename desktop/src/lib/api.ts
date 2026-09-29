// Mirrors of the daemon's JSON (internal/mux/http.go, stats.go).
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
