.PHONY: setup help deploy deploy-auto deploy-cpu deploy-nvidia deploy-vaapi \
        deploy-vpn deploy-auto-vpn deploy-nvidia-vpn deploy-vaapi-vpn \
        detect-gpu \
        restart logs down build test test-verbose clean \
        dev-frontend dev-backend probe-gpu sonar-scan

# ─────────────────────────────────────────
# Deploy variants — combine GPU vendor × VPN
# ─────────────────────────────────────────
#
#   make deploy            → CPU-only, no VPN          (Alpine, smallest image)
#   make deploy-nvidia     → NVIDIA NVENC, no VPN      (CUDA runtime, ~700MB)
#   make deploy-vaapi      → AMD/Intel VAAPI, no VPN   (Debian + mesa/iHD)
#
#   make deploy-vpn        → CPU + gluetun VPN routing
#   make deploy-nvidia-vpn → NVIDIA + gluetun
#   make deploy-vaapi-vpn  → VAAPI + gluetun
#
# To change vendor later, just re-run a different target.
# The capability prober auto-detects and the API exposes /api/transcode/capabilities.

# Deploy target — read from .env (gitignored) with a generic fallback, or via env var.
#   DOCKER_CONTEXT=my-server  DEPLOY_HOST=user@host  in .env
DOCKER_CONTEXT ?= $(or $(shell grep -E '^DOCKER_CONTEXT=' .env 2>/dev/null | head -1 | cut -d= -f2-),default)
DEPLOY_HOST    ?= $(or $(shell grep -E '^DEPLOY_HOST=' .env 2>/dev/null | head -1 | cut -d= -f2-),user@your-server)
DEPLOY_ADDR    := $(shell echo '$(DEPLOY_HOST)' | sed 's/.*@//')
# Config directory on the remote server (where config.yaml is synced).
REMOTE_CONFIG_DIR ?= $(or $(shell grep -E '^REMOTE_CONFIG_DIR=' .env 2>/dev/null | head -1 | cut -d= -f2-),/opt/jackui)
# The production compose that INCLUDES jackui alongside gluetun/postgres.
# JackUI runs in network_mode:service:gluetun — the repo compose is for dev only.
VPN_GATEWAY_DIR ?= /portainer/Files/AppData/Config/vpn-gateway
IMAGE_CPU      := jackui:latest
IMAGE_NVIDIA   := jackui:nvidia
IMAGE_VAAPI    := jackui:vaapi

# Build metadata (served by GET /status). Resolved from the local git tree.
GIT_COMMIT     := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_TIME     := $(shell date +%s)
APP_VERSION    := $(shell bash scripts/semver.sh 2>/dev/null || git describe --tags --always 2>/dev/null || echo dev)
VERSION_PKG    := github.com/lgldsilva/jackui/internal/version
GO_LDFLAGS     := -X $(VERSION_PKG).Commit=$(GIT_COMMIT) -X $(VERSION_PKG).BuildTime=$(BUILD_TIME) -X $(VERSION_PKG).Version=$(APP_VERSION)
# Build-args reused by every `docker build` target below.
BUILD_ARGS     := --build-arg BUILD_TIMESTAMP="$(BUILD_TIME)" --build-arg GIT_COMMIT="$(GIT_COMMIT)" --build-arg APP_VERSION="$(APP_VERSION)"

# Colors
GREEN  := \033[0;32m
YELLOW := \033[0;33m
CYAN   := \033[0;36m
RESET  := \033[0m

step = @printf "$(CYAN)▶ %s$(RESET)\n" "$(1)"
ok   = @printf "$(GREEN)✓ %s$(RESET)\n" "$(1)"

# ─────────────────────────────────────────
# help — list deploy variants
# ─────────────────────────────────────────
help:
	@printf "$(CYAN)JackUI deploy targets:$(RESET)\n"
	@printf "  $(GREEN)make deploy-auto$(RESET)       Detect GPU on $(DEPLOY_HOST) and pick the right variant\n"
	@printf "  $(GREEN)make deploy$(RESET)            CPU only (default, smallest image)\n"
	@printf "  $(GREEN)make deploy-nvidia$(RESET)     NVIDIA NVENC encoder\n"
	@printf "  $(GREEN)make deploy-vaapi$(RESET)      AMD Radeon / Intel iGPU via VAAPI\n"
	@printf "  $(GREEN)make deploy-vpn$(RESET)        CPU + gluetun VPN routing\n"
	@printf "  $(GREEN)make deploy-auto-vpn$(RESET)   Auto-detect GPU + gluetun\n"
	@printf "  $(GREEN)make deploy-nvidia-vpn$(RESET) NVIDIA + gluetun\n"
	@printf "  $(GREEN)make deploy-vaapi-vpn$(RESET)  VAAPI + gluetun\n"
	@printf "\n$(CYAN)Detection:$(RESET)\n"
	@printf "  $(GREEN)make detect-gpu$(RESET)        Show which GPU was detected (without deploying)\n"
	@printf "\n$(CYAN)Operations:$(RESET)\n"
	@printf "  $(GREEN)make logs$(RESET)              follow container logs\n"
	@printf "  $(GREEN)make restart$(RESET)           restart jackui\n"
	@printf "  $(GREEN)make probe-gpu$(RESET)         query /api/transcode/capabilities\n"
	@printf "  $(GREEN)make down$(RESET)              stop container\n"
	@printf "\n$(CYAN)Desktop (Electron):$(RESET)\n"
	@printf "  $(GREEN)make dev-electron$(RESET)      Start Go backend + Electron dev mode\n"
	@printf "  $(GREEN)make build-electron$(RESET)     Build Electron app package (.dmg/.exe/.AppImage)\n"

# ─────────────────────────────────────────
# setup — run once before the first deploy
# ─────────────────────────────────────────
setup:
	$(call step,Checking Docker context '$(DOCKER_CONTEXT)'...)
	@docker context inspect $(DOCKER_CONTEXT) > /dev/null 2>&1 || \
		(echo "  Error: context '$(DOCKER_CONTEXT)' not found. Run: docker context create ..."; exit 1)
	$(call ok,Context OK)

	$(call step,Creating 'media' network on the server \(skips if it already exists\)...)
	@docker --context $(DOCKER_CONTEXT) network create media 2>/dev/null || true
	$(call ok,Network ready)

	$(call step,Preparing configuration file...)
	@if [ ! -f .env ]; then \
		cp .env.example .env; \
		printf "$(YELLOW)  ⚠  .env created — edit it with your JACKETT_API_KEY before deploying$(RESET)\n"; \
	else \
		printf "  .env already exists, keeping it\n"; \
	fi

	@if [ ! -f config.yaml ]; then \
		cp config.yaml.example config.yaml; \
		printf "$(YELLOW)  ⚠  config.yaml created — edit it with your download clients$(RESET)\n"; \
	else \
		printf "  config.yaml already exists, keeping it\n"; \
	fi
	$(call ok,Setup done — next step: make deploy)

# ─────────────────────────────────────────
# Internal: sync config.yaml + docker-compose.yml — used by all deploys
# ─────────────────────────────────────────
# The remote config.yaml is SEED-ONLY: it is only uploaded while it does not
# exist yet on the server. Overwriting on every deploy wiped what the UI saved
# (allowed_users of mounts, settings, etc.). When seeding, chown to uid 1000
# (the container's uid) so PUT /api/mounts can persist changes.
_sync-config:
	$(call step,Syncing config.yaml to the server...)
	@ssh $(DEPLOY_HOST) "sudo mkdir -p $(REMOTE_CONFIG_DIR)"
	@if ssh $(DEPLOY_HOST) "sudo test -f $(REMOTE_CONFIG_DIR)/config.yaml"; then \
		printf "  config.yaml already exists on the server — keeping it (UI Settings persist)\n"; \
	else \
		scp config.yaml $(DEPLOY_HOST):/tmp/jackui-config.yaml && \
		ssh $(DEPLOY_HOST) "sudo mv /tmp/jackui-config.yaml $(REMOTE_CONFIG_DIR)/config.yaml && sudo chown 1000:1000 $(REMOTE_CONFIG_DIR)/config.yaml"; \
	fi
	$(call ok,config.yaml synced)
	# NOTE: the server's docker-compose.yml lives in
	# $(DEPLOY_HOST):$(REMOTE_CONFIG_DIR)/docker-compose.yml
	# and is managed SEPARATELY from the repo (it contains hardcoded secrets).
	# When adding new env vars, edit the file on the server too:
	#   ssh $(DEPLOY_HOST) "nano $(REMOTE_CONFIG_DIR)/docker-compose.yml"
	#   cd $(VPN_GATEWAY_DIR) && docker compose up -d

# ─────────────────────────────────────────
# GPU detection — runs on the deploy host via SSH
# Sets a variable in a child shell. Prints chosen variant to stdout.
# Detection order (best → worst): NVIDIA > VAAPI > CPU
# ─────────────────────────────────────────
# Internal helper that just echoes "nvidia" | "vaapi" | "cpu"
_detect_gpu_remote = ssh -o ConnectTimeout=5 $(DEPLOY_HOST) ' \
  if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L 2>/dev/null | grep -q "GPU"; then \
    echo nvidia; \
  elif [ -e /dev/dri/renderD128 ]; then \
    echo vaapi; \
  else \
    echo cpu; \
  fi'

detect-gpu:
	$(call step,Detecting GPU on $(DEPLOY_HOST)...)
	@VARIANT=`$(_detect_gpu_remote)`; \
	case "$$VARIANT" in \
	  nvidia) printf "$(GREEN)✓ NVIDIA detected$(RESET)\n"; ssh $(DEPLOY_HOST) "nvidia-smi -L 2>/dev/null | head -3";; \
	  vaapi)  printf "$(GREEN)✓ VAAPI device available$(RESET) (/dev/dri/renderD128)\n";; \
	  cpu)    printf "$(YELLOW)⚠  No GPU detected — falling back to CPU$(RESET)\n";; \
	esac

# ─────────────────────────────────────────
# Deploy targets — six variants
# ─────────────────────────────────────────
deploy: deploy-auto

deploy-auto:
	$(call step,Detecting GPU on $(DEPLOY_HOST)...)
	@VARIANT=`$(_detect_gpu_remote)`; \
	printf "$(GREEN)✓ Chosen variant: %s$(RESET)\n" "$$VARIANT"; \
	case "$$VARIANT" in \
	  nvidia) $(MAKE) deploy-nvidia;; \
	  vaapi)  $(MAKE) deploy-vaapi;; \
	  cpu)    $(MAKE) deploy-cpu;; \
	  *)      printf "$(YELLOW)⚠  Detection failed — using CPU$(RESET)\n"; $(MAKE) deploy-cpu;; \
	esac

deploy-auto-vpn:
	$(call step,Detecting GPU on $(DEPLOY_HOST)...)
	@VARIANT=`$(_detect_gpu_remote)`; \
	printf "$(GREEN)✓ Chosen variant: %s + VPN$(RESET)\n" "$$VARIANT"; \
	case "$$VARIANT" in \
	  nvidia) $(MAKE) deploy-nvidia-vpn;; \
	  vaapi)  $(MAKE) deploy-vaapi-vpn;; \
	  cpu)    $(MAKE) deploy-vpn;; \
	  *)      printf "$(YELLOW)⚠  Detection failed — using CPU+VPN$(RESET)\n"; $(MAKE) deploy-vpn;; \
	esac

# All deploy targets build the image via remote Docker context, then SSH
# to the server and redeploy using the HAND-MAINTAINED production compose
# at $(REMOTE_CONFIG_DIR)/docker-compose.yml (which uses gluetun VPN,
# per-container env vars, and no host port binding — the repo compose is
# for local development only).

deploy-cpu: _sync-config
	$(call step,Building CPU image (Alpine)...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile -t $(IMAGE_CPU) .
	$(call ok,CPU image ready)
	$(call step,Deploying via the server compose (CPU)...)
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose up -d --no-deps --force-recreate jackui"
	$(call ok,JackUI [CPU] running)

deploy-nvidia: _sync-config
	$(call step,Building NVIDIA image (CUDA + ffmpeg-nvenc)...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile.nvidia -t $(IMAGE_NVIDIA) .
	$(call ok,NVIDIA image ready)
	$(call step,Deploying via the server compose (NVIDIA)...)
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose up -d --no-deps --force-recreate jackui"
	$(call ok,JackUI [NVIDIA] running)

deploy-vaapi: _sync-config
	$(call step,Building VAAPI image (Debian + mesa/iHD)...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile.vaapi -t $(IMAGE_VAAPI) .
	$(call ok,VAAPI image ready)
	$(call step,Deploying via the server compose (VAAPI)...)
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose up -d --no-deps --force-recreate jackui"
	$(call ok,JackUI [VAAPI] running)

# ─── With VPN (gluetun overlay) — same as above, server compose already includes gluetun ───
deploy-vpn: _sync-config
	$(call step,Building CPU image + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile -t $(IMAGE_CPU) .
	$(call ok,Image ready)
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose up -d --no-deps --force-recreate jackui"
	$(call ok,JackUI [CPU+VPN] running)

deploy-nvidia-vpn: _sync-config
	$(call step,Building NVIDIA image + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile.nvidia -t $(IMAGE_NVIDIA) .
	$(call ok,Image ready)
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose up -d --no-deps --force-recreate jackui"
	$(call ok,JackUI [NVIDIA+VPN] running)

deploy-vaapi-vpn: _sync-config
	$(call step,Building VAAPI image + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile.vaapi -t $(IMAGE_VAAPI) .
	$(call ok,Image ready)
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose up -d --no-deps --force-recreate jackui"
	$(call ok,JackUI [VAAPI+VPN] running)

# ─────────────────────────────────────────
# container operations
# ─────────────────────────────────────────
restart:
	$(call step,Restarting jackui container...)
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose restart jackui"
	$(call ok,Restarted)

logs:
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose logs -f jackui"

down:
	$(call step,Stopping container...)
	@ssh $(DEPLOY_HOST) "cd $(VPN_GATEWAY_DIR) && docker compose down"
	$(call ok,Container stopped)

# Query the GPU/CPU capability matrix from the running container
probe-gpu:
	$(call step,Probing transcoder capabilities...)
	@ssh $(DEPLOY_HOST) "curl -s http://localhost:8989/api/transcode/capabilities?refresh=1" | python3 -m json.tool

# ─────────────────────────────────────────
# dependency check and installation
# ─────────────────────────────────────────
_check-go:
	@command -v go >/dev/null 2>&1 || { printf "$(YELLOW)Error: Go is not installed. Please install the Go SDK (go >= 1.22).$(RESET)\n"; exit 1; }

_check-npm:
	@command -v npm >/dev/null 2>&1 || { printf "$(YELLOW)Error: npm is not installed. Please install Node.js and npm.$(RESET)\n"; exit 1; }

web/node_modules: _check-npm web/package.json
	$(call step,web/node_modules missing or outdated. Running npm install in the frontend...)
	@cd web && npm install
	@touch web/node_modules

node_modules: _check-npm package.json
	$(call step,node_modules missing or outdated. Running npm install...)
	@npm install
	@touch node_modules

# ─────────────────────────────────────────
# local build (binary without Docker)
# ─────────────────────────────────────────
build: _check-go web/node_modules
	$(call step,[1/2] Building frontend...)
	@cd web && npm run build
	$(call ok,Frontend built in ui/dist/)

	$(call step,[2/2] Building Go binary...)
	@go build -trimpath -ldflags "-s -w $(GO_LDFLAGS)" -o jackui ./cmd/server
	$(call ok,Binary generated: ./jackui)

clean:
	@rm -rf ui/dist jackui
	$(call ok,Clean)

# ─────────────────────────────────────────
# development
# ─────────────────────────────────────────
dev-frontend: web/node_modules
	@cd web && npm run dev

dev-backend: _check-go
	@JACKUI_AUTH_ENABLED=0 JACKUI_ALLOW_INSECURE_AUTH=1 go run ./cmd/server

# ─────────────────────────────────────────
# Electron (Desktop)
# ─────────────────────────────────────────

# dev-electron: start Go backend + Electron in dev mode.
# 1. Build the React frontend (so Go embeds the latest)
# 2. Run Go server in background
# 3. Run Electron pointing to Go server
dev-electron: _check-go web/node_modules node_modules
	$(call step,[1/2] Building frontend...)
	@cd web && npm run build
	$(call ok,Frontend ready)
	$(call step,[2/2] Starting Go + Electron...)
	@npm run dev

# build-electron: produce distributable packages (.dmg / .exe / .AppImage).
# Cross-compile Go for the target platform, build React, then run
# electron-builder. Accepts PLATFORM and ARCH as optional args:
#   make build-electron           (current OS + arch)
#   make build-electron linux amd64
build-electron:
	$(call step,Building for $(or $(filter-out $@,$(MAKECMDGOALS)),$(shell uname -s | tr A-Z a-z))/$(or $(word 2,$(MAKECMDGOALS)),$(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')))
	@bash scripts/build-electron.sh $(or $(filter-out $@,$(MAKECMDGOALS)),$(shell uname -s | tr A-Z a-z))
	$(call ok,Electron package generated in dist-electron/)

# ─────────────────────────────────────────
# tests
# ─────────────────────────────────────────
test:
	$(call step,Running tests...)
	@go test ./internal/...
	$(call ok,All tests passed)

test-verbose:
	@go test -v ./internal/...

# CA-3.2 — pass-rate over N consecutive runs (default 20). E.g.: make test-stability RUNS=5
test-stability:
	@./scripts/ci-stability-audit.sh $(or $(RUNS),20)

branch-hygiene:
	@./scripts/branch-hygiene.sh $(if $(DELETE),--delete-merged,)

# ─────────────────────────────────────────
# SonarQube analysis + thresholds
# ─────────────────────────────────────────

SONAR_HOST_URL  ?= $(or $(shell grep -E '^SONAR_HOST_URL=' .env 2>/dev/null | head -1 | cut -d= -f2-),https://sonar.example.com)
SONAR_TOKEN     ?= $(shell grep SONAR_TOKEN .env 2>/dev/null | head -1 | cut -d= -f2-)

# REAL local gate: same config as CI (sonar-project.properties is the single
# source, incl. sonar.qualitygate.wait=true) — if the quality gate fails, make
# FAILS. This target used to swallow the test failure (`|| echo`) and the
# scanner exit (`-@`), giving a false green vs. the CI gate (audit finding #413).
sonar-scan:
	$(call step,Generating test coverage...)
	@go test -coverprofile=coverage.out ./internal/... || { echo "  ✗ tests failed — fix them before scanning"; exit 1; }
	$(call ok,Coverage saved to coverage.out)

	$(call step,Checking sonar-scanner...)
	@command -v sonar-scanner >/dev/null 2>&1 || { echo "  Error: sonar-scanner not found. Install with: brew install sonar-scanner"; exit 1; }
	$(call ok,sonar-scanner found)

	$(call step,Running SonarQube analysis (waits for the quality gate verdict)...)
	@sonar-scanner \
		-Dsonar.host.url=$(SONAR_HOST_URL) \
		-Dsonar.token=$(SONAR_TOKEN) \
		-Dsonar.projectKey=jackui \
		-Dsonar.projectName=JackUI \
		> /tmp/jackui-sonar-scan.log 2>&1; rc=$$?; tail -15 /tmp/jackui-sonar-scan.log; exit $$rc
	@rm -f coverage.out
	$(call ok,Quality gate OK — same gate as CI)
