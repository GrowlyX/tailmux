#!/bin/sh
# Shown after installing the tailmux-cli package. Nothing runs until the
# user sets it up: the service needs their tailnets first.
cat <<'MSG'

tailmux is installed. Next:
  tailmux setup                  add your tailnets and log in
  sudo tailmux service install   run it as a systemd service (TUN mode)
Without systemd, run `tailmux up -tun` as root from your init system
(docs/install.md has OpenRC and runit examples).

MSG
