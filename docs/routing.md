# How a destination is routed

## Names, in order

1. **Pins** (`"corp.internal": "work"`).
2. **`host.<tailnet>`**: `db.home` means db in the tailnet named home.
3. **Full MagicDNS names** like `db.tail1234.ts.net`. Suffixes are unique per tailnet.
4. **Split DNS domains** from each tailnet's admin console. The query goes to
   that tailnet's resolvers, through that tailnet.
5. **Bare hostnames** like `db`, matched against every tailnet's peers. An
   online peer wins, then config order. This works in proxy mode only.
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
share an IP. Names never collide, so prefer them. [TUN mode](tun.md) gives each
name its own fake IP for exactly this reason.

See what's contested and who wins:

```sh
tailmux status             # conflicts section
tailmux resolve db         # one destination: which tailnet, why, who else claimed it
```
