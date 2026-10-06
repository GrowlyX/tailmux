# Exit nodes

Pick an exit node and everything outside your tailnets leaves through it,
like the official client's **Exit Node** menu. Tailnet traffic is unchanged:
`ssh db.home` still goes to home, `grafana.work` to work. Any device that offers
itself as an exit node in any of your tailnets works, and so do
[Mullvad's](https://tailscale.com/kb/1258/mullvad-exit-nodes) if a tailnet has
the add-on.

```sh
tailmux exit-node                  # list them, * marks the one in use
tailmux exit-node mullvad          # filter: name, country or city
tailmux exit-node use nas.home     # a device (name.tailnet, name, IP or MagicDNS name)
tailmux exit-node use se           # the best Mullvad node in Sweden
tailmux exit-node use Tokyo        # ... or in a city
tailmux exit-node off
```

The switch is immediate, with no restart, and is saved as `exit_node` in the config:

```json
"exit_node": { "tailnet": "home", "node": "se-sto-wg-001.mullvad.ts.net" }
```

The [menu bar app](menubar.md) and the [desktop app](desktop.md) have the
same picker, with Mullvad's nodes grouped by country and city.

## What goes through it

| | TUN mode | Proxy mode |
| --- | --- | --- |
| Tailnet destinations | their tailnet, as always | their tailnet, as always |
| Everything else | the exit node, for every app | the exit node, for apps that use the proxy |
| The local network (your LAN, printers, the router) | direct | direct |
| DNS for other names | the exit node's resolver on Linux (systemd-resolved) and Windows; your network's resolver on macOS | the exit node's resolver |

**If the exit node can't be used, nothing leaks.** That covers the device going
away, its tailnet being switched off or logged out, and reconnecting after sleep.
Traffic that would have gone through the exit node fails instead of going direct.
`tailmux status` says why. Choose **None** (or `tailmux exit-node off`) to go
direct again.

## TUN mode

With an exit node, tailmux routes `0.0.0.0/1` and `128.0.0.0/1` (and the IPv6
halves) into the TUN. These two routes together beat the default route without
replacing it. Your LAN's own routes are more specific, so the LAN stays reachable.
tailmux's tailnet connections still use the real network:

- **macOS, Windows**: Tailscale binds its sockets to the interface of the
  default route, just as the official client does.
- **Linux**: policy routing, like `tailscaled`. Tailscale marks its sockets
  (fwmark `0x80000`). Unmarked traffic looks up table 5280, whose default
  route is `tailmux0`, after the main table's non-default routes (rules 5280
  and 5290). This runs alongside `tailscaled`, which uses 5210-5270. If
  `net.ipv4.conf.all.rp_filter` is `1` (strict), replies can be dropped. Set
  it to `2`, as Tailscale also recommends.

`tailmux status` shows `exit_routes` under `tun`.

## Limits

- **macOS DNS.** `/etc/resolver` can only send *some* domains somewhere, so
  macOS keeps asking your network's resolver for other names. The lookups
  themselves still go through the exit node if that resolver is a public one
  (set one in System Settings → Network → DNS, e.g. Mullvad's `194.242.2.2`).
  A resolver on your LAN (usually the router) is asked directly.
- **IPv6 on Windows.** The TUN only has an IPv4 address there, so IPv6
  traffic isn't routed into it.
- One exit node at a time, for the whole machine. tailmux joins several
  tailnets, but traffic outside them leaves through one place.
- The official app's per-app and "allow LAN access" switches don't exist.
  The local network is always direct.
