#!/bin/bash
# Run on a 64-bit Pi. Builds before stopping the old service; preserves its
# original config/database for rollback. nginx continues to own port 80.
set -euo pipefail
cd "$(dirname "$0")/.."
[ "$(uname -m)" = aarch64 ] || { echo 'Requires 64-bit Raspberry Pi OS.' >&2; exit 1; }
sudo docker compose version >/dev/null
compose=(sudo docker compose -f deploy/pi/docker-compose.yml)
"${compose[@]}" --progress plain build

sudo mkdir -p /etc/magicboxie /var/lib/magicboxie-docker /content/movies /content/music
legacy=
for unit in magicbox magicboxie; do
  if systemctl is-active --quiet "$unit"; then legacy=$unit; break; fi
done
if ! sudo test -f /etc/magicboxie/docker.yaml; then
  source_config=/etc/magicboxie/config.yaml
  sudo test -f "$source_config" || source_config=/etc/magicbox/config.yaml
  sudo test -f "$source_config" || { echo 'Provide /etc/magicboxie/config.yaml with your password hash and TMDB token first.' >&2; exit 1; }
  # Only listener/data path change; preserve credentials and media locations.
  sudo sed -e 's|^listen_addr:.*|listen_addr: ":8080"|' \
    -e 's|^data_dir:.*|data_dir: "/var/lib/magicboxie"|' \
    "$source_config" | sudo tee /etc/magicboxie/docker.yaml >/dev/null
  sudo chmod 600 /etc/magicboxie/docker.yaml
fi
rollback() {
  "${compose[@]}" stop || true
  if [ -n "$legacy" ]; then sudo systemctl start "$legacy"; fi
}
trap rollback ERR
if [ -n "$legacy" ]; then sudo systemctl stop "$legacy"; fi
# Copy only on the first migration, after the old writer has stopped.
if ! sudo test -f /var/lib/magicboxie-docker/magicboxie.sqlite; then
  for old in /var/lib/magicboxie /var/lib/magicbox; do
    if sudo test -f "$old/magicboxie.sqlite" || sudo test -f "$old/magicbox.sqlite"; then
      sudo cp -a "$old/." /var/lib/magicboxie-docker/
      if ! sudo test -f /var/lib/magicboxie-docker/magicboxie.sqlite; then
        sudo mv /var/lib/magicboxie-docker/magicbox.sqlite /var/lib/magicboxie-docker/magicboxie.sqlite
      fi
      break
    fi
  done
fi
"${compose[@]}" up -d
healthy=false
for ((i=0; i<60; i++)); do
  if curl -fsS http://127.0.0.1:8080/api/health >/dev/null; then healthy=true; break; fi
  sleep 1
done
if [ "$healthy" != true ]; then
  "${compose[@]}" logs --tail=50
  false
fi
if [ -n "$legacy" ]; then sudo systemctl disable "$legacy"; fi
trap - ERR
echo 'MagicBoxie is running in Docker on port 8080 behind the existing nginx proxy.'
