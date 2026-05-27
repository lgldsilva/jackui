# JackUI — Claude Code Instructions

Servidor de **streaming de torrents** com transcode por hardware e UI web. Começou como um buscador visual para o Jackett e evoluiu para um media server completo: busca → stream BitTorrent→HTTP (sem esperar download completo) → transcode sob demanda → playback no navegador (incl. Safari via HLS).

## Stack

- **Backend**: Go 1.22 + Gin. Streaming via `anacrolix/torrent` (BitTorrent → HTTP com Range). Transcode via ffmpeg (NVENC/VAAPI/QSV/libx264).
- **Frontend**: React 18 + TypeScript + Vite + TailwindCSS (dark theme), embutido no binário (`//go:embed all:dist`).
- **Infra**: Docker no Raspberry Pi (context `raspberrypisrv`, `192.168.0.100`). **Deploy roteia pela VPN (gluetun)** — ver abaixo.

## Comandos essenciais

```bash
make test              # go test ./... (184 testes, 19 pacotes)
make deploy-auto-vpn   # ✅ DEPLOY PADRÃO: auto-detecta GPU + roteia pela VPN (gluetun)
make deploy-auto       # ⚠️ SEM VPN — torrents saem pelo IP real; use só p/ teste local
make dev-frontend      # Vite :5173 com proxy p/ :8989
make dev-backend       # go run ./cmd/server em :8989
```

**SEMPRE deployar com `deploy-auto-vpn`.** O alvo `-vpn` adiciona `docker-compose.gluetun.yml` (`network_mode: container:gluetun`), roteando todo o tráfego de saída pela VPN. Deploy leech-only (não seeda → não precisa publicar porta de peer). A UI é alcançada pelo NPM via `gluetun:8989` (o NPM já está na rede `vpn-gateway_vpn-net`); nenhuma porta é publicada no host.

## Funcionalidades

- **Streaming**: torrent → HTTP com Range; toca antes de baixar tudo. Cache em disco com eviction LRU (favoritos protegidos).
- **Transcode sob demanda**: HEVC/AV1/x265 → H.264 via GPU. Safari recebe **HLS** (`.m3u8` + segmentos `.ts`) — único caminho que o `<video>` do Safari aceita.
- **Legendas**: embutidas (probe ffmpeg), sidecar `.srt`/`.vtt` no torrent, e externas (OpenSubtitles). Escolha persiste por arquivo (localStorage).
- **TMDB**: enriquece resultados/biblioteca com pôster + metadados (cache SQLite, TTL 30d).
- **Thumbnails por torrent**: arte resolvida e persistida por `info_hash` (colunas no metadata cache). Cadeia fail-safe no play (`POST /api/stream/art/:hash/resolve`): imagem embutida no torrent (poster/cover) → pôster TMDB → frame capturado. `GET /api/stream/art/:hash` serve a arte (bytes/302/204). Cards preferem a arte por infoHash, caindo no pôster TMDB-por-título.
- **IA p/ identificar título** (opcional): chain OpenAI-compatible (`internal/ai`) com fallback + circuit breaker, limpa o nome cru do release antes do TMDB. Liga sozinha via `GROQ_API_KEY`/`OPENROUTER_API_KEY`/`OLLAMA_BASE_URL`. Benchmark modificável (Settings → admin) mede acurácia+latência, calcula score composto (acurácia ÷ √latência) e reordena a chain (persistido em `.ai-benchmark.db`).
- **Playlists**, **Watchlists** (cron + push ntfy), **Continue Watching** (library com resume position), **Downloads em background** (qBittorrent/Transmission), **browser de arquivos locais** (mounts).
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
  subtitles/  tmdb/  local/  parser/  dbutil/  downloads/
```

## Notas críticas (gotchas que já morderam)

- **HLS para Safari**: progressive MP4 via chunked é rejeitado (`SRC_NOT_SUPPORTED`). Use HLS. Sem `append_list` (gera `EXT-X-DISCONTINUITY` que o Safari recusa). `-hls_playlist_type event` (não `vod` — o ffmpeg adia o m3u8 até o fim do transcode).
- **Source seekável obrigatório**: ffmpeg lê o torrent via servidor HTTP loopback com Range (`serveSource`), não via pipe — senão MP4 com `moov` no fim quebra. Seek+Read são atômicos sob mutex (corrida STSC/STCO).
- **Token de mídia**: `<video>/<track>` não mandam header → usam `?token=`. O middleware só aceita `?token=` em rotas de mídia (`/api/stream/*`, `/api/subtitles/download/*`, `/api/local/file`).
- **VOD/seek (#61)** está atrás da flag `hlsVODEnabled` em `internal/transcode/hls.go` — instável no Safari (em avaliação); quando off, cai no EVENT/live estável.
- **`dbutil.ParseTime`** para ler timestamps SQLite (modernc emite RFC3339 às vezes) — não usar `time.Parse` com layout único.

## Convenções

- Comentários só onde o WHY não é óbvio. Erros retornam JSON `{"error": "..."}`.
- Stores SQLite: `MaxOpenConns(1)`, migrations com `IF NOT EXISTS` / `hasColumn`.
- Teste com `net/http/httptest` — sem deps externas. `make test` deve ficar verde (184/19).
- Deploy: **sempre `make deploy-auto-vpn`**.
