#!/usr/bin/env bash
# Release gate for the images the release publishes. The build pushes every
# image BY DIGEST only (no tag), this script scans exactly those digests, and a
# tag moves only afterwards — so what is scanned is byte-for-byte what a tag
# will serve, for every platform, and a failed scan leaves every tag untouched.
#
# Usage:
#   release-images.sh scan    <repository@sha256:digest> <os/arch> <sbom-file>
#   release-images.sh promote <repository@sha256:digest> <tag>...
#
# scan    fails on any fixable CRITICAL vulnerability in that platform of the
#         digest, then writes its CycloneDX SBOM.
# promote points each <tag> of the same repository at the digest and verifies
#         the registry now serves that exact digest under every tag.
#
# Needs trivy (scan), docker buildx and jq (promote), and a logged-in registry.
set -euo pipefail

readonly TRIVY_TIMEOUT="${TRIVY_TIMEOUT:-20m}"

die() {
	printf '::error::%s\n' "$*" >&2
	exit 1
}

usage() {
	die 'usage: release-images.sh scan <repo@sha256:digest> <os/arch> <sbom-file>' \
		'| promote <repo@sha256:digest> <tag>...'
}

# A tag can be re-pointed between the scan and the promotion; a digest cannot.
# Everything here therefore refuses any reference that is not digest-pinned.
require_digest_ref() {
	[[ "$1" =~ ^[^@[:space:]]+@sha256:[0-9a-f]{64}$ ]] ||
		die "not a digest-pinned image reference: '$1'"
}

scan() {
	[[ $# -eq 3 ]] || usage
	local ref="$1" platform="$2" sbom="$3"
	require_digest_ref "${ref}"
	[[ "${platform}" =~ ^[a-z0-9]+/[a-z0-9]+(/[a-z0-9]+)?$ ]] ||
		die "invalid platform: '${platform}'"
	[[ -n "${sbom}" ]] || usage
	mkdir -p -- "$(dirname -- "${sbom}")"

	# --image-src remote: read the registry, never a same-named local image.
	local common=(--image-src remote --platform "${platform}"
		--timeout "${TRIVY_TIMEOUT}" --ignore-unfixed)

	echo "Trivy gate: ${ref} (${platform})"
	trivy image "${common[@]}" --severity CRITICAL --exit-code 1 "${ref}" ||
		die "Trivy gate failed for ${ref} (${platform}); no tag was moved"

	trivy image "${common[@]}" --format cyclonedx --output "${sbom}" "${ref}" ||
		die "SBOM generation failed for ${ref} (${platform})"
	[[ -s "${sbom}" ]] || die "SBOM ${sbom} is empty"
}

# tag_digest <repository:tag> → the digest the registry serves for that tag.
tag_digest() {
	docker buildx imagetools inspect --format '{{json .Manifest}}' "$1" |
		jq -r '.digest // empty'
}

promote() {
	[[ $# -ge 2 ]] || usage
	local ref="$1"
	shift
	require_digest_ref "${ref}"
	local repo="${ref%@*}" digest="${ref#*@}" tag args=() served

	for tag in "$@"; do
		[[ "${tag}" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]] ||
			die "invalid tag: '${tag}'"
		args+=(--tag "${repo}:${tag}")
	done

	docker buildx imagetools create "${args[@]}" "${ref}" ||
		die "could not promote ${ref} to: $*"

	# A single-source create re-tags the manifest as is. Fail loudly if the
	# registry ends up serving anything other than the digest that was scanned.
	for tag in "$@"; do
		served="$(tag_digest "${repo}:${tag}")" ||
			die "cannot read back ${repo}:${tag} after promotion"
		[[ "${served}" == "${digest}" ]] ||
			die "${repo}:${tag} serves '${served}', not the scanned ${digest}"
		echo "${repo}:${tag} -> ${digest}"
	done
}

[[ $# -ge 1 ]] || usage
cmd="$1"
shift
case "${cmd}" in
scan) scan "$@" ;;
promote) promote "$@" ;;
*) usage ;;
esac
