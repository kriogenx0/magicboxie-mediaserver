# Raspberry Pi Docker deployment

Run `make deploy` (or `make publish`) from your development checkout. It syncs source to `admin@magicboxie.lan:/home/admin/magicboxie-web`, builds the ARM64 image there, and starts Docker Compose behind nginx on port 80. Setup installs `deploy/pi/nginx/magicboxie.conf` as nginx's default site, so the app answers on any name or IP, and removes nginx's stock welcome page.

Override the connection with `MAGICBOXIE_SSH_TARGET` and `MAGICBOXIE_REMOTE_DIR`. The Pi needs 64-bit Raspberry Pi OS, Docker with Compose and sudo access. This workflow no longer installs a host Go/Node toolchain. GitHub runs tests only. The Pi pulls `main` itself (see Self-update below).

## Self-update

Like the player, the Pi keeps itself on the latest `main`. Setup installs `magicboxie-self-update.timer`, which runs `scripts/pi-self-update.sh` a few minutes after boot and every night around 4 AM. The script fetches `main` from GitHub and, only if it differs from what is installed, checks it out and runs `pi-setup.sh`. The old container keeps serving while the new image builds, and a failed start rolls back. Offline runs just retry next time.

The admin Info page shows the installed and latest commits and has an **Update now** button. The container can't reach the host's systemd, so the button writes `update-request` into the data dir (`/var/lib/magicboxie-docker` on the host) and `magicboxie-self-update.path` starts the update. Status comes back the same way, through `update-status.json`. Logs: `journalctl -u magicboxie-self-update`.

The first run turns the deployed copy into a git checkout. `make deploy` still works for trying something out, but the next nightly run puts the Pi back on `main`.

On-device commands: `./scripts/pi-setup.sh` builds and deploys; `make pi-logs` follows container logs.

## Existing installation

The first migration copies the configuration from `/etc/magicboxie/config.yaml` or the older `/etc/magicbox/config.yaml` to `/etc/magicboxie/docker.yaml`. Credentials are preserved. The listener becomes `:8080` inside the container; Docker exposes it only on host loopback.

After building successfully, setup stops the existing service and copies its data to `/var/lib/magicboxie-docker`. The legacy SQLite filename is renamed in the copy. Original data/configuration remain untouched. `/content` remains the media location; custom media paths require corresponding Compose bind mounts.

If startup fails, setup stops the container and restarts the previously active service. After a successful health check, that old service is disabled. Docker's restart policy starts the container after reboot. To manually roll back immediately after migration, stop Compose and re-enable/start the original service. Later container writes are not present in the old database.

## New installation

Provide `/etc/magicboxie/config.yaml` based on `configs/magicboxie.example.yaml`, including a password hash and TMDB token, before running setup. Existing Docker configuration and data are retained on later deployments.

## Public access (magicboxie.homecloud.one)

The Pi can also be reached from outside at `https://magicboxie.homecloud.one`, through the shared VPS. A password is only needed there; at home (`magicboxie.lan`, or the Pi's IP) the web UI opens without one.

How it decides: the Pi's nginx tags each request with `X-MagicBoxie-Network`. A request straight from a private address with no `X-Forwarded-For` is `lan`; everything else is `public`, including everything the VPS forwards over the tunnel (its nginx always adds `X-Forwarded-For`, and the tunnel subnet 10.9.1.0/24 counts as public even without it). `docker-compose.yml` sets `MAGICBOXIE_TRUST_NETWORK_HEADER=1` so the app acts on the tag; elsewhere (magicboxie.com) the tag is ignored and nothing changes.

- **At home** the web UI gets a home-only token from `POST /api/auth/lan` without a password. Jellyfin apps can log in with any password and also get a home-only token. Home-only tokens are refused through the public address.
- **In public** every authenticated endpoint needs the real password, and so do the device check-in and download endpoints that are open at home. Poster images stay open, as in Jellyfin.

To publish it, follow the Magic Boxie section of home-cloud's `docs/README.md` (perfct-ink/home-cloud). The VPS reaches this nginx over the existing DropSync tunnel at `10.9.1.2:80`. The route is a Coolify (Traefik) dynamic config; `deploy/public/` here is the legacy plain-nginx alternative (`MAGICBOXIE_PI_UPSTREAM=10.9.1.2:80 deploy/public/deploy.sh`).

Both VPS routes rate-limit login attempts (10 a minute per IP).
