# Install

## Homebrew (macOS, Linux)

```sh
brew install growlyx/tap/tailmux
tailmux setup
```

This builds the CLI from source. On macOS it also builds the [menu bar app](menubar.md).
The config lives at `$(brew --prefix)/etc/tailmux/config.json` and state at
`$(brew --prefix)/var/lib/tailmux`. The CLI and the background service share both.

## Running it

| | Command | Reaches tailnets from |
| --- | --- | --- |
| TUN mode | `sudo brew services start tailmux` | every app ([how](tun.md)) |
| Proxy mode | `brew services start tailmux` | apps you point at the proxy ([how](proxy.md)) |
| Foreground | `tailmux up` (add `-tun` and `sudo` for TUN) | same as above |

TUN mode comes from `"tun": {"enabled": true}` in the config. `tailmux setup` toggles it with `t`.
Choose one of TUN mode or proxy mode and stick with it: state files are owned by
whichever user runs the service.

Logs go to `$(brew --prefix)/var/log/tailmux.log`.

## Without Homebrew

```sh
go install github.com/GrowlyX/tailmux/cmd/tailmux@latest
```

Or download a binary from [Releases](https://github.com/GrowlyX/tailmux/releases).
The config then lives at `~/.config/tailmux/config.json` and state at `~/.local/share/tailmux`.
Override them with `-config`, `TAILMUX_CONFIG` or `TAILMUX_STATE_DIR`.
