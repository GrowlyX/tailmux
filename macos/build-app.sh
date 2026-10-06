#!/bin/sh
# Builds TailmuxBar.app, the menu bar companion.
# usage: macos/build-app.sh [out-dir] [version]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${1:-$here/build}
version=${2:-0.0.0-dev}

cd "$here/TailmuxBar"
# --disable-sandbox: SwiftPM's own sandbox can't nest inside Homebrew's.
swift build -c release --disable-sandbox
bin="$(swift build -c release --disable-sandbox --show-bin-path)/TailmuxBar"

app="$out/TailmuxBar.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin" "$app/Contents/MacOS/TailmuxBar"
cp "$here/AppIcon.icns" "$app/Contents/Resources/AppIcon.icns" # from macos/icon.py
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>TailmuxBar</string>
  <key>CFBundleDisplayName</key><string>tailmux</string>
  <key>CFBundleIdentifier</key><string>com.github.growlyx.tailmux.bar</string>
  <key>CFBundleExecutable</key><string>TailmuxBar</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version#v}</string>
  <key>CFBundleVersion</key><string>${version#v}</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSAppTransportSecurity</key>
  <dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict>
</plist>
PLIST
codesign --force --sign - "$app" >/dev/null 2>&1 || true
echo "$app"
