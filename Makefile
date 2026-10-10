BINARY := magicboxie
IMAGE := magicboxie
# dev/run listen on Docker's :8080 (docker-compose.yml); run-local listens
# on :8090 instead so it can run side by side without a port clash.
URL := $(if $(MAGICBOXIE_URL),$(MAGICBOXIE_URL),http://localhost:8080)

# Raspberry Pi deployment builds and runs through Docker Compose.

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

# Build and deploy this checkout on the LAN Pi using Docker Compose.
deploy:
	./scripts/pi-publish.sh

# Publish the current checkout to the LAN Raspberry Pi. Override the target
# with MAGICBOXIE_SSH_TARGET=user@host when needed.
publish:
	./scripts/pi-publish.sh

# Complete Raspberry Pi setup, using Docker behind the existing nginx proxy. Run on the Pi itself.
setup:
	./scripts/pi-setup.sh

# Run these on the Pi; publish syncs this checkout and invokes setup remotely.
pi-setup pi-install pi-run:
	./scripts/pi-setup.sh

pi-logs:
	sudo docker compose -f deploy/pi/docker-compose.yml logs -f

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
