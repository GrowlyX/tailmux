import type { TailnetStatus } from "./api";

// Store.swift's Palette, in order.
export const palette = [
  "rgb(64, 133, 245)",
  "rgb(242, 140, 51)",
  "rgb(77, 186, 120)",
  "rgb(204, 89, 191)",
  "rgb(237, 92, 102)",
  "rgb(51, 179, 199)",
  "rgb(158, 128, 77)",
  "rgb(140, 140, 242)",
  "rgb(153, 158, 168)",
];

export function color(i: number): string {
  return palette[((i % palette.length) + palette.length) % palette.length];
}

export function formatBytes(b: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = b;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  if (i === 0) return `${Math.trunc(v)} B`;
  return `${v < 10 ? v.toFixed(1) : v.toFixed(0)} ${units[i]}`;
}

export function formatRate(bps: number): string {
  return formatBytes(bps) + "/s";
}

export function needsLogin(t: TailnetStatus): boolean {
  return Boolean(t.auth_url);
}

/// The row's status line, as in PanelView.swift.
export function detail(t: TailnetStatus): string {
  if (!t.enabled) return "Off";
  if (t.auth_url) return "Needs login · click to sign in";
  switch (t.state) {
    case "Running": {
      let s = `${t.online}/${t.peers} online`;
      const routes = t.routes?.length ?? 0;
      if (routes > 0) s += ` · ${routes} route${routes === 1 ? "" : "s"}`;
      return s;
    }
    case "Starting":
    case "NoState":
    case undefined:
    case "":
      return "Connecting…";
    default:
      return t.state;
  }
}

/// The card's state word, as in MainWindow.swift.
export function stateWord(t: TailnetStatus): string {
  if (!t.enabled) return "Off";
  if (t.auth_url) return "Needs login";
  switch (t.state) {
    case "Running":
      return "Connected";
    case "Starting":
    case "NoState":
    case undefined:
    case "":
      return "Connecting…";
    default:
      return t.state;
  }
}

export function timeLabel(secondsAgo: number): string {
  const s = -secondsAgo;
  if (s === 0) return "now";
  if (s % 60 === 0) return `${s / 60}m`;
  return `${s}s`;
}
