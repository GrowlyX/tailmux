# TUN mode: every app, no proxy settings

Turn it on with `t` in `tailmux setup`, or set `"tun": {"enabled": true}`.
Then run as root: `sudo brew services start tailmux`.

tailmux then:

- creates a TUN device (`utunN` on macOS, `tailmux0` on Linux) and routes every
  subnet your tailnets advertise into it;
- points the OS resolver at itself for tailnet domains only. That covers the
  tailnet names, the MagicDNS suffixes and the split-DNS domains, using
  `/etc/resolver/<domain>` on macOS and systemd-resolved routing domains on
  Linux;
- routes every tailnet device's own address (one /32 each) into the TUN, so a
  name in ordinary DNS that points at a device (`grafana.example.com →
  100.101.102.103`) works too;
- answers those names with the device's **real address** (`100.x.y.z`), like
  the official client, as long as no other tailnet uses the same address.
  When two tailnets do (both number a device `100.64.0.1`, say), each name
  gets a **fake IP** from `198.18.0.0/15` instead, unique per name. That
  keeps `web.work` and `web.home` apart. See [why 198.18.x.x](#why-does-a-name-resolve-to-19818xx).

A userspace TCP/IP stack (gVisor, the same one Tailscale uses) terminates each
TCP and UDP flow. tailmux maps fake IPs back to names and re-dials each flow
(fake or real address) through the owning tailnet. So `ssh db.home`, `psql -h db.corp.internal` and
`http://grafana.lab` in a browser all just work.

On macOS, TUN mode also installs DNS search domains, so `ssh db` works
without a suffix. Tailmux tries active tailnet aliases in configuration order
(`db.work`, then `db.home`, for example). The first successful lookup wins;
use an explicit suffix when the same hostname exists in several tailnets.
Search domains update as tailnets are enabled or disabled, remain during
reconnection, and are removed when the daemon stops. Existing LAN and VPN
resolver files are preserved. `"no_dns": true` disables this DNS integration.

With an [exit node](exit-nodes.md) selected, TUN mode also takes the rest
of the internet into the TUN and sends it out through the exit node.

## Safety

- **Your LAN stays yours.** A subnet that overlaps a local network is skipped
  (the log says so). Pin it to force it.
- **The official Tailscale app can coexist.** tailmux routes only the
  addresses of devices in its own tailnets, not all of `100.64.0.0/10`
  (unless you set `"cgnat": true`). Turn that off with `"peer_routes": false`.
- **No loops.** Traffic that arrives on the TUN is never dialed "directly".
  If no tailnet claims it, the connection is refused, unless an exit node is
  selected, in which case it goes through the exit node.
- **Clean exit.** Routes go away with the interface. Resolver files carry a
  marker and are removed on exit, or at the next start after a crash.

`ping` works too. tailmux answers an echo only after the destination
actually answered through its tailnet, so the round-trip time is real.

## Why does a name resolve to 198.18.x.x?

Because another tailnet has a device with the same address, and the name
has to keep pointing at the right one. Each tailnet numbers its devices from
`100.64.0.0/10` on its own, so `100.64.0.1` can be a device in `work`
*and* one in `home`. tailmux can't tell which one you meant from the
address alone, so it gives each name its own fake IP from `198.18.0.0/15`
and remembers which device that is.

That address works for everything: connections, `ping` (the round trip is
real), browsers. It just isn't the device's own. To see the real address
and what else claims it:

```sh
tailmux resolve db.home          # the device's real address(es)
tailmux resolve 100.64.0.1       # which tailnet that address goes to; "contested" lists the others
```

You can always use the real address directly, e.g. `ping 100.101.102.103`.
If several tailnets claim it, it goes to the one `tailmux resolve` shows.

A name also gets a fake IP when its real address isn't routed into the TUN,
for example because it overlaps your LAN or because `"peer_routes": false`.
Set `"tun": {"real_ips": false}` to always use fake IPs.

## Sleep, wake and changing networks

After the Mac wakes up or the network changes (new Wi-Fi, new address),
tailmux does three things:

- re-applies its routes and DNS configuration;
- re-checks which subnets overlap your current LAN;
- flushes the OS DNS cache, once right away and again 20 seconds later, after
  the tailnets have reconnected.

A tailnet that's reconnecting keeps its names and routes in the meantime.
Lookups get a "try again" answer rather than a cached "doesn't exist".

If anything ever looks stale, `tailmux repair` does the same by hand.

## Limits

- On Linux and Windows, bare hostnames like `db` still need a suffix in TUN
  mode (`db.home`). Automatic search domains are currently macOS only.
- IPv6 ping isn't forwarded.
