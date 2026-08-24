#!/usr/bin/env bash
# Gera electron/version.json — artefato de build (gitignored), não commitar.
#
# Precedência do campo "version":
#   1. scripts/semver.sh (tag vX.Y.Z no HEAD ou próxima versão calculada dos
#      Conventional Commits) — mesma fonte do release.yml e do APP_VERSION dos
#      builds Docker, então app Electron e imagem reportam o mesmo valor;
#   2. "version" do package.json raiz, quando o repo não tem nenhuma tag semver
#      (clone shallow, checkout sem tags) ou o semver.sh falha.
# O valor segue para o diálogo Sobre (electron/main.ts lê version.json) e, via
# scripts/build-electron.sh, para o /status do servidor Go embutido — os três
# artefatos do mesmo build mostram a mesma versão.
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
