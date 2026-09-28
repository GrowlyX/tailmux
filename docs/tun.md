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
- answers those names with a **fake IP** from `198.18.0.0/15`, unique per name.
  That keeps `web.work` and `web.home` apart even when both are really
  100.64.0.1.

A userspace TCP/IP stack (gVisor, the same one Tailscale uses) terminates each
TCP and UDP flow. tailmux maps fake IPs back to names and re-dials each flow
through the owning tailnet. So `ssh db.home`, `psql -h db.corp.internal` and
`http://grafana.lab` in a browser all just work.

## Safety

- **Your LAN stays yours.** A subnet that overlaps a local network is skipped
  (the log says so). Pin it to force it.
- **The official Tailscale app can coexist.** tailmux leaves `100.64.0.0/10`
  alone unless you set `"cgnat": true`. Reach devices by name instead.
- **No loops.** Traffic that arrives on the TUN is never dialed "directly".
  If no tailnet claims it, the connection is refused.
- **Clean exit.** Routes go away with the interface. Resolver files carry a
  marker and are removed on exit, or at the next start after a crash.

## Limits

- ICMP (ping) isn't forwarded.
- Bare hostnames like `db` need a suffix in TUN mode (`db.home`), because
  tailmux doesn't install search domains.
