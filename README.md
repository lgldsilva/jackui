# JackUI

A self-hosted **torrent streaming media server** — search, stream a torrent to your browser *before it finishes downloading*, transcode on the fly with hardware acceleration, and watch (Safari included, via HLS). Go backend, React UI embedded in a single binary, runs in Docker.

![status](https://img.shields.io/badge/status-active-success) ![go](https://img.shields.io/badge/Go-1.22+-00ADD8) ![license](https://img.shields.io/badge/license-MIT-blue)

> JackUI started as a visual front-end for [Jackett](https://github.com/Jackett/Jackett) and grew into a full media server: find a release, start playing it while it downloads, and let the server transcode incompatible codecs to something your browser can play. No "wait for the download to finish", no separate transcoding box.

> [!NOTE]
> The web UI is currently Portuguese-only. Internationalisation (i18n) is on the [roadmap](#roadmap). This README and the developer docs are in English.

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
- **On-demand transcode** — HEVC/AV1/x265 → H.264 via GPU (NVENC/VAAPI/QSV) or libx264 fallback. Safari gets **HLS**; everyone else gets direct-play or progressive transcode.
- **Swarm health on cards** — `SeedBadge` shows seeders + availability, probed in the background and cached.
- **Subtitles** — embedded (ffmpeg probe), sidecar `.srt`/`.vtt` inside the torrent, and external (OpenSubtitles). Choice persists per file.
- **TMDB enrichment** — posters + metadata on results and library (SQLite cache, 30-day TTL). **Discover** page surfaces weekly-trending titles.
- **Per-torrent artwork** — resolved and persisted by `info_hash` through a fail-safe chain: embedded torrent image → TMDB poster → keyless web image search → captured frame.
- **AI title cleanup** (optional) — an OpenAI-compatible chain (Groq / OpenRouter / Ollama) cleans raw release names before the TMDB lookup, with fallback + circuit breaker.
- **Background downloads** — queue torrents to disk via the internal worker (qBittorrent/Transmission clients also supported).
- **`*arr` provider** — exposes a Transmission-RPC-compatible endpoint so **Sonarr/Radarr/Prowlarr** can use JackUI as their download client (opt-in). See [docs/TRANSMISSION_RPC.md](docs/TRANSMISSION_RPC.md).
- **Library extras** — Playlists, Watchlists (cron + ntfy push), Continue Watching (resume position), a local-files browser over configured mounts, and an Incognito toggle that skips history/library writes.
- **Desktop app** (optional) — an Electron wrapper bundling the Go server, with a status tray, magnet deep-links, and native downloads. See [`electron/`](electron/).
- **Auth** — optional JWT (`JACKUI_AUTH_ENABLED=1`) with rotated refresh tokens and `AdminOnly` routes.

## Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.22, [Gin](https://github.com/gin-gonic/gin), [anacrolix/torrent](https://github.com/anacrolix/torrent) |
| Transcode | ffmpeg (NVENC / VAAPI / QSV / libx264), HLS for Safari |
| Frontend | React 18 + TypeScript + Vite + TailwindCSS, embedded via `//go:embed all:dist` |
| Storage | SQLite (`modernc.org/sqlite`, pure-Go) for state; disk cache for pieces |
| Deploy | Docker, single binary + embedded UI |

## Requirements

- **Go 1.22+** and **Node 18+** (for development/building).
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

State (`JACKUI_CONFIG_DIR`) is deliberately separate from the piece cache + streamer DBs (`JACKUI_CACHE_DIR`) to reduce I/O contention.

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

`deploy-auto` is the standard path. The `-vpn` variants add a gluetun overlay (`network_mode: container:gluetun`); it is **not** the default because the VPN was cutting peer connectivity on many torrents.

CI/CD (Jenkins multibranch + SonarQube quality gate + Trivy + Dependency-Track) is documented in [docs/CICD.md](docs/CICD.md).

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

- [ ] **i18n / multi-language UI** — the web UI is Portuguese-only today; extract strings and support English (and others).
- [ ] HLS master playlist (Phase 2): N audio/subtitle tracks + multi-resolution in one VOD stream.
- [ ] Streamer reconciles pieces with already-downloaded files (play without re-downloading).
- [ ] "Promote" button on the local-files page; split the streamer's SQLite stores from the cache dir via `JACKUI_STATE_DIR`.

## Docs

| Doc | What |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Canonical architecture + reading order for new contributors |
| [docs/design-decisions.md](docs/design-decisions.md) | Why the tricky bits are the way they are (and what not to "fix") |
| [docs/TRANSMISSION_RPC.md](docs/TRANSMISSION_RPC.md) | Using JackUI as a Sonarr/Radarr/Prowlarr download client |
| [docs/CICD.md](docs/CICD.md) | Build pipeline, quality gates, deploy |
| [docs/RCLONE.md](docs/RCLONE.md) | Tuning rclone/Google-Drive mounts for streaming (cache, chunking, read-ahead) |

## License

MIT.
