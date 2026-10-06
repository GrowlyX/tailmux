# Desktop app (Windows and Linux)

<img src="../desktop/screenshots/panel-dark.png" width="340" alt="tray panel">

The tray app for Windows and Linux lives in [`desktop/`](../desktop/README.md).
It is a Tauri 2 app that mirrors the [macOS menu bar app](menubar.md):
the same dot-grid icon, panel, and main window with Overview, Tailnets,
Devices, Exit node, Settings and Logs.

- Talks to the daemon's local HTTP API (see [menubar.md](menubar.md#api))
  from Rust, with the `X-Tailmux` header on every changing request.
- **Install service…** (Settings, or the offline view) runs the bundled
  `tailmux service install` with elevation: `pkexec` on Linux, a UAC prompt
  on Windows. TUN mode needs the service.
- **Exit node** (a row in the panel, and its own page) picks the device
  that internet traffic leaves through, like the official client's Exit
  Node menu: None, your own exit nodes per tailnet, and Mullvad locations
  by country and city with a "Best available" pick each, searchable. It
  applies right away; when the chosen node can't be used the row turns
  orange and says why (that traffic is blocked rather than going direct).
- **Start at login** registers the app with the OS (XDG autostart or the
  Windows registry).

Build, development and screenshot instructions are in the
[desktop README](../desktop/README.md).
