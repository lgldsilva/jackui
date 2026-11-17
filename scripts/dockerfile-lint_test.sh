#!/usr/bin/env bash
# Supply-chain contract tests for the shipped Dockerfiles (every Dockerfile*
# except Dockerfile.ci, which ci-arm_test.sh covers). They are static on
# purpose: the images are only built by the release workflow, so the cheap
# host-side check is that nobody reintroduces a floating base tag or lets npm
# dependency lifecycle scripts run during a release build.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# DOCKERFILE_LINT_ROOT lets the self-test below point the checks at fixtures.
readonly PROJECT_ROOT="${DOCKERFILE_LINT_ROOT:-${SCRIPT_DIR}/..}"
readonly DIGEST_RE='@sha256:[0-9a-f]{64}$'

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

production_dockerfiles() {
	local file
	for file in "${PROJECT_ROOT}"/Dockerfile*; do
		[[ -f "${file}" && "${file##*/}" != Dockerfile.ci ]] && printf '%s\n' "${file}"
	done
	return 0
}

# Prints "<line>:<image>" for each FROM that is neither `scratch` nor an earlier
# build stage. Flags such as --platform are skipped; the stage name follows AS.
external_bases() {
	awk '
		toupper($1) == "FROM" {
			i = 2
			while (i <= NF && $i ~ /^--/) i++
			image = $i
			if (image != "scratch" && !(tolower(image) in stages)) print NR ":" image
			if (toupper($(i + 1)) == "AS") stages[tolower($(i + 2))] = 1
		}
	' "$1"
}

check_bases_are_digest_pinned() {
	local file=$1 entry image
	while IFS= read -r entry; do
		image="${entry#*:}"
		[[ "${image}" =~ ${DIGEST_RE} ]] || fail "${file##*/}:${entry%%:*}: base image ${image} is not pinned by @sha256 digest"
		# Keep the tag next to the digest: it is what Dependabot bumps and what a
		# reviewer reads.
		[[ "${image%@*}" == *:* ]] || fail "${file##*/}:${entry%%:*}: base image ${image} has a digest but no tag"
	done < <(external_bases "${file}")
}

check_npm_ci_ignores_scripts() {
	local file=$1 lines
	lines="$(grep -nE '(^|[^[:alnum:]_-])npm[[:space:]]+(ci|install|i)([[:space:]]|$)' "${file}" | grep -vE '^[0-9]+:[[:space:]]*#' || true)"
	[[ -z "${lines}" ]] && return 0
	! grep -Fv -- '--ignore-scripts' <<<"${lines}" || fail "${file##*/}: npm install above runs dependency lifecycle scripts (add --ignore-scripts)"
}

test_production_dockerfiles() {
	local file count=0
	while IFS= read -r file; do
		[[ -n "$(external_bases "${file}")" ]] || fail "${file##*/}: no base image found"
		check_bases_are_digest_pinned "${file}"
		check_npm_ci_ignores_scripts "${file}"
		count=$((count + 1))
	done < <(production_dockerfiles)
	((count > 0)) || fail 'no production Dockerfile found'
}

# The checks above pass vacuously if the parser stops matching, so prove on
# fixtures that each violation is caught and that the exceptions are honoured.
expect_fixture() {
	local expected=$1 name=$2 body=$3 dir status=0
	dir="$(mktemp -d)"
	printf '%s\n' "${body}" >"${dir}/Dockerfile"
	DOCKERFILE_LINT_ROOT="${dir}" DOCKERFILE_LINT_FIXTURE=1 bash "${BASH_SOURCE[0]}" >/dev/null 2>&1 || status=1
	rm -rf "${dir}"
	[[ "${status}" == "${expected}" ]] || fail "self-test '${name}': expected exit ${expected}, got ${status}"
}

test_self() {
	local pin='@sha256:0000000000000000000000000000000000000000000000000000000000000000'
	expect_fixture 0 'pinned multi-stage' "FROM node:1-alpine${pin} AS web
RUN npm ci --ignore-scripts
FROM --platform=\$BUILDPLATFORM golang:1${pin} AS backend
FROM web AS again
FROM scratch
COPY --from=backend /x /x"
	expect_fixture 1 'floating tag' 'FROM alpine:3.24'
	expect_fixture 1 'floating tag behind a flag' "FROM --platform=linux/amd64 alpine:3.24 AS a"
	expect_fixture 1 'digest without a tag' "FROM alpine${pin}"
	expect_fixture 1 'truncated digest' 'FROM alpine:3.24@sha256:abc'
	expect_fixture 1 'build-arg base' "FROM \${BASE_IMAGE}"
	expect_fixture 1 'npm ci with scripts' "FROM alpine:3${pin}
RUN npm ci"
	expect_fixture 1 'chained npm install with scripts' "FROM alpine:3${pin}
RUN true && npm install --omit=dev"
	expect_fixture 1 'no base image' '# empty'
}

test_workflow_runs_this_suite() {
	grep -Fq 'scripts/dockerfile-lint_test.sh' "${PROJECT_ROOT}/.github/workflows/ci.yml" || fail 'workflow does not run the Dockerfile contract tests'
}

test_production_dockerfiles
if [[ -z "${DOCKERFILE_LINT_FIXTURE:-}" ]]; then
	test_self
	test_workflow_runs_this_suite
	printf 'dockerfile contract tests passed\n'
fi
