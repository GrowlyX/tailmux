# Security Policy

## Supported versions

tailmux [updates itself](docs/updates.md), so only the latest release gets
security fixes. If you turned automatic updates off, run `tailmux update` (or
your package manager) to pick up a fix.

| Version          | Supported          |
| ---------------- | ------------------ |
| 1.x (latest)     | :white_check_mark: |
| 1.x (older)      | :x: upgrade        |
| < 1.0            | :x:                |

## Reporting a vulnerability

Please don't open a public issue for security problems.

Report it privately through GitHub instead:
[**Report a vulnerability**](https://github.com/GrowlyX/tailmux/security/advisories/new)
(the Security tab, then "Report a vulnerability").

Include what you can:

- tailmux version (`tailmux version`), OS, and install method
- whether it runs in TUN mode (as root) or proxy mode
- steps to reproduce, or a proof of concept
- what an attacker gains

### What to expect

- **Acknowledgement** within 3 business days.
- **Assessment** within 10 business days: we confirm the issue, or explain why
  we don't consider it a vulnerability.
- **Updates** at least weekly while we work on a fix.
- **Fix and disclosure**: we ship a patched release, publish a GitHub security
  advisory (with a CVE when warranted), and credit you unless you'd rather we
  didn't. We aim to fix within 90 days and will agree a disclosure date with you.

If we decline the report, we'll tell you why, and you're free to disclose it.

## Scope

In scope:

- The `tailmux` daemon and CLI, including TUN mode running as root
- The SOCKS5 and HTTP proxies, PAC file, DNS server and local HTTP API
  (default `127.0.0.1:1055`/`1056`), e.g. anything reachable from other
  hosts or that lets another local user or a web page control the daemon
- Routing that sends traffic to the wrong tailnet or leaks it outside one
- Handling of auth keys, node state and config files
- The self-updater (checksum verification, privilege handling) and the
  Homebrew, Linux and Windows packaging
- The macOS menu bar app and the desktop app

Out of scope:

- Vulnerabilities in Tailscale, Headscale or Mullvad themselves. Report those
  to [Tailscale](https://tailscale.com/security) or the relevant project
  (though tell us too if tailmux makes them worse)
- Attacks that already require root or the same user account as the daemon
- Exposing the proxies or API on a non-loopback address you configured
  yourself, unless tailmux does so without saying

Thanks for helping keep tailmux and its users safe.
