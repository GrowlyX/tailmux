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

## Windows

Download `tailmux_<version>_x64-setup.exe` (or the `.msi`) from
[Releases](https://github.com/GrowlyX/tailmux/releases/latest) and run it.
That installs the [desktop app](desktop.md) with the tailmux service inside.
Open it and press **Install service…**; Windows asks for Administrator once.
The service runs TUN mode, so every app can reach your tailnets. Its config
lives at `%ProgramData%\tailmux\config.json`.

## Linux

Pick one. All of them are the same tailmux; the desktop app adds the tray
icon and window on top of the CLI.

| | You get | For |
| --- | --- | --- |
| Desktop app `.deb` / `.rpm` | tray app + CLI | Debian, Ubuntu, Mint / Fedora, RHEL, openSUSE |
| Desktop app `.AppImage` | tray app + CLI, nothing installed | any distro |
| `tailmux-cli` packages | CLI + daemon, no tray app | servers; `.deb`, `.rpm`, `.apk` (Alpine), `.pkg.tar.zst` (Arch) |
| [Build from source](#build-from-source) | either | any distro, your own build |
| Homebrew on Linux | CLI, built from source | `brew install growlyx/tap/tailmux` |

Everything is on [Releases](https://github.com/GrowlyX/tailmux/releases/latest),
for x86-64 (the CLI packages also for arm64):

```sh
sudo apt install ./tailmux_<version>_amd64.deb                     # Debian, Ubuntu
sudo dnf install ./tailmux-<version>-1.x86_64.rpm                  # Fedora, RHEL (zypper on openSUSE)
chmod +x tailmux_<version>_amd64.AppImage && ./tailmux_<version>_amd64.AppImage

sudo apt install ./tailmux-cli_<version>_linux_amd64.deb           # CLI only
sudo dnf install ./tailmux-cli_<version>_linux_amd64.rpm
sudo apk add --allow-untrusted ./tailmux-cli_<version>_linux_amd64.apk
sudo pacman -U ./tailmux-cli_<version>_linux_amd64.pkg.tar.zst
```

Then press **Install service…** in the app, or from a terminal:

```sh
tailmux setup                    # add your tailnets and log in
sudo tailmux service install     # systemd unit, TUN mode, config in /etc/tailmux
tailmux service status
```

`service install` uses the tailnets from your own `tailmux setup`, if you ran it first.

What tailmux needs at runtime: `ip` (iproute2) and root for TUN mode, and
systemd for `service install` (or see [without systemd](#without-systemd)).
systemd-resolved is optional. With it, TUN mode makes tailnet names work in
every app. Without it, addresses and subnets still work, and names work
through the [proxies](proxy.md).

Packages put tailmux in `/usr/bin`, and your package manager updates it.
tailmux doesn't replace it itself (see [updates](updates.md)).

### Build from source

**The CLI** needs Go. If yours is older than `go.mod` asks for, `make` lets
Go download the right version; run `GOTOOLCHAIN=local make` to refuse that.

```sh
git clone https://github.com/GrowlyX/tailmux && cd tailmux
git checkout v<version>          # a release; or stay on main
make                             # ./tailmux
sudo make install                # /usr/local/bin/tailmux; PREFIX=/usr for /usr/bin
tailmux setup
sudo tailmux service install
```

On Arch, [`packaging/arch/PKGBUILD`](../packaging/arch/PKGBUILD) does the
same as a package: `cd packaging/arch && makepkg -si`. It builds the newest
release tag, or any other ref if you set `_ref`.

**The desktop app** also needs Rust ([rustup](https://rustup.rs)), Node 22+,
pnpm (`corepack enable pnpm`) and WebKitGTK:

```sh
# Debian, Ubuntu
sudo apt install build-essential libwebkit2gtk-4.1-dev libayatana-appindicator3-dev librsvg2-dev libxdo-dev libssl-dev
# Fedora
sudo dnf install webkit2gtk4.1-devel libappindicator-gtk3-devel librsvg2-devel libxdo-devel openssl-devel && sudo dnf group install c-development
# Arch
sudo pacman -S --needed base-devel webkit2gtk-4.1 libappindicator-gtk3 librsvg xdotool openssl
# openSUSE
sudo zypper in webkit2gtk3-devel libappindicator3-1 librsvg-devel libopenssl-devel && sudo zypper in -t pattern devel_basis
```

Other distros: see [Tauri's prerequisites](https://v2.tauri.app/start/prerequisites/#linux).
Then:

```sh
make desktop                     # the CLI, then the app around it
sudo make install-desktop        # tailmux-desktop and tailmux in /usr/local/bin, plus a menu entry
```

**Install service…** in the app needs `pkexec` (polkit). Otherwise run
`sudo tailmux service install` yourself. To build `.deb`, `.rpm` or an
AppImage instead, see the [desktop README](../desktop/README.md#build-and-bundle).

Source builds never replace themselves with a release binary. They tell you
when there's a new release (`tailmux status`, the app's banner). To update:
`git pull && make && sudo make install`, and the running daemon restarts onto
the new build by itself. `sudo make uninstall` removes what `make install` put there.

### Without systemd

Run `tailmux up -tun` as root from your init system, with the system config.
Create the config with `sudo tailmux setup -config /etc/tailmux/config.json`,
then use one of these.

OpenRC (Alpine, Gentoo, Artix), `/etc/init.d/tailmux`:

```sh
#!/sbin/openrc-run
description="tailmux: all your tailnets at once"
command=/usr/bin/tailmux
command_args="up -tun -config /etc/tailmux/config.json -state-dir /var/lib/tailmux"
command_background=true
pidfile=/run/tailmux.pid
output_log=/var/log/tailmux.log
error_log=/var/log/tailmux.log

depend() {
	need net
}
```

```sh
sudo chmod +x /etc/init.d/tailmux && sudo rc-update add tailmux default && sudo rc-service tailmux start
```

runit (Void), `/etc/sv/tailmux/run`:

```sh
#!/bin/sh
exec /usr/bin/tailmux up -tun -config /etc/tailmux/config.json -state-dir /var/lib/tailmux 2>&1
```

```sh
sudo chmod +x /etc/sv/tailmux/run && sudo ln -s /etc/sv/tailmux /var/service/
```

Use `/usr/local/bin/tailmux` if you built it with `make install`.

## Uninstalling

Uninstalling the app on Windows, or removing a `.deb` or `.rpm` (the desktop
app or `tailmux-cli`), also removes the service it installed (Windows asks
for Administrator once more). The config and state are kept. To remove the
service by hand, or after using the `.AppImage` or a source build:

```sh
sudo tailmux service uninstall   # Windows: tailmux service uninstall, as Administrator
```

## Without an installer

```sh
go install github.com/GrowlyX/tailmux/cmd/tailmux@latest
```

Or download a binary from [Releases](https://github.com/GrowlyX/tailmux/releases).
The config then lives at `~/.config/tailmux/config.json` and state at `~/.local/share/tailmux`.
Override them with `-config`, `TAILMUX_CONFIG` or `TAILMUX_STATE_DIR`.
