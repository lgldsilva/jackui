#!/usr/bin/env bash
# CA-3.2 — measures the pass-rate of go test + vitest over N consecutive runs (no retry).
# Usage: scripts/ci-stability-audit.sh [runs]   (default: 20)
# Requires: go, npm (cwd at the repo root). Postgres is NOT required (tests use mocks
# or skip when JACKUI_TEST_DATABASE_URL is absent — same as local CI without PG).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUNS="${1:-20}"
GO_PASSED=0
GO_FAILED=0
WEB_PASSED=0
WEB_FAILED=0

echo "=== CA-3.2 stability audit — $RUNS runs ==="
echo "Repo: $ROOT"
echo

cd "$ROOT"
for i in $(seq 1 "$RUNS"); do
  if go test -count=1 -timeout 20m ./internal/... >/dev/null 2>&1; then
    GO_PASSED=$((GO_PASSED + 1))
    printf "go   run %2d/%d OK\n" "$i" "$RUNS"
  else
    GO_FAILED=$((GO_FAILED + 1))
    printf "go   run %2d/%d FAIL\n" "$i" "$RUNS"
  fi
done

cd "$ROOT/web"
for i in $(seq 1 "$RUNS"); do
  if npm test --silent >/dev/null 2>&1; then
    WEB_PASSED=$((WEB_PASSED + 1))
    printf "web  run %2d/%d OK\n" "$i" "$RUNS"
  else
    WEB_FAILED=$((WEB_FAILED + 1))
    printf "web  run %2d/%d FAIL\n" "$i" "$RUNS"
  fi
done

GO_RATE=$(awk "BEGIN {printf \"%.1f\", ($GO_PASSED/$RUNS)*100}")
WEB_RATE=$(awk "BEGIN {printf \"%.1f\", ($WEB_PASSED/$RUNS)*100}")
TARGET=99.0

echo
echo "=== Result ==="
echo "Go:       $GO_PASSED/$RUNS passed (${GO_RATE}%)"
echo "Vitest:   $WEB_PASSED/$RUNS passed (${WEB_RATE}%)"
echo "CA-3.2 target: ≥${TARGET}%"

FAIL=0
awk -v r="$GO_RATE" -v t="$TARGET" 'BEGIN { exit (r+0 >= t+0) ? 0 : 1 }' || FAIL=1
awk -v r="$WEB_RATE" -v t="$TARGET" 'BEGIN { exit (r+0 >= t+0) ? 0 : 1 }' || FAIL=1
exit "$FAIL"
