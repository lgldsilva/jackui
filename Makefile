.PHONY: setup deploy restart logs down build test dev-frontend dev-backend clean

DOCKER_CONTEXT ?= homeserver
IMAGE          := jackui:latest

# Cores
GREEN  := \033[0;32m
YELLOW := \033[0;33m
CYAN   := \033[0;36m
RESET  := \033[0m

step = @printf "$(CYAN)▶ %s$(RESET)\n" "$(1)"
ok   = @printf "$(GREEN)✓ %s$(RESET)\n" "$(1)"

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
# deploy — build da imagem + sobe o container
# ─────────────────────────────────────────
deploy:
	$(call step,[1/2] Construindo imagem Docker no servidor '$(DOCKER_CONTEXT)'...)
	@docker --context $(DOCKER_CONTEXT) build --progress=plain -t $(IMAGE) .
	$(call ok,Imagem pronta)

	$(call step,[2/2] Subindo container...)
	@docker --context $(DOCKER_CONTEXT) compose up -d --remove-orphans
	$(call ok,JackUI rodando em http://<servidor>:8989)

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
