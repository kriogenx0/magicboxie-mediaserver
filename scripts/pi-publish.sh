#!/bin/bash
# Publishes the current checkout to a MagicBoxie Raspberry Pi and runs the
# idempotent on-device setup/build. Local configs and generated data never
# leave the development machine.
set -euo pipefail

SSH_TARGET="${MAGICBOXIE_SSH_TARGET:-admin@magicboxie.lan}"
REMOTE_DIR="${MAGICBOXIE_REMOTE_DIR:-/home/admin/magicboxie-web}"
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_DIR=$(cd "$SCRIPT_DIR/.." && pwd)

echo "==> Preparing $REMOTE_DIR on $SSH_TARGET"
ssh "$SSH_TARGET" "mkdir -p '$REMOTE_DIR'"

echo "==> Syncing MagicBoxie source"
rsync -az --delete \
  --exclude .git/ \
  --exclude bin/ \
  --exclude content/ \
  --exclude data/ \
  --exclude frontend/node_modules/ \
  --exclude internal/web/dist/ \
  --exclude configs/magicbox.yaml \
  --exclude configs/magicboxie.yaml \
  --exclude configs/magicboxie.local.yaml \
  "$REPO_DIR/" "$SSH_TARGET:$REMOTE_DIR/"

echo "==> Building and installing on $SSH_TARGET"
ssh -tt "$SSH_TARGET" "cd '$REMOTE_DIR' && ./scripts/pi-setup.sh"
