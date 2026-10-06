// Grouping and labels for the exit node picker, shaped like the official
// client's Exit Node menu: your own exit nodes per tailnet, then Mullvad
// by country and city, each with a "Best available" pick.
import type { ExitNodeInfo, ExitStatus } from "./api";

export interface ExitCity {
  name: string;
  nodes: ExitNodeInfo[];
}

export interface ExitCountry {
  name: string;
  code: string;
  cities: ExitCity[];
  nodes: ExitNodeInfo[];
}

export interface ExitGroups {
  own: { tailnet: string; nodes: ExitNodeInfo[] }[];
  countries: ExitCountry[];
}

/// What PUT /exit-node gets as `node`: the MagicDNS name, else the ID.
export function nodeRef(n: ExitNodeInfo): string {
  return n.fqdn || n.id;
}

/// The flag emoji for a two-letter country code ("" when there is none).
/// Windows draws these as the two letters, which still reads fine.
export function flag(code?: string): string {
  if (!code || !/^[a-z]{2}$/i.test(code)) return "";
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}

/// The highest-priority online node; Mullvad nodes don't always report
/// presence, so with none online, the highest priority overall.
export function best(nodes: ExitNodeInfo[]): ExitNodeInfo | undefined {
  const pool = nodes.some((n) => n.online) ? nodes.filter((n) => n.online) : nodes;
  let top: ExitNodeInfo | undefined;
  for (const n of pool) {
    if (!top || (n.location?.priority ?? 0) > (top.location?.priority ?? 0)) top = n;
  }
  return top;
}

/// Short name of a device: its MagicDNS host without the tailnet suffix.
export function shortName(n: { name?: string; fqdn?: string; node?: string }): string {
  return n.name || n.fqdn?.split(".")[0] || n.node || "";
}

/// How the current exit node reads in a row: a place for Mullvad
/// ("🇩🇪 Frankfurt, Germany"), the device name otherwise.
export function exitLabel(s: ExitStatus): string {
  const l = s.location;
  if (l?.country) {
    const place = l.city ? `${l.city}, ${l.country}` : l.country;
    return `${flag(l.country_code)} ${place}`.trim();
  }
  return shortName(s);
}

function matches(n: ExitNodeInfo, q: string): boolean {
  if (!q) return true;
  const l = n.location;
  return [n.name, n.fqdn, n.tailnet, l?.country, l?.city, l?.country_code, l?.city_code]
    .some((s) => s?.toLowerCase().includes(q));
}

/// Groups the daemon's list (already sorted) after filtering by name,
/// tailnet, country or city.
export function groupExitNodes(nodes: ExitNodeInfo[], query: string): ExitGroups {
  const q = query.trim().toLowerCase();
  const own: ExitGroups["own"] = [];
  const countries: ExitCountry[] = [];
  for (const n of nodes) {
    if (!matches(n, q)) continue;
    const l = n.location;
    if (!n.mullvad && !l?.country) {
      let g = own.find((g) => g.tailnet === n.tailnet);
      if (!g) own.push((g = { tailnet: n.tailnet, nodes: [] }));
      g.nodes.push(n);
      continue;
    }
    const name = l?.country || "Unknown";
    let c = countries.find((c) => c.name === name);
    if (!c) countries.push((c = { name, code: l?.country_code ?? "", cities: [], nodes: [] }));
    c.nodes.push(n);
    const cityName = l?.city || name;
    let city = c.cities.find((x) => x.name === cityName);
    if (!city) c.cities.push((city = { name: cityName, nodes: [] }));
    city.nodes.push(n);
  }
  return { own, countries };
}
