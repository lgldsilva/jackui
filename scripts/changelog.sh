#!/usr/bin/env bash
# Generates a markdown changelog from the Conventional Commits since the last
# semver tag up to HEAD, grouped by type. Feeds the GitHub Release body.
#
# Usage:  scripts/changelog.sh [<new-version>]   → prints markdown to stdout.
# Env (optional):
#   REPO_URL   repo base URL (e.g. https://github.com/lgldsilva/jackui)
#              → adds a "Full changelog: <last>...<new-version>" footer.
#
# The new-version does NOT exist as a tag yet when this runs (the Release creates
# it), so the range goes from the last EXISTING tag to HEAD.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

newver="${1:-}"
glob='v[0-9]*.[0-9]*.[0-9]*'

last=$(git tag --list "$glob" --sort=-v:refname 2>/dev/null | head -1 || true)
if [ -n "$last" ]; then
  range="$last..HEAD"
else
  range="HEAD"
fi

# Subjects of the real commits (skipping the "Merge pull request ..." merges, which are noise).
subjects=$(git log "$range" --no-merges --format='%s|%h' 2>/dev/null || true)

# section <title> <type-alternation> → prints "### title" + items, if any.
section() {
  local title="$1" types="$2" lines
  lines=$(printf '%s\n' "$subjects" \
    | grep -iE "^($types)(\([^)]*\))?!?:" \
    | sed -E "s/^([a-zA-Z]+(\([^)]*\))?!?): *(.*)\|([0-9a-f]+)$/- \3 (\4)/" || true)
  if [ -n "$lines" ]; then
    printf '### %s\n%s\n\n' "$title" "$lines"
  fi
}

# Highlight BREAKING CHANGES — only the real "BREAKING CHANGE:" FOOTER (start of
# line + ":", uppercase), not the phrase quoted in prose (otherwise a commit that
# merely mentions "BREAKING CHANGE" in an explanation becomes a fake changelog section).
breaking=$(git log "$range" --no-merges --format='%B' 2>/dev/null \
  | grep -E '^BREAKING[ -]CHANGE:' | sed -E 's/^BREAKING[ -]CHANGE: */- /' || true)
if [ -n "$breaking" ]; then
  printf '### ⚠️ BREAKING CHANGES\n%s\n\n' "$breaking"
fi

section '✨ Features'      'feat'
section '🔒 Security'      'security'
section '🐛 Fixes'         'fix'
section '⚡ Performance'    'perf'
section '♻️ Refactor'      'refactor'
section '🔧 Chore / CI / Docs' 'chore|ci|docs|build|test|style'

# "Other": commits without a recognized conventional type (they fall in no section).
# The first grep requires the "|<hash>" separator, discarding the empty line that the
# printf emits when there is no commit in the range.
others=$(printf '%s\n' "$subjects" \
  | grep -E '\|[0-9a-f]+$' \
  | grep -viE "^(feat|fix|perf|security|refactor|chore|ci|docs|build|test|style)(\([^)]*\))?!?:" \
  | sed -E 's/^(.*)\|([0-9a-f]+)$/- \1 (\2)/' || true)
if [ -n "$others" ]; then
  printf '### 📦 Other\n%s\n\n' "$others"
fi

# Footer with the compare link, when REPO_URL is known.
if [ -n "${REPO_URL:-}" ] && [ -n "$last" ] && [ -n "$newver" ]; then
  printf '**Full changelog:** [%s...%s](%s/compare/%s...%s)\n' \
    "$last" "$newver" "${REPO_URL%/}" "$last" "$newver"
fi
