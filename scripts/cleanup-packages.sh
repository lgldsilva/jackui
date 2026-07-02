#!/bin/bash
# Limpeza de pacotes container antigos no Gitea Registry.
# Mantém a tag :nvidia e as 2 versões de commit-sha mais recentes.

set -e

GITEA_API="${GITEA_API:-https://gitea.raspberrypi.lan/api/v1}"
GITEA_USER="${GITEA_USER:-lgldsilva}"
GITEA_TOKEN="${GITEA_TOKEN}"

if [ -z "$GITEA_TOKEN" ]; then
  echo "Aviso: GITEA_TOKEN ausente. Pulando limpeza de pacotes."
  exit 0
fi

echo "=== Iniciando limpeza de pacotes antigos de jackui ==="

# Busca a lista de pacotes via API e filtra usando jq.
# Se o runner tiver o jq instalado (imagem catthehacker/ubuntu possui jq nativo),
# não precisamos rodar o jq via Docker container!
# Vamos testar se o jq está disponível, senão usamos python que está sempre disponível.
# Usando python3 para processar o JSON é mais portátil e garantido em qualquer container de trabalho.

python3 -c '
import sys, json, urllib.request, ssl

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE

req = urllib.request.Request(
    "'"$GITEA_API"'/packages/'"$GITEA_USER"'?type=container&limit=100",
    headers={"Authorization": "token '"$GITEA_TOKEN"'"}
)

try:
    with urllib.request.urlopen(req, context=ctx) as response:
        packages = json.loads(response.read().decode())
        
    # Filtra pacotes com nome jackui e versão que seja um commit hash (sha8 a sha40)
    import re
    commit_regex = re.compile(r"^[0-9a-f]{8,40}$")
    
    jackui_versions = [
        p for p in packages 
        if p.get("name") == "jackui" and commit_regex.match(p.get("version", ""))
    ]
    
    # Ordena por data de criação (mais recente primeiro)
    jackui_versions.sort(key=lambda x: x.get("created_at", ""), reverse=True)
    
    # Mantém os 2 mais recentes, apaga o resto
    to_delete = jackui_versions[2:]
    
    for p in to_delete:
        version = p["version"]
        print(version)
except Exception as e:
    print(f"Erro ao buscar/filtrar pacotes: {e}", file=sys.stderr)
    sys.exit(1)
' | while read -r v; do
  if [ -n "$v" ]; then
    echo "Apagando pacote jackui:$v..."
    HTTP_CODE=$(curl -sk -o /dev/null -w "%{http_code}" -X DELETE \
      -H "Authorization: token $GITEA_TOKEN" \
      "$GITEA_API/packages/$GITEA_USER/container/jackui/$v")
    echo "  -> Resultado: HTTP $HTTP_CODE"
  fi
done

echo "=== Limpeza de pacotes concluída ==="
