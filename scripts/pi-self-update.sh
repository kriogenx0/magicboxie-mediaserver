#!/bin/bash
# Keeps the media server Pi on the latest main, like the player's self-update.
# Run as root on the Pi host by magicboxie-self-update.service: daily and
# shortly after boot (the .timer), and whenever the admin's "Update now"
# drops /var/lib/magicboxie-docker/update-request (the .path unit).
#
# Fetches main and, only if it differs from the revision last installed,
# checks it out and runs pi-setup.sh, which builds the new image before
# touching the running container (and rolls back if it won't start). Offline
# is not an error; the next run retries. Progress goes to update-status.json
# in the container's data dir, where the admin Info page reads it.
set -uo pipefail
cd "$(dirname "$0")/.."
REPO_URL="${MAGICBOXIE_REPO_URL:-https://github.com/kriogenx0/magicboxie-mediaserver.git}"
BRANCH="${MAGICBOXIE_BRANCH:-main}"
STATE_DIR=/var/lib/magicboxie-docker
STATUS="$STATE_DIR/update-status.json"
INSTALLED="$STATE_DIR/installed-revision"

mkdir -p "$STATE_DIR"
rm -f "$STATE_DIR/update-request"
installed=$(cat "$INSTALLED" 2>/dev/null || true)
latest=

status() { # state message
  local tmp
  tmp=$(mktemp "$STATE_DIR/.update-status.XXXXXX")
  printf '{"state":"%s","installed":"%s","latest":"%s","checked_at":"%s","message":"%s"}\n' \
    "$1" "$installed" "$latest" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$2" >"$tmp"
  chmod 644 "$tmp"
  mv -f "$tmp" "$STATUS"
}

# The checkout belongs to whoever deploys with `make deploy`; keep git's files theirs.
owner=$(stat -c %U .)
as_owner() { if [ "$(id -un)" = "$owner" ]; then "$@"; else runuser -u "$owner" -- "$@"; fi; }

status checking "Checking for an update"
command -v git >/dev/null || apt-get install -y git >/dev/null || { status failed "git is not installed"; exit 0; }
# `make deploy` copies source without .git, so the first run turns the copy into a checkout.
if [ ! -d .git ]; then
  as_owner git init -q -b "$BRANCH" && as_owner git remote add origin "$REPO_URL" || { status failed "Could not set up git"; exit 1; }
fi
as_owner git remote set-url origin "$REPO_URL"
if ! as_owner git fetch -q origin "$BRANCH"; then
  echo "self-update: fetch failed (no internet?) - will retry on the next run"
  status offline "Could not reach GitHub; will retry later"
  exit 0
fi
latest=$(as_owner git rev-parse FETCH_HEAD)

if [ "$installed" = "$latest" ]; then
  echo "self-update: already up to date at $latest"
  status current "Up to date"
  exit 0
fi

echo "self-update: ${installed:-unknown} -> $latest, installing"
status installing "Building the new version; the server restarts when it's ready"
# Untracked leftovers of an older copy could break the build; ignored files
# (none on the Pi besides build output) stay.
as_owner git reset -q --hard FETCH_HEAD && as_owner git clean -qfd
if MAGICBOXIE_REVISION="$latest" ./scripts/pi-setup.sh; then
  installed=$latest
  echo "$latest" >"$INSTALLED"
  status current "Updated"
else
  status failed "Installing $latest failed; the previous version is still running. See: journalctl -u magicboxie-self-update"
  exit 1
fi
