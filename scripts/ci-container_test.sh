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
readonly RACE_PKGS=(streamer downloads transcode transfer localcache watchlist)

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
	assert_lines_contain "${CONTAINER_SCRIPT}" '^[[:space:]]*go test .*-race' '"${RACE_PKGS[@]}"'
	assert_lines_contain "${MAKEFILE}" '^[[:space:]]*@?go test .*-race' '$(GO_RACE_PKGS)'
	for pkg in "${RACE_PKGS[@]}"; do
		grep -Eq "^[[:space:]]*\./internal/${pkg}/\.\.\.\$" "${CONTAINER_SCRIPT}" || fail "CI race gate misses internal/${pkg}"
		grep -E '^GO_RACE_PKGS[[:space:]]*:?=' "${MAKEFILE}" | grep -Fq "./internal/${pkg}/..." || fail "Makefile race gate misses internal/${pkg}"
	done
}

test_workflow_runs_this_suite() {
	grep -Fq 'scripts/ci-container_test.sh' "${PROJECT_ROOT}/.github/workflows/ci.yml" || fail 'workflow does not run the CI container contract tests'
}

test_go_gates_cover_cmd
test_ui_dist_is_seeded_before_go_gates
test_race_detector_gate
test_workflow_runs_this_suite
printf 'ci-container contract tests passed\n'
