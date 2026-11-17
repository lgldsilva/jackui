#!/usr/bin/env bash
# Keeps published version tags immutable. scripts/semver.sh returns the LAST tag
# when nothing is releasable, so a release run from a newer commit would rebuild
# and overwrite an image that is already deployed. This allows the version only
# when it is not in the registry yet, or when HEAD is exactly the commit the git
# tag points at (idempotent rebuild of the same source).
#
# Usage: scripts/release-guard.sh <image-repository> <version>
# Runs in the current git checkout; needs its tags and a logged-in docker CLI.
set -euo pipefail

die() {
	printf '::error::%s\n' "$*" >&2
	exit 1
}

# image_state <image:tag> → "present" | "absent". Any other registry answer
# (auth, network) fails closed: guessing "absent" would allow the overwrite.
image_state() {
	local out
	if out="$(docker manifest inspect "$1" 2>&1)"; then
		echo present
	elif grep -qiE 'manifest unknown|no such manifest' <<<"${out}"; then
		echo absent
	else
		die "cannot tell whether $1 is published: ${out}"
	fi
}

[[ $# -eq 2 ]] || die 'usage: release-guard.sh <image-repository> <version>'
image="$1:$2"
version="$2"

state="$(image_state "${image}")"
if [[ "${state}" == absent ]]; then
	echo "${image} is not published yet"
	exit 0
fi

head="$(git rev-parse HEAD)"
tag_commit="$(git rev-parse -q --verify "refs/tags/${version}^{commit}" || true)"
if [[ "${head}" == "${tag_commit}" ]]; then
	echo "${image} is published; HEAD is the ${version} commit, rebuilding it"
	exit 0
fi

# A run that pushed the image and then failed before tagging leaves this state.
[[ -n "${tag_commit}" ]] ||
	die "${version} is already published but has no git tag (an earlier run" \
		"pushed the image and failed before tagging). Tag the commit that was" \
		"built as ${version} and push the tag, then re-run."

die "${version} is already published and HEAD (${head}) is not its commit" \
	"(${tag_commit}). Nothing releasable since ${version}:" \
	"land a feat/fix commit or push a new v* tag."
