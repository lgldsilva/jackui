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

# Deploy target — lido do .env (gitignored) com fallback genérico, ou via env var.
#   DOCKER_CONTEXT=meu-server  DEPLOY_HOST=user@host  no .env
DOCKER_CONTEXT ?= $(or $(shell grep -E '^DOCKER_CONTEXT=' .env 2>/dev/null | head -1 | cut -d= -f2-),default)
DEPLOY_HOST    ?= $(or $(shell grep -E '^DEPLOY_HOST=' .env 2>/dev/null | head -1 | cut -d= -f2-),user@your-server)
DEPLOY_ADDR    := $(shell echo '$(DEPLOY_HOST)' | sed 's/.*@//')
# Diretório de config no servidor remoto (onde o config.yaml é sincronizado).
REMOTE_CONFIG_DIR ?= $(or $(shell grep -E '^REMOTE_CONFIG_DIR=' .env 2>/dev/null | head -1 | cut -d= -f2-),/opt/jackui)
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

# Cores
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
# setup — roda uma vez antes do primeiro deploy
# ─────────────────────────────────────────
setup:
	$(call step,Verificando Docker context '$(DOCKER_CONTEXT)'...)
	@docker context inspect $(DOCKER_CONTEXT) > /dev/null 2>&1 || \
		(echo "  Erro: context '$(DOCKER_CONTEXT)' não encontrado. Rode: docker context create ..."; exit 1)
	$(call ok,Context OK)

	$(call step,Criando rede 'media' no servidor \(ignora se já existe\)...)
	@docker --context $(DOCKER_CONTEXT) network create media 2>/dev/null || true
	$(call ok,Rede pronta)

	$(call step,Preparando arquivo de configuração...)
	@if [ ! -f .env ]; then \
		cp .env.example .env; \
		printf "$(YELLOW)  ⚠  .env criado — edite com sua JACKETT_API_KEY antes de fazer deploy$(RESET)\n"; \
	else \
		printf "  .env já existe, mantendo\n"; \
	fi

	@if [ ! -f config.yaml ]; then \
		cp config.yaml.example config.yaml; \
		printf "$(YELLOW)  ⚠  config.yaml criado — edite com seus clientes de download$(RESET)\n"; \
	else \
		printf "  config.yaml já existe, mantendo\n"; \
	fi
	$(call ok,Setup concluído — próximo passo: make deploy)

# ─────────────────────────────────────────
# Internal: sync config.yaml + docker-compose.yml — used by all deploys
# ─────────────────────────────────────────
# O config.yaml remoto é SEED-ONLY: só é enviado quando ainda não existe no
# servidor. Sobrescrever a cada deploy apagava o que a UI salvou (allowed_users
# dos mounts, settings, etc.). Ao semear, chown p/ uid 1000 (o uid do container)
# para que PUT /api/mounts consiga persistir.
_sync-config:
	$(call step,Sincronizando config.yaml no servidor...)
	@ssh $(DEPLOY_HOST) "sudo mkdir -p $(REMOTE_CONFIG_DIR)"
	@if ssh $(DEPLOY_HOST) "sudo test -f $(REMOTE_CONFIG_DIR)/config.yaml"; then \
		printf "  config.yaml já existe no servidor — mantendo (Settings da UI persistem)\n"; \
	else \
		scp config.yaml $(DEPLOY_HOST):/tmp/jackui-config.yaml && \
		ssh $(DEPLOY_HOST) "sudo mv /tmp/jackui-config.yaml $(REMOTE_CONFIG_DIR)/config.yaml && sudo chown 1000:1000 $(REMOTE_CONFIG_DIR)/config.yaml"; \
	fi
	$(call ok,config.yaml sincronizado)
	# NOTA: O docker-compose.yml do servidor vive em
	# $(DEPLOY_HOST):$(REMOTE_CONFIG_DIR)/docker-compose.yml
	# e é gerenciado SEPARADAMENTE do repo (contém secrets hardcoded).
	# Se adicionar novas env vars, edite também o arquivo no servidor:
	#   ssh $(DEPLOY_HOST) "nano $(REMOTE_CONFIG_DIR)/docker-compose.yml"
	#   cd $(REMOTE_CONFIG_DIR) && docker compose up -d

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
	$(call step,Detectando GPU em $(DEPLOY_HOST)...)
	@VARIANT=`$(_detect_gpu_remote)`; \
	case "$$VARIANT" in \
	  nvidia) printf "$(GREEN)✓ NVIDIA detectada$(RESET)\n"; ssh $(DEPLOY_HOST) "nvidia-smi -L 2>/dev/null | head -3";; \
	  vaapi)  printf "$(GREEN)✓ VAAPI device disponível$(RESET) (/dev/dri/renderD128)\n";; \
	  cpu)    printf "$(YELLOW)⚠  Nenhuma GPU detectada — usará CPU$(RESET)\n";; \
	esac

# ─────────────────────────────────────────
# Deploy targets — six variants
# ─────────────────────────────────────────
deploy: deploy-auto

deploy-auto:
	$(call step,Detectando GPU em $(DEPLOY_HOST)...)
	@VARIANT=`$(_detect_gpu_remote)`; \
	printf "$(GREEN)✓ Variante escolhida: %s$(RESET)\n" "$$VARIANT"; \
	case "$$VARIANT" in \
	  nvidia) $(MAKE) deploy-nvidia;; \
	  vaapi)  $(MAKE) deploy-vaapi;; \
	  cpu)    $(MAKE) deploy-cpu;; \
	  *)      printf "$(YELLOW)⚠  Detecção falhou — usando CPU$(RESET)\n"; $(MAKE) deploy-cpu;; \
	esac

deploy-auto-vpn:
	$(call step,Detectando GPU em $(DEPLOY_HOST)...)
	@VARIANT=`$(_detect_gpu_remote)`; \
	printf "$(GREEN)✓ Variante escolhida: %s + VPN$(RESET)\n" "$$VARIANT"; \
	case "$$VARIANT" in \
	  nvidia) $(MAKE) deploy-nvidia-vpn;; \
	  vaapi)  $(MAKE) deploy-vaapi-vpn;; \
	  cpu)    $(MAKE) deploy-vpn;; \
	  *)      printf "$(YELLOW)⚠  Detecção falhou — usando CPU+VPN$(RESET)\n"; $(MAKE) deploy-vpn;; \
	esac

deploy-cpu: _sync-config
	$(call step,Construindo imagem CPU (Alpine)...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile -t $(IMAGE_CPU) .
	$(call ok,Imagem CPU pronta)
	$(call step,Subindo container (CPU-only)...)
	@docker --context $(DOCKER_CONTEXT) rm -f jackui 2>/dev/null || true
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml up -d --no-deps --force-recreate jackui
	$(call ok,JackUI [CPU] rodando em http://$(DEPLOY_ADDR):8989)

deploy-nvidia: _sync-config
	$(call step,Construindo imagem NVIDIA (CUDA + ffmpeg-nvenc)...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile.nvidia -t $(IMAGE_NVIDIA) .
	$(call ok,Imagem NVIDIA pronta)
	$(call step,Subindo container (NVIDIA)...)
	@docker --context $(DOCKER_CONTEXT) rm -f jackui 2>/dev/null || true
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.nvidia.yml up -d --no-deps --force-recreate jackui
	$(call ok,JackUI [NVIDIA] rodando em http://$(DEPLOY_ADDR):8989)

deploy-vaapi: _sync-config
	$(call step,Construindo imagem VAAPI (Debian + mesa/iHD)...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile.vaapi -t $(IMAGE_VAAPI) .
	$(call ok,Imagem VAAPI pronta)
	$(call step,Subindo container (VAAPI)...)
	@docker --context $(DOCKER_CONTEXT) rm -f jackui 2>/dev/null || true
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.vaapi.yml up -d --no-deps --force-recreate jackui
	$(call ok,JackUI [VAAPI] rodando em http://$(DEPLOY_ADDR):8989)

# ─── With VPN (gluetun overlay) ────────────────────────────────────────────
deploy-vpn: _sync-config
	$(call step,Construindo imagem CPU + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile -t $(IMAGE_CPU) .
	$(call ok,Imagem pronta)
	$(call step,Subindo container atrás do gluetun (VPN)...)
	@docker --context $(DOCKER_CONTEXT) rm -f jackui 2>/dev/null || true
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.gluetun.yml up -d --no-deps --force-recreate jackui
	$(call ok,JackUI [CPU+VPN] rodando — acesse via porta exposta pelo gluetun)

deploy-nvidia-vpn: _sync-config
	$(call step,Construindo imagem NVIDIA + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile.nvidia -t $(IMAGE_NVIDIA) .
	$(call ok,Imagem pronta)
	$(call step,Subindo container NVIDIA atrás do gluetun...)
	@docker --context $(DOCKER_CONTEXT) rm -f jackui 2>/dev/null || true
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.nvidia.yml -f docker-compose.gluetun.yml up -d --no-deps --force-recreate jackui
	$(call ok,JackUI [NVIDIA+VPN] rodando)

deploy-vaapi-vpn: _sync-config
	$(call step,Construindo imagem VAAPI + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain $(BUILD_ARGS) -f Dockerfile.vaapi -t $(IMAGE_VAAPI) .
	$(call ok,Imagem pronta)
	$(call step,Subindo container VAAPI atrás do gluetun...)
	@docker --context $(DOCKER_CONTEXT) rm -f jackui 2>/dev/null || true
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.vaapi.yml -f docker-compose.gluetun.yml up -d --no-deps --force-recreate jackui
	$(call ok,JackUI [VAAPI+VPN] rodando)

# ─────────────────────────────────────────
# operações do container
# ─────────────────────────────────────────
restart:
	$(call step,Reiniciando container jackui...)
	@docker --context $(DOCKER_CONTEXT) compose restart jackui
	$(call ok,Reiniciado)

logs:
	@docker --context $(DOCKER_CONTEXT) compose logs -f jackui

down:
	$(call step,Parando container...)
	@docker --context $(DOCKER_CONTEXT) compose down
	$(call ok,Container parado)

# Query the GPU/CPU capability matrix from the running container
probe-gpu:
	$(call step,Probing transcoder capabilities...)
	@ssh $(DEPLOY_HOST) "curl -s http://localhost:8989/api/transcode/capabilities?refresh=1" | python3 -m json.tool

# ─────────────────────────────────────────
# verificação e instalação de dependências
# ─────────────────────────────────────────
_check-go:
	@command -v go >/dev/null 2>&1 || { printf "$(YELLOW)Erro: Go não está instalado. Por favor, instale o Go SDK (go >= 1.22).$(RESET)\n"; exit 1; }

_check-npm:
	@command -v npm >/dev/null 2>&1 || { printf "$(YELLOW)Erro: npm não está instalado. Por favor, instale o Node.js e o npm.$(RESET)\n"; exit 1; }

web/node_modules: _check-npm web/package.json
	$(call step,web/node_modules não encontrado ou desatualizado. Executando npm install no frontend...)
	@cd web && npm install
	@touch web/node_modules

node_modules: _check-npm package.json
	$(call step,node_modules não encontrado ou desatualizado. Executando npm install...)
	@npm install
	@touch node_modules

# ─────────────────────────────────────────
# build local (binário sem Docker)
# ─────────────────────────────────────────
build: _check-go web/node_modules
	$(call step,[1/2] Compilando frontend...)
	@cd web && npm run build
	$(call ok,Frontend pronto em ui/dist/)

	$(call step,[2/2] Compilando binário Go...)
	@go build -ldflags "$(GO_LDFLAGS)" -o jackui ./cmd/server
	$(call ok,Binário gerado: ./jackui)

clean:
	@rm -rf ui/dist jackui
	$(call ok,Limpo)

# ─────────────────────────────────────────
# desenvolvimento
# ─────────────────────────────────────────
dev-frontend: web/node_modules
	@cd web && npm run dev

dev-backend: _check-go
	@go run ./cmd/server

# ─────────────────────────────────────────
# Electron (Desktop)
# ─────────────────────────────────────────

# dev-electron: start Go backend + Electron in dev mode.
# 1. Build the React frontend (so Go embeds the latest)
# 2. Run Go server in background
# 3. Run Electron pointing to Go server
dev-electron: _check-go web/node_modules node_modules
	$(call step,[1/2] Compilando frontend...)
	@cd web && npm run build
	$(call ok,Frontend pronto)
	$(call step,[2/2] Iniciando Go + Electron...)
	@npm run dev

# build-electron: produce distributable packages (.dmg / .exe / .AppImage).
# Cross-compile Go for the target platform, build React, then run
# electron-builder. Accepts PLATFORM and ARCH as optional args:
#   make build-electron           (current OS + arch)
#   make build-electron linux amd64
build-electron:
	$(call step,Compilando para $(or $(filter-out $@,$(MAKECMDGOALS)),$(shell uname -s | tr A-Z a-z))/$(or $(word 2,$(MAKECMDGOALS)),$(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')))
	@bash scripts/build-electron.sh $(or $(filter-out $@,$(MAKECMDGOALS)),$(shell uname -s | tr A-Z a-z))
	$(call ok,Electron package gerado em dist-electron/)

# ─────────────────────────────────────────
# testes
# ─────────────────────────────────────────
test:
	$(call step,Rodando testes...)
	@go test ./internal/...
	$(call ok,Todos os testes passaram)

test-verbose:
	@go test -v ./internal/...

# ─────────────────────────────────────────
# análise SonarQube + thresholds
# ─────────────────────────────────────────

SONAR_HOST_URL  ?= $(or $(shell grep -E '^SONAR_HOST_URL=' .env 2>/dev/null | head -1 | cut -d= -f2-),https://sonar.example.com)
SONAR_TOKEN     ?= $(shell grep SONAR_TOKEN .env 2>/dev/null | head -1 | cut -d= -f2-)

# Gate local REAL: mesma config do CI (sonar-project.properties é a fonte única,
# incl. sonar.qualitygate.wait=true) — se o quality gate reprovar, o make FALHA.
# Antes este alvo engolia falha de teste (`|| echo`) e o exit do scanner (`-@`),
# dando falso-verde vs. o gate do CI (achado #413 da auditoria).
sonar-scan:
	$(call step,Gerando cobertura de testes...)
	@go test -coverprofile=coverage.out ./internal/... || { echo "  ✗ testes falharam — corrija antes de escanear"; exit 1; }
	$(call ok,Cobertura salva em coverage.out)

	$(call step,Verificando sonar-scanner...)
	@command -v sonar-scanner >/dev/null 2>&1 || { echo "  Erro: sonar-scanner não encontrado. Instale: brew install sonar-scanner"; exit 1; }
	$(call ok,sonar-scanner encontrado)

	$(call step,Executando análise SonarQube (aguarda o veredito do quality gate)...)
	@sonar-scanner \
		-Dsonar.host.url=$(SONAR_HOST_URL) \
		-Dsonar.token=$(SONAR_TOKEN) \
		2>&1 | tail -15
	@rm -f coverage.out
	$(call ok,Quality gate OK — mesmo gate do CI)
