#!/usr/bin/env bash
# Unit tests for the host-side CI boundary. Docker-facing functions are stubbed,
# so these tests never contact a daemon or mutate the real checkout.
set -euo pipefail

TEST_SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly TEST_SCRIPT_DIR
cd "${TEST_SCRIPT_DIR}"
# shellcheck disable=SC1091 # Resolved at runtime after the script changes directory.
source ./ci-arm.sh

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}
expect_failure() {
	if "$@" >/dev/null 2>&1; then
		fail "expected failure: $*"
	fi
}

make_index_repo() {
	local repo=$1
	git init -q "${repo}"
	git -C "${repo}" config user.email ci@example.test
	git -C "${repo}" config user.name 'CI test'
	printf '%s\n' tracked >"${repo}/tracked.txt"
	: >"${repo}/Dockerfile.ci"
	: >"${repo}/docker-compose.ci.yml"
	mkdir -p "${repo}/internal/auth"
	: >"${repo}/internal/auth/auth_credentials.go"
	git -C "${repo}" add .
	# commit-tree builds a private fixture object without invoking repository hooks.
	local tree base_commit
	tree="$(git -C "${repo}" write-tree)"
	base_commit="$(printf 'initial\n' | git -C "${repo}" commit-tree "${tree}")"
	git -C "${repo}" update-ref refs/remotes/origin/main "${base_commit}"
}

test_allowlisted_env_and_redaction() {
	local env_file output
	env_file=$(mktemp)
	printf '%s\n' \
		'JACKUI_CI_IMAGE=from-file:old' \
		'JACKUI_CI_COMPOSE_PROJECT=jackui-ci-file' \
		'UNRELATED_SECRET=must-not-be-imported' >"${env_file}"
	export JACKUI_CI_IMAGE=from-environment:current
	unset JACKUI_CI_COMPOSE_PROJECT UNRELATED_SECRET
	load_ci_env "${env_file}"
	[[ "${JACKUI_CI_IMAGE}" == from-environment:current ]] || fail 'exported image was overwritten'
	[[ "${JACKUI_CI_COMPOSE_PROJECT}" == jackui-ci-file ]] || fail 'allowlisted value not loaded'
	[[ ! -v UNRELATED_SECRET ]] || fail 'non-allowlisted variable was imported'
	printf '%s\n' 'super-secret-value' >"${env_file}"
	if output="$(load_ci_env "${env_file}" 2>&1)"; then
		fail 'malformed CI .env line was accepted'
	fi
	[[ "${output}" != *super-secret-value* ]] || fail 'malformed CI .env content was echoed'
	rm -f "${env_file}"
}

test_validation() {
	# Values are consumed by functions in the sourced script.
	# shellcheck disable=SC2034
	JACKUI_CI_DOCKER_CONTEXT=default
	# shellcheck disable=SC2034
	JACKUI_CI_COMPOSE_PROJECT=jackui-ci-test
	# shellcheck disable=SC2034
	JACKUI_CI_POSTGRES_PORT=0
	# shellcheck disable=SC2034,SC2089 # JSON needs literal double quotes.
	JACKUI_CI_RUNNER_LABELS='["ubuntu-24.04-arm"]'
	# shellcheck disable=SC2034
	JACKUI_CI_IMAGE=registry.example/jackui-ci:local
	validate_ci_settings
	JACKUI_CI_DOCKER_CONTEXT=''
	expect_failure validate_ci_settings
	JACKUI_CI_DOCKER_CONTEXT='bad context;rm'
	expect_failure validate_ci_settings
	JACKUI_CI_DOCKER_CONTEXT=default
	JACKUI_CI_COMPOSE_PROJECT=other-project
	expect_failure validate_ci_settings
	JACKUI_CI_COMPOSE_PROJECT=jackui-ci-this-prefix-is-far-too-long
	expect_failure validate_ci_settings
	JACKUI_CI_COMPOSE_PROJECT=jackui-ci-test
	# shellcheck disable=SC2034
	JACKUI_CI_POSTGRES_PORT=65536
	expect_failure validate_ci_settings
	# shellcheck disable=SC2034
	JACKUI_CI_POSTGRES_PORT=0
	# shellcheck disable=SC2034
	JACKUI_CI_RUNNER_LABELS='not-json'
	expect_failure validate_ci_settings
}

test_run_suffix_is_unique() {
	local first second
	first="$(run_suffix)"
	second="$(run_suffix)"
	[[ "${first}" =~ ^[a-f0-9]{16}-[a-z0-9-]+$ ]] || fail "unsafe run suffix: ${first}"
	[[ "${first}" != "${second}" ]] || fail 'two CI runs received the same nonce'
}

test_index_snapshot_boundary() {
	local repo temp_root snapshot
	repo=$(mktemp -d)
	temp_root=$(mktemp -d)
	make_index_repo "${repo}"
	printf '%s\n' edited >>"${repo}/tracked.txt"
	expect_failure assert_index_snapshot_ready "${repo}"
	git -C "${repo}" checkout-index --force tracked.txt
	printf '%s\n' untracked >"${repo}/untracked.txt"
	expect_failure assert_index_snapshot_ready "${repo}"
	rm -f "${repo}/untracked.txt"
	printf '%s\n' staged >"${repo}/new-staged.txt"
	git -C "${repo}" add new-staged.txt
	assert_index_snapshot_ready "${repo}"
	CI_TEMP_ROOT="${temp_root}"
	CI_SNAPSHOT_DIR=""
	create_index_snapshot "${repo}"
	snapshot="${CI_SNAPSHOT_DIR}"
	[[ -f "${snapshot}/new-staged.txt" ]] || fail 'staged file missing from index snapshot'
	grep -Fq 'new-staged.txt' "${snapshot}/.ci-golangci.patch" || fail 'index patch missed staged file'
	[[ -f "${snapshot}/internal/auth/auth_credentials.go" ]] || fail 'tracked auth_credentials.go missing from snapshot'
	remove_snapshot "${snapshot}" "${temp_root}"
	[[ ! -e "${snapshot}" ]] || fail 'exact temporary snapshot was not removed'
	rm -rf -- "${repo}" "${temp_root}"
}

test_conflict_and_artifact_fail_closed() {
	local artifact_dir
	# shellcheck disable=SC2329 # Called indirectly by assert_project_unused.
	docker() {
		if [[ " $* " == *' ps '* ]]; then
			printf '%s\n' existing-resource
		fi
	}
	# shellcheck disable=SC2034
	JACKUI_CI_DOCKER_CONTEXT=default
	JACKUI_CI_EFFECTIVE_PROJECT=jackui-ci-test-conflict
	expect_failure assert_project_unused
	unset -f docker

	artifact_dir=$(mktemp -d)
	expect_failure verify_required_artifacts "${artifact_dir}"
	printf '%s\n' mode: set >"${artifact_dir}/coverage.out"
	printf '%s\n' '{"ok":true}' >"${artifact_dir}/healthz.json"
	verify_required_artifacts "${artifact_dir}"
	rm -rf -- "${artifact_dir}"
}

test_artifact_directory_must_be_empty() {
	local artifact_dir
	artifact_dir=$(mktemp -d)
	printf '%s\n' stale >"${artifact_dir}/coverage.out"
	CI_ARTIFACT_DIR="${artifact_dir}"
	expect_failure prepare_artifacts
	rm -f "${artifact_dir}/coverage.out"
	prepare_artifacts
	rmdir "${artifact_dir}"
	CI_ARTIFACT_DIR=''
}

test_jackett_endpoint_is_scoped_to_smoke() {
	! grep -Eq '^[[:space:]]*JACKETT_URL:' "${PROJECT_ROOT}/docker-compose.ci.yml" || fail 'Jackett endpoint leaked into CI test environment'
	grep -Eq '^[[:space:]]*JACKETT_URL=http://127\.0\.0\.1:9 "\$\{SERVER_BIN\}"' "${SCRIPT_DIR}/ci-container.sh" || fail 'smoke process does not isolate the Jackett endpoint'
}

test_workflow_prepares_artifact_directory() {
	grep -Fq 'scripts/ci-arm_test.sh' "${PROJECT_ROOT}/.github/workflows/ci.yml" || fail 'workflow does not gate the host-side CI orchestrator tests'
	# shellcheck disable=SC2016 # The workflow must contain this literal variable reference.
	grep -Fq 'mkdir -p -- "$CI_ARTIFACT_DIR"' "${PROJECT_ROOT}/.github/workflows/ci.yml" || fail 'workflow does not prepare its configured artifact directory'
}

test_cleanup_is_scoped() {
	local log_file cleanup_rc temp_root snapshot
	log_file=$(mktemp)
	temp_root=$(mktemp -d)
	snapshot=$(mktemp -d "${temp_root}/jackui-ci-snapshot.XXXXXX")
	: >"${snapshot}/.ci-golangci.patch"
	compose() { printf 'compose %s\n' "$*" >>"${log_file}"; }
	collect_artifacts() { printf '%s\n' collect >>"${log_file}"; }
	remove_execution_image() { printf '%s\n' image >>"${log_file}"; }
	# shellcheck disable=SC2034
	JACKUI_CI_EFFECTIVE_PROJECT=jackui-ci-test-123
	# shellcheck disable=SC2034
	JACKUI_CI_EFFECTIVE_IMAGE=jackui-ci:test-123
	CI_STACK_OWNED=1
	CI_IMAGE_EPHEMERAL=1
	CI_ARTIFACT_DIR=$(mktemp -d)
	# shellcheck disable=SC2034
	CI_TEMP_ROOT="${temp_root}"
	CI_SNAPSHOT_DIR="${snapshot}"
	# shellcheck disable=SC2034
	CLEANUP_DONE=0
	set +e
	(cleanup 17)
	cleanup_rc=$?
	set -e
	[[ "${cleanup_rc}" == 17 ]] || fail "cleanup changed exit code: ${cleanup_rc}"
	grep -Fqx 'compose down --volumes --remove-orphans' "${log_file}" || fail 'cleanup used an unsafe compose target'
	[[ ! -e "${snapshot}" ]] || fail 'cleanup did not remove only its snapshot'
	rm -f "${log_file}"
	rmdir "${CI_ARTIFACT_DIR}"
	rmdir "${temp_root}"
}

test_cleanup_failures_are_not_masked() {
	local artifact_dir cleanup_rc mode snapshot temp_root
	for mode in compose image; do
		temp_root=$(mktemp -d)
		snapshot=$(mktemp -d "${temp_root}/jackui-ci-snapshot.XXXXXX")
		: >"${snapshot}/.ci-golangci.patch"
		artifact_dir=$(mktemp -d)
		printf '%s\n' mode: set >"${artifact_dir}/coverage.out"
		printf '%s\n' '{"ok":true}' >"${artifact_dir}/healthz.json"
		set +e
		(
			# shellcheck disable=SC2329 # Called indirectly by cleanup.
			compose() { [[ "${mode}" != compose ]]; }
			# shellcheck disable=SC2329 # Called indirectly by cleanup.
			collect_artifacts() { return 0; }
			# shellcheck disable=SC2329 # Called indirectly by cleanup.
			remove_execution_image() { [[ "${mode}" != image ]]; }
			# shellcheck disable=SC2034
			JACKUI_CI_EFFECTIVE_PROJECT=jackui-ci-test-failure
			# shellcheck disable=SC2034
			JACKUI_CI_EFFECTIVE_IMAGE=jackui-ci:test-failure
			CI_STACK_OWNED=1
			CI_IMAGE_EPHEMERAL=1
			CI_ARTIFACT_DIR="${artifact_dir}"
			# shellcheck disable=SC2034
			CI_TEMP_ROOT="${temp_root}"
			CI_SNAPSHOT_DIR="${snapshot}"
			# shellcheck disable=SC2034
			CLEANUP_DONE=0
			cleanup 0
		)
		cleanup_rc=$?
		set -e
		[[ "${cleanup_rc}" == 1 ]] || fail "cleanup masked ${mode} teardown failure: ${cleanup_rc}"
		[[ ! -e "${snapshot}" ]] || fail "cleanup left snapshot after ${mode} teardown failure"
		rm -f "${artifact_dir}/coverage.out" "${artifact_dir}/healthz.json"
		rmdir "${artifact_dir}" "${temp_root}"
	done
}

test_conflict_never_cleans_unowned_stack() {
	local artifact_dir cleanup_rc log_file snapshot temp_root
	log_file=$(mktemp)
	temp_root=$(mktemp -d)
	snapshot=$(mktemp -d "${temp_root}/jackui-ci-snapshot.XXXXXX")
	: >"${snapshot}/.ci-golangci.patch"
	artifact_dir=$(mktemp -d)
	set +e
	# The functions and assignments below intentionally override sourced script
	# internals; main invokes them indirectly inside this isolated fixture.
	# shellcheck disable=SC2034,SC2329
	(
		set -e
		load_ci_env() { return 0; }
		set_ci_defaults() {
			JACKUI_CI_DOCKER_CONTEXT=default
			JACKUI_CI_COMPOSE_PROJECT=jackui-ci-test
			JACKUI_CI_RUNNER_LABELS='["ubuntu-24.04-arm"]'
			JACKUI_CI_IMAGE=jackui-ci:test
			JACKUI_CI_POSTGRES_PORT=0
		}
		validate_ci_settings() { return 0; }
		assert_index_snapshot_ready() { return 0; }
		create_index_snapshot() { CI_SNAPSHOT_DIR="${snapshot}"; }
		prepare_artifacts() { CI_ARTIFACT_DIR="${artifact_dir}"; }
		validate_docker_context() { return 0; }
		compose() { printf 'compose %s\n' "$*" >>"${log_file}"; }
		assert_project_unused() { return 73; }
		docker() { printf 'docker %s\n' "$*" >>"${log_file}"; }
		CI_STACK_OWNED=0
		CI_IMAGE_EPHEMERAL=0
		CI_TEMP_ROOT="${temp_root}"
		CLEANUP_DONE=0
		main all
	)
	cleanup_rc=$?
	set -e
	[[ "${cleanup_rc}" == 73 ]] || fail "conflict exit code changed: ${cleanup_rc}"
	grep -Fqx 'compose config' "${log_file}" || fail 'conflict fixture did not reach Compose validation'
	! grep -Eq 'compose down|^docker ' "${log_file}" || fail 'conflict cleanup touched an unowned Docker resource'
	[[ ! -e "${snapshot}" ]] || fail 'conflict cleanup did not remove its local snapshot'
	rm -f "${log_file}"
	rmdir "${artifact_dir}" "${temp_root}"
}

test_sonar_tls_is_fail_closed() {
	local sonar_script="${PROJECT_ROOT}/scripts/sonar/sonar-ephemeral.sh"
	! grep -Eq 'NODE_TLS_REJECT_UNAUTHORIZED|openssl s_client|local tls=\(-k\)' "${sonar_script}" || fail 'Sonar transport still permits unverified TLS'
	grep -Fq 'curl --fail --silent --show-error' "${sonar_script}" || fail 'Sonar API is not fail-closed'
}

test_deepwork_ignore_override_is_complete() {
	grep -Fqx '!.slim/' "${PROJECT_ROOT}/.ignore" || fail '.ignore does not reopen the ignored .slim parent'
	grep -Fqx '!.slim/deepwork/**' "${PROJECT_ROOT}/.ignore" || fail '.ignore does not expose deepwork state to repository tools'
}

test_host_suite_has_no_ripgrep_dependency() {
	! grep -Eq '^[[:space:]]*(![[:space:]]+)?rg[[:space:]]' "${TEST_SCRIPT_DIR}/ci-arm_test.sh" || fail 'host-side CI tests require ripgrep'
}

test_allowlisted_env_and_redaction
test_validation
test_run_suffix_is_unique
test_index_snapshot_boundary
test_conflict_and_artifact_fail_closed
test_artifact_directory_must_be_empty
test_jackett_endpoint_is_scoped_to_smoke
test_workflow_prepares_artifact_directory
test_cleanup_is_scoped
test_cleanup_failures_are_not_masked
test_conflict_never_cleans_unowned_stack
test_sonar_tls_is_fail_closed
test_deepwork_ignore_override_is_complete
test_host_suite_has_no_ripgrep_dependency
printf 'ci-arm script tests passed\n'
