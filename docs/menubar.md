# Menu bar app

<img src="panel-dark.png" width="340" alt="menu bar panel">

```sh
tailmux bar
```

The Homebrew install builds it. You can also run `ln -sf $(brew --prefix)/opt/tailmux/TailmuxBar.app /Applications/`
and enable **Open at login** from its menu.

- **Icon**: Tailscale's 3×3 dot grid, with one dot lit per connected tailnet.
  Dots fill in the order that draws Tailscale's "T", so five tailnets spell the
  logo. The grid dims when the daemon isn't running.
- **Switches**: turning a tailnet off works like `tailscale down` for that one
  tailnet. Its routes and names drop out immediately, it keeps its login, and
  tailmux remembers the choice.
- **Updates**: a banner offers a new release, with an **Update** button. The
  app relaunches itself after Homebrew upgrades it.
- **Charts**: two minutes of per-tailnet throughput, plus a sparkline and the
  current rate on each row. Hover a row for its routes and byte totals.

To build it by hand, run `macos/build-app.sh`. It needs only the Command Line Tools.

## API

The app uses the daemon's local HTTP API (the `http` address, default `127.0.0.1:1056`):

| Endpoint | |
| --- | --- |
| `GET /status` | Tailnets, routes, conflicts, TUN state |
| `GET /stats` | Per-tailnet byte counters and 120 one-second rate samples |
| `GET /resolve?host=` | Routing decision for one destination |
| `GET /proxy.pac` | The PAC file |
| `GET /peers` | Every device in every tailnet |
| `POST /update` | Install the latest release now; progress shows in `/status` |
| `POST /tailnets/{name}/enable`, `/disable` | Requires an `X-Tailmux` header, which blocks cross-site requests |
