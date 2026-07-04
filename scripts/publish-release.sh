#!/usr/bin/env bash
# Publica um Release do Gitea (que CRIA a tag em $SHA) com changelog automático.
#
# - Idempotente: se já existe Release/tag pra $SEMVER, não faz nada (rebuild do
#   mesmo commit OU push não-releasable em que semver.sh devolveu a última tag).
# - Robusto: uma falha aqui NÃO derruba o deploy (loga aviso) — o artefato já foi
#   buildado+escaneado+pushado; a Release pode ser refeita num re-run.
# - TLS verificado (alinhado ao trust-a-CA do #463): usa a CA baked no runner
#   quando existe; senão confia no trust store do OS (que já resolve o Gitea).
#
# Env obrigatórios: GITEA_API, REPO, TOKEN, SEMVER, SHA
# Env opcional:     REPO_URL (link de comparação no changelog)
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

: "${SEMVER:=}"
if [ -z "$SEMVER" ]; then
  echo "publish-release: SEMVER vazio — nada a publicar."
  exit 0
fi
: "${GITEA_API:?GITEA_API obrigatório}"
: "${REPO:?REPO obrigatório}"
: "${TOKEN:?TOKEN obrigatório}"
: "${SHA:?SHA obrigatório}"

CA=/usr/local/share/ca-certificates/gitea-ca.crt
CURL_CA=()
[ -f "$CA" ] && CURL_CA=(--cacert "$CA")
api() { curl -s --max-time 30 "${CURL_CA[@]}" -H "Authorization: token $TOKEN" "$@"; }

# A TAG já existe? → não republica. Cobre os dois no-ops de uma vez, server-side
# (imune a checkout stale): rebuild do mesmo commit E push não-releasable (semver.sh
# devolveu a última tag existente). Só uma versão NOVA (sem tag) segue pra criação.
code=$(api -o /dev/null -w '%{http_code}' "$GITEA_API/repos/$REPO/git/refs/tags/$SEMVER" || echo 000)
if [ "$code" = "200" ]; then
  echo "publish-release: tag $SEMVER já existe — sem nova versão."
  exit 0
fi

notes_file=$(mktemp)
REPO_URL="${REPO_URL:-}" bash scripts/changelog.sh "$SEMVER" > "$notes_file" || true

payload_file=$(mktemp)
SEMVER="$SEMVER" SHA="$SHA" NOTES_FILE="$notes_file" python3 - "$payload_file" <<'PY'
import json, os, sys
body = open(os.environ["NOTES_FILE"], encoding="utf-8").read().strip() or "Sem changelog."
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
  echo "publish-release: Release $SEMVER criado (tag em ${SHA:0:7})."
else
  echo "Aviso: falha ao criar Release $SEMVER (HTTP $code):"
  cat /tmp/release-resp.json 2>/dev/null || true
fi
