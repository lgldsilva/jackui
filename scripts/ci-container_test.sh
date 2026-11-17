#!/usr/bin/env bash
# Contract tests for the Go gates in ci-container.sh and the Makefile. They are
# static on purpose: the gates themselves only run inside the CI image, so the
# cheap host-side check is that nobody narrows their scope again.
# shellcheck disable=SC2016 # Assertions match literal $(...) and ${...} text.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly PROJECT_ROOT="${SCRIPT_DIR}/.."
readonly CONTAINER_SCRIPT="${SCRIPT_DIR}/ci-container.sh"
readonly MAKEFILE="${PROJECT_ROOT}/Makefile"
readonly E2E_SMOKE_SCRIPT="${SCRIPT_DIR}/e2e-smoke.sh"
readonly RACE_PKGS=(streamer downloads transcode transfer localcache watchlist config)
# internal/handlers is too slow to run whole under the race detector; the gate
# runs these tests by name instead.
readonly HANDLERS_RACE_TESTS=(TestConfigHandlers_ConcurrentWritesAndReadsAreRaceFree)

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# Every line of $1 matching the ERE $2 must also contain the fixed string $3,
# and at least one line must match.
assert_lines_contain() {
	local file=$1 pattern=$2 needle=$3 lines
	lines="$(grep -E -- "${pattern}" "${file}" || true)"
	[[ -n "${lines}" ]] || fail "${file##*/}: no line matches ${pattern}"
	! grep -Fv -- "${needle}" <<<"${lines}" || fail "${file##*/}: line above does not cover ${needle}"
}

test_go_gates_cover_cmd() {
	assert_lines_contain "${CONTAINER_SCRIPT}" '^[[:space:]]*go vet ' './cmd/...'
	assert_lines_contain "${CONTAINER_SCRIPT}" '^[[:space:]]*go test .*-coverprofile' './cmd/...'
	assert_lines_contain "${MAKEFILE}" '^GO_TEST_PKGS[[:space:]]*:?=' './cmd/...'
	assert_lines_contain "${MAKEFILE}" '^[[:space:]]*@?go test ' '$(GO_'
	! grep -En '^[[:space:]]*@?go (vet|test) .*\./internal/\.\.\.' "${MAKEFILE}" || fail 'Makefile hardcodes an internal-only Go package list'
}

# cmd/server embeds ui/dist, which a fresh checkout does not have: without the
# placeholder the cmd packages do not compile and the gate above cannot run.
test_ui_dist_is_seeded_before_go_gates() {
	local seed_line vet_line
	seed_line="$(grep -n '^[[:space:]]*seed_ui_dist$' "${CONTAINER_SCRIPT}" | head -1 | cut -d: -f1)"
	vet_line="$(grep -n '^[[:space:]]*run_go_gates$' "${CONTAINER_SCRIPT}" | head -1 | cut -d: -f1)"
	[[ -n "${seed_line}" && -n "${vet_line}" ]] || fail 'ci-container.sh does not seed ui/dist and run the Go gates from main'
	((seed_line < vet_line)) || fail 'ci-container.sh runs the Go gates before seeding ui/dist'
	assert_lines_contain "${MAKEFILE}" '^(test|test-verbose|sonar-scan):' '_ui-dist-stub'
}

test_race_detector_gate() {
	local pkg
	assert_lines_contain "${CONTAINER_SCRIPT}" '^[[:space:]]*go test .*-race.*RACE_PKGS' '"${RACE_PKGS[@]}"'
	assert_lines_contain "${MAKEFILE}" '^[[:space:]]*@?go test .*-race.*RACE_PKGS' '$(GO_RACE_PKGS)'
	for pkg in "${RACE_PKGS[@]}"; do
		grep -Eq "^[[:space:]]*\./internal/${pkg}/\.\.\.\$" "${CONTAINER_SCRIPT}" || fail "CI race gate misses internal/${pkg}"
		grep -E '^GO_RACE_PKGS[[:space:]]*:?=' "${MAKEFILE}" | grep -Fq "./internal/${pkg}/..." || fail "Makefile race gate misses internal/${pkg}"
	done
}

# A -run filter that matches nothing still exits 0, so a renamed test would
# silently empty the gate: each name must be in both gates and still exist.
test_handlers_race_gate() {
	local name
	assert_lines_contain "${CONTAINER_SCRIPT}" '^[[:space:]]*go test .*-race.*\./internal/handlers/' '"${HANDLERS_RACE_RUN}"'
	assert_lines_contain "${MAKEFILE}" '^[[:space:]]*@?go test .*-race.*\./internal/handlers/' '$(GO_RACE_HANDLERS_RUN)'
	for name in "${HANDLERS_RACE_TESTS[@]}"; do
		grep -E '^readonly HANDLERS_RACE_RUN=' "${CONTAINER_SCRIPT}" | grep -Fq "${name}" || fail "CI race gate misses ${name}"
		grep -E '^GO_RACE_HANDLERS_RUN[[:space:]]*:?=' "${MAKEFILE}" | grep -Fq "${name}" || fail "Makefile race gate misses ${name}"
		grep -rqE "^func ${name}\(" "${PROJECT_ROOT}/internal/handlers" || fail "internal/handlers has no test named ${name}"
	done
}

test_workflow_runs_this_suite() {
	grep -Fq 'scripts/ci-container_test.sh' "${PROJECT_ROOT}/.github/workflows/ci.yml" || fail 'workflow does not run the CI container contract tests'
}

# The Playwright page smoke has one entry point shared by CI and `make`, and
# the web gates above (tsc, eslint) must keep covering the spec itself.
test_e2e_smoke_is_wired() {
	local web="${PROJECT_ROOT}/web"
	grep -Eq '^[[:space:]]*run: scripts/e2e-smoke\.sh$' "${PROJECT_ROOT}/.github/workflows/ci.yml" || fail 'workflow does not run scripts/e2e-smoke.sh'
	assert_lines_contain "${MAKEFILE}" '^[[:space:]]*@?\./scripts/e2e-smoke\.sh' './scripts/e2e-smoke.sh'
	assert_lines_contain "${web}/package.json" '"e2e":' 'playwright test'
	assert_lines_contain "${web}/package.json" '"lint":' ' e2e '
	assert_lines_contain "${web}/tsconfig.json" '"include":' '"e2e"'
}

# Runs ONLY require_disposable_database from the sourced script, in a child
# shell: nothing is built, connected to or started.
e2e_guard_accepts() {
	E2E_DATABASE_URL=$1 bash -c 'source "$1"; require_disposable_database' _ "${E2E_SMOKE_SCRIPT}" 2>/dev/null
}

# The smoke server migrates and boots every worker against this database, so
# the guard must parse the database name instead of spotting "test" anywhere.
test_e2e_guard_accepts_disposable_databases() {
	local url
	for url in \
		'postgres://jackui:jackui_ci_test_only@127.0.0.1:5432/jackui_test?sslmode=disable' \
		'postgresql://user:pw@127.0.0.1:55432/jackui_test' \
		'postgres://user:pw@localhost/test' \
		'postgres://user:pw@localhost/test_jackui?sslmode=disable' \
		'postgres://user:pw@localhost/jackui_test_e2e'; do
		e2e_guard_accepts "${url}" || fail "e2e guard rejects a disposable database: ${url}"
	done
	grep -Fq 'E2E_DATABASE_URL: postgres://jackui:jackui_ci_test_only@127.0.0.1:5432/jackui_test?sslmode=disable' "${PROJECT_ROOT}/.github/workflows/ci.yml" ||
		fail 'workflow E2E_DATABASE_URL differs from the URL the guard test accepts'
}

test_e2e_guard_rejects_production_shaped_values() {
	local url
	for url in \
		'' \
		'postgres://jackui:pw@testhost:5432' \
		'postgres://jackui:test123@127.0.0.1:5432' \
		'postgres://test:pw@127.0.0.1:5432' \
		'postgres://test:test@testhost:5432/jackui' \
		'host=127.0.0.1 user=jackui password=test123 dbname=jackui' \
		'host=127.0.0.1 dbname=jackui application_name=test' \
		'host=127.0.0.1 dbname=jackui_test' \
		'postgres://jackui:pw@host/jackui#test' \
		'postgres://jackui:pw@host/jackui_test#frag' \
		'postgres://jackui:pw@host/jackui?application_name=test' \
		'postgres://jackui:pw@host/contest_prod' \
		'postgres://jackui:pw@host/latest' \
		'postgres://jackui:pw@host/jackui/test' \
		'postgres://jackui:pw@host/' \
		'mysql://jackui:pw@host/jackui_test'; do
		! e2e_guard_accepts "${url}" || fail "e2e guard accepts a non-disposable value: ${url}"
	done
}

# The job is not a required check and must never run inside a reusable call:
# release.yml calls ci.yml, and a flaky smoke would then block a release.
test_e2e_smoke_skips_reusable_calls() {
	local job
	job="$(awk '/^  e2e-smoke:$/ {on = 1; next} on && /^  [a-z][a-z0-9-]*:$/ {exit} on' "${PROJECT_ROOT}/.github/workflows/ci.yml")"
	[[ -n "${job}" ]] || fail 'workflow has no e2e-smoke job'
	grep -Fqx "    if: github.event_name == 'pull_request' || (github.event_name == 'push' && github.ref == 'refs/heads/main')" <<<"${job}" ||
		fail 'e2e-smoke job lost the condition that skips it on release (workflow_call) runs'
}

# A local run puts a torrent client with DHT on the host network; the script
# must demand an explicit opt-in outside CI and never reuse a busy port.
test_e2e_smoke_local_run_is_opt_in() {
	local out
	out="$(env -u CI -u E2E_ALLOW_HOST_NETWORK bash -c 'source "$1"; require_network_opt_in' _ "${E2E_SMOKE_SCRIPT}" 2>&1)" &&
		fail 'e2e smoke starts outside CI without E2E_ALLOW_HOST_NETWORK=1'
	grep -Fq 'E2E_ALLOW_HOST_NETWORK=1' <<<"${out}" || fail 'e2e smoke opt-in refusal does not name the variable'
	env -u CI E2E_ALLOW_HOST_NETWORK=1 bash -c 'source "$1"; require_network_opt_in' _ "${E2E_SMOKE_SCRIPT}" || fail 'e2e smoke ignores E2E_ALLOW_HOST_NETWORK=1'
	env -u E2E_ALLOW_HOST_NETWORK CI=true bash -c 'source "$1"; require_network_opt_in' _ "${E2E_SMOKE_SCRIPT}" || fail 'e2e smoke refuses to run in CI'
	grep -Eq '^[[:space:]]*require_free_port$' "${E2E_SMOKE_SCRIPT}" || fail 'e2e smoke does not check that its port is free'
}

test_go_gates_cover_cmd
test_ui_dist_is_seeded_before_go_gates
test_race_detector_gate
test_handlers_race_gate
test_workflow_runs_this_suite
test_e2e_smoke_is_wired
test_e2e_guard_accepts_disposable_databases
test_e2e_guard_rejects_production_shaped_values
test_e2e_smoke_skips_reusable_calls
test_e2e_smoke_local_run_is_opt_in
printf 'ci-container contract tests passed\n'
