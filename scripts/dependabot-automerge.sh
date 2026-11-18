#!/usr/bin/env bash
# Decides whether a Dependabot PR qualifies for auto-merge (issue #223).
#
# Inputs (env, from dependabot/fetch-metadata and the PR event):
#   ECOSYSTEM    package-ecosystem as in the branch name: npm_and_yarn,
#                go_modules, github_actions, docker
#   UPDATE_TYPE  highest change in the PR: version-update:semver-{patch,minor,major}
#   LABELS       comma-separated PR labels (optional)
#
# Exit 0 = enable auto-merge, 1 = leave the PR for a human, 2 = usage error.
# Anything unknown or missing fails closed (exit 1). Auto-merge still waits
# for the required checks of branch protection; this only picks candidates.
set -euo pipefail

readonly OPT_OUT_LABEL=do-not-auto-merge

skip() {
	printf 'skip: %s\n' "$*"
	exit 1
}

if [[ -z ${ECOSYSTEM:-} ]]; then
	echo 'usage: ECOSYSTEM=<eco> UPDATE_TYPE=<type> [LABELS=a,b] dependabot-automerge.sh' >&2
	exit 2
fi
update_type="${UPDATE_TYPE:-}"

case ",${LABELS:-}," in
*",${OPT_OUT_LABEL},"*) skip "PR carries the ${OPT_OUT_LABEL} label" ;;
esac

case "${update_type}" in
version-update:semver-patch) level='patch' ;;
version-update:semver-minor) level='minor' ;;
version-update:semver-major) skip 'major update: needs a human' ;;
'') skip 'no semver update type (e.g. digest-only bump)' ;;
*) skip "unknown update type '${update_type}'" ;;
esac

case "${ECOSYSTEM}:${level}" in
npm_and_yarn:patch | npm_and_yarn:minor | \
	go_modules:patch | go_modules:minor | \
	github_actions:patch | github_actions:minor | \
	docker:patch)
	printf 'merge: %s %s update qualifies for auto-merge\n' "${ECOSYSTEM}" "${level}"
	;;
docker:*) skip "docker ${level} base-image bump: patch only" ;;
*) skip "ecosystem '${ECOSYSTEM}' is not covered by the policy" ;;
esac
