# How a destination is routed

## Names, in order

1. **Pins** (`"corp.internal": "work"`).
2. **`host.<tailnet>`**: `db.home` means db in the tailnet named home.
3. **Full MagicDNS names** like `db.tail1234.ts.net`. Suffixes are unique per tailnet.
4. **Split DNS domains** from each tailnet's admin console. The query goes to
   that tailnet's resolvers, through that tailnet. If the answer is somewhere
   that tailnet can't reach, like a public IP, the connection goes direct.
   Tailscale's catch-all `ts.net` route is ignored, since every tailnet has it.
5. **Bare hostnames** like `db`, matched against every tailnet's peers. An
   online peer wins, then config order in proxy mode. In macOS TUN mode, the
   OS instead tries tailnet search suffixes in configuration order; the first
   successful lookup wins. Use `db.home` to select a specific tailnet.
6. **Anything else** is resolved normally. If the answer falls in a tailnet's
   subnet route, it goes there. Otherwise it goes direct.

## IPs

Longest prefix wins, so a peer's own address (/32) beats any subnet, and
`10.0.5.0/24` beats `10.0.0.0/16`. On a tie, the tailnet whose router is online
wins, then config order. Pins override all of this. A pin to a disabled or
stopped tailnet falls back to normal routing.

Tailnets that are off or logged out drop out entirely. A contested subnet then
fails over to the next tailnet that routes it.

## Collisions

Every tailnet numbers its devices from `100.64.0.0/10`, so two tailnets often
share an IP. Names never collide, so prefer them. When an address is shared,
[TUN mode](tun.md#why-does-a-name-resolve-to-19818xx) gives each name its own
fake IP for exactly this reason; otherwise names resolve to the real address.

See what's there, what's contested and who wins:

```sh
tailmux peers dormlab      # every device, as the name to type (host.tailnet)
tailmux status             # conflicts section
tailmux resolve db         # one destination: which tailnet, why, who else claimed it
```
