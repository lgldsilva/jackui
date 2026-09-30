#!/usr/bin/env bash
# Idempotent Cloud Agent bootstrap for JackUI. Safe to re-run.
# Pins Node to the same 24.19.0 release as Dockerfile.ci and installs the
# local PostgreSQL 16 used by .cursor/cloud-start.sh.
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

sudo apt-get update
sudo apt-get install -y --no-install-recommends \
	postgresql-16 \
	postgresql-client-16 \
	ca-certificates \
	curl \
	xz-utils

NODE_VERSION=24.19.0
NODE_PREFIX=/usr/local/lib/jackui-node
if ! [[ -x "${NODE_PREFIX}/bin/node" ]] || [[ "$("${NODE_PREFIX}/bin/node" -v)" != "v${NODE_VERSION}" ]]; then
	tmp="$(mktemp)"
	# --proto/--tlsv1.2 reject a downgrade to cleartext HTTP (shell:S6506).
	curl --proto '=https' --tlsv1.2 -fsSL \
		"https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
		-o "${tmp}"
	sudo rm -rf "${NODE_PREFIX}"
	sudo mkdir -p "${NODE_PREFIX}"
	sudo tar -xJf "${tmp}" -C "${NODE_PREFIX}" --strip-components=1
	rm -f "${tmp}"
fi

export PATH="${NODE_PREFIX}/bin:${PATH}"
hash -r

# /exec-daemon is ahead of /usr/local/bin on the agent PATH, so point the
# binaries the shell finds first at the pinned Node.
if [[ -d /exec-daemon ]]; then
	sudo ln -sfn "${NODE_PREFIX}/bin/node" /exec-daemon/node
	sudo ln -sfn "${NODE_PREFIX}/bin/npm" /exec-daemon/npm
	sudo ln -sfn "${NODE_PREFIX}/bin/npx" /exec-daemon/npx
fi

if ! command -v golangci-lint >/dev/null 2>&1 || ! golangci-lint version 2>/dev/null | grep -q '2\.13\.1'; then
	tmpbin="$(mktemp -d)"
	GOBIN="${tmpbin}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1
	sudo install -m 0755 "${tmpbin}/golangci-lint" /usr/local/bin/golangci-lint
	rm -rf "${tmpbin}"
fi

go mod download
# --ignore-scripts skips dependency lifecycle scripts (shell:S6505). The
# embed build is invoked explicitly below, same as scripts/ci-container.sh.
npm ci --ignore-scripts --prefix web
# go:embed in ui/embed.go requires ui/dist before `go run ./cmd/server`.
npm run build --prefix web
