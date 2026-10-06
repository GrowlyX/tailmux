#!/bin/sh
# Removing the package takes the service it installed with it; otherwise
# systemd keeps restarting a binary that is gone. Upgrades keep it, and a
# service installed from another copy of tailmux isn't ours to remove.
set -e
unit=/etc/systemd/system/tailmux.service
# $1 is "remove" for a .deb and "0" (copies left) for an .rpm.
if { [ "$1" = remove ] || [ "$1" = 0 ]; } && [ -f "$unit" ] && grep -q '^ExecStart=/usr/bin/tailmux ' "$unit"; then
  /usr/bin/tailmux service uninstall || true
fi
