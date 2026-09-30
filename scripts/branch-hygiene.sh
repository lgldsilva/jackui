#!/usr/bin/env bash
# R4 — branch hygiene: removes local branches already merged into main; lists pending orphans.
# Uso: scripts/branch-hygiene.sh [--delete-merged]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
git fetch origin --prune 2>/dev/null || true

KEEP_REGEX='^(main|refactor/r3-stream-api-decomp)$'
DELETE="${1:-}"

echo "=== Local branches merged into main (delete candidates) ==="
MERGED=$(git branch --merged main | sed 's/^[*+ ]*//' | grep -Ev "$KEEP_REGEX" || true)
if [ -z "$MERGED" ]; then
  echo "(none)"
else
  echo "$MERGED"
  if [ "$DELETE" = "--delete-merged" ]; then
    echo "$MERGED" | while read -r b; do
      [ -n "$b" ] && git branch -d "$b" && echo "deleted: $b"
    done
  fi
fi

echo
echo "=== Local branches NOT merged (review before deleting) ==="
git branch --no-merged main | sed 's/^[*+ ]*//' | grep -Ev "$KEEP_REGEX" || echo "(none)"

echo
echo "=== Remote branches merged into origin/main (candidates for git push origin --delete) ==="
git branch -r --merged origin/main 2>/dev/null \
  | sed 's|^[[:space:]]*origin/||' \
  | grep -Ev "$KEEP_REGEX|^HEAD$" \
  || echo "(none)"
