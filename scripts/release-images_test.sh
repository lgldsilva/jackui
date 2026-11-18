#!/usr/bin/env bash
# Tests for the release image gate: scripts/release-images.sh (scan a pushed
# digest per platform, promote it to tags only afterwards) and the wiring of
# the build-and-push job in .github/workflows/release.yml. `trivy` and `docker`
# are stubs on PATH that log their arguments, so nothing here touches the
# network, a registry or a real scanner.
# shellcheck disable=SC2016 # The workflow patterns are literal text.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly GATE="${SCRIPT_DIR}/release-images.sh"
readonly WORKFLOW="${SCRIPT_DIR}/../.github/workflows/release.yml"
readonly REPO=registry.test/owner/app
DIGEST="sha256:$(printf 'a%.0s' {1..64})"
OTHER="sha256:$(printf 'b%.0s' {1..64})"
readonly DIGEST OTHER
readonly REF="${REPO}@${DIGEST}"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
mkdir -p "${work}/bin"
export CALLS="${work}/calls"

# Stub trivy: STUB_TRIVY_GATE is the exit status of the --exit-code run;
# STUB_TRIVY_SBOM=fail breaks the SBOM run; otherwise --output gets a document.
cat >"${work}/bin/trivy" <<'STUB'
#!/usr/bin/env bash
echo "trivy $*" >>"${CALLS:?}"
out=''
gate=0
while [[ $# -gt 0 ]]; do
	case "$1" in
	--output) out="$2"; shift ;;
	--exit-code) gate=1 ;;
	esac
	shift
done
if [[ ${gate} -eq 1 ]]; then
	exit "${STUB_TRIVY_GATE:-0}"
fi
[[ "${STUB_TRIVY_SBOM:-ok}" == ok ]] || exit 1
[[ -z "${out}" ]] || echo '{"bomFormat":"CycloneDX"}' >"${out}"
STUB

# Stub docker: `imagetools create` succeeds unless STUB_CREATE=fail;
# `imagetools inspect` reports STUB_SERVED as the digest behind any tag.
cat >"${work}/bin/docker" <<'STUB'
#!/usr/bin/env bash
echo "docker $*" >>"${CALLS:?}"
case "$3" in
create) [[ "${STUB_CREATE:-ok}" == ok ]] || { echo 'denied' >&2; exit 1; } ;;
inspect) printf '{"mediaType":"x","digest":"%s","manifests":[{"digest":"sha256:child"}]}\n' "${STUB_SERVED:?}" ;;
*) exit 64 ;;
esac
STUB
chmod +x "${work}/bin/trivy" "${work}/bin/docker"

# gate <args...> → exit status of release-images.sh with the stubs on PATH.
gate() {
	: >"${CALLS}"
	PATH="${work}/bin:${PATH}" bash "${GATE}" "$@" >"${work}/out" 2>&1
}
expect_pass() {
	local what="$1"
	shift
	gate "$@" || fail "${what}: expected success, got: $(cat "${work}/out")"
}
expect_fail() {
	local what="$1"
	shift
	if gate "$@"; then
		fail "${what}: expected failure"
	fi
}
called() { grep -Fq -- "$1" "${CALLS}"; }

# --- scan -------------------------------------------------------------------
sbom="${work}/sbom/bom.json"
expect_pass 'clean scan' scan "${REF}" linux/arm64 "${sbom}"
[[ -s "${sbom}" ]] || fail 'clean scan: SBOM was not written'
called "--severity CRITICAL --exit-code 1 ${REF}" ||
	fail "scan must gate on CRITICAL by digest: $(cat "${CALLS}")"
[[ "$(grep -c -- '--image-src remote --platform linux/arm64' "${CALLS}")" -eq 2 ]] ||
	fail "gate and SBOM must both read the registry for the asked platform: $(cat "${CALLS}")"
[[ "$(grep -c -- '--ignore-unfixed' "${CALLS}")" -eq 2 ]] ||
	fail 'gate and SBOM must both pass --ignore-unfixed'
called "--format cyclonedx --output ${sbom} ${REF}" ||
	fail 'SBOM must be generated from the same digest'

rm -f "${sbom}"
STUB_TRIVY_GATE=1 expect_fail 'CRITICAL finding' scan "${REF}" linux/amd64 "${sbom}"
grep -Fq 'Trivy gate failed' "${work}/out" || fail 'gate failure must be named'
[[ ! -e "${sbom}" ]] || fail 'a failed gate must not leave an SBOM behind'
STUB_TRIVY_SBOM=fail expect_fail 'SBOM failure' scan "${REF}" linux/amd64 "${sbom}"

expect_fail 'scan of a tag' scan "${REPO}:latest" linux/amd64 "${sbom}"
[[ ! -s "${CALLS}" ]] || fail 'a tag reference must be refused before trivy runs'
expect_fail 'scan of a short digest' scan "${REPO}@sha256:abc" linux/amd64 "${sbom}"
expect_fail 'scan with a bad platform' scan "${REF}" 'linux amd64' "${sbom}"
expect_fail 'scan without an SBOM path' scan "${REF}" linux/amd64

# --- promote ----------------------------------------------------------------
STUB_SERVED="${DIGEST}" expect_pass 'promotion' promote "${REF}" v1.2.3 latest
called "docker buildx imagetools create --tag ${REPO}:v1.2.3 --tag ${REPO}:latest ${REF}" ||
	fail "promotion must re-tag the scanned digest: $(cat "${CALLS}")"
for tag in v1.2.3 latest; do
	called "imagetools inspect --format {{json .Manifest}} ${REPO}:${tag}" ||
		fail "promotion must read ${tag} back: $(cat "${CALLS}")"
done

STUB_SERVED="${OTHER}" expect_fail 'tag serving another digest' promote "${REF}" latest
grep -Fq "not the scanned ${DIGEST}" "${work}/out" ||
	fail "digest mismatch must be explained: $(cat "${work}/out")"
STUB_CREATE=fail STUB_SERVED="${DIGEST}" expect_fail 'registry refuses the tag' promote "${REF}" latest

STUB_SERVED="${DIGEST}" expect_fail 'promotion of a tag' promote "${REPO}:staging" latest
[[ ! -s "${CALLS}" ]] || fail 'a tag source must be refused before docker runs'
STUB_SERVED="${DIGEST}" expect_fail 'promotion to a bad tag' promote "${REF}" 'bad tag'
STUB_SERVED="${DIGEST}" expect_fail 'promotion to a tag with a repository' promote "${REF}" other/app:latest
STUB_SERVED="${DIGEST}" expect_fail 'promotion without tags' promote "${REF}"
expect_fail 'unknown command' publish "${REF}"
expect_fail 'no command'

# --- release.yml wiring -----------------------------------------------------
# The build-and-push job, with line numbers, so ordering can be asserted.
job="$(awk '
	$0 == "  build-and-push:" { inside = 1; next }
	inside && /^  [A-Za-z_-]+:/ { exit }
	inside { printf "%d:%s\n", NR, $0 }
' "${WORKFLOW}")"
[[ -n "${job}" ]] || fail 'release.yml: no build-and-push job'

# lines <grep-mode> <pattern> → line numbers of the job lines that match.
lines() { grep "$1" -- "$2" <<<"${job}" | cut -d: -f1 || true; }
count() { lines "$1" "$2" | grep -c . || true; }

# Nothing may reach a tag straight from a build.
[[ "$(count -E 'push: *true')" -eq 0 ]] ||
	fail 'release.yml: a build step pushes directly (push: true)'
[[ "$(count -E '^[0-9]+: +tags:')" -eq 0 ]] ||
	fail 'release.yml: a build step sets tags; tags move only through promote'
[[ "$(count -F 'load: true')" -eq 0 ]] ||
	fail 'release.yml: a locally loaded image is not the published artifact'
builds="$(count -F 'docker/build-push-action@')"
[[ "${builds}" -eq 2 ]] || fail "release.yml: expected 2 image builds, found ${builds}"
[[ "$(count -F 'push-by-digest=true,name-canonical=true,push=true')" -eq "${builds}" ]] ||
	fail 'release.yml: every build must push by digest only'
[[ "$(count -E 'trivy image|imagetools')" -eq 0 ]] ||
	fail 'release.yml: scan and promotion must go through scripts/release-images.sh'

# Every published image and platform is scanned.
scan_default_amd64="$(lines -E 'release-images\.sh scan "\$\{IMAGE\}@\$\{DIGEST\}" linux/amd64 sbom/bom-linux-amd64\.json')"
scan_default_arm64="$(lines -E 'release-images\.sh scan "\$\{IMAGE\}@\$\{DIGEST\}" linux/arm64 sbom/bom-linux-arm64\.json')"
scan_nvidia="$(lines -E 'release-images\.sh scan "\$\{IMAGE\}@\$\{DIGEST\}" linux/amd64 sbom/bom-nvidia-linux-amd64\.json')"
[[ -n "${scan_default_amd64}" ]] || fail 'release.yml: default image amd64 is not scanned'
[[ -n "${scan_default_arm64}" ]] || fail 'release.yml: default image arm64 is not scanned'
[[ -n "${scan_nvidia}" ]] || fail 'release.yml: nvidia image is not scanned'
[[ "$(count -F 'DIGEST: ${{ steps.build_default.outputs.digest }}')" -eq 3 ]] ||
	fail 'release.yml: default scans and promotion must use the build_default digest'
[[ "$(count -F 'DIGEST: ${{ steps.build_nvidia.outputs.digest }}')" -eq 2 ]] ||
	fail 'release.yml: nvidia scan and promotion must use the build_nvidia digest'

# Tags move only after the last scan.
promote_default="$(lines -F 'release-images.sh promote "${IMAGE}@${DEFAULT_DIGEST}" "${VERSION}" latest')"
promote_nvidia="$(lines -F 'release-images.sh promote "${IMAGE}@${NVIDIA_DIGEST}" nvidia')"
[[ -n "${promote_default}" ]] || fail 'release.yml: default image is not promoted to <version> and latest'
[[ -n "${promote_nvidia}" ]] || fail 'release.yml: nvidia image is not promoted to :nvidia'
[[ "$(count -F 'release-images.sh promote')" -eq 2 ]] ||
	fail 'release.yml: unexpected extra promotion'
for scan_line in "${scan_default_amd64}" "${scan_default_arm64}" "${scan_nvidia}"; do
	[[ "${scan_line}" -lt "${promote_default}" && "${scan_line}" -lt "${promote_nvidia}" ]] ||
		fail 'release.yml: a tag is promoted before every scan has run'
done
[[ "$(count -E 'if:|continue-on-error')" -eq 0 ]] ||
	fail 'release.yml: build-and-push steps must not be conditional or allowed to fail'

# The GitHub Release waits for the gate and ships one SBOM per scanned image.
release="$(awk '
	$0 == "  release:" { inside = 1; next }
	inside && /^  [A-Za-z_-]+:/ { exit }
	inside { print }
' "${WORKFLOW}")"
grep -Eq '^    needs: \[.*build-and-push.*\]$' <<<"${release}" ||
	fail 'release.yml: release must need build-and-push'
for bom in bom-linux-amd64.json bom-linux-arm64.json bom-nvidia-linux-amd64.json; do
	grep -Eq "^ +${bom//./\\.}\$" <<<"${release}" ||
		fail "release.yml: ${bom} is not attached to the GitHub Release"
done

echo 'release-images tests passed'
