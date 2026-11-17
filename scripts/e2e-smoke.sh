#!/usr/bin/env bash
# Playwright SPA page smoke (web/e2e) against a freshly built server.
#
# Builds the web bundle and the server binary, boots the server with auth
# ENABLED (the spec exercises the login form and the anonymous → /login
# redirect) on a private port, runs `playwright test`, then stops the server.
#
# The server migrates the schema and bootstraps the admin user, so it must
# only ever see a DISPOSABLE database:
#
#   E2E_DATABASE_URL=postgres://user:pass@127.0.0.1:55432/jackui_test?sslmode=disable \
#     make e2e-smoke
#
# Chromium must already be installed: `cd web && npx playwright install chromium`.
#
# Knobs: E2E_PORT (default 18989), E2E_SKIP_BUILD=1 (reuse ui/dist and the
# binary from a previous run), E2E_ARTIFACT_DIR (server log destination).
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly PROJECT_ROOT="${SCRIPT_DIR}/.."
readonly PORT="${E2E_PORT:-18989}"
readonly BASE_URL="http://127.0.0.1:${PORT}"
readonly ADMIN_USER=admin
# Per-run credentials for a throwaway database: nothing reusable in the repo.
ADMIN_PASSWORD="$(openssl rand -hex 16)"
JWT_SECRET="$(openssl rand -hex 32)"
readonly ADMIN_PASSWORD JWT_SECRET
readonly SERVER_BIN="${PROJECT_ROOT}/web/test-results/jackui-e2e-server"
server_pid=""
work_dir=""

fail() {
	printf 'e2e-smoke: %s\n' "$*" >&2
	exit 1
}

cleanup() {
	local rc=$?
	if [[ -n "${server_pid}" ]] && kill -0 "${server_pid}" 2>/dev/null; then
		kill "${server_pid}" 2>/dev/null || true
		wait "${server_pid}" 2>/dev/null || true
	fi
	[[ -z "${work_dir}" ]] || rm -rf -- "${work_dir}"
	exit "${rc}"
}

# The database name is the last path segment of the URL, before any query.
database_name() {
	local url=${1%%\?*}
	printf '%s' "${url##*/}"
}

require_disposable_database() {
	[[ -n "${E2E_DATABASE_URL:-}" ]] || fail 'E2E_DATABASE_URL is required (a disposable PostgreSQL database)'
	[[ "$(database_name "${E2E_DATABASE_URL}")" == *test* ]] ||
		fail 'refusing to run: the database name in E2E_DATABASE_URL must contain "test"'
}

build_artifacts() {
	if [[ "${E2E_SKIP_BUILD:-0}" == 1 ]]; then
		[[ -x "${SERVER_BIN}" ]] || fail "E2E_SKIP_BUILD=1 but ${SERVER_BIN} does not exist"
		return 0
	fi
	pushd "${PROJECT_ROOT}/web" >/dev/null
	[[ -d node_modules ]] || npm ci --ignore-scripts
	npm run build
	popd >/dev/null
	mkdir -p "$(dirname -- "${SERVER_BIN}")"
	(cd "${PROJECT_ROOT}" && go build -o "${SERVER_BIN}" ./cmd/server)
}

# The server runs from an empty directory with a scrubbed environment so a
# developer's config.yaml, .env or exported JACKUI_* never leak into the run.
start_server() {
	local log_file=$1
	mkdir -p "${work_dir}/stream" "${work_dir}/downloads" "${work_dir}/library"
	pushd "${work_dir}" >/dev/null
	env -i PATH="${PATH}" HOME="${work_dir}" TZ=UTC \
		JACKUI_PORT="${PORT}" \
		JACKUI_DATABASE_URL="${E2E_DATABASE_URL}" \
		JACKUI_AUTH_ENABLED=1 \
		JACKUI_ADMIN_USERNAME="${ADMIN_USER}" \
		JACKUI_ADMIN_PASSWORD="${ADMIN_PASSWORD}" \
		JACKUI_JWT_SECRET="${JWT_SECRET}" \
		JACKUI_STREAM_DIR="${work_dir}/stream" \
		JACKUI_DOWNLOAD_DIR="${work_dir}/downloads" \
		JACKUI_SHARED_DIR="${work_dir}/library" \
		JACKETT_URL=http://127.0.0.1:9 \
		"${SERVER_BIN}" >"${log_file}" 2>&1 &
	server_pid=$!
	popd >/dev/null
}

wait_for_healthz() {
	local log_file=$1
	for _ in {1..60}; do
		kill -0 "${server_pid}" 2>/dev/null || break
		if curl --fail --silent --output /dev/null "${BASE_URL}/healthz"; then
			return 0
		fi
		sleep 1
	done
	tail -n 50 -- "${log_file}" >&2 || true
	fail "server did not become healthy on ${BASE_URL}"
}

main() {
	local artifact_dir log_file
	require_disposable_database
	trap cleanup EXIT INT TERM
	work_dir="$(mktemp -d)"
	artifact_dir="${E2E_ARTIFACT_DIR:-${PROJECT_ROOT}/web/test-results}"
	mkdir -p "${artifact_dir}"
	log_file="${artifact_dir}/server.log"

	build_artifacts
	start_server "${log_file}"
	wait_for_healthz "${log_file}"

	cd "${PROJECT_ROOT}/web"
	E2E_BASE_URL="${BASE_URL}" E2E_ADMIN_USER="${ADMIN_USER}" E2E_ADMIN_PASSWORD="${ADMIN_PASSWORD}" \
		npm run e2e -- "$@"
}

main "$@"
