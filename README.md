# MagicBoxie Web (media server)

The MagicBoxie media server, or home cloud: a Go backend with a web frontend
that imports, stores and transcodes the movie library and serves the web app.
Production runs on a Raspberry Pi under systemd (`make pi-setup`,
`make pi-install`; see the Pi section at the top of the `Makefile`). Docker
is for local development (`make dev`).

## The two Raspberry Pis

MagicBoxie runs on two separate Raspberry Pis:

| | **Media server (home cloud)** | **Player** |
| --- | --- | --- |
| Repository | `magicboxie-web` (this repo) | `magicboxie-player` |
| Hardware | Raspberry Pi | Raspberry Pi Zero 2 W |
| Hostname | `magicboxie` (`magicboxie.lan`, `magicboxie.local`) | `magicboxie-player` (`magicboxie-player.local`) |
| Where it lives | At home on the home network | In the car, playing on the Honda Pilot screen over an HDMI-to-composite converter |
| Job | Imports, stores and transcodes the movie library; serves the web app | Plays movies, serves its own control page and API, BLE control from the iOS app |

The player is offline most of the time. Whenever it is on the home Wi-Fi it
checks in with this server at `http://magicboxie.lan`, registers itself and
downloads any movies it doesn't have yet. Deploy each repo only to its own Pi.
