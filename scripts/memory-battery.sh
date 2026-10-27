#!/bin/bash
# Bateria anti-OOM jackui — exercita os caminhos que vazavam e mede memória.
set -u
BASE=http://localhost:8989
CF=/portainer/Files/AppData/Config/jackui/docker-compose.yml
CG=/sys/fs/cgroup/system.slice/docker-$(docker inspect --format '{{.Id}}' jackui).scope

PASS=$(docker exec jackui printenv JACKUI_ADMIN_PASSWORD)
PASS_ESC=$(printf '%s' "$PASS" | sed 's/\\/\\\\/g; s/"/\\"/g')
LOGIN=$(curl -s --max-time 10 -X POST $BASE/api/auth/login -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$PASS_ESC\"}")
TOK=$(printf '%s' "$LOGIN" | grep -oP '"access":"\K[^"]+')
if [ -z "${TOK:-}" ]; then echo "LOGIN_FALHOU (ver MFA/senha)"; exit 1; fi
AH="Authorization: Bearer $TOK"
ok200() { curl -s -o /dev/null -w '%{http_code}' -H "$AH" "$BASE/debug/pprof/goroutine?debug=1"; }

gor()  { curl -s -H "$AH" "$BASE/debug/pprof/goroutine?debug=1" | head -1; }
heap() { curl -s -H "$AH" "$BASE/debug/pprof/heap?debug=1" | grep -m1 '^HeapAlloc' ; }
snap() { echo "goroutines: $(gor) | $(heap) | mem.current=$(cat $CG/memory.current) mem.peak=$(cat $CG/memory.peak)"; }

echo "pprof auth probe: HTTP $(ok200)"
echo "════ BASELINE ════"
snap
docker stats --no-stream jackui --format 'docker: {{.MemUsage}} ({{.MemPercent}})'

( while :; do echo "$(date +%H:%M:%S) $(cat $CG/memory.current)"; sleep 15; done > /tmp/battery_mem.log ) &
SAMPLER=$!

echo "════ FLOOD: 12 magnets mortos, concorrentes (espera ~60-70s) ════"
HASHES=()
for i in $(seq 1 12); do HASHES+=("$(printf 'b%039d' "$i")"); done
PIDS=()
for i in "${!HASHES[@]}"; do
  H=${HASHES[$i]}
  curl -s --max-time 90 -X POST $BASE/api/stream/add -H "$AH" -H 'Content-Type: application/json' \
    -d "{\"magnet\":\"magnet:?xt=urn:btih:$H&dn=bateria-$((i+1))\"}" -o /tmp/battery_add_$i.json &
  PIDS+=($!)
done
wait "${PIDS[@]}"
FAILS=0
for i in $(seq 0 11); do grep -q '"error"' /tmp/battery_add_$i.json 2>/dev/null && FAILS=$((FAILS+1)); rm -f /tmp/battery_add_$i.json; done
echo "adds que falharam como esperado: $FAILS/12"

echo "════ HASHES MORTOS AINDA ATIVOS? (esperado 0) ════"
ACTIVE=0
for H in "${HASHES[@]}"; do
  CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "$AH" "$BASE/api/stream/info/$H")
  [ "$CODE" != "404" ] && [ "$CODE" != "400" ] && ACTIVE=$((ACTIVE+1)) && echo "  hash $H → HTTP $CODE"
done
echo "ativos: $ACTIVE/12"

echo "════ POS-FLOOD ════"
snap

echo "════ BATCH de metadata com hashes reais (rajada) ════"
REAL=$(curl -s -H "$AH" "$BASE/api/stream/favorites" | grep -oP '"infoHash":"\K[a-f0-9]{40}' | sort -u | head -30 | sed 's/^/"/; s/$/"/' | paste -sd,)
if [ -n "${REAL:-}" ]; then
  curl -s --max-time 20 -X POST $BASE/api/stream/metadata/batch -H "$AH" -H 'Content-Type: application/json' -d "{\"hashes\":[$REAL]}" -o /tmp/battery_batch.json -w 'batch HTTP %{http_code} em %{time_total}s, %{size_download} bytes\n'
  rm -f /tmp/battery_batch.json
  snap
else
  echo "sem hashes na biblioteca — batch pulado"
fi

echo "════ AGUARDANDO 2 ticks do gc (150s) para o watchdog ════"
sleep 150
echo "════ FINAL ════"
snap
docker stats --no-stream jackui --format 'docker: {{.MemUsage}} ({{.MemPercent}})'
WD=$(docker logs jackui --since 10m 2>&1 | grep -c 'retention watchdog')
echo "watchdog de órfãos disparou: $WD vez(es) (esperado 0)"
OOMK=$(docker inspect jackui --format '{{.State.OOMKilled}} {{.RestartCount}}')
echo "oomkilled/restarts: $OOMK"
kill $SAMPLER 2>/dev/null
echo "════ TRAJETÓRIA mem.current (15s cada) ════"
awk '{if(min==""||$2<min)min=$2; if($2>max)max=$2} END{printf "mín=%d (%.1fMiB) máx=%d (%.1fMiB)\n", min, min/1048576, max, max/1048576}' /tmp/battery_mem.log
tail -5 /tmp/battery_mem.log
