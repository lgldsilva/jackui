.PHONY: dev build clean install docker-build deploy deploy-up

DOCKER_CONTEXT ?= raspberrypisrv
IMAGE          := jackui:latest

# --- desenvolvimento local ---

install:
	cd web && npm install

dev-frontend:
	cd web && npm run dev

dev-backend:
	go run ./cmd/server

# --- build local (single binary) ---

build:
	cd web && npm run build
	go build -o jackui ./cmd/server

clean:
	rm -rf ui/dist jackui

# --- docker ---

docker-build:
	docker --context $(DOCKER_CONTEXT) build -t $(IMAGE) .

deploy: docker-build
	docker --context $(DOCKER_CONTEXT) compose up -d

deploy-restart:
	docker --context $(DOCKER_CONTEXT) compose restart jackui

deploy-logs:
	docker --context $(DOCKER_CONTEXT) compose logs -f jackui

deploy-down:
	docker --context $(DOCKER_CONTEXT) compose down

# --- testes ---

test:
	go test ./internal/...

test-verbose:
	go test -v ./internal/...
