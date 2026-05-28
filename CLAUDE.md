# JackUI — Claude Code Instructions

Servidor de **streaming de torrents** com transcode por hardware e UI web. Começou como um buscador visual para o Jackett e evoluiu para um media server completo: busca → stream BitTorrent→HTTP (sem esperar download completo) → transcode sob demanda → playback no navegador (incl. Safari via HLS).

## Stack

- **Backend**: Go 1.22 + Gin. Streaming via `anacrolix/torrent` (BitTorrent → HTTP com Range). Transcode via ffmpeg (NVENC/VAAPI/QSV/libx264).
- **Frontend**: React 18 + TypeScript + Vite + TailwindCSS (dark theme), embutido no binário (`//go:embed all:dist`).
- **Infra**: Docker no homeserver (context `homeserver`, `192.168.0.100`). Deploy direto (sem VPN — o overlay gluetun cortava muitos seeds e foi tirado do default). Container roda na bridge `vpn-gateway_vpn-net` (mesma do NPM), alcançado em `jackui:8989`. Saída pelo IP real do host.

## Comandos essenciais

```bash
make test              # go test ./... (233 testes, 23 pacotes)
make deploy-auto       # ✅ DEPLOY PADRÃO: auto-detecta GPU, sem VPN
make deploy-auto-vpn   # com gluetun overlay — só se realmente quiser sair via VPN
make dev-frontend      # Vite :5173 com proxy p/ :8989
make dev-backend       # go run ./cmd/server em :8989
```

**Deploy padrão é `make deploy-auto`.** O `-vpn` adiciona `docker-compose.gluetun.yml` (`network_mode: container:gluetun`) e roteia tudo pela VPN — deixou de ser default porque em muitos torrents o gluetun matava a conectividade com peers. Sem VPN, o NPM alcança jackui em `jackui:8989` na bridge `vpn-gateway_vpn-net`.

## Funcionalidades

- **Streaming**: torrent → HTTP com Range; toca antes de baixar tudo. Cache em disco com eviction LRU (favoritos protegidos).
- **Saúde do swarm nos cards**: `SeedBadge` mostra seeds + disponibilidade. `GET /api/stream/health/:hash?magnet=` devolve o último snapshot (persistido no metadata cache com timestamp) na hora e dispara re-sonda em background se stale; sonda inativa = add~6s → conta → drop (semáforo de 3, dedupe, guarda de ponteiro pra não derrubar play concorrente).
- **Transcode sob demanda**: HEVC/AV1/x265 → H.264 via GPU. Safari recebe **HLS** (`.m3u8` + segmentos `.ts`) — único caminho que o `<video>` do Safari aceita.
- **Legendas**: embutidas (probe ffmpeg), sidecar `.srt`/`.vtt` no torrent, e externas (OpenSubtitles). Escolha persiste por arquivo (localStorage).
- **TMDB**: enriquece resultados/biblioteca com pôster + metadados (cache SQLite, TTL 30d). Resolve o `imdb_id` (external_ids) e persiste junto da arte. **Discover** (`/discover`): grade de "Em alta" (trending semanal, cache em memória 6h) → clicar semeia a busca via `?q=`.
- **Thumbnails por torrent**: arte resolvida e persistida por `info_hash` (colunas no metadata cache). Cadeia fail-safe (`POST /api/stream/art/:hash/resolve`): imagem embutida no torrent (poster/cover) → pôster TMDB → **busca web** (`internal/imagesearch`: DuckDuckGo→Bing keyless, safe-search off — p/ adulto/obscuro que o TMDB não cobre, só após TMDB falhar) → frame capturado. `GET /api/stream/art/:hash` serve a arte (bytes/302/204); aceita `?name=` p/ resolução proativa de torrent inativo. Continuar Assistindo dispara resolve nos itens sem arte. Cards preferem a arte por infoHash, caindo no pôster TMDB-por-título.
- **IA p/ identificar título** (opcional): chain OpenAI-compatible (`internal/ai`) com fallback + circuit breaker, limpa o nome cru do release antes do TMDB. Liga sozinha via `GROQ_API_KEY`/`OPENROUTER_API_KEY`/`OLLAMA_BASE_URL`. Benchmark modificável (Settings → admin) mede acurácia+latência, calcula score composto (acurácia ÷ √latência) e reordena a chain (persistido em `.ai-benchmark.db`).
- **Playlists**, **Watchlists** (cron + push ntfy), **Continue Watching** (library com resume position), **Downloads em background** (qBittorrent/Transmission), **browser de arquivos locais** (mounts; `external.mounts` no config OU env `JACKUI_EXTERNAL_MOUNTS=Nome:/caminho,...`). **Modo Incógnito** (toggle no header): header `X-JackUI-Incognito: 1` ou `?incognito=1` (pra SSE) → middleware seta `c.Set("incognito", true)`; handlers de history/library/StreamAdd consultam `middleware.IsIncognito(c)` e pulam o write silenciosamente. **Transcode local** (`/api/local/play`): ffprobe decide direct-play vs HLS — MKV/HEVC/AC3/DTS caem em HLS reusando o `HLSSessionManager` dos torrents. Paths no deploy: **state em `/portainer/Files/AppData/Config/jackui/`** (`jackui.db` history + `auth.db`), separado pra aliviar contenção de I/O com Jackett/Ollama no LVM; **cache de pieces + DBs do streamer** (favorites, metadata-cache, library, playlists, downloads, tmdb, watchlist, ai-benchmark) em `/mnt/lvm1-storage/jacktrack/cache/`. Completos do worker movem-se pra `/mnt/jacktrack-download/`; irmão `/Downloads/` é o **compartilhado**. Mounts navegáveis (ro): `Downloads` + `Meus downloads`. TODO: streamer reconciliar pieces com arquivos já em `/Downloads` (tocar sem re-baixar); botão "promover" no LocalPage; separar os 7 DBs do streamer do dir de cache LVM (via `JACKUI_STATE_DIR`).
- **Auth** JWT opcional (`JACKUI_AUTH_ENABLED=1`), com refresh token rotacionado e `AdminOnly` para rotas sensíveis.

## Arquitetura

```
web/src/            → React (dev :5173, prod embutido); PlayerProvider mantém o player acima do router
ui/embed.go         → //go:embed all:dist
cmd/server/main.go  → wiring Gin: /api/* + SPA fallback + workers (downloads, watchlist)
internal/
  config/   jackett/   downloader/   handlers/      → base (busca, download, config)
  streamer/                                         → anacrolix: Add/FileReader/probe/cache/favorites
  transcode/                                        → ffmpeg pipeline + HLS (sessões, seek-restart)
  auth/  history/  library/  playlists/  watchlist/ → SQLite stores (modernc.org/sqlite)
  middleware/                                       → middlewares Gin cross-cutting (hoje: incognito)
  subtitles/  tmdb/  local/  parser/  dbutil/  downloads/
```

## Notas críticas (gotchas que já morderam)

- **HLS para Safari**: progressive MP4 via chunked é rejeitado (`SRC_NOT_SUPPORTED`). Use HLS. Sem `append_list` (gera `EXT-X-DISCONTINUITY` que o Safari recusa). `-hls_playlist_type event` (não `vod` — o ffmpeg adia o m3u8 até o fim do transcode).
- **Source seekável obrigatório**: ffmpeg lê o torrent via servidor HTTP loopback com Range (`serveSource`), não via pipe — senão MP4 com `moov` no fim quebra. Seek+Read são atômicos sob mutex (corrida STSC/STCO).
- **Token de mídia**: `<video>/<track>` não mandam header → usam `?token=`. O middleware só aceita `?token=` em rotas de mídia (`/api/stream/*`, `/api/subtitles/download/*`, `/api/local/file`, `/api/local/hls/*`).
- **VOD/seek (#61)** está atrás da flag `hlsVODEnabled` em `internal/transcode/hls.go` — instável no Safari (em avaliação); quando off, cai no EVENT/live estável.
- **`dbutil.ParseTime`** para ler timestamps SQLite (modernc emite RFC3339 às vezes) — não usar `time.Parse` com layout único.
- **Worker de downloads é assíncrono**: `internal/downloads/worker.go` faz `EnsureActive`+`GotInfo` (até 90s) em goroutine separada (mapas `pending`/`retries` sob mutex), com até `maxInitRetries=3` retries em memória antes de marcar `failed`. Um magnet morto NÃO congela mais os outros downloads. `Stop()` cancela in-flight via context.
- **`UpdateName` pós-metadata**: o row de download é criado com o título da busca; o nome real (`t.Name()`) vem depois e é persistido via `store.UpdateName` pra que o boot-time `RegisterDownload` no `NewWorker` proteja o path certo da LRU.
- **Merge de trackers no agrupamento** (`web/src/lib/group.ts`): ao agrupar resultados por `infoHash` (ou `name|size` fallback), os `tr=` de TODOS os magnets do bucket são folded no magnet do primary — mais peers em Play/Download sem nenhuma mudança no backend (anacrolix já honra múltiplos `tr=`).
- **Local transcode reusa o `HLSSessionManager`**: `internal/handlers/local_play.go` faz ffprobe; se container/codec não casa com browser → HLS via o MESMO manager dos torrents. `/api/local/hls/` está no whitelist do `isMediaPath` (auth/middleware.go) pra aceitar `?token=` no `<video>`.

## Convenções

- Comentários só onde o WHY não é óbvio. Erros retornam JSON `{"error": "..."}`.
- Stores SQLite: `MaxOpenConns(1)`, migrations com `IF NOT EXISTS` / `hasColumn`.
- Teste com `net/http/httptest` — sem deps externas. `make test` deve ficar verde (233/23).
- Deploy: `make deploy-auto` (padrão sem VPN); `-vpn` é opt-in.
- **RTK summariza `git diff`** por padrão. Quando precisar do output bruto pra `git apply` / parsing, use `rtk proxy git diff ...` — senão `git apply` cospe "No valid patches in input".
