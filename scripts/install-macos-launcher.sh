#!/bin/bash
set -euo pipefail
[[ "$(uname -s)" == Darwin ]] || { echo 'macOS required.' >&2; exit 1; }
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
app="$HOME/Applications/CLIProxyAPI Service.app"
launcher="$app/Contents/MacOS/cliproxyapi-service"
if [[ -f "$launcher" ]]; then
  echo "Launcher already installed: $app"
  exit 0
fi
destination="$app"
mkdir -p "$HOME/Applications"
stage="$(mktemp -d "$HOME/Applications/.cliproxyapi-launcher-XXXXXXXX")"
trap 'rm -rf "$stage"' EXIT
app="$stage/CLIProxyAPI Service.app"
launcher="$app/Contents/MacOS/cliproxyapi-service"
mkdir -p "$app/Contents/MacOS"
go -C "$repo_root" build -o "$launcher" ./cmd/macos-launcher
/usr/bin/plutil -create xml1 "$app/Contents/Info.plist"
/usr/bin/plutil -insert CFBundleIdentifier -string com.byvict.cliproxyapi.service "$app/Contents/Info.plist"
/usr/bin/plutil -insert CFBundleExecutable -string cliproxyapi-service "$app/Contents/Info.plist"
/usr/bin/plutil -insert CFBundleName -string 'CLIProxyAPI Service' "$app/Contents/Info.plist"
/usr/bin/plutil -insert CFBundlePackageType -string APPL "$app/Contents/Info.plist"
/usr/bin/plutil -insert LSUIElement -bool YES "$app/Contents/Info.plist"
/usr/bin/codesign --force --sign - --identifier com.byvict.cliproxyapi.service "$app"
mv "$app" "$destination"
echo "Launcher installed: $destination"
echo 'Open it with --check to authorize removable-volume access before deployment.'
