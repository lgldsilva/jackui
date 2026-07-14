#!/usr/bin/env bash
# R4 — higiene de branches: remove locais já mergeadas em main; lista órfãs pendentes.
# Uso: scripts/branch-hygiene.sh [--delete-merged]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
git fetch origin --prune 2>/dev/null || true

KEEP_REGEX='^(main|refactor/r3-stream-api-decomp)$'
DELETE="${1:-}"

echo "=== Branches locais mergeadas em main (candidatas a delete) ==="
MERGED=$(git branch --merged main | sed 's/^[*+ ]*//' | grep -Ev "$KEEP_REGEX" || true)
if [ -z "$MERGED" ]; then
  echo "(nenhuma)"
else
  echo "$MERGED"
  if [ "$DELETE" = "--delete-merged" ]; then
    echo "$MERGED" | while read -r b; do
      [ -n "$b" ] && git branch -d "$b" && echo "deleted: $b"
    done
  fi
fi

echo
echo "=== Branches locais NÃO mergeadas (revisar antes de apagar) ==="
git branch --no-merged main | sed 's/^[*+ ]*//' | grep -Ev "$KEEP_REGEX" || echo "(nenhuma)"

echo
echo "=== Remotas órfãs sugeridas (R4 REQUIREMENTS — confirmar antes de push --delete) ==="
for b in feat/i18n feat/hls-vod-seekbar feat/web-push feat/web-push-v2; do
  git show-ref --verify --quiet "refs/remotes/origin/$b" 2>/dev/null && echo "  origin/$b"
done
