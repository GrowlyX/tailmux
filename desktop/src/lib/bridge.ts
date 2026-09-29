// Every call to the Rust side goes through here. Outside Tauri (the
// screenshot script serves the built page in Chromium) the same calls
// are answered by plain HTTP against a same-origin `/api/` proxy that
// adds the X-Tailmux header, so the pages render identically.
import { invoke as tauriInvoke } from "@tauri-apps/api/core";
import { listen as tauriListen, type UnlistenFn } from "@tauri-apps/api/event";

export const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;

export interface Info {
  base: string;
  os: string;
  hostname: string;
  sidecar: string | null;
  start_page: string | null;
}

async function shim(cmd: string, args: Record<string, unknown> = {}): Promise<unknown> {
  switch (cmd) {
    case "api": {
      const method = String(args.method ?? "GET");
      const path = String(args.path ?? "").replace(/^\//, "");
      const r = await fetch(`/api/${path}`, {
        method,
        headers: method === "GET" ? {} : { "X-Tailmux": "1", "Content-Type": "application/json" },
        body: args.body == null ? undefined : JSON.stringify(args.body),
      });
      const text = await r.text();
      if (!r.ok) throw new Error(text.trim() || `HTTP ${r.status}`);
      return text.trim() ? JSON.parse(text) : null;
    }
    case "info":
      return { base: location.origin + "/api", os: "linux", hostname: "pc", sidecar: null, start_page: null } satisfies Info;
    case "autostart_enabled":
      return false;
    case "set_autostart":
      return Boolean(args.on);
    case "copy_text":
      try { await navigator.clipboard.writeText(String(args.text)); } catch { /* not focused */ }
      return null;
    case "open_url":
      window.open(String(args.url), "_blank");
      return null;
    default:
      return null;
  }
}

export function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  return (inTauri ? tauriInvoke<T>(cmd, args) : (shim(cmd, args) as Promise<T>));
}

export function listen<T>(event: string, cb: (payload: T) => void): Promise<UnlistenFn> {
  if (!inTauri) return Promise.resolve(() => {});
  return tauriListen<T>(event, (e) => cb(e.payload));
}
