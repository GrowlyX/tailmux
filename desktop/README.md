# tailmux desktop

The Windows and Linux tray app: a Tauri 2 shell (Rust) around a small
Svelte 5 frontend. It mirrors the macOS menu bar app in
`macos/TailmuxBar`, page for page.

- **Tray icon**: Tailscale's 3x3 dot grid, one dot lit per connected tailnet
  (in the order that draws the "T"); all dots dim when the daemon is down.
  Left click toggles the panel, right click opens a menu (Linux tray icons
  only have the menu, so it gets a "Show panel" entry).
- **Panel**: throughput chart, one row per tailnet with a switch, an update
  banner, and an offline state.
- **Main window**: Overview, Tailnets (add, remove, log in), Devices,
  Settings (device name, TUN, auto-update, start at login, service install,
  files) and Logs.

All HTTP to the daemon happens in Rust (`src-tauri/src/api.rs`): the daemon
sends no CORS headers on purpose, and every changing request carries
`X-Tailmux: 1`. The frontend calls one `api` command. The daemon's address
comes from `TAILMUX_API`, else the `http` key of the first config file found
(`TAILMUX_CONFIG`; Linux `~/.config/tailmux/config.json`,
`/etc/tailmux/config.json`, `/home/linuxbrew/.linuxbrew/etc/tailmux/config.json`;
Windows `%ProgramData%\tailmux\config.json`, `%APPDATA%\tailmux\config.json`),
else `127.0.0.1:1056`.

## Layout

```
desktop/
  src/                 Svelte frontend (Panel.svelte, Main.svelte, pages/, ui/, lib/)
  src-tauri/           Rust: lib.rs (windows, commands), tray.rs, api.rs
  src-tauri/icons/     app icons (from `pnpm tauri icon`) and tray/ PNGs
  src-tauri/binaries/  the bundled `tailmux` daemon (externalBin)
  scripts/             tray-icons.py, screenshot.mjs, ensure-sidecar.mjs
  screenshots/         what CI captures, light and dark
```

## Develop

Needs Rust (1.77+), Node 22+ and pnpm. On Linux also WebKitGTK and
friends; [docs/install.md](../docs/install.md#build-from-source) lists the
packages for Debian/Ubuntu, Fedora, Arch and openSUSE. On Debian/Ubuntu:

```sh
sudo apt-get install -y libwebkit2gtk-4.1-dev libayatana-appindicator3-dev librsvg2-dev patchelf libxdo-dev build-essential
```

To build and install it from source without making a package, use the
top-level Makefile: `make desktop && sudo make install-desktop`.

```sh
cd desktop
pnpm install
TAILMUX_API=http://127.0.0.1:1056 pnpm tauri dev              # tray + hidden windows
TAILMUX_API=http://127.0.0.1:1056 pnpm tauri dev -- -- --page settings   # main window open
```

`--page <overview|tailnets|devices|settings|logs>` opens the main window on
that page at launch; otherwise only the tray icon shows.

Useful checks: `pnpm check` (svelte-check), and
`TAILMUX_API=... cargo test` in `src-tauri/` runs an integration test that
adds and removes a throwaway tailnet and round-trips the settings against
a live daemon (skipped without `TAILMUX_API`).

## Build and bundle

```sh
pnpm tauri build                 # release, all bundles for this OS
pnpm tauri build --debug         # quick local check
```

`bundle.externalBin` expects `src-tauri/binaries/tailmux-<target-triple>[.exe]`.
The release pipeline drops the daemon build there first; locally,
`scripts/ensure-sidecar.mjs` (run by `beforeBuildCommand`) writes a stub so
bundling works. The app treats a tiny stub as "not bundled" and falls back
to `tailmux` on PATH for **Install service…** (which runs
`tailmux service install` through `pkexec` on Linux and a UAC prompt on
Windows).

CI commands:

```sh
# ubuntu-latest
sudo apt-get update && sudo apt-get install -y libwebkit2gtk-4.1-dev libayatana-appindicator3-dev librsvg2-dev patchelf libxdo-dev build-essential
cd desktop && pnpm install --frozen-lockfile
cp ../dist/tailmux-linux-amd64 src-tauri/binaries/tailmux-x86_64-unknown-linux-gnu
pnpm tauri build --bundles deb,rpm,appimage
# artifacts: src-tauri/target/release/bundle/deb/*.deb, .../rpm/*.rpm, .../appimage/*.AppImage

# windows-latest (PowerShell)
cd desktop; pnpm install --frozen-lockfile
Copy-Item ..\dist\tailmux-windows-amd64.exe src-tauri\binaries\tailmux-x86_64-pc-windows-msvc.exe
pnpm tauri build --bundles msi,nsis
# artifacts: src-tauri\target\release\bundle\msi\*.msi, ...\nsis\*-setup.exe
```

## Screenshots

`scripts/screenshot.mjs` captures the panel and all five pages, light and
dark, into `screenshots/`. It serves the built `dist/` from a tiny HTTP
server whose `/api/` path proxies to the daemon (adding `X-Tailmux`), so the
pages run unchanged in headless Chromium: `src/lib/bridge.ts` falls back to
that proxy when Tauri isn't present. Capturing a Tauri webview to a file has
no cross-platform API, and Chromium draws the same DOM, so this is what CI
runs on both ubuntu-latest and windows-latest. It exits non-zero when the
daemon is unreachable.

```sh
pnpm exec playwright install chromium      # once
pnpm build
TAILMUX_API=http://127.0.0.1:21056 pnpm screenshot           # all, light and dark
pnpm screenshot --only panel,settings --dark --out /tmp/shots
```

In CI, start a daemon first (the fake lab from `internal/mux`'s `TestLab`
works: `TAILMUX_LAB=/tmp/lab/lab.json go test ./internal/mux -run TestLab -timeout 0 &`
then `go run ./cmd/tailmux up -config /tmp/lab/lab.json`, and point
`TAILMUX_API` at its `http` address).

## Tray icons

`scripts/tray-icons.py` regenerates `src-tauri/icons/tray/{light,dark}-{0..9,off}.png`
(white dots for dark taskbars and Linux panels; dark dots for Windows in
light mode). `--app` also writes `icons/app-source.png`; run
`pnpm tauri icon src-tauri/icons/app-source.png` after that.
