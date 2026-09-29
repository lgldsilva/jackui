#!/bin/sh
# update-jackui-port.sh — integrates jackui into gluetun's port forwarding.
#
# Called via VPN_PORT_FORWARDING_UP_COMMAND when ProtonVPN (re)assigns the
# forwarded port. JackUI uses a PULL model — it reads the port from gluetun's
# control server and restarts gracefully to rebind (anacrolix pins the port at
# boot). This script just FORCES an immediate refresh instead of waiting for the
# internal watcher's ~2min poll. It is idempotent: jackui only restarts if the
# port actually changed.
#
# Requires:
#   - TORRENT_PORT_FORWARD_TARGET=jackui (dispatches here in update-torrent-port.sh)
#   - JACKUI_CONTROL_TOKEN set BOTH in gluetun (this script) and in jackui (handler)
#   - jackui in the SAME netns as gluetun (network_mode: container/service:gluetun),
#     reachable at localhost:8989

JACKUI_URL="http://localhost:8989/api/stream/peer-port/refresh"
TOKEN="${JACKUI_CONTROL_TOKEN:-}"
MAX_RETRIES=18
RETRY_DELAY=10

if [ -z "$TOKEN" ]; then
    echo "JACKUI_CONTROL_TOKEN not set; skipping jackui refresh" >&2
    exit 0
fi

attempt=0
while [ "$attempt" -lt "$MAX_RETRIES" ]; do
    attempt=$((attempt + 1))
    if RESP=$(wget -qO- --post-data='' --header="Authorization: Bearer $TOKEN" "$JACKUI_URL" 2>&1); then
        echo "jackui peer-port refresh: $RESP"
        exit 0
    fi
    echo "jackui unavailable (attempt $attempt/$MAX_RETRIES); waiting ${RETRY_DELAY}s..."
    sleep "$RETRY_DELAY"
done

echo "Could not reach the jackui refresh endpoint after $MAX_RETRIES attempts" >&2
exit 1
