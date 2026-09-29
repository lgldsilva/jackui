#!/usr/bin/env bash
# Publishes a Gitea Release (which CREATES the tag at $SHA) with an automatic changelog.
#
# - Idempotent: if a Release/tag for $SEMVER already exists, it does nothing (a rebuild of
#   the same commit OR a non-releasable push where semver.sh returned the last tag).
# - Robust: a failure here does NOT take down the deploy (logs a warning) — the artifact
#   was already built+scanned+pushed; the Release can be redone on a re-run.
# - Verified TLS (aligned with the trust-a-CA from #463): uses the CA baked into the
#   runner when it exists; otherwise trusts the OS trust store (which already resolves Gitea).
#
# Required env: GITEA_API, REPO, TOKEN, SEMVER, SHA
# Optional env: REPO_URL (compare link in the changelog)
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

: "${SEMVER:=}"
if [ -z "$SEMVER" ]; then
  echo "publish-release: empty SEMVER — nothing to publish."
  exit 0
fi
: "${GITEA_API:?GITEA_API is required}"
: "${REPO:?REPO is required}"
: "${TOKEN:?TOKEN is required}"
: "${SHA:?SHA is required}"

CA=/usr/local/share/ca-certificates/gitea-ca.crt
CURL_CA=()
[ -f "$CA" ] && CURL_CA=(--cacert "$CA")
api() { curl -s --max-time 30 "${CURL_CA[@]}" -H "Authorization: token $TOKEN" "$@"; }

# Does the TAG already exist? → do not re-publish. Covers both no-ops at once, server-side
# (immune to stale checkouts): a rebuild of the same commit AND a non-releasable push
# (semver.sh returned the last existing tag). Only a NEW version (no tag) proceeds to creation.
code=$(api -o /dev/null -w '%{http_code}' "$GITEA_API/repos/$REPO/git/refs/tags/$SEMVER" || echo 000)
if [ "$code" = "200" ]; then
  echo "publish-release: tag $SEMVER already exists — no new version."
  exit 0
fi

notes_file=$(mktemp)
REPO_URL="${REPO_URL:-}" bash scripts/changelog.sh "$SEMVER" > "$notes_file" || true

payload_file=$(mktemp)
SEMVER="$SEMVER" SHA="$SHA" NOTES_FILE="$notes_file" python3 - "$payload_file" <<'PY'
import json, os, sys
body = open(os.environ["NOTES_FILE"], encoding="utf-8").read().strip() or "No changelog."
data = {
    "tag_name": os.environ["SEMVER"],
    "target_commitish": os.environ["SHA"],
    "name": os.environ["SEMVER"],
    "body": body,
    "draft": False,
    "prerelease": False,
}
open(sys.argv[1], "w", encoding="utf-8").write(json.dumps(data))
PY

code=$(api -o /tmp/release-resp.json -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' \
  "$GITEA_API/repos/$REPO/releases" -d @"$payload_file" || echo 000)
rm -f "$notes_file" "$payload_file"
if [ "$code" = "201" ] || [ "$code" = "200" ]; then
  echo "publish-release: Release $SEMVER created (tag at ${SHA:0:7})."
else
  echo "Warning: failed to create Release $SEMVER (HTTP $code):"
  cat /tmp/release-resp.json 2>/dev/null || true
fi
