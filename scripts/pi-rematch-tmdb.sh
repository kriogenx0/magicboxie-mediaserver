#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
sudo docker compose -f deploy/pi/docker-compose.yml exec -T magicboxie magicboxie rematch-tmdb
