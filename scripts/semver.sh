#!/usr/bin/env bash
# Calcula a PRÓXIMA versão semver a partir dos Conventional Commits desde a
# última tag vX.Y.Z — e SÓ bumpa quando há mudança "releasable":
#
#   feat:                  → bump MINOR
#   fix: / perf:           → bump PATCH
#   <tipo>!: / BREAKING    → bump MINOR enquanto major==0 (0.x), MAJOR a partir de 1.0
#   só chore/ci/docs/test/build/style/refactor (ou nada convencional) → SEM bump
#
# Quando NÃO há nada releasable (ou o HEAD já está exatamente numa tag), imprime
# a ÚLTIMA tag inalterada. O chamador (release.yml) trata "computado == tag que já
# existe" como "não criar tag/Release nova" — build+deploy seguem, sem inflar a
# versão a cada merge trivial (era 1 tag por merge → 173 tags).
#
# Robusto a merge commits do Gitea: o assunto do merge carrega o título do PR
# ("Merge pull request 'fix(x): ...'"), então o tipo é detectado tanto no início
# do assunto quanto logo após "Merge pull request '".
#
# Uso:  scripts/semver.sh          → imprime "vX.Y.Z" no stdout (só isso).
# Não cria nem dá push de tag — quem decide isso é o chamador.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

semver_tag_glob='v[0-9]*.[0-9]*.[0-9]*'

# HEAD já tagueado? reusa a maior tag semver que aponta pra ele (rebuild idempotente).
head_tag=$(git tag --points-at HEAD --list "$semver_tag_glob" 2>/dev/null | sort -V | tail -1 || true)
if [ -n "$head_tag" ]; then
  echo "$head_tag"
  exit 0
fi

# Última tag semver e o range de commits desde ela.
last=$(git tag --list "$semver_tag_glob" --sort=-v:refname 2>/dev/null | head -1 || true)
if [ -n "$last" ]; then
  range="$last..HEAD"
else
  last="v0.0.0"
  range="HEAD"
fi

# TODOS os assuntos (inclui merges: o título do PR mora no assunto do merge) + os
# corpos (pra "BREAKING CHANGE" no rodapé, que os commits reais carregam).
subjects=$(git log "$range" --format='%s' 2>/dev/null || true)
bodies=$(git log "$range" --format='%B' 2>/dev/null || true)

# match_type <alternação-de-tipos> → sucesso se algum commit é daquele(s) tipo(s),
# aceitando o tipo no início do assunto OU dentro do título de um merge do Gitea.
match_type() {
  printf '%s\n' "$subjects" | grep -qiE \
    "^($1)(\([^)]*\))?!?:|^Merge pull request '($1)(\([^)]*\))?!?:"
}

# breaking: "<tipo>!:" no assunto (qualquer forma) OU "BREAKING CHANGE" como FOOTER
# do corpo. Ancorado em início de linha + ":" e case-sensitive (a spec exige o
# footer em maiúsculas) pra NÃO casar a frase citada em prosa — um commit que só
# MENCIONA "BREAKING CHANGE" no meio de uma explicação não é um breaking change.
is_breaking() {
  printf '%s\n' "$subjects" | grep -qE \
    "^[a-zA-Z]+(\([^)]*\))?!:|^Merge pull request '[a-zA-Z]+(\([^)]*\))?!:" \
    || printf '%s\n' "$bodies" | grep -qE '^BREAKING[ -]CHANGE:'
}

bump=none
if match_type 'fix|perf'; then bump=patch; fi
if match_type 'feat';     then bump=minor; fi
if is_breaking;           then bump=break; fi

if [ "$bump" = none ]; then
  # Nada releasable → não bumpa; devolve a última tag (o chamador não cria Release).
  echo "$last"
  exit 0
fi

v=${last#v}
major=${v%%.*}; rest=${v#*.}; minor=${rest%%.*}; patch=${rest#*.}
case "$bump" in
  break)
    if [ "$major" -eq 0 ]; then
      # Convenção 0.x: breaking bumpa MINOR (não pula pra 1.0.0 sozinho).
      minor=$((minor + 1)); patch=0
    else
      major=$((major + 1)); minor=0; patch=0
    fi
    ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac
echo "v${major}.${minor}.${patch}"
