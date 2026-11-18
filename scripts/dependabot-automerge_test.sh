#!/usr/bin/env bash
# Tests for the Dependabot auto-merge policy: scripts/dependabot-automerge.sh
# (which update types qualify) and the wiring of
# .github/workflows/dependabot-automerge.yml (trigger, actor gate, permissions,
# pinned actions, no PR code checked out). Pure bash, no network.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly POLICY="${SCRIPT_DIR}/dependabot-automerge.sh"
readonly WORKFLOW="${SCRIPT_DIR}/../.github/workflows/dependabot-automerge.yml"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

out="$(mktemp)"
trap 'rm -f "${out}"' EXIT

# decide <ecosystem> <update-type> [labels] → exit status of the policy.
decide() {
	local ecosystem="$1" update_type="$2" labels="${3:-}"
	ECOSYSTEM="${ecosystem}" UPDATE_TYPE="${update_type}" LABELS="${labels}" \
		bash "${POLICY}" >"${out}" 2>&1
}
expect_merge() {
	local name="$1" ecosystem="$2" update_type="$3" labels="${4:-}"
	decide "${ecosystem}" "${update_type}" "${labels}" ||
		fail "${name}: expected auto-merge, got: $(cat "${out}")"
}
expect_skip() {
	local name="$1" ecosystem="$2" update_type="$3" labels="${4:-}" status=0
	decide "${ecosystem}" "${update_type}" "${labels}" || status=$?
	[[ ${status} -eq 1 ]] ||
		fail "${name}: expected skip (exit 1), got ${status}: $(cat "${out}")"
}

readonly PATCH=version-update:semver-patch
readonly MINOR=version-update:semver-minor
readonly MAJOR=version-update:semver-major

expect_merge 'npm patch' npm_and_yarn "${PATCH}"
expect_merge 'npm minor' npm_and_yarn "${MINOR}"
expect_merge 'go module patch' go_modules "${PATCH}"
expect_merge 'go module minor' go_modules "${MINOR}"
expect_merge 'github-actions patch' github_actions "${PATCH}"
expect_merge 'github-actions minor' github_actions "${MINOR}"
expect_merge 'docker base image patch' docker "${PATCH}"

expect_skip 'docker base image minor' docker "${MINOR}"
for eco in npm_and_yarn go_modules github_actions docker; do
	expect_skip "${eco} major" "${eco}" "${MAJOR}"
done
# A docker digest-only bump carries no semver update type: fail closed.
expect_skip 'missing update type' docker ''
expect_skip 'unknown update type' go_modules 'version-update:semver-prerelease'
expect_skip 'unknown ecosystem' pip "${PATCH}"
expect_skip 'opt-out label' go_modules "${PATCH}" 'dependencies,do-not-auto-merge'
grep -Fq 'do-not-auto-merge' "${out}" ||
	fail "opt-out skip must name the label: $(cat "${out}")"
expect_merge 'unrelated labels' npm_and_yarn "${MINOR}" 'dependencies,javascript'

status=0
ECOSYSTEM='' UPDATE_TYPE="${PATCH}" bash "${POLICY}" >"${out}" 2>&1 || status=$?
[[ ${status} -eq 2 ]] || fail "missing ECOSYSTEM must be a usage error (exit 2), got ${status}"

# ── Workflow wiring ─────────────────────────────────────────────────────────
[[ -f ${WORKFLOW} ]] || fail "missing ${WORKFLOW}"
has() {
	local mode="$1" pattern="$2"
	grep -q "${mode}" -- "${pattern}" "${WORKFLOW}"
}

has -E '^  pull_request_target:' ||
	fail 'workflow must run on pull_request_target (base-branch code only)'
has -E '^permissions: \{\}$' ||
	fail 'workflow-level permissions must be empty'
# shellcheck disable=SC2016 # Literal GitHub expressions, not shell expansions.
{
	has -F "github.event.pull_request.user.login == 'dependabot[bot]'" ||
		fail 'job must be gated on the PR author being dependabot[bot]'
	has -F 'github.event.pull_request.head.repo.full_name == github.repository' ||
		fail 'job must be gated on the PR head living in this repository'
	# github.actor is forgeable ("Dependabot confusion", Sonar S8232).
	if grep -Eq '^[^#]*github\.(triggering_)?actor' "${WORKFLOW}"; then
		fail 'workflow must not trust github.actor'
	fi
	has -F 'gh pr merge --auto --squash "${PR_URL}"' ||
		fail 'workflow must enable squash auto-merge via gh'
	has -F 'scripts/dependabot-automerge.sh' ||
		fail 'workflow must decide through scripts/dependabot-automerge.sh'
}
has -E '^      contents: write$' || fail 'job needs contents: write'
has -E '^      pull-requests: write$' || fail 'job needs pull-requests: write'
[[ $(grep -cE '^ +[a-z-]+: write$' "${WORKFLOW}") -eq 2 ]] ||
	fail 'job must not request write scopes beyond contents and pull-requests'
# Never check out (and so never execute) the PR head in a privileged job.
if grep -Eq 'head\.(sha|ref)|github\.head_ref|refs/pull/' "${WORKFLOW}"; then
	fail 'workflow must not reference the PR head ref'
fi
has -E 'uses: dependabot/fetch-metadata@[0-9a-f]{40} # v' ||
	fail 'dependabot/fetch-metadata must be pinned by full SHA'
if grep -E '^ +(- )?uses: ' "${WORKFLOW}" | grep -Evq '@[0-9a-f]{40} # v'; then
	fail 'every action must be pinned by full SHA'
fi

echo 'dependabot-automerge tests passed'
