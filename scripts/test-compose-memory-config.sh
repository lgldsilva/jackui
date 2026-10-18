#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
compose=(
  docker compose
  --project-directory "$repo_root"
  --env-file /dev/null
  -f "$repo_root/docker-compose.yml"
)

resolve_memory_limit() {
  local canonical_state="$1" canonical_value="$2"
  local legacy_state="$3" legacy_value="$4"
  local -a unset_args=() assignments=()

  if [[ "$canonical_state" == unset ]]; then
    unset_args+=(-u JACKUI_GOMEMLIMIT)
  else
    assignments+=("JACKUI_GOMEMLIMIT=$canonical_value")
  fi
  if [[ "$legacy_state" == unset ]]; then
    unset_args+=(-u GOMEMLIMIT)
  else
    assignments+=("GOMEMLIMIT=$legacy_value")
  fi

  env "${unset_args[@]}" "${assignments[@]}" \
    POSTGRES_PASSWORD=test-placeholder \
    JACKUI_ADMIN_PASSWORD=test-placeholder \
    JACKUI_JWT_SECRET=test-placeholder \
    JACKETT_API_KEY=test-placeholder \
    "${compose[@]}" config --format json 2>/dev/null |
    python3 -c '
import json, sys
doc = json.load(sys.stdin)
print(doc["services"]["jackui"]["environment"]["JACKUI_GOMEMLIMIT"])
'
}

assert_value() {
  local label="$1" canonical_state="$2" canonical_value="$3"
  local legacy_state="$4" legacy_value="$5" expected="$6" actual
  actual="$(
    resolve_memory_limit \
      "$canonical_state" "$canonical_value" \
      "$legacy_state" "$legacy_value"
  )"
  if [[ "$actual" != "$expected" ]]; then
    printf 'FAIL: %s expected %s, got %s\n' \
      "$label" "$expected" "$actual" >&2
    exit 1
  fi
}

# Parse the compose file and verify legacy fallback, canonical precedence,
# unset/empty handling, and the default without starting any containers.
assert_value 'canonical value wins' set 1200MiB set 900MiB 1200MiB
assert_value 'empty canonical falls back' set '' set 900MiB 900MiB
assert_value 'unset canonical falls back' unset '' set 900MiB 900MiB
assert_value 'empty values use default' set '' set '' 1600MiB
assert_value 'unset values use default' unset '' unset '' 1600MiB

hard_limit="$(
  env -u JACKUI_GOMEMLIMIT -u GOMEMLIMIT \
    POSTGRES_PASSWORD=test-placeholder \
    JACKUI_ADMIN_PASSWORD=test-placeholder \
    JACKUI_JWT_SECRET=test-placeholder \
    JACKETT_API_KEY=test-placeholder \
    "${compose[@]}" config --format json 2>/dev/null |
    python3 -c '
import json, sys
doc = json.load(sys.stdin)
print(doc["services"]["jackui"]["deploy"]["resources"]["limits"]["memory"])
'
)"
[[ "$hard_limit" == 2147483648 ]] || {
  printf 'FAIL: expected a 2GiB cgroup limit, got %s\n' "$hard_limit" >&2
  exit 1
}

printf 'PASS: Compose memory cap and GC limits validated\n'
