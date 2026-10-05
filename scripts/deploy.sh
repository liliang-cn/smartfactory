#!/usr/bin/env bash
# Build and publish the static site to dell VM 105 (apps).
#
#   https://factory.superleo.cn  →  aliyun Caddy (TLS)  →  tailnet 100.104.134.33:44719
#   →  apps Caddy file_server  →  /var/www/smartfactory
#
# Usage: scripts/deploy.sh
set -euo pipefail

HOST=${HOST:-ops@192.168.123.65}
DEST=/var/www/smartfactory

cd "$(dirname "$0")/.."
npm run build

ssh "$HOST" "sudo mkdir -p $DEST && sudo chown ops:ops $DEST"
# Hashed assets first, index.html last, so a visitor never gets a page whose chunks are missing.
rsync -az --exclude index.html dist/ "$HOST:$DEST/"
rsync -az dist/index.html "$HOST:$DEST/index.html"
rsync -az --delete dist/ "$HOST:$DEST/"

curl -fsS -o /dev/null -w 'apps :44719 %{http_code}\n' http://192.168.123.65:44719/
