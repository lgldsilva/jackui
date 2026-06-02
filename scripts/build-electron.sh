#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST_ELECTRON="$ROOT/dist-electron"

echo "▶ [0/4] Generating version info..."
bash "$ROOT/scripts/version.sh"

echo "▶ [1/4] Building Go binary..."
cd "$ROOT"
mkdir -p "$DIST_ELECTRON"

PLATFORM="${1:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
ARCH="${2:-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')}"

GOOS="$PLATFORM" GOARCH="$ARCH" go build -ldflags="-s -w" -o "$DIST_ELECTRON/jackui-server" ./cmd/server
echo "  ✓ jackui-server ($PLATFORM/$ARCH)"

echo "▶ [2/3] Building React frontend..."
cd "$ROOT/web" && npm run build
echo "  ✓ ui/dist/"

echo "▶ [3/3] Packaging Electron app..."
cd "$ROOT"
npx electron-builder --config electron/builder.config.ts
echo "  ✓ Package pronto em $DIST_ELECTRON/"
