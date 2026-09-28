# tailmux

[![CI](https://github.com/GrowlyX/tailmux/actions/workflows/ci.yml/badge.svg)](https://github.com/GrowlyX/tailmux/actions/workflows/ci.yml)

**Be on all your tailnets at once.** Tailscale's client joins one tailnet at a time;
tailmux joins them all and sends each connection to the tailnet that owns it.

<img src="docs/panel-dark.png" width="340" alt="tailmux menu bar">

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
- [Development](docs/development.md): the in-process test lab, CI, releases
