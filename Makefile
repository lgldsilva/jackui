.PHONY: setup help deploy deploy-auto deploy-cpu deploy-nvidia deploy-vaapi \
        deploy-vpn deploy-auto-vpn deploy-nvidia-vpn deploy-vaapi-vpn \
        detect-gpu \
        restart logs down build test test-verbose clean \
        dev-frontend dev-backend probe-gpu

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

DOCKER_CONTEXT ?= homeserver
DEPLOY_HOST    ?= lgldsilva@192.168.0.100
IMAGE_CPU      := jackui:latest
IMAGE_NVIDIA   := jackui:nvidia
IMAGE_VAAPI    := jackui:vaapi

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
# Internal: sync config.yaml — used by all deploys
# ─────────────────────────────────────────
_sync-config:
	$(call step,Sincronizando config.yaml no servidor...)
	@ssh $(DEPLOY_HOST) "mkdir -p /home/lgldsilva/jackui"
	@scp config.yaml $(DEPLOY_HOST):/home/lgldsilva/jackui/config.yaml
	$(call ok,config.yaml sincronizado)

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
	@docker --context $(DOCKER_CONTEXT) build --progress=plain --build-arg BUILD_TIMESTAMP="$$(date +%s)" -f Dockerfile -t $(IMAGE_CPU) .
	$(call ok,Imagem CPU pronta)
	$(call step,Subindo container (CPU-only)...)
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml up -d --remove-orphans
	$(call ok,JackUI [CPU] rodando em http://192.168.0.100:8989)

deploy-nvidia: _sync-config
	$(call step,Construindo imagem NVIDIA (CUDA + ffmpeg-nvenc)...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain --build-arg BUILD_TIMESTAMP="$$(date +%s)" -f Dockerfile.nvidia -t $(IMAGE_NVIDIA) .
	$(call ok,Imagem NVIDIA pronta)
	$(call step,Subindo container (NVIDIA)...)
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.nvidia.yml up -d --remove-orphans
	$(call ok,JackUI [NVIDIA] rodando em http://192.168.0.100:8989)

deploy-vaapi: _sync-config
	$(call step,Construindo imagem VAAPI (Debian + mesa/iHD)...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain --build-arg BUILD_TIMESTAMP="$$(date +%s)" -f Dockerfile.vaapi -t $(IMAGE_VAAPI) .
	$(call ok,Imagem VAAPI pronta)
	$(call step,Subindo container (VAAPI)...)
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.vaapi.yml up -d --remove-orphans
	$(call ok,JackUI [VAAPI] rodando em http://192.168.0.100:8989)

# ─── With VPN (gluetun overlay) ────────────────────────────────────────────
deploy-vpn: _sync-config
	$(call step,Construindo imagem CPU + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain --build-arg BUILD_TIMESTAMP="$$(date +%s)" -f Dockerfile -t $(IMAGE_CPU) .
	$(call ok,Imagem pronta)
	$(call step,Subindo container atrás do gluetun (VPN)...)
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.gluetun.yml up -d --remove-orphans
	$(call ok,JackUI [CPU+VPN] rodando — acesse via porta exposta pelo gluetun)

deploy-nvidia-vpn: _sync-config
	$(call step,Construindo imagem NVIDIA + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain --build-arg BUILD_TIMESTAMP="$$(date +%s)" -f Dockerfile.nvidia -t $(IMAGE_NVIDIA) .
	$(call ok,Imagem pronta)
	$(call step,Subindo container NVIDIA atrás do gluetun...)
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.nvidia.yml -f docker-compose.gluetun.yml up -d --remove-orphans
	$(call ok,JackUI [NVIDIA+VPN] rodando)

deploy-vaapi-vpn: _sync-config
	$(call step,Construindo imagem VAAPI + gluetun overlay...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain --build-arg BUILD_TIMESTAMP="$$(date +%s)" -f Dockerfile.vaapi -t $(IMAGE_VAAPI) .
	$(call ok,Imagem pronta)
	$(call step,Subindo container VAAPI atrás do gluetun...)
	@docker --context $(DOCKER_CONTEXT) compose -f docker-compose.yml -f docker-compose.vaapi.yml -f docker-compose.gluetun.yml up -d --remove-orphans
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
	@ssh lgldsilva@192.168.0.100 "curl -s http://localhost:8989/api/transcode/capabilities?refresh=1" | python3 -m json.tool

# ─────────────────────────────────────────
# build local (binário sem Docker)
# ─────────────────────────────────────────
build:
	$(call step,[1/2] Compilando frontend...)
	@cd web && npm run build
	$(call ok,Frontend pronto em ui/dist/)

	$(call step,[2/2] Compilando binário Go...)
	@go build -o jackui ./cmd/server
	$(call ok,Binário gerado: ./jackui)

clean:
	@rm -rf ui/dist jackui
	$(call ok,Limpo)

# ─────────────────────────────────────────
# desenvolvimento
# ─────────────────────────────────────────
dev-frontend:
	@cd web && npm run dev

dev-backend:
	@go run ./cmd/server

# ─────────────────────────────────────────
# testes
# ─────────────────────────────────────────
test:
	$(call step,Rodando testes...)
	@go test ./internal/...
	$(call ok,Todos os testes passaram)

test-verbose:
	@go test -v ./internal/...
