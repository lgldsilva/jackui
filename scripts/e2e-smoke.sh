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
# Outside CI it also needs E2E_ALLOW_HOST_NETWORK=1: the server starts its
# BitTorrent client (peer port + DHT) on the host network.
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
# Per-run credentials for a throwaway database, generated in main: nothing
# reusable lives in the repo.
ADMIN_PASSWORD=""
JWT_SECRET=""
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

# Only the URL form with an explicit database path is accepted, so the name is
# never guessed: keyword DSNs, path-less URLs (the driver would default the
# database to the user name) and fragments are rejected. The name must carry
# "test" as a whole underscore-separated token: jackui_test, test_jackui.
require_disposable_database() {
	local url_re='^postgres(ql)?://[^/?#]+/([^/?#]+)(\?[^#]*)?$'
	local name_re='^(.+_)?test(_.+)?$'
	[[ -n "${E2E_DATABASE_URL:-}" ]] || fail 'E2E_DATABASE_URL is required (a disposable PostgreSQL database)'
	[[ "${E2E_DATABASE_URL}" =~ ${url_re} ]] ||
		fail 'refusing to run: E2E_DATABASE_URL must be postgres://user:pass@host[:port]/<database>[?params]'
	[[ "${BASH_REMATCH[2]}" =~ ${name_re} ]] ||
		fail 'refusing to run: the database name in E2E_DATABASE_URL must be "test" or carry it as a token (jackui_test, test_jackui)'
}

# The server has no switch to turn off the torrent client, so it opens the
# default peer port and joins the DHT on whatever network the host uses. CI
# runners are throwaway; a workstation (which may deliberately keep torrent
# traffic behind a VPN) has to opt in.
require_network_opt_in() {
	[[ -n "${CI:-}" || "${E2E_ALLOW_HOST_NETWORK:-0}" == 1 ]] ||
		fail 'refusing to run outside CI: the smoke server starts a BitTorrent client (peer port + DHT) on this host network; set E2E_ALLOW_HOST_NETWORK=1 to accept that'
}

# Never run the tests against a process this script did not start.
require_free_port() {
	if (exec 3<>"/dev/tcp/127.0.0.1/${PORT}") 2>/dev/null; then
		fail "port ${PORT} is already in use; set E2E_PORT to a free one"
	fi
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
	require_network_opt_in
	require_free_port
	trap cleanup EXIT INT TERM
	ADMIN_PASSWORD="$(openssl rand -hex 16)"
	JWT_SECRET="$(openssl rand -hex 32)"
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

# Sourced by scripts/ci-container_test.sh to exercise the guards in isolation.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
