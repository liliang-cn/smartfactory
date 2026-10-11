#!/usr/bin/env bash
# One binary: the front end (web/) embedded into the Go server (server/).
#
#   scripts/build.sh              → server/bin/smartfactory (this machine)
#   GOOS=linux GOARCH=amd64 scripts/build.sh   → server/bin/smartfactory-linux-amd64
set -euo pipefail
cd "$(dirname "$0")/.."

(cd web && npm run build && npm run build:engine)
rm -rf server/web/dist && mkdir -p server/web/dist && cp -R web/dist/. server/web/dist/ && touch server/web/dist/.keep

version=$(git describe --always --dirty 2>/dev/null || echo dev)
out=server/bin/smartfactory
[ -n "${GOOS:-}" ] && out="$out-${GOOS}-${GOARCH:-amd64}"
(cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "../$out" ./cmd/smartfactory)
ls -lh "$out"
