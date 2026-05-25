.PHONY: dev build clean install docker-build

build:
	cd web && npm run build
	go build -o jackui ./cmd/server

dev-frontend:
	cd web && npm run dev

dev-backend:
	go run ./cmd/server

install:
	cd web && npm install

clean:
	rm -rf ui/dist jackui

docker-build:
	docker build -t jackui .
