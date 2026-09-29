#!/bin/sh
# Screenshots the real desktop app on a virtual display (CI). Run inside
# `xvfb-run ... dbus-run-session --`: the tray and the single-instance
# lock need a D-Bus session bus, like any real desktop has.
# usage: scripts/ci-app-screenshot-linux.sh <app binary> <out.png>
set -u
app=$1
out=$2
WEBKIT_DISABLE_DMABUF_RENDERER=1 "$app" --page overview > app.log 2>&1 &
pid=$!
sleep 15
import -window root "$out"
if ! kill -0 "$pid" 2>/dev/null; then
  echo "the app exited early:"
  cat app.log
  exit 1
fi
kill "$pid"
