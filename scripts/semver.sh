#!/usr/bin/env bash
# Computes the NEXT semver version from the Conventional Commits since the last
# vX.Y.Z tag — and only bumps when there is a "releasable" change:
#
#   feat:                  → MINOR bump
#   fix: / perf: / security: → PATCH bump
#   <type>!: / BREAKING    → MINOR bump while major==0 (0.x), MAJOR from 1.0 on
#   only chore/ci/docs/test/build/style/refactor (or nothing conventional) → NO bump
#
# When there is nothing releasable (or HEAD is exactly on a tag), prints the
# unchanged LAST tag. The caller (release.yml) treats "computed == existing tag"
# as "do not create a new tag/Release" — build+deploy still run, without inflating
# the version on every trivial merge (it used to be 1 tag per merge → 173 tags).
#
# Robust to merge commits (GitHub/Gitea): the merge subject carries the PR title
# ("Merge pull request 'fix(x): ...'" / "Merge pull request #N …"), so the type is
# detected both at the start of the subject and right after the merge prefix.
#
# Usage:  scripts/semver.sh          → prints "vX.Y.Z" to stdout (nothing else).
# Does not create or push a tag — the caller decides that.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

semver_tag_glob='v[0-9]*.[0-9]*.[0-9]*'

# HEAD already tagged? Reuse the largest semver tag pointing at it (idempotent rebuild).
head_tag=$(git tag --points-at HEAD --list "$semver_tag_glob" 2>/dev/null | sort -V | tail -1 || true)
if [ -n "$head_tag" ]; then
  echo "$head_tag"
  exit 0
fi

# Last semver tag and the commit range since it.
last=$(git tag --list "$semver_tag_glob" --sort=-v:refname 2>/dev/null | head -1 || true)
if [ -n "$last" ]; then
  range="$last..HEAD"
else
  last="v0.0.0"
  range="HEAD"
fi

# ALL subjects (includes merges: the PR title lives in the merge subject) + the
# bodies (for the "BREAKING CHANGE" footer, which real commits carry).
subjects=$(git log "$range" --format='%s' 2>/dev/null || true)
bodies=$(git log "$range" --format='%B' 2>/dev/null || true)

# match_type <type-alternation> → succeeds if any commit is of that/those type(s),
# accepting the type at the start of the subject OR inside a Gitea merge title.
match_type() {
  printf '%s\n' "$subjects" | grep -qiE \
    "^($1)(\([^)]*\))?!?:|^Merge pull request '($1)(\([^)]*\))?!?:"
}

# breaking: "<type>!:" in the subject (any form) OR "BREAKING CHANGE" as a body
# FOOTER. Anchored at start of line + ":" and case-sensitive (the spec requires the
# footer in uppercase) so it does NOT match the phrase quoted in prose — a commit that
# only MENTIONS "BREAKING CHANGE" in the middle of an explanation is not a breaking change.
is_breaking() {
  printf '%s\n' "$subjects" | grep -qE \
    "^[a-zA-Z]+(\([^)]*\))?!:|^Merge pull request '[a-zA-Z]+(\([^)]*\))?!:" \
    || printf '%s\n' "$bodies" | grep -qE '^BREAKING[ -]CHANGE:'
}

bump=none
if match_type 'fix|perf|security'; then bump=patch; fi
if match_type 'feat';     then bump=minor; fi
if is_breaking;           then bump=break; fi

if [ "$bump" = none ]; then
  # Nothing releasable → no bump; return the last tag (the caller creates no Release).
  echo "$last"
  exit 0
fi

v=${last#v}
major=${v%%.*}; rest=${v#*.}; minor=${rest%%.*}; patch=${rest#*.}
case "$bump" in
  break)
    if [ "$major" -eq 0 ]; then
      # 0.x convention: breaking bumps MINOR (does not jump to 1.0.0 on its own).
      minor=$((minor + 1)); patch=0
    else
      major=$((major + 1)); minor=0; patch=0
    fi
    ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac
echo "v${major}.${minor}.${patch}"
