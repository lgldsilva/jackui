#!/usr/bin/env bash
# Commands run inside Dockerfile.ci.  Keep artifacts in the stopped ci container
# so the host orchestrator can retrieve them with `docker compose cp` on failure.
set -euo pipefail

readonly ARTIFACT_DIR="${CI_ARTIFACT_DIR:-/artifacts}"
readonly SERVER_BIN=/tmp/jackui-ci-server
server_pid=""

stop_server() {
	local rc=$?
	if [[ -n "${server_pid}" ]] && kill -0 "${server_pid}" 2>/dev/null; then
		kill "${server_pid}" 2>/dev/null || true
		wait "${server_pid}" 2>/dev/null || true
	fi
	exit "${rc}"
}

wait_for_healthz() {
	for _ in {1..30}; do
		if curl --fail --silent --show-error http://127.0.0.1:8989/healthz >"${ARTIFACT_DIR}/healthz.json"; then
			return 0
		fi
		sleep 1
	done
	return 1
}

main() {
	mkdir -p "${ARTIFACT_DIR}" /tmp/jackui-ci-stream /tmp/jackui-ci-downloads /tmp/jackui-ci-library
	trap stop_server EXIT INT TERM

	[[ -z "$(gofmt -l .)" ]]
	go vet ./internal/...
	go test -p 4 -timeout 20m -coverprofile="${ARTIFACT_DIR}/coverage.out" ./internal/...

	pushd web >/dev/null
	npm ci --ignore-scripts
	./node_modules/.bin/tsc --noEmit
	npm run lint
	npm run check:i18n
	npm test
	npm run build
	popd >/dev/null

	# The patch is generated on the runner before the Docker build because .git
	# is intentionally excluded from this image and remote contexts.
	golangci-lint run --new-from-patch=.ci-golangci.patch ./...

	go build -o "${SERVER_BIN}" ./cmd/server
	# Keep the unavailable Jackett endpoint scoped to the smoke process. Config
	# tests must observe the normal default/fixture environment instead.
	JACKETT_URL=http://127.0.0.1:9 "${SERVER_BIN}" >"${ARTIFACT_DIR}/server.log" 2>&1 &
	server_pid=$!
	wait_for_healthz
	kill "${server_pid}"
	wait "${server_pid}"
	server_pid=""
}

main "$@"
