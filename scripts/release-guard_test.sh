#!/usr/bin/env bash
# Tests for the release gates: scripts/release-guard.sh (published version tags
# are immutable) and the wiring of .github/workflows/release.yml (CI must pass
# and the ref must be main or a v* tag before anything is pushed). The registry
# is a stub `docker` on PATH, so nothing here touches the network.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly GUARD="${SCRIPT_DIR}/release-guard.sh"
readonly WORKFLOW="${SCRIPT_DIR}/../.github/workflows/release.yml"
readonly IMAGE=registry.test/owner/app

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

# Stub registry: STUB_DOCKER selects the `docker manifest inspect` outcome.
mkdir -p "${work}/bin"
cat >"${work}/bin/docker" <<'STUB'
#!/usr/bin/env bash
case "${STUB_DOCKER:?}" in
present) echo '{"schemaVersion":2}' ;;
absent) echo 'manifest unknown' >&2; exit 1 ;;
*) echo 'unauthorized: authentication required' >&2; exit 1 ;;
esac
STUB
chmod +x "${work}/bin/docker"

# Fixture repo: v1.0.0 on the first commit, one untagged commit on top.
repo="${work}/repo"
git init -q "${repo}"
# Hosted runners have no global git identity; commit-tree needs one.
git -C "${repo}" config user.email ci@example.test
git -C "${repo}" config user.name 'CI test'
commit() {
	local tree parent=()
	tree="$(git -C "${repo}" write-tree)"
	if git -C "${repo}" rev-parse -q --verify HEAD >/dev/null; then
		parent=(-p HEAD)
	fi
	# commit-tree builds fixture objects without invoking repository hooks.
	git -C "${repo}" update-ref HEAD \
		"$(printf '%s\n' "$1" | git -C "${repo}" commit-tree "${tree}" "${parent[@]}")"
}
commit 'feat: first'
git -C "${repo}" tag v1.0.0
tagged="$(git -C "${repo}" rev-parse HEAD)"
commit 'chore: second'
untagged="$(git -C "${repo}" rev-parse HEAD)"

# run_guard <registry-state> <commit> <version> → exit status of the guard.
run_guard() {
	git -C "${repo}" update-ref HEAD "$2"
	(cd "${repo}" && STUB_DOCKER="$1" PATH="${work}/bin:${PATH}" \
		bash "${GUARD}" "${IMAGE}" "$3") >"${work}/out" 2>&1
}
expect_pass() {
	run_guard "$2" "$3" "$4" || fail "$1: expected success, got: $(cat "${work}/out")"
}
expect_block() {
	if run_guard "$2" "$3" "$4"; then
		fail "$1: expected the guard to block the release"
	fi
}

expect_pass 'new version is not published yet' absent "${untagged}" v1.0.1
expect_pass 'rebuild of the tagged commit' present "${tagged}" v1.0.0
expect_block 'published version from another commit' present "${untagged}" v1.0.0
grep -Fq 'v1.0.0 is already published' "${work}/out" ||
	fail "block message must name the published version: $(cat "${work}/out")"
expect_block 'published image without a git tag' present "${untagged}" v9.9.9
grep -Fq 'has no git tag' "${work}/out" ||
	fail "block message must explain the missing git tag: $(cat "${work}/out")"
expect_block 'registry error fails closed' error "${untagged}" v1.0.1

# job_block <job> → the YAML lines of one job of release.yml.
job_block() {
	awk -v job="  $1:" '
		$0 == job { inside = 1; next }
		inside && /^  [A-Za-z_-]+:/ { exit }
		inside { print }
	' "${WORKFLOW}"
}
# job_has <job> <grep-mode> <pattern>. The block is captured first: piping awk
# into `grep -q` lets grep exit early and fail the pipeline with SIGPIPE.
job_has() {
	local block
	block="$(job_block "$1")"
	grep -q "$2" -- "$3" <<<"${block}"
}
# shellcheck disable=SC2016 # Literal GitHub expression, not a shell expansion.
readonly REF_GUARD='github.ref == '\''refs/heads/main'\'' || startsWith(github.ref, '\''refs/tags/v'\'')'

job_has ci -F 'uses: ./.github/workflows/ci.yml' ||
	fail 'release.yml: no ci job calling ci.yml'
job_has build-and-push -E '^    needs: \[(semver, ci|ci, semver)\]$' ||
	fail 'release.yml: build-and-push must need semver and ci'
for job in semver ci; do
	job_has "${job}" -F "if: ${REF_GUARD}" ||
		fail "release.yml: ${job} is not restricted to main or v* tags"
done
job_has semver -F 'scripts/release-guard.sh' ||
	fail 'release.yml: semver job does not run scripts/release-guard.sh'

echo 'release-guard tests passed'
