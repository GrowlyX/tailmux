<p align="center">
  <img src="docs/banner.svg" alt="tailmux: be on all your tailnets at once" width="720">
</p>

<p align="center">
  <a href="https://github.com/GrowlyX/tailmux/actions/workflows/ci.yml"><img src="https://github.com/GrowlyX/tailmux/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/GrowlyX/tailmux/releases/latest"><img src="https://img.shields.io/github/v/release/GrowlyX/tailmux" alt="latest release"></a>
</p>

Tailscale's client joins one tailnet at a time. tailmux joins them all and sends
each connection to the tailnet that owns it.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/panel-dark.png">
    <img src="docs/panel-light.png" width="372" alt="tailmux menu bar: four tailnets with switches and live throughput">
  </picture>
</p>

```sh
brew install growlyx/tap/tailmux
tailmux setup                        # add tailnets, log in
sudo brew services start tailmux     # every app can now reach every tailnet
```

`ssh db.home`, `curl http://grafana.work`, `psql -h db.corp.internal`: names
and subnets from every tailnet work side by side, even when two tailnets use the same IPs.

## Docs

- [Install](docs/install.md): Homebrew, service modes, without Homebrew
- [Setup and config](docs/setup.md): the `tailmux setup` TUI and every config key
- [Routing](docs/routing.md): how tailmux picks a tailnet, and what happens on conflicts
- [TUN mode](docs/tun.md): proxy-free, for every app
- [Proxy mode](docs/proxy.md): SOCKS5, HTTP, PAC, ssh, kubectl
- [Menu bar app](docs/menubar.md): switches, throughput, local API
- [Updates](docs/updates.md): automatic, and how to turn that off
- [Development](docs/development.md): the in-process test lab, CI, releases
