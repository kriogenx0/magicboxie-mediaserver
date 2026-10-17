#!/bin/bash
# Publishes the Pi's media server at https://magicboxie.homecloud.one through
# the shared nginx host. Same bootstrap-vhost -> Certbot webroot -> HTTPS-vhost
# flow as deploy/install-redirect/deploy.sh. Safe to re-run.
#
# Needs, before the first run:
#   - DNS: magicboxie.homecloud.one A record -> 104.131.183.186
#   - a tunnel from the Pi to this host, so the VPS can reach the Pi's nginx;
#     pass the Pi's tunnel address as MAGICBOXIE_PI_UPSTREAM (host[:port])
#   - server/sudoers.d/magicboxie-public installed (see that file)
#
#   MAGICBOXIE_PI_UPSTREAM=10.9.2.2:80 deploy/public/deploy.sh
set -euo pipefail

SSH_TARGET="${MAGICBOXIE_PUBLIC_SSH_TARGET:-deploy@104.131.183.186}"
DOMAIN="magicboxie.homecloud.one"
CERTBOT_EMAIL="simplex0@gmail.com"
PI_UPSTREAM="${MAGICBOXIE_PI_UPSTREAM:?set MAGICBOXIE_PI_UPSTREAM to the Pi tunnel address, e.g. 10.9.2.2:80}"
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
CERTBOT_CMD="sudo -n /usr/bin/certbot certonly --webroot -w /var/www/certbot -d $DOMAIN --non-interactive --agree-tos -m $CERTBOT_EMAIL"

UPSTREAM_PATTERN='^[A-Za-z0-9.:-]+$'
if [[ ! "$PI_UPSTREAM" =~ $UPSTREAM_PATTERN ]]; then
  echo "MAGICBOXIE_PI_UPSTREAM must be host[:port], got: $PI_UPSTREAM" >&2
  exit 1
fi

install_vhost() {
  local src="$1"
  local filename
  filename=$(basename "$src")

  sed "s|__PI_UPSTREAM__|$PI_UPSTREAM|g" "$src" | ssh "$SSH_TARGET" "cat > '/tmp/$filename'"
  ssh "$SSH_TARGET" "sudo -n /bin/cp '/tmp/$filename' '/etc/nginx/sites-available/$DOMAIN' && sudo -n /bin/ln -sf '/etc/nginx/sites-available/$DOMAIN' '/etc/nginx/sites-enabled/$DOMAIN' && sudo -n /usr/sbin/nginx -t && sudo -n /bin/systemctl reload nginx"
}

if ssh "$SSH_TARGET" "sudo -n /usr/bin/test -f '/etc/letsencrypt/live/$DOMAIN/cert.pem'"; then
  echo "==> Certificate already exists"
else
  echo "==> Installing HTTP bootstrap vhost"
  install_vhost "$SCRIPT_DIR/server/sites-available/$DOMAIN.bootstrap.conf"

  echo "==> Requesting TLS certificate"
  ssh "$SSH_TARGET" "$CERTBOT_CMD"
fi

echo "==> Installing HTTPS proxy vhost (upstream $PI_UPSTREAM)"
install_vhost "$SCRIPT_DIR/server/sites-available/$DOMAIN.conf"

echo "==> Checking the Pi answers through the tunnel"
ssh "$SSH_TARGET" "curl -fsS --max-time 10 http://$PI_UPSTREAM/api/health" >/dev/null \
  && echo "    ok" || echo "    WARNING: the VPS can't reach http://$PI_UPSTREAM yet -- is the tunnel up?"

echo "Done. https://$DOMAIN serves the Pi; a password is required there, not on the LAN."
