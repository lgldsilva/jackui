#!/bin/sh
# update-jackui-port.sh — integra o jackui ao port-forwarding do gluetun.
#
# Chamado via VPN_PORT_FORWARDING_UP_COMMAND quando o ProtonVPN (re)atribui a
# porta forwardada. O jackui usa modelo PULL — ele lê a porta no control server
# do gluetun e reinicia gracioso pra rebindar (anacrolix fixa a porta no boot).
# Este script só FORÇA o refresh imediato, em vez de esperar o poll de ~2min do
# watcher interno. É idempotente: o jackui só reinicia se a porta mudou de fato.
#
# Requer:
#   - TORRENT_PORT_FORWARD_TARGET=jackui (despacha pra cá em update-torrent-port.sh)
#   - JACKUI_CONTROL_TOKEN definido NO gluetun (este script) E no jackui (handler)
#   - jackui no MESMO netns do gluetun (network_mode: container/service:gluetun),
#     alcançável em localhost:8989

JACKUI_URL="http://localhost:8989/api/stream/peer-port/refresh"
TOKEN="${JACKUI_CONTROL_TOKEN:-}"
MAX_RETRIES=18
RETRY_DELAY=10

if [ -z "$TOKEN" ]; then
    echo "JACKUI_CONTROL_TOKEN não definido; pulando refresh do jackui" >&2
    exit 0
fi

attempt=0
while [ "$attempt" -lt "$MAX_RETRIES" ]; do
    attempt=$((attempt + 1))
    if RESP=$(wget -qO- --post-data='' --header="Authorization: Bearer $TOKEN" "$JACKUI_URL" 2>&1); then
        echo "jackui peer-port refresh: $RESP"
        exit 0
    fi
    echo "jackui indisponível (tentativa $attempt/$MAX_RETRIES); aguardando ${RETRY_DELAY}s..."
    sleep "$RETRY_DELAY"
done

echo "Não foi possível alcançar o endpoint de refresh do jackui após $MAX_RETRIES tentativas" >&2
exit 1
