# Updates

tailmux keeps itself current. Every six hours (and 30 seconds after
starting) the daemon checks the latest GitHub release. If there's a newer
one, it installs it the same way you installed tailmux:

- **Homebrew**: `brew update` and `brew upgrade growlyx/tap/tailmux`, run as
  the user who owns Homebrew even when the daemon runs as root. That also
  upgrades the [menu bar app](menubar.md), which relaunches itself.
- **Binary** (a release download or `go install`): downloads the archive for
  this OS and CPU, checks it against the release's `checksums.txt`, and swaps
  the executable in place.

The daemon watches its own executable. When an upgrade replaces it, the
daemon shuts down cleanly (removing the TUN device and resolver files) and
re-executes the new binary. That happens whether the update came from
tailmux, the menu bar, `brew upgrade` or anything else. There's no sudo
prompt and no `brew services restart`.

Starting the service with `sudo brew services start` makes Homebrew's copy
of tailmux root-owned, so a later non-root `brew upgrade` can't delete the
old version. When tailmux runs as root it removes those old versions itself
(everything except the running one and the one Homebrew links), like
`brew cleanup` would.

## Doing it by hand

```sh
tailmux update -check   # is there a newer release?
tailmux update          # install it now
tailmux status          # shows "update: ... is available" when there is one
```

The menu bar shows a banner with an **Update** button when a release is out.

## Turning it off

In `tailmux setup`, press `o` and turn off "Install updates automatically".
Or set it in the config:

To check and notify but never install:

```json
"updates": { "auto": false }
```

To not check at all:

```json
"updates": { "check": false }
```
