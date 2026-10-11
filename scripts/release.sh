#!/usr/bin/env bash
# Release builds: one binary per platform, front end embedded, with checksums.
#
#   scripts/release.sh   → server/bin/release/<version>/smartfactory-<version>-<os>-<arch>.tar.gz, SHA256SUMS
#
# Linux is what `smartfactory install` sets up as a systemd service; macOS runs it directly. No
# Windows build: the agent framework's process control is Unix only (WSL runs the Linux one).
set -euo pipefail
cd "$(dirname "$0")/.."

npm run build >/dev/null
npm run build:engine >/dev/null
rm -rf server/web/dist && mkdir -p server/web/dist && cp -R dist/. server/web/dist/ && touch server/web/dist/.keep

version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
out=server/bin/release/$version
rm -rf "$out" && mkdir -p "$out"
for target in linux/amd64 linux/arm64 darwin/arm64 darwin/amd64; do
  os=${target%/*} arch=${target#*/}
  name=smartfactory-$version-$os-$arch
  dir=$(mktemp -d)
  (cd server && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$dir/$name/smartfactory" ./cmd/smartfactory)
  cp README.md "$dir/$name/"
  # no macOS extended attributes: Linux tar warns about each one
  COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C "$dir" -czf "$out/$name.tar.gz" "$name"
  rm -rf "$dir"
done
(cd "$out" && shasum -a 256 smartfactory-* > SHA256SUMS)
ls -lh "$out"
