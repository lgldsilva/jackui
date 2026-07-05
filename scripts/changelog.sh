#!/usr/bin/env bash
# Gera um changelog em markdown a partir dos Conventional Commits desde a última
# tag semver até HEAD, agrupado por tipo. Alimenta o corpo do Release do Gitea.
#
# Uso:  scripts/changelog.sh [<nova-versão>]   → imprime markdown no stdout.
# Env (opcional):
#   REPO_URL   base do repo (ex. https://gitea.raspberrypi.lan/lgldsilva/jackui)
#              → adiciona rodapé "Full changelog: <last>...<nova-versão>".
#
# A nova-versão AINDA NÃO existe como tag quando isto roda (o Release a cria), então
# o range vai da última tag EXISTENTE até HEAD.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

newver="${1:-}"
glob='v[0-9]*.[0-9]*.[0-9]*'

last=$(git tag --list "$glob" --sort=-v:refname 2>/dev/null | head -1 || true)
if [ -n "$last" ]; then
  range="$last..HEAD"
else
  range="HEAD"
fi

# Assuntos dos commits reais (sem os merges "Merge pull request ..." que só ruído).
subjects=$(git log "$range" --no-merges --format='%s|%h' 2>/dev/null || true)

# section <título> <alternação-de-tipos> → imprime "### título" + itens, se houver.
section() {
  local title="$1" types="$2" lines
  lines=$(printf '%s\n' "$subjects" \
    | grep -iE "^($types)(\([^)]*\))?!?:" \
    | sed -E "s/^([a-zA-Z]+(\([^)]*\))?!?): *(.*)\|([0-9a-f]+)$/- \3 (\4)/" || true)
  if [ -n "$lines" ]; then
    printf '### %s\n%s\n\n' "$title" "$lines"
  fi
}

# BREAKING CHANGES em destaque — só o FOOTER real "BREAKING CHANGE:" (início de
# linha + ":", maiúsculas), não a frase citada em prosa (senão um commit que só
# menciona "BREAKING CHANGE" numa explicação vira uma seção falsa no changelog).
breaking=$(git log "$range" --no-merges --format='%B' 2>/dev/null \
  | grep -E '^BREAKING[ -]CHANGE:' | sed -E 's/^BREAKING[ -]CHANGE: */- /' || true)
if [ -n "$breaking" ]; then
  printf '### ⚠️ BREAKING CHANGES\n%s\n\n' "$breaking"
fi

section '✨ Features'      'feat'
section '🔒 Security'      'security'
section '🐛 Fixes'         'fix'
section '⚡ Performance'    'perf'
section '♻️ Refactor'      'refactor'
section '🔧 Chore / CI / Docs' 'chore|ci|docs|build|test|style'

# "Outros": commits sem tipo convencional reconhecido (não caem em nenhuma seção).
# O primeiro grep exige o separador "|<hash>", descartando a linha vazia que o
# printf gera quando não há commit no range.
others=$(printf '%s\n' "$subjects" \
  | grep -E '\|[0-9a-f]+$' \
  | grep -viE "^(feat|fix|perf|security|refactor|chore|ci|docs|build|test|style)(\([^)]*\))?!?:" \
  | sed -E 's/^(.*)\|([0-9a-f]+)$/- \1 (\2)/' || true)
if [ -n "$others" ]; then
  printf '### 📦 Outros\n%s\n\n' "$others"
fi

# Rodapé com link de comparação, quando o REPO_URL é conhecido.
if [ -n "${REPO_URL:-}" ] && [ -n "$last" ] && [ -n "$newver" ]; then
  printf '**Full changelog:** [%s...%s](%s/compare/%s...%s)\n' \
    "$last" "$newver" "${REPO_URL%/}" "$last" "$newver"
fi
