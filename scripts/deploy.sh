#!/usr/bin/env bash
# Build the single binary and roll it out.
#
#   scripts/deploy.sh          the demo installations on dell VM 105 apps, one per demo company:
#                              https://<id>.factory.superleo.cn → aliyun Caddy → tailnet → apps Caddy :44719 → smartfactory@<id>
#   scripts/deploy.sh edge     dell VM 104 infra, two services from the same binary:
#                              smartfactory-edge     protocol gateway in front of the dell host's real sensors
#                                                    (node_exporter on 192.168.123.98:9100)
#                              smartfactory-devices  emulated equipment of every plant, one MQTT client /
#                                                    Modbus slave / OPC UA server per plant
#
# Configs live next to the binary (/opt/smartfactory/*.json, 0600) and are not in the repo; see server/examples.
set -euo pipefail
target=${1:-server}
cd "$(dirname "$0")/.."

case $target in
  # the demo companies, each its own installation (smartfactory@<id>, https://<id>.factory.superleo.cn)
  server) host=ops@192.168.123.65 unit='$(systemctl list-units --plain --no-legend "smartfactory@*" | cut -d" " -f1)' ;;
  edge) host=ops@192.168.123.64 unit="smartfactory-edge smartfactory-devices" ;;
  *) echo "usage: $0 [server|edge]" >&2; exit 2 ;;
esac

GOOS=linux GOARCH=amd64 scripts/build.sh
bin=server/bin/smartfactory-linux-amd64
sum=$(shasum -a 256 "$bin" | cut -d' ' -f1)
scp -q "$bin" "$host:/tmp/smartfactory.new"
ssh "$host" "set -e
  echo '$sum  /tmp/smartfactory.new' | sha256sum -c --quiet
  [ -f /opt/smartfactory/bin/smartfactory ] && sudo cp /opt/smartfactory/bin/smartfactory /opt/smartfactory/bin/smartfactory.bak
  sudo install -m 755 /tmp/smartfactory.new /opt/smartfactory/bin/smartfactory && rm /tmp/smartfactory.new
  units=\"$unit\"
  sudo systemctl restart \$units && sleep 2 && systemctl is-active \$units | tr '\\n' ' '"

if [ "$target" = server ]; then
  curl -fsS -H "Host: huaduan.factory.superleo.cn" http://192.168.123.65:44719/api/site | tr -d ' \n'; echo
fi
