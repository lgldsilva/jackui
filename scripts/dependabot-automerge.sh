#!/usr/bin/env bash
# Decides whether a Dependabot PR qualifies for auto-merge (issue #223).
#
# Inputs (env, from dependabot/fetch-metadata and the PR event):
#   ECOSYSTEM    package-ecosystem as in the branch name: npm_and_yarn,
#                go_modules, github_actions, docker
#   UPDATE_TYPE  highest change in the PR: version-update:semver-{patch,minor,major}
#   DEPENDENCY_NAMES  comma-separated names of every package in the PR
#                (fetch-metadata `dependency-names`; covers grouped PRs)
#   LABELS       comma-separated PR labels (optional)
#
# Exit 0 = enable auto-merge, 1 = leave the PR for a human, 2 = usage error.
# Anything unknown or missing fails closed (exit 1). Auto-merge still waits
# for the required checks of branch protection; this only picks candidates.
set -euo pipefail

readonly OPT_OUT_LABEL=do-not-auto-merge

# Critical dependencies: Dependabot still opens PRs for them, but they are
# NEVER auto-merged; a human reviews and merges. To add one, append a line:
#   'name'    exact package name (e.g. 'modernc.org/sqlite')
#   'prefix/' trailing slash = that package and everything under it
# A grouped PR is denied if ANY of its packages matches.
readonly DENYLIST=(
	'github.com/anacrolix/torrent' # streaming core, no swarm test in CI
	'github.com/anacrolix/'        # its sibling libraries (dht, missinggo, ...)
)

on_denylist() {
	local name="$1" entry
	for entry in "${DENYLIST[@]}"; do
		if [[ ${entry} == */ ]]; then
			[[ ${name} == "${entry}"* ]] && return 0
		else
			[[ ${name} == "${entry}" ]] && return 0
		fi
	done
	return 1
}

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

# Fail closed on missing names: ECOSYSTEM/UPDATE_TYPE say nothing about WHICH
# package changes, so a denylisted one cannot be ruled out. Every real
# Dependabot PR (single or grouped) carries names.
has_names=false
IFS=',' read -r -a names <<<"${DEPENDENCY_NAMES:-}"
for name in "${names[@]}"; do
	name="${name//[[:space:]]/}"
	[[ -n ${name} ]] || continue
	has_names=true
	on_denylist "${name}" && skip "${name} is on the critical-dependency denylist: needs a human"
done
[[ ${has_names} == true ]] || skip 'DEPENDENCY_NAMES is empty: cannot rule out a denylisted package'

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
