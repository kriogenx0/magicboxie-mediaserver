#!/bin/bash
# The player (perfct-ink/magicboxie-player) belongs only on the Pi Zero in the
# car. In October 2026 it was installed on this media server Pi by mistake,
# which renamed it magicboxie-player, turned off nginx and could take over the
# magicboxie-self-update units (both repos use that name). Run by pi-setup.sh
# before it installs this server's own units, nginx included; does nothing
# when no player is installed. The player's own pi/remove-from-media-server.sh
# does the same from its side, and its installer now refuses to run here.
set -uo pipefail

for unit in magicboxie-player.service magicboxie-device.service magicboxie-splash.service \
  magicboxie-wifi-startup.service magicboxie-wifi-retry.timer magicboxie-wifi-retry.service \
  magicboxie-wifi-search.service magicboxie-hotspot.service magicboxie-apt-update.timer \
  magicboxie-apt-update.service magicboxie-boot-update.service; do
  [ -e "/etc/systemd/system/$unit" ] || continue
  echo "pi-remove-player: removing $unit"
  sudo systemctl disable --now "$unit" 2>/dev/null || true
  sudo rm -f "/etc/systemd/system/$unit"
done
# The player's self-update runs make; pi-setup.sh reinstalls this server's right after.
if grep -q 'make -C' /etc/systemd/system/magicboxie-self-update.service 2>/dev/null; then
  echo "pi-remove-player: removing the player's self-update units"
  sudo systemctl disable magicboxie-self-update.timer 2>/dev/null || true
  sudo rm -f /etc/systemd/system/magicboxie-self-update.service /etc/systemd/system/magicboxie-self-update.timer
fi
sudo systemctl daemon-reload

sudo nmcli connection delete magicboxie-hotspot 2>/dev/null || true
sudo rm -rf /etc/sudoers.d/magicboxie /etc/magicboxie-hotspot-dnsmasq.conf /usr/local/lib/magicboxie \
  /var/lib/magicboxie-hotspot /run/magicboxie-boot-update.json \
  /var/lib/magicboxie/thumbnails /var/lib/magicboxie/transcoded \
  /var/lib/magicboxie/wifi-networks.json /var/lib/magicboxie/home-server.env \
  "/content/Sample Feature Film.mp4" "/content/Sample TV Episode.mp4" "/content/Sample Clip.mp4"
if [ -f /boot/firmware/cmdline.txt ]; then
  sudo sed -i 's/ video=HDMI-A-1:720x480@60//' /boot/firmware/cmdline.txt
fi
if [ "$(hostname)" = magicboxie-player ]; then
  echo "pi-remove-player: renaming this Pi back to magicboxie"
  sudo hostnamectl set-hostname magicboxie
  sudo sed -i 's/\bmagicboxie-player\b/magicboxie/g' /etc/hosts
  sudo systemctl restart avahi-daemon 2>/dev/null || true
fi
for dir in /home/*/magicboxie-player /home/*/magicboxie-device; do
  if [ -d "$dir/pi/player_app" ]; then
    echo "pi-remove-player: deleting player checkout $dir"
    sudo rm -rf "$dir"
  fi
done
