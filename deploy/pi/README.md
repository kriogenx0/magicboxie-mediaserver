# Raspberry Pi Docker deployment

Run `make deploy` (or `make publish`) from your development checkout. It syncs source to `admin@magicboxie.lan:/home/admin/magicboxie-web`, builds the ARM64 image there, and starts Docker Compose behind the Pi's existing nginx proxy on port 80.

Override the connection with `MAGICBOXIE_SSH_TARGET` and `MAGICBOXIE_REMOTE_DIR`. The Pi needs 64-bit Raspberry Pi OS, Docker with Compose, sudo access, and nginx proxying to `127.0.0.1:8080`. This workflow no longer installs a host Go/Node toolchain. GitHub runs tests only; deployment is explicit.

On-device commands: `./scripts/pi-setup.sh` builds and deploys; `make pi-logs` follows container logs.

## Existing installation

The first migration copies the configuration from `/etc/magicboxie/config.yaml` or the older `/etc/magicbox/config.yaml` to `/etc/magicboxie/docker.yaml`. Credentials are preserved. The listener becomes `:8080` inside the container; Docker exposes it only on host loopback.

After building successfully, setup stops the existing service and copies its data to `/var/lib/magicboxie-docker`. The legacy SQLite filename is renamed in the copy. Original data/configuration remain untouched. `/content` remains the media location; custom media paths require corresponding Compose bind mounts.

If startup fails, setup stops the container and restarts the previously active service. After a successful health check, that old service is disabled. Docker's restart policy starts the container after reboot. To manually roll back immediately after migration, stop Compose and re-enable/start the original service. Later container writes are not present in the old database.

## New installation

Provide `/etc/magicboxie/config.yaml` based on `configs/magicboxie.example.yaml`, including a password hash and TMDB token, before running setup. Existing Docker configuration and data are retained on later deployments.
