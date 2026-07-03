# JackUI

A self-hosted **torrent streaming media server** — search, stream a torrent to your browser *before it finishes downloading*, transcode on the fly with hardware acceleration, and watch (Safari included, via HLS). Go backend, React UI embedded in a single binary, runs in Docker.

![status](https://img.shields.io/badge/status-active-success) ![go](https://img.shields.io/badge/Go-1.25+-00ADD8) ![license](https://img.shields.io/badge/license-MIT-blue)

> JackUI started as a visual front-end for [Jackett](https://github.com/Jackett/Jackett) and grew into a full media server: find a release, start playing it while it downloads, and let the server transcode incompatible codecs to something your browser can play. No "wait for the download to finish", no separate transcoding box.
>
> JackUI is an independent project — it is **not affiliated with or endorsed by Jackett**.

> [!NOTE]
> The web UI is internationalised (react-i18next): Portuguese and English ship today (`web/src/locales/{pt,en}.json`). This README and the developer docs are in English.

## How it works

```
                    ┌──────────┐   magnet / .torrent
   browser  ──────► │  JackUI  │ ──────────────────────────┐
   (search)         │  (Gin)   │                            ▼
                    └────┬─────┘                   ┌──────────────────┐
                         │  GET /api/stream/:hash  │ anacrolix/torrent │
   browser  ◄────────────┤   (HTTP + Range)        │  BitTorrent swarm │
   <video>              ▲│                          └────────┬─────────┘
                        ││ HLS (.m3u8 + .ts)  or  raw MP4 Range        │ pieces
                        │▼                                             ▼
                    ┌─────────┐   incompatible codec?          ┌───────────────┐
                    │ ffmpeg  │ ◄──── HEVC/AV1/AC3/DTS ──────── │ disk cache    │
                    │transcode│   NVENC / VAAPI / QSV / x264    │ (LRU evict)   │
                    └─────────┘                                 └───────────────┘
```

The torrent is exposed as a seekable HTTP source with Range support, so ffmpeg (and the browser) can start reading from any offset before the download completes. Incompatible codecs are transcoded on demand; Safari is served HLS because its `<video>` rejects progressive MP4 over chunked transfer.

## Features

- **Stream before download** — torrent → HTTP with Range; playback starts on the first pieces. Disk cache with LRU eviction (favourites protected).
- **On-demand transcode** — HEVC/AV1/x265 → H.264 via GPU (NVENC/VAAPI/QSV) or libx264 fallback. Safari gets **HLS**; everyone else gets direct-play or progressive transcode. The GPU is bounded: a semaphore caps concurrent CUDA decoders (`JACKUI_MAX_GPU_TRANSCODES`, default 3) and an extra session falls back to CPU-decode + NVENC-encode on `CUDA_ERROR_OUT_OF_MEMORY`, so playback never hard-fails for lack of VRAM.
- **Music player** — audio playlists with **gapless** playback, selectable HLS audio track (torrent + local), cover art / chapters / `mediaSession` (lock-screen + AirPlay on Safari/iOS), and a footer mini-player you can drag and expand.
- **Swarm health on cards** — `SeedBadge` shows seeders + availability. A background **tracker scrape** (BEP 48, private trackers included) backs the count so an active torrent never sits at a misleading 0; results are cached.
- **Subtitles** — embedded (ffmpeg probe), sidecar `.srt`/`.vtt` inside the torrent, and external (OpenSubtitles). Choice persists per file.
- **Discover & recommendations** — a **Discover** page with weekly-trending titles, **personalised recommendations** seeded from your favourites + watch history (with dismiss), and a **Music** mode showing trending albums (keyless Apple RSS). Both seed the search on click.
- **TMDB enrichment** — posters + metadata on results and library (SQLite cache, 30-day TTL).
- **Per-torrent artwork** — resolved and persisted by `info_hash` through a fail-safe chain: embedded torrent image → TMDB poster → keyless web image search → captured frame.
- **AI title cleanup** (optional) — an OpenAI-compatible chain (Groq / OpenRouter / Ollama) cleans raw release names before the TMDB lookup, with per-provider fallback + circuit breaker. A tunable benchmark (Settings → admin) scores providers on accuracy, latency and cost/energy, keeps history, and reorders the chain.
- **Downloads queue** — the internal worker treats a **multi-file torrent as one unit**: the scheduler counts a slot per torrent and steers the anacrolix file priorities (selected = download, the rest = cancel), so one big pack no longer hogs every slot or spikes CPU/RAM. The list groups one card per torrent; you can filter completed into **Seeding vs On-disk** and sort by speed / seeds / date / name / size / progress. Downloads land under a category folder by default, with a destination picker (incl. browse into mounts) and **auto-seed of completed** torrents (re-seeded in place from the cached metainfo). qBittorrent/Transmission clients are also supported.
- **`*arr` provider** — exposes a Transmission-RPC-compatible endpoint so **Sonarr/Radarr/Prowlarr** can use JackUI as their download client (opt-in). See [docs/TRANSMISSION_RPC.md](docs/TRANSMISSION_RPC.md).
- **Local files** — browse configured mounts; a downloaded video on **local disk** seeks instantly (`http.ServeFile`/sendfile), while remote/rclone mounts get read-ahead + a whole-file LRU disk cache. Local play falls back to HLS when the container/codec needs it. Continue Watching tracks local items too; the list filters by **downloading / done**.
- **Library extras** — Playlists, Watchlists (cron + ntfy push, opt-in auto-download with quality filters), Continue Watching (resume position), and an Incognito toggle that skips history/library writes. A global "reveal hidden" curtain hides flagged items across the UI.
- **Low-footprint mode** — the HLS pipeline shuts ffmpeg down when the **last** viewer leaves (no 5-min survival), the UI pauses its polling when the tab is hidden, and a balanced runtime profile (Go `GOGC`/`GOMEMLIMIT`/`GOMAXPROCS` + `JACKUI_MAX_CONNS`/`JACKUI_PEERS_HIGH`) keeps idle memory low on a home server.
- **Desktop app** (optional) — an Electron wrapper bundling the Go server, with a status tray, magnet deep-links, and native downloads. See [`electron/`](electron/).
- **Auth** — optional JWT (`JACKUI_AUTH_ENABLED=1`) with rotated refresh tokens, roles, MFA/passkeys, and `AdminOnly` routes (incl. admin password reset).
- **Observability** — public `/status` (version/commit/buildTime), Prometheus `/api/metrics` (admin JWT or `JACKUI_METRICS_TOKEN`), structured logs (`JACKUI_LOG_FORMAT=json`), and scheduled **bandwidth windows** for the streamer.

## Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.25, [Gin](https://github.com/gin-gonic/gin), [anacrolix/torrent](https://github.com/anacrolix/torrent) |
| Transcode | ffmpeg (NVENC / VAAPI / QSV / libx264), HLS for Safari |
| Frontend | React 18 + TypeScript + Vite + TailwindCSS, embedded via `//go:embed all:dist` |
| Storage | SQLite (`modernc.org/sqlite`, pure-Go) for state; disk cache for pieces |
| Deploy | Docker, single binary + embedded UI |

## Requirements

- **Go 1.25+** and **Node 18+** (for development/building).
- **ffmpeg** with the hardware encoders you intend to use (NVENC needs an NVIDIA GPU + the NVIDIA container toolkit; VAAPI/QSV need `/dev/dri`).
- **Docker** (for deployment) and a [Jackett](https://github.com/Jackett/Jackett) instance for search.

## Quick start

```bash
# 1. Backend (Go server on :8989, serves the embedded UI)
make dev-backend

# 2. Frontend with hot-reload (Vite on :5173, proxies the API to :8989)
make dev-frontend

# 3. Run the test suite
make test
```

Open `http://localhost:5173` (dev) or `http://localhost:8989` (the embedded build). Configure your Jackett URL + API key in **Settings**, then search.

## Configuration

Runtime config comes from `config.yaml` plus environment overrides (env wins). Key variables:

| Variable | Default | Purpose |
|---|---|---|
| `JACKUI_PORT` | `8989` | HTTP listen port |
| `JACKETT_URL` / `JACKETT_API_KEY` | — | Jackett search backend |
| `JACKUI_CONFIG_DIR` | `./data/config` | **State**: `jackui.db` (history) + `auth.db` |
| `JACKUI_CACHE_DIR` | `./data/cache` | Piece cache + streamer SQLite stores |
| `JACKUI_STORAGE_DIR` | `./data/storage` | Shared library: browsable mounts + "promote" target |
| `JACKUI_STREAM_MAX_GB` | `50` | Cache size cap (LRU eviction above this) |
| `JACKUI_AUTH_ENABLED` | `0` | Enable JWT auth (`1`/`true`) |
| `TMDB_API_KEY` | — | Poster/metadata enrichment (optional) |
| `GROQ_API_KEY` / `OPENROUTER_API_KEY` / `OLLAMA_BASE_URL` | — | AI title cleanup (optional, auto-detected) |
| `JACKUI_TRANSMISSION_RPC_ENABLED` | `0` | Expose the Transmission-RPC `*arr` provider (opt-in) |
| `JACKUI_MAX_GPU_TRANSCODES` | `3` | Cap on concurrent CUDA decoders (`0` = unlimited; extras fall back to CPU-decode) |
| `JACKUI_MAX_CONNS` / `JACKUI_PEERS_HIGH` | — | Peer-connection tuning (conns per torrent / swarm high-water) |

State (`JACKUI_CONFIG_DIR`) is deliberately separate from the piece cache + streamer DBs (`JACKUI_CACHE_DIR`) to reduce I/O contention.

*Low-footprint runtime tuning* (Go `GOGC`/`GOMEMLIMIT`/`GOMAXPROCS`) is applied via the process environment, not `config.yaml`; production sets it in the deploy compose (see below).

## Deployment

JackUI deploys as a Docker container to a home server. It runs on a bridge shared with a reverse proxy (e.g. Nginx Proxy Manager), reachable at `jackui:8989`.

### Local

```bash
make dev-backend     # go run ./cmd/server on :8989
make dev-frontend    # Vite :5173 with API proxy
```

### Production (home server)

```bash
make deploy-auto        # ✅ default: auto-detects GPU (NVENC/VAAPI/QSV), no VPN
make deploy-auto-vpn    # same, but routes through a gluetun VPN overlay (opt-in)
```

`deploy-auto` is the standard `make` target (auto-detects the GPU, no VPN); `-vpn` adds a gluetun overlay (`network_mode: container:gluetun`).

> [!NOTE]
> The author's own production instance currently runs **behind gluetun** (`network_mode: container:gluetun`, on the VPN's forwarded port — `watchForwardedPort` in `cmd/server/main.go` triggers a graceful restart to rebind when the port rotates), even though the no-VPN path is the documented default. Pick the mode that keeps your swarm healthy.

CI/CD is a Jenkins multibranch job (SonarQube quality gate + Trivy + Dependency-Track), documented in [docs/CICD.md](docs/CICD.md). The deploy step runs `docker compose up -d --force-recreate` against a **hand-maintained** `docker-compose.yml` on the server (not a Portainer stack) — Jenkins only swaps the image. **New env vars added to the repo's compose do not reach production by themselves**; edit the server-side compose too.

## Architecture

A short map; the canonical reference is [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

```
cmd/server/main.go   Gin wiring: /api/* + SPA fallback + background workers
ui/embed.go          //go:embed all:dist  (the React build ships inside the binary)
web/src/             React app (PlayerProvider lives above the router)
internal/
  streamer/          anacrolix: Add / FileReader / probe / cache / favourites
  transcode/         ffmpeg pipeline + HLS sessions (seek-restart)
  handlers/          HTTP handlers (search, config, stream, local, classify, …)
  config/ jackett/ downloader/ downloads/ tmdb/ subtitles/ parser/
  auth/ history/ library/ playlists/ watchlist/   SQLite stores
  transmissionrpc/   Transmission-RPC compatibility layer (*arr provider)
  middleware/         cross-cutting Gin middleware (incognito, media-token auth)
```

## Security

- **Auth is optional but real** — with `JACKUI_AUTH_ENABLED=1`, JWT with rotated refresh tokens; `AdminOnly` guards sensitive routes. Media elements (`<video>`/`<track>`) can't send headers, so media routes accept a scoped `?token=`.
- **SSRF guard** — torrent `.torrent`/metadata fetches are restricted to the configured Jackett host.
- **The `*arr` RPC surface is opt-in** (`JACKUI_TRANSMISSION_RPC_ENABLED`, default off) and, when auth is enabled, requires Basic Auth.
- **Local-file browser is confined** to explicitly configured, read-only mounts.

What JackUI does **not** do: it is not hardened for public-internet exposure without a reverse proxy + auth. Run it behind your proxy on a trusted network.

## Roadmap

- [x] **i18n / multi-language UI** — done: react-i18next ships Portuguese + English (`web/src/locales/`).
- [ ] HLS master playlist (Phase 2): N audio/subtitle tracks + multi-resolution in one VOD stream.
- [x] Streamer reconciles pieces with already-downloaded files (play without re-downloading).
- [x] "Promote" button on the local-files page; split the streamer's SQLite stores from the cache dir via `JACKUI_STATE_DIR`.

## Docs

| Doc | What |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Canonical architecture + reading order for new contributors |
| [docs/design-decisions.md](docs/design-decisions.md) | Why the tricky bits are the way they are (and what not to "fix") |
| [docs/TRANSMISSION_RPC.md](docs/TRANSMISSION_RPC.md) | Using JackUI as a Sonarr/Radarr/Prowlarr download client |
| [docs/CICD.md](docs/CICD.md) | Build pipeline, quality gates, deploy |
| [docs/RCLONE.md](docs/RCLONE.md) | Tuning rclone/Google-Drive mounts for streaming (cache, chunking, read-ahead) |

## Legal

JackUI is a **neutral tool**: it searches indexers you configure and streams/downloads content over the BitTorrent protocol. It does not host, index, or distribute any content by itself, and it ships with no indexers or trackers configured.

You are solely responsible for what you search for, download, stream, and share with it. Make sure your use complies with the laws of your jurisdiction and with the rights of content owners. The authors and contributors do not condone copyright infringement and provide this software without any warranty (see [LICENSE](LICENSE)).

## License

[MIT](LICENSE).
