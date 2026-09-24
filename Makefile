BINARY := magicboxie
IMAGE := magicboxie
# dev/run listen on Docker's :8080 (docker-compose.yml); run-local listens
# on :8090 instead so it can run side by side without a port clash.
URL := $(if $(MAGICBOXIE_URL),$(MAGICBOXIE_URL),http://localhost:8080)

# Raspberry Pi (Raspbian/Raspberry Pi OS) bare-metal target: builds and
# runs directly on the Pi via systemd (deploy/systemd/magicboxie.service)
# instead of Docker -- lighter on Pi-class hardware and avoids multi-arch
# image builds. Run pi-* targets ON the Pi itself.
PI_GO_VERSION := 1.26.5
PI_ARCH := $(shell uname -m | sed -e 's/aarch64/arm64/' -e 's/armv7l/armv6l/')
# apt's go/nodejs are almost always too old for this repo's go.mod/Vite --
# pi-setup installs Go to /usr/local/go instead of relying on PATH, since a
# non-login `make` invocation right after pi-setup won't have re-sourced
# /etc/profile.d yet.
PI_GO := $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
# Where movies_dir/music_dir live on the Pi's own filesystem (e.g. an
# external drive) -- override with `make pi-setup PI_CONTENT_DIR=/mnt/media`.
PI_CONTENT_DIR ?= /content
# mDNS name this Pi answers to (http://$(PI_HOSTNAME).local) -- lets a
# magicboxie-device Pi find this server without a fixed/static IP.
PI_HOSTNAME ?= magicboxie

.PHONY: default build build-local build-web build-go run run-local dev restart deploy publish open test test-docker tidy setup pi-setup pi-install pi-run pi-logs

# Keep the no-argument workflow aligned with `make dev`.
default: dev

# Builds the full multi-stage Docker image (frontend, Go binary, ffmpeg
# runtime) -- no local node/npm/go toolchain required, and this is what gets
# deployed (see Dockerfile/docker-compose.yml).
build:
	docker build -t $(IMAGE) .

# Host-based build for fast local dev iteration without Docker overhead;
# requires node/npm and go installed locally.
build-local: build-web build-go

build-web:
	@if [ -f frontend/package.json ]; then \
		cd frontend && npm ci && npm run build ; \
	else \
		echo "frontend/package.json not found yet -- using placeholder internal/web/dist" ; \
	fi

build-go:
	go build -o bin/$(BINARY) ./cmd/magicboxie

# Runs the app the same way it's actually deployed: via Docker (see
# docker-compose.yml). Bootstraps configs/magicboxie.yaml from the example on
# first run, since docker-compose bind-mounts it directly.
run:
	@test -f configs/magicboxie.yaml || { \
		cp configs/magicboxie.example.yaml configs/magicboxie.yaml; \
		echo "Created configs/magicboxie.yaml from the example -- set auth.password_hash" \
		     "(generate one with: docker run --rm $(IMAGE) hash-password '<password>')" \
		     "before logging in."; \
	}
	docker compose up --build

# Runs the locally-built binary directly (no Docker), against
# configs/magicboxie.local.yaml -- for fast local dev iteration. Static
# assets (movies/music) and app data live under ./content and ./data.
run-local: build-local
	@test -f configs/magicboxie.local.yaml || { \
		echo "configs/magicboxie.local.yaml not found -- copy configs/magicboxie.example.yaml," \
		     "point movies_dir/music_dir/data_dir at local paths (e.g. content/movies," \
		     "content/music, data), and set auth.password_hash." ; \
		exit 1 ; \
	}
	@mkdir -p content/movies content/music data
	MAGICBOXIE_CONFIG=configs/magicboxie.local.yaml ./bin/$(BINARY)

# Same as run, but also opens the server in the browser once it's ready to
# accept connections. Ctrl+C stops the server (docker compose up runs in
# the foreground).
dev:
	@test -f configs/magicboxie.yaml || { \
		cp configs/magicboxie.example.yaml configs/magicboxie.yaml; \
		echo "Created configs/magicboxie.yaml from the example -- set auth.password_hash" \
		     "(generate one with: docker run --rm $(IMAGE) hash-password '<password>')" \
		     "before logging in."; \
	}
	@( \
		i=0 ; \
		until curl -sf "$(URL)" >/dev/null 2>&1 || [ $$i -ge 600 ]; do sleep 0.2; i=$$((i+1)); done ; \
		if curl -sf "$(URL)" >/dev/null 2>&1; then \
			$(MAKE) open ; \
		else \
			echo "Server did not respond at $(URL) within 2m -- not opening browser" >&2 ; \
		fi \
	) &
	docker compose up --build

restart: build-local
	@test -f configs/magicboxie.local.yaml || { \
		echo "configs/magicboxie.local.yaml not found -- copy configs/magicboxie.example.yaml," \
		     "point movies_dir/music_dir/data_dir at local paths (e.g. content/movies," \
		     "content/music, data), and set auth.password_hash." ; \
		exit 1 ; \
	}
	@mkdir -p content/movies content/music data
	@pkill -f './bin/magicboxie' || true
	@MAGICBOXIE_CONFIG=configs/magicboxie.local.yaml ./bin/$(BINARY) > /tmp/magicboxie.log 2>&1 & \
	 echo "Restarted MagicBoxie (PID $$!)"

# Deploy the published container image and refresh this app's production
# compose/nginx/TLS configuration. Server bootstrap remains a one-time task
# handled by deploy/server_setup.sh.
deploy:
	./deploy/deploy.sh

# Publish the current checkout to the LAN Raspberry Pi. Override the target
# with MAGICBOXIE_SSH_TARGET=user@host when needed.
publish:
	./deploy/pi/publish.sh

# Complete Raspberry Pi setup, including hostname, nginx on port 80, build,
# and systemd services. Run on the Pi itself.
setup:
	./setup.sh

# One-time, run on the Pi itself: installs Go (official tarball -- apt's
# package is far behind go.mod's requirement) + Node (via NodeSource --
# apt's version varies too much by Raspbian release to trust for Vite) +
# ffmpeg, creates the `magicboxie` system user the systemd unit runs as, and
# the directories it needs. Safe to re-run.
pi-setup:
	@echo "Detected arch: $(PI_ARCH)"
	sudo apt-get update
	sudo apt-get install -y curl ffmpeg avahi-daemon
	@if [ "$$(hostname)" != "$(PI_HOSTNAME)" ]; then \
		echo "Setting hostname to $(PI_HOSTNAME) (was $$(hostname))" ; \
		if grep -q '^127\.0\.1\.1' /etc/hosts; then \
			sudo sed -i "s/127\\.0\\.1\\.1.*/127.0.1.1\t$(PI_HOSTNAME)/" /etc/hosts ; \
		else \
			echo "127.0.1.1\t$(PI_HOSTNAME)" | sudo tee -a /etc/hosts >/dev/null ; \
		fi ; \
		sudo hostnamectl set-hostname "$(PI_HOSTNAME)" ; \
	fi
	sudo systemctl enable --now avahi-daemon
	@echo "Reachable at http://$(PI_HOSTNAME).local once setup.sh has installed nginx."
	@if ! $(PI_GO) version 2>/dev/null | grep -q "go$(PI_GO_VERSION)"; then \
		echo "Installing Go $(PI_GO_VERSION) ($(PI_ARCH))" ; \
		curl -fsSL "https://go.dev/dl/go$(PI_GO_VERSION).linux-$(PI_ARCH).tar.gz" -o /tmp/go.tar.gz ; \
		sudo rm -rf /usr/local/go ; \
		sudo tar -C /usr/local -xzf /tmp/go.tar.gz ; \
		rm /tmp/go.tar.gz ; \
		echo 'export PATH=$$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh >/dev/null ; \
	fi
	@command -v node >/dev/null 2>&1 || { \
		curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash - ; \
		sudo apt-get install -y nodejs ; \
	}
	sudo id magicboxie >/dev/null 2>&1 || sudo useradd --system --create-home --home-dir /opt/magicboxie --shell /usr/sbin/nologin magicboxie
	sudo mkdir -p /opt/magicboxie/bin /etc/magicboxie /var/lib/magicboxie $(PI_CONTENT_DIR)/movies $(PI_CONTENT_DIR)/music
	sudo chown -R magicboxie:magicboxie /opt/magicboxie /var/lib/magicboxie $(PI_CONTENT_DIR)
	@echo "Done. Open a new shell (so /usr/local/go/bin is on PATH), then: make pi-install"

# Builds on the Pi and installs the binary + systemd unit as the `magicboxie`
# system user. Never overwrites an existing /etc/magicboxie/config.yaml --
# that holds the auth password hash and TMDB token, set by hand once (see
# configs/magicboxie.example.yaml's own comments) and preserved across
# re-installs/updates.
pi-install:
	cd frontend && npm ci && npm run build
	$(PI_GO) build -o bin/$(BINARY) ./cmd/magicboxie
	sudo install -m 0755 -o magicboxie -g magicboxie bin/$(BINARY) /opt/magicboxie/bin/$(BINARY)
	sudo test -f /etc/magicboxie/config.yaml || { \
		sudo install -m 0640 -o magicboxie -g magicboxie configs/magicboxie.example.yaml /etc/magicboxie/config.yaml ; \
		echo "Created /etc/magicboxie/config.yaml from the example -- set auth.password_hash" \
		     "(generate with: /opt/magicboxie/bin/$(BINARY) hash-password '<password>')" \
		     "and tmdb.api_read_token before starting the service." ; \
	}
	sudo install -m 0644 deploy/systemd/magicboxie.service /etc/systemd/system/magicboxie.service
	sudo systemctl daemon-reload
	sudo systemctl enable magicboxie
	@echo "Installed. Run 'make pi-run' to (re)start it."

# Rebuilds + reinstalls (pi-install), then (re)starts the systemd service
# and tails its logs -- Ctrl+C stops following logs without stopping the
# service. Safe to re-run after a git pull to deploy an update.
pi-run: pi-install
	sudo systemctl restart magicboxie
	$(MAKE) pi-logs

pi-logs:
	sudo journalctl -u magicboxie -f

# Opens the running server in the default browser. Override the target URL
# with MAGICBOXIE_URL=... if your local config listens on a different address.
open:
	@open "$(URL)" 2>/dev/null || xdg-open "$(URL)" 2>/dev/null || echo "Open $(URL) in your browser"

test:
	go test ./...

# Same suite (including the Jellyfin API-conformance tests) in a throwaway Go
# container, for machines without a local Go toolchain. Module and build
# caches live in named volumes so repeat runs are fast.
test-docker:
	docker run --rm -v "$(CURDIR)":/src -w /src \
		-v magicboxie-gomod:/go/pkg/mod -v magicboxie-gobuild:/root/.cache/go-build \
		golang:1.26 go test ./...

tidy:
	go mod tidy
