#!/usr/bin/env bash
# Generates electron/version.json — a build artifact (gitignored), do not commit.
#
# Precedence for the "version" field:
#   1. scripts/semver.sh (vX.Y.Z tag at HEAD or the next version computed from the
#      Conventional Commits) — same source as release.yml and the APP_VERSION of the
#      Docker builds, so the Electron app and the image report the same value;
#   2. the root package.json "version", when the repo has no semver tag at all
#      (shallow clone, checkout without tags) or semver.sh fails.
# The value flows to the About dialog (electron/main.ts reads version.json) and, via
# scripts/build-electron.sh, to the embedded Go server's /status — all three
# artifacts of the same build show the same version.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMMIT=$(git -C "$ROOT" describe --always --dirty 2>/dev/null || echo "unknown")
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

VERSION=""
SEMVER_TAGS=$(git -C "$ROOT" tag --list 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)
if [ -n "$SEMVER_TAGS" ]; then
  VERSION=$(bash "$ROOT/scripts/semver.sh" 2>/dev/null || true)
fi
if [ -z "$VERSION" ]; then
  VERSION=$(node -p "require('$ROOT/package.json').version" 2>/dev/null || echo "0.1.0")
fi

cat > "$ROOT/electron/version.json" <<EOF
{
  "version": "$VERSION",
  "commit": "$COMMIT",
  "date": "$DATE"
}
EOF
echo "→ electron/version.json: $(cat "$ROOT/electron/version.json")"
