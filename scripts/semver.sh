#!/usr/bin/env bash
# Calcula a PRÓXIMA versão semver a partir dos Conventional Commits desde a
# última tag vX.Y.Z:
#   feat:                              → bump MINOR
#   fix:/perf:/refactor:/chore:/etc    → bump PATCH
#   <tipo>!: ... ou "BREAKING CHANGE"  → bump MAJOR
#
# Idempotente: se o HEAD já está exatamente numa tag semver (rebuild do mesmo
# commit), imprime essa tag em vez de inventar uma nova.
#
# Uso:  scripts/semver.sh          → imprime "vX.Y.Z" no stdout (nada mais).
# Não cria nem dá push de tag — quem decide isso é o chamador (Jenkinsfile).
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

semver_tag_glob='v[0-9]*.[0-9]*.[0-9]*'

# HEAD já tagueado? reusa a maior tag semver que aponta pra ele.
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

# --no-merges: os merge commits do Gitea ("Merge pull request ...") não carregam
# o tipo do conventional commit; os commits reais (feat:/fix:) estão dentro do PR.
subjects=$(git log "$range" --no-merges --format='%s' 2>/dev/null || true)
bodies=$(git log "$range" --no-merges --format='%B' 2>/dev/null || true)

bump=patch
if printf '%s\n' "$subjects" | grep -qiE '^feat(\([^)]*\))?!?:'; then
  bump=minor
fi
# MAJOR vence: "<tipo>!:" no assunto OU "BREAKING CHANGE" no corpo.
if printf '%s\n' "$subjects" | grep -qE '^[a-zA-Z]+(\([^)]*\))?!:' \
  || printf '%s\n' "$bodies" | grep -qE 'BREAKING[ -]CHANGE'; then
  bump=major
fi

v=${last#v}
major=${v%%.*}; rest=${v#*.}; minor=${rest%%.*}; patch=${rest#*.}
case "$bump" in
  major) major=$((major + 1)); minor=0; patch=0 ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac
echo "v${major}.${minor}.${patch}"
