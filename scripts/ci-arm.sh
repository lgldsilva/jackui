#!/usr/bin/env bash
# Run the complete, disposable JackUI CI stack from an explicit Git-index
# snapshot. The Docker daemon (especially over SSH) never receives the live
# checkout, local credentials, untracked files, or unstaged edits.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
PROJECT_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
readonly PROJECT_ROOT

CI_ARTIFACT_DIR="${CI_ARTIFACT_DIR:-}"
CI_TEMP_ROOT="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"
CI_SNAPSHOT_DIR=""
JACKUI_CI_EFFECTIVE_PROJECT=""
JACKUI_CI_EFFECTIVE_IMAGE=""
CI_IMAGE_EPHEMERAL=0
CI_STACK_OWNED=0
CLEANUP_DONE=0

trim() {
	local value=$1
	value="${value#"${value%%[![:space:]]*}"}"
	value="${value%"${value##*[![:space:]]}"}"
	printf '%s' "${value}"
}

is_ci_key() {
	case "$1" in
	JACKUI_CI_DOCKER_CONTEXT | JACKUI_CI_COMPOSE_PROJECT | JACKUI_CI_RUNNER_LABELS | JACKUI_CI_IMAGE | JACKUI_CI_POSTGRES_PORT) return 0 ;;
	*) return 1 ;;
	esac
}

load_ci_env() {
	local env_file=$1 raw line key value quote line_number=0
	[[ -f "${env_file}" ]] || return 0
	while IFS= read -r raw || [[ -n "${raw}" ]]; do
		((line_number += 1))
		line="$(trim "${raw}")"
		[[ -z "${line}" || "${line}" == \#* ]] && continue
		[[ "${line}" == export\ * ]] && line="$(trim "${line#export }")"
		if [[ "${line}" != *=* ]]; then
			printf 'invalid CI .env syntax on line %d\n' "${line_number}" >&2
			return 64
		fi
		key="$(trim "${line%%=*}")"
		value="$(trim "${line#*=}")"
		is_ci_key "${key}" || continue
		if [[ "${value}" =~ ^\".*\"$ || "${value}" =~ ^\'.*\'$ ]]; then
			quote=${value:0:1}
			[[ "${value: -1}" == "${quote}" ]] || {
				printf 'unclosed CI .env value on line %d\n' "${line_number}" >&2
				return 64
			}
			value=${value:1:${#value}-2}
		fi
		# `-v` distinguishes an explicitly exported empty value from an unset one.
		if [[ ! -v "${key}" ]]; then
			printf -v "${key}" '%s' "${value}"
			export "${key?}"
		fi
	done <"${env_file}"
}

set_ci_defaults() {
	if [[ ! -v JACKUI_CI_DOCKER_CONTEXT ]]; then
		JACKUI_CI_DOCKER_CONTEXT=default
	fi
	if [[ ! -v JACKUI_CI_COMPOSE_PROJECT ]]; then
		JACKUI_CI_COMPOSE_PROJECT=jackui-ci
	fi
	if [[ ! -v JACKUI_CI_RUNNER_LABELS ]]; then
		# shellcheck disable=SC2089 # JSON needs literal double quotes.
		JACKUI_CI_RUNNER_LABELS='["ubuntu-24.04-arm"]'
	fi
	if [[ ! -v JACKUI_CI_IMAGE ]]; then
		JACKUI_CI_IMAGE=jackui-ci:local
	fi
	if [[ ! -v JACKUI_CI_POSTGRES_PORT ]]; then
		JACKUI_CI_POSTGRES_PORT=0
	fi
	# shellcheck disable=SC2090 # Runner labels intentionally contain JSON quotes.
	export JACKUI_CI_DOCKER_CONTEXT JACKUI_CI_COMPOSE_PROJECT JACKUI_CI_RUNNER_LABELS JACKUI_CI_IMAGE JACKUI_CI_POSTGRES_PORT
}

validate_ci_settings() {
	[[ -n "${JACKUI_CI_DOCKER_CONTEXT}" && "${JACKUI_CI_DOCKER_CONTEXT}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || {
		printf 'JACKUI_CI_DOCKER_CONTEXT must be a non-empty safe Docker context name\n' >&2
		return 64
	}
	[[ "${JACKUI_CI_COMPOSE_PROJECT}" =~ ^jackui-ci([_-][a-z0-9][a-z0-9_-]*)?$ ]] || {
		printf 'JACKUI_CI_COMPOSE_PROJECT must start with jackui-ci and use lowercase safe characters\n' >&2
		return 64
	}
	((${#JACKUI_CI_COMPOSE_PROJECT} <= 22)) || {
		printf 'JACKUI_CI_COMPOSE_PROJECT must be at most 22 characters before the unique suffix\n' >&2
		return 64
	}
	if ! [[ "${JACKUI_CI_POSTGRES_PORT}" =~ ^[0-9]+$ ]] || ((JACKUI_CI_POSTGRES_PORT > 65535)); then
		printf 'JACKUI_CI_POSTGRES_PORT must be between 0 and 65535\n' >&2
		return 64
	fi
	jq -e 'type == "array" and length > 0 and all(.[]; type == "string" and test("^[A-Za-z0-9_.-]+$"))' \
		<<<"${JACKUI_CI_RUNNER_LABELS}" >/dev/null || {
		printf 'JACKUI_CI_RUNNER_LABELS must be a non-empty JSON array of safe label strings\n' >&2
		return 64
	}
	[[ "${JACKUI_CI_IMAGE}" =~ ^[a-z0-9][a-z0-9._-]*(:[0-9]+)?(/[a-z0-9][a-z0-9._-]*)*(:[A-Za-z0-9][A-Za-z0-9._-]*)?$ ]] || {
		printf 'JACKUI_CI_IMAGE is not a safe Docker image reference\n' >&2
		return 64
	}
}

assert_index_snapshot_ready() {
	local root=$1 untracked_count
	git -C "${root}" diff --quiet --ignore-submodules -- || {
		printf 'CI requires an index snapshot: stage or discard tracked working-tree changes first\n' >&2
		return 64
	}
	untracked_count="$(git -C "${root}" ls-files --others --exclude-standard | wc -l | tr -d '[:space:]')"
	[[ "${untracked_count}" == 0 ]] || {
		printf 'CI requires an index snapshot: stage or remove %s untracked file(s) first\n' "${untracked_count}" >&2
		return 64
	}
}

create_index_snapshot() {
	local root=$1 base_ref=origin/main
	[[ -d "${CI_TEMP_ROOT}" ]] || {
		printf 'CI temporary directory is unavailable\n' >&2
		return 73
	}
	git -C "${root}" rev-parse --verify "${base_ref}" >/dev/null
	CI_SNAPSHOT_DIR="$(mktemp -d "${CI_TEMP_ROOT%/}/jackui-ci-snapshot.XXXXXX")"
	# Mark it before any operation that can fail so the EXIT trap can prove it
	# owns this exact temporary directory.
	: >"${CI_SNAPSHOT_DIR}/.ci-golangci.patch"
	git -C "${root}" checkout-index --all --prefix="${CI_SNAPSHOT_DIR}/"
	git -C "${root}" diff --cached --binary "${base_ref}" >"${CI_SNAPSHOT_DIR}/.ci-golangci.patch"
	[[ -f "${CI_SNAPSHOT_DIR}/docker-compose.ci.yml" && -f "${CI_SNAPSHOT_DIR}/Dockerfile.ci" ]] || {
		printf 'CI snapshot is missing its tracked stack files; stage the intended snapshot first\n' >&2
		return 64
	}
	export CI_SNAPSHOT_DIR
}

remove_snapshot() {
	local snapshot=$1 temp_root=$2
	[[ -n "${snapshot}" && -n "${temp_root}" && -d "${snapshot}" ]] || return 0
	case "${snapshot}" in
	"${temp_root%/}"/jackui-ci-snapshot.*) ;;
	*)
		printf 'refusing to remove a non-CI temporary directory\n' >&2
		return 64
		;;
	esac
	[[ -f "${snapshot}/.ci-golangci.patch" ]] || {
		printf 'refusing to remove an unmarked CI temporary directory\n' >&2
		return 64
	}
	rm -rf -- "${snapshot}"
}

run_suffix() {
	local entropy source
	entropy="$(od -An -N8 -tx1 /dev/urandom | tr -d '[:space:]')"
	[[ "${entropy}" =~ ^[a-f0-9]{16}$ ]] || {
		printf 'failed to generate a unique CI run suffix\n' >&2
		return 70
	}
	source="${entropy}-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}"
	source=${source,,}
	source=${source//[^a-z0-9]/-}
	printf '%s' "${source:0:40}"
}

derive_execution_names() {
	local suffix=$1 image_name image_tag
	JACKUI_CI_EFFECTIVE_PROJECT="${JACKUI_CI_COMPOSE_PROJECT}-${suffix}"
	if [[ "${JACKUI_CI_IMAGE}" == *:* && "${JACKUI_CI_IMAGE##*/}" == *:* ]]; then
		image_name=${JACKUI_CI_IMAGE%:*}
		image_tag=${JACKUI_CI_IMAGE##*:}
	else
		image_name=${JACKUI_CI_IMAGE}
		image_tag=local
	fi
	JACKUI_CI_EFFECTIVE_IMAGE="${image_name}:${image_tag}-ci-${suffix}"
	export JACKUI_CI_EFFECTIVE_PROJECT JACKUI_CI_EFFECTIVE_IMAGE
}

compose() {
	docker --context "$JACKUI_CI_DOCKER_CONTEXT" compose --project-directory "$CI_SNAPSHOT_DIR" -p "$JACKUI_CI_EFFECTIVE_PROJECT" -f "$CI_SNAPSHOT_DIR/docker-compose.ci.yml" "$@"
}

validate_docker_context() {
	local server_arch
	docker context inspect -- "$JACKUI_CI_DOCKER_CONTEXT" >/dev/null
	server_arch="$(docker --context "$JACKUI_CI_DOCKER_CONTEXT" version --format '{{.Server.Arch}}')"
	[[ "${server_arch}" == arm64 ]] || {
		printf 'JackUI CI requires an ARM64 Docker daemon; got %s\n' "${server_arch}" >&2
		return 64
	}
	JACKUI_CI_DOCKER_ARCH="${server_arch}"
	export JACKUI_CI_DOCKER_ARCH
}

assert_project_unused() {
	local resources
	resources="$(docker --context "$JACKUI_CI_DOCKER_CONTEXT" ps -aq --filter "label=com.docker.compose.project=${JACKUI_CI_EFFECTIVE_PROJECT}")"
	resources+="$(docker --context "$JACKUI_CI_DOCKER_CONTEXT" network ls -q --filter "label=com.docker.compose.project=${JACKUI_CI_EFFECTIVE_PROJECT}")"
	resources+="$(docker --context "$JACKUI_CI_DOCKER_CONTEXT" volume ls -q --filter "label=com.docker.compose.project=${JACKUI_CI_EFFECTIVE_PROJECT}")"
	[[ -z "${resources}" ]] || {
		printf 'refusing to reuse an existing CI Compose project; choose a new run suffix\n' >&2
		return 73
	}
}

prepare_artifacts() {
	if [[ -z "${CI_ARTIFACT_DIR}" ]]; then
		CI_ARTIFACT_DIR="$(mktemp -d "${CI_TEMP_ROOT%/}/jackui-ci-artifacts.XXXXXX")"
	else
		[[ -d "${CI_ARTIFACT_DIR}" ]] || {
			printf 'CI_ARTIFACT_DIR must be an existing directory when provided\n' >&2
			return 64
		}
		[[ -z "$(find "${CI_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 -print -quit)" ]] || {
			printf 'CI_ARTIFACT_DIR must be empty to prevent stale artifact reuse\n' >&2
			return 73
		}
	fi
}

collect_artifacts() {
	local rc=0
	[[ "${CI_STACK_OWNED}" == 1 && -n "${JACKUI_CI_EFFECTIVE_PROJECT}" && -n "${CI_ARTIFACT_DIR}" ]] || return 0
	compose logs --no-color >"${CI_ARTIFACT_DIR}/compose.log" 2>&1 || rc=1
	compose cp ci:/artifacts/. "${CI_ARTIFACT_DIR}" >/dev/null 2>&1 || rc=1
	return "${rc}"
}

verify_required_artifacts() {
	local artifact_dir=$1
	[[ -s "${artifact_dir}/coverage.out" ]] || {
		printf 'required CI coverage artifact was not recovered\n' >&2
		return 1
	}
	[[ -s "${artifact_dir}/healthz.json" ]] || {
		printf 'required CI healthz artifact was not recovered\n' >&2
		return 1
	}
}

remove_execution_image() {
	[[ "${CI_IMAGE_EPHEMERAL}" == 1 && -n "${JACKUI_CI_EFFECTIVE_IMAGE}" ]] || return 0
	# The unique tag is only this invocation's. A non-forced remove refuses an
	# image still referenced by a container outside this project.
	docker --context "$JACKUI_CI_DOCKER_CONTEXT" image rm "$JACKUI_CI_EFFECTIVE_IMAGE" >/dev/null 2>&1
}

cleanup() {
	local rc=${1:-$?} artifact_rc=0 teardown_rc=0
	if [[ "${CLEANUP_DONE}" == 1 ]]; then
		trap - EXIT INT TERM
		exit "${rc}"
	fi
	CLEANUP_DONE=1
	trap - EXIT INT TERM
	set +e
	collect_artifacts || artifact_rc=1
	if ((rc == 0)) && { ((artifact_rc != 0)) || ! verify_required_artifacts "${CI_ARTIFACT_DIR}"; }; then
		rc=1
	fi
	if [[ "${CI_STACK_OWNED}" == 1 && -n "${JACKUI_CI_EFFECTIVE_PROJECT}" ]]; then
		compose down --volumes --remove-orphans >/dev/null 2>&1 || teardown_rc=1
	fi
	remove_execution_image || teardown_rc=1
	remove_snapshot "${CI_SNAPSHOT_DIR}" "${CI_TEMP_ROOT}" || {
		teardown_rc=1
	}
	((rc == 0 && teardown_rc != 0)) && rc=1
	exit "${rc}"
}

main() {
	[[ "${1:-all}" == all ]] || {
		printf 'usage: %s all\n' "$0" >&2
		return 64
	}
	load_ci_env "${PROJECT_ROOT}/.env"
	set_ci_defaults
	validate_ci_settings
	assert_index_snapshot_ready "${PROJECT_ROOT}"
	trap 'cleanup "$?"' EXIT INT TERM
	create_index_snapshot "${PROJECT_ROOT}"
	prepare_artifacts
	validate_docker_context
	derive_execution_names "$(run_suffix)"
	compose config >/dev/null
	assert_project_unused
	CI_STACK_OWNED=1
	CI_IMAGE_EPHEMERAL=1
	printf 'JackUI CI: context=%s project=%s image=%s artifacts=%s\n' \
		"${JACKUI_CI_DOCKER_CONTEXT}" "${JACKUI_CI_EFFECTIVE_PROJECT}" "${JACKUI_CI_EFFECTIVE_IMAGE}" "${CI_ARTIFACT_DIR}"
	# `pipefail` is enabled globally: tee preserves build/test output in a
	# recoverable artifact without hiding Compose's exit code.
	compose up --build --abort-on-container-exit --exit-code-from ci ci 2>&1 | tee "${CI_ARTIFACT_DIR}/run.log"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
