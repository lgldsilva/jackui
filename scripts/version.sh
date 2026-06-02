#!/usr/bin/env bash
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMMIT=$(git -C "$ROOT" describe --always --dirty 2>/dev/null || echo "unknown")
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION=$(node -p "require('$ROOT/package.json').version" 2>/dev/null || echo "0.1.0")
cat > "$ROOT/electron/version.json" <<EOF
{
  "version": "$VERSION",
  "commit": "$COMMIT",
  "date": "$DATE"
}
EOF
echo "→ electron/version.json: $(cat "$ROOT/electron/version.json")"
