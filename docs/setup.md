# Setup and config

## `tailmux setup`

A terminal UI for everything in the config:

```
 tailmux setup   /opt/homebrew/etc/tailmux/config.json

▸ 1  work               tailscale              browser   ● connected (12/15 online)
  2  home               tailscale              browser   ! needs login
  3  lab                headscale.example.com  auth key  · not joined

  TUN mode  on (runs as root)     proxies 127.0.0.1:1055  127.0.0.1:1056
  daemon    running

  a add · e edit · d delete · J/K reorder · t TUN · p pins · o options · l log in · s save · q quit
```

- **l** logs in. Each tailnet that needs it shows a login URL; press enter to
  open it. Sign in with the account that owns *that* tailnet. The screen goes
  green as each one connects. If the daemon is running, the login goes through
  it. Otherwise setup brings the tailnets up itself just long enough to log in.
- **J/K** reorders. Earlier tailnets win ties (see [routing](routing.md)).
- **p** edits pins, one per line: `10.0.0.0/24 = home`.
- **o** sets the device name, the proxy ports, whether TUN mode takes
  `100.64.0.0/10`, and whether updates install themselves.

Each tailnet sees this machine as a new device, `tailmux-<hostname>`.
Consider turning off key expiry for it in the admin console.

## The config file

`tailmux setup` writes this for you. It's plain JSON if you'd rather edit it by hand:

```json
{
  "tailnets": [
    { "name": "work" },
    { "name": "home", "auth_key": "env:TS_HOME_AUTHKEY" },
    { "name": "lab", "control_url": "https://headscale.example.com" }
  ],
  "pins": { "10.0.0.0/24": "home", "corp.internal": "work" },
  "tun": { "enabled": true }
}
```

| Key | Default | |
| --- | --- | --- |
| `tailnets[].name` | required | Short label. Also a DNS suffix: `web.home` |
| `tailnets[].auth_key` | empty: browser login | Literal key, or `env:VAR` |
| `tailnets[].control_url` | Tailscale | Headscale or another control server |
| `tailnets[].hostname` | top-level `hostname` | Per-tailnet device name |
| `tailnets[].ephemeral` | `false` | Device disappears when tailmux stops |
| `hostname` | `tailmux-<host>` | Device name in every tailnet |
| `pins` | none | CIDR or domain → tailnet, for contested routes |
| `socks5` | `127.0.0.1:1055` | [Proxy mode](proxy.md) |
| `http` | `127.0.0.1:1056` | HTTP proxy, PAC file and the [local API](menubar.md#api) |
| `dns` | off | Extra DNS server, e.g. `127.0.0.1:1053`, for [proxy-mode setups](proxy.md#an-extra-dns-server-optional); not needed in TUN mode |
| `direct` | `true` | Proxy traffic no tailnet claims goes out normally |
| `exit_node` | none | `{"tailnet": "home", "node": "nas"}`: send that traffic through an [exit node](exit-nodes.md) instead |
| `state_dir` | see [install](install.md) | Logins and fake-IP table |
| `tun.enabled` | `false` | [TUN mode](tun.md) |
| `tun.peer_routes` | `true` | Route each tailnet device's address, for DNS names that point at one |
| `tun.cgnat` | `false` | Route all of `100.64.0.0/10` in TUN mode |
| `tun.real_ips` | `true` | Answer names with real `100.x` addresses unless another tailnet uses the same one ([why](tun.md#why-does-a-name-resolve-to-19818xx)) |
| `tun.fake_range` | `198.18.0.0/15` | Where fake IPs come from, for addresses several tailnets use |
| `tun.name` / `tun.mtu` / `tun.no_dns` | auto / 1500 / false | |
| `updates.check` | `true` | Check GitHub for releases ([updates](updates.md)) |
| `updates.auto` | `true` | Install them and restart; `false` only notifies |
