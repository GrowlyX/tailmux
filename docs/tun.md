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
- answers those names with a **fake IP** from `198.18.0.0/15`, unique per name.
  That keeps `web.work` and `web.home` apart even when both are really
  100.64.0.1.

A userspace TCP/IP stack (gVisor, the same one Tailscale uses) terminates each
TCP and UDP flow. tailmux maps fake IPs back to names and re-dials each flow
through the owning tailnet. So `ssh db.home`, `psql -h db.corp.internal` and
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
