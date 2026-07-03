#!/bin/bash
# Geração de SBOM e upload para o Dependency-Track via API.
# Roda nativamente de dentro do container de trabalho do Gitea Actions.

set -e

# DT_API vem do workflow (secrets.DT_API); sem default hardcoded para não vazar o host interno.
DT_API="${DT_API}"
DT_USER="${DT_USER}"
DT_PASS="${DT_PASS}"

echo "=== Iniciando geração de SBOM ==="
# Executa cdxgen nativo via npx (sem QEMU ou mounts complexos de volumes docker)
FETCH_LICENSE=false npx -y @cyclonedx/cdxgen --spec-version 1.6 -r -t go -t javascript -o bom.json .

if [ ! -s bom.json ]; then
  echo "Erro: bom.json está vazio ou não foi gerado pelo cdxgen"
  exit 1
fi

echo "=== SBOM gerado com sucesso (~$(du -h bom.json | cut -f1)) ==="

if [ -z "$DT_API" ] || [ -z "$DT_USER" ] || [ -z "$DT_PASS" ]; then
  echo "Aviso: Dependency-Track não configurado (DT_API / DT_USER / DT_PASS). Pulando upload."
  exit 0
fi

echo "=== Efetuando login no Dependency-Track ==="
JWT=$(curl -sk --max-time 20 -X POST "$DT_API/api/v1/user/login" \
  --data-urlencode "username=$DT_USER" \
  --data-urlencode "password=$DT_PASS" || true)

if [ -z "$JWT" ]; then
  echo "Aviso: Login no Dependency-Track falhou/vazio. Pulando upload."
  exit 0
fi

echo "=== Enviando SBOM para o Dependency-Track ==="
printf '{"projectName":"jackui","projectVersion":"main","autoCreate":true,"bom":"%s"}' \
  "$(base64 -w0 bom.json)" > dt-payload.json

HTTP_CODE=$(curl -sk --max-time 60 -o /dev/null -w "%{http_code}" -X PUT "$DT_API/api/v1/bom" \
  -H "Authorization: Bearer $JWT" \
  -H 'Content-Type: application/json' \
  --data-binary @dt-payload.json)

if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "201" ]; then
  echo "=== Upload concluído com sucesso [HTTP $HTTP_CODE] ==="
else
  echo "Aviso: Falha no upload do SBOM para o Dependency-Track [HTTP $HTTP_CODE]"
fi

# Limpeza
rm -f bom.json dt-payload.json
