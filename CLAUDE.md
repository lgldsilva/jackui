# JackUI — Claude Code Instructions

**Torrent streaming** server with hardware transcode and a web UI. Started as a visual search front-end for Jackett and grew into a full media server: search → BitTorrent→HTTP stream (no waiting for the full download) → on-demand transcode → playback in the browser (Safari included, via HLS).

## Stack

- **Backend**: Go 1.22 + Gin. Streaming via `anacrolix/torrent` (BitTorrent → HTTP with Range). Transcode via ffmpeg (NVENC/VAAPI/QSV/libx264).
- **Frontend**: React 18 + TypeScript + Vite + TailwindCSS (dark theme default; light via CSS vars in `web/src/index.css`), embedded in the binary (`//go:embed all:dist`). i18n via react-i18next (`web/src/lib/i18n.ts`, `web/src/locales/{pt,en}.json`). Pure-function tests with vitest (`web/vitest.config.mts`).
- **Desktop** (optional): Electron wrapper in `electron/` (`make dev-electron` / `make build-electron`).
- **Infra**: Docker on a home server via a remote context (set `DOCKER_CONTEXT`/`DEPLOY_HOST` in `.env`). Direct deploy (no VPN — the gluetun overlay was cutting too many seeds, so it was dropped from the default). The container runs on a bridge shared with the reverse proxy (e.g. NPM), reachable at `jackui:8989`. Egress via the host's real IP.

## Essential commands

```bash
make test                                  # go test ./internal/... (the whole Go suite)
go test ./internal/streamer/ -run TestName # a single Go test
cd web && npm test                         # vitest run (pure functions; append a path for one file)
make deploy-auto       # ✅ DEFAULT DEPLOY: auto-detects GPU, no VPN
make deploy-auto-vpn   # with the gluetun overlay — only if you really want VPN egress
make dev-frontend      # Vite :5173 proxying to :8989
make dev-backend       # go run ./cmd/server on :8989
make sonar-scan        # local SonarQube analysis with quality gate
```

**The default deploy is `make deploy-auto`.** `-vpn` adds `docker-compose.gluetun.yml` (`network_mode: container:gluetun`) and routes everything through the VPN — it stopped being the default because on many torrents gluetun killed peer connectivity. Without VPN, NPM reaches jackui at `jackui:8989` on the `vpn-gateway_vpn-net` bridge.

## Features

- **Streaming**: torrent → HTTP with Range; plays before the full download. Disk cache with LRU eviction (favourites protected).
- **Swarm health on cards**: `SeedBadge` shows seeders + availability. `GET /api/stream/health/:hash?magnet=` returns the last snapshot (persisted in the metadata cache with a timestamp) immediately and kicks off a background re-probe if stale; an inactive probe is add~6s → count → drop (semaphore of 3, dedupe, pointer guard so it doesn't tear down a concurrent play).
- **On-demand transcode**: HEVC/AV1/x265 → H.264 via GPU. Safari gets **HLS** (`.m3u8` + `.ts` segments) — the only path Safari's `<video>` accepts.
- **Subtitles**: embedded (ffmpeg probe), sidecar `.srt`/`.vtt` inside the torrent, and external (OpenSubtitles). The choice persists per file (localStorage).
- **TMDB**: enriches results/library with poster + metadata (SQLite cache, 30-day TTL). Resolves `imdb_id` (external_ids) and persists it alongside the art. **Discover** (`/discover`): a "Trending" grid (weekly trending, 6h in-memory cache) → clicking seeds the search via `?q=`.
- **Per-torrent thumbnails**: art resolved and persisted by `info_hash` (columns in the metadata cache). Fail-safe chain (`POST /api/stream/art/:hash/resolve`): image embedded in the torrent (poster/cover) → TMDB poster → **web search** (`internal/imagesearch`: DuckDuckGo→Bing keyless, safe-search off — for adult/obscure titles TMDB doesn't cover, only after TMDB fails) → captured frame. `GET /api/stream/art/:hash` serves the art (bytes/302/204); accepts `?name=` to proactively resolve an inactive torrent. Continue Watching triggers a resolve on items without art. Cards prefer the per-infoHash art, falling back to the TMDB-by-title poster.
- **AI title identification** (optional): an OpenAI-compatible chain (`internal/ai`) with fallback + circuit breaker, cleans the raw release name before TMDB. Auto-enables via `GROQ_API_KEY`/`OPENROUTER_API_KEY`/`OLLAMA_BASE_URL`. A tweakable benchmark (Settings → admin) measures accuracy+latency, computes a composite score (accuracy ÷ √latency) and reorders the chain (persisted in `.ai-benchmark.db`).
- **Downloads queue** (`internal/downloads/scheduler.go`): global `max_active` cap + per-user cap, aging bonus for long-waiting items, stall detection (demote → pause after `max_stalls`), and bandwidth windows by `HH:MM` (container `TZ` matters — windows compare wall-clock time). On completion the file is moved out of the piece cache and, if an AI provider is configured, **auto-renamed** to a clean title (`internal/renamer`).
- **Playlists**, **Watchlists** (cron + ntfy push), **Continue Watching** (library with resume position), **background downloads** (qBittorrent/Transmission), **local-files browser** (mounts; `external.mounts` in config OR env `JACKUI_EXTERNAL_MOUNTS=Name:/path[,:usersubpath],...`). Mounts support **`AllowedUsers`** (restrict visibility/access to specific usernames; empty = everyone) and **`UserSubpath`** (each user sees/writes only `mount/{username}/`); writable non-UserSubpath mounts double as **promote** destinations (`BuildPromoteDests`). Remote/rclone mounts get read-ahead (`JACKUI_LOCAL_READAHEAD_MB`) and a whole-file LRU disk cache (`internal/localcache`, `JACKUI_LOCAL_CACHE_GB`). **Incognito mode** (header toggle): header `X-JackUI-Incognito: 1` or `?incognito=1` (for SSE) → middleware sets `c.Set("incognito", true)`; history/library/StreamAdd handlers check `middleware.IsIncognito(c)` and skip the write silently. **Local transcode** (`/api/local/play`): ffprobe decides direct-play vs HLS — MKV/HEVC/AC3/DTS fall back to HLS reusing the torrents' `HLSSessionManager`. Deploy paths (host paths configurable via `.env` → `JACKUI_CONFIG_DIR`/`JACKUI_CACHE_DIR`/`JACKUI_STORAGE_DIR`, see `docker-compose.yml`): **state in `JACKUI_CONFIG_DIR`** (`jackui.db` history + `auth.db`), separate from the **piece cache + streamer DBs** (favorites, metadata-cache, library, playlists, downloads, tmdb, watchlist, ai-benchmark, in `JACKUI_CACHE_DIR`) to ease I/O contention. The shared library (`JACKUI_STORAGE_DIR`) hosts the browsable mounts (ro) and the "promote" target. Reconciling on-disk pieces (play without re-downloading) is DONE: the downloads worker calls `VerifyFile` before `f.Download()` and streaming reconciles on-demand via `verifyFilePieces` (skips already-complete pieces, so a graceful restart costs ~nothing). TODO: "promote" button on the LocalPage; split the 7 streamer DBs out of the cache dir (via `JACKUI_STATE_DIR`).
- **Auth** optional JWT (`JACKUI_AUTH_ENABLED=1`): rotated refresh tokens (reuse detection revokes all sessions), roles `admin`/`user`/`guest` (`GuestRestrict` blocks mutating routes outside an explicit allowlist), `AdminOnly` on sensitive routes, login lockout, **MFA TOTP** + backup codes, **passkeys/WebAuthn**, invite/register with e-mail verification and forgot/reset flows (SMTP via `JACKUI_SMTP_*`, links use `JACKUI_BASE_URL`), and a short-lived **media token** scope for `?token=` URLs.
- **Observability**: `GET /status` is public (version/commit/buildTime via ldflags); `GET /api/metrics` is Prometheus format, gated by admin JWT or static `JACKUI_METRICS_TOKEN`; `JACKUI_LOG_FORMAT=json` switches to structured logs.

## Architecture

```
web/src/            → React (dev :5173, prod embedded); PlayerProvider keeps the player above the router
ui/embed.go         → //go:embed all:dist
electron/           → optional desktop wrapper (main.ts, preload, builder config)
cmd/server/main.go  → Gin wiring: /api/* + SPA fallback + workers (downloads, watchlist)
internal/
  config/   jackett/   downloader/   handlers/      → base (search, download, config)
  streamer/                                         → anacrolix: Add/FileReader/probe/cache/favorites
  transcode/                                        → ffmpeg pipeline + HLS (sessions, seek-restart)
  auth/  history/  library/  playlists/  watchlist/ → SQLite stores (modernc.org/sqlite)
  middleware/                                       → cross-cutting Gin middleware (today: incognito)
  downloads/                                        → background download worker + queue scheduler
  ai/  renamer/                                     → OpenAI-compatible chain (title cleanup, benchmark) + post-download rename
  local/  localcache/  localstream/  diskutil/      → mounts browser, whole-file LRU cache, local play plumbing
  subtitles/  tmdb/  imagesearch/  parser/  dbutil/
  mailer/  metrics/  gluetun/  transmissionrpc/  version/
```

## Critical notes (gotchas that already bit)

- **Playback premise**: **VOD (seekbar) is the default; EVENT/live is the LAST RESORT** (only when duration is unknown or there's no data buffered ahead). With data on disk, always VOD. Do NOT fix a VOD bug by switching to live — make VOD work. Support N audio/subtitle tracks + multi-resolution (Phase 2: HLS master playlist).
- **HLS for Safari**: progressive MP4 over chunked is rejected (`SRC_NOT_SUPPORTED`). Use HLS. No `append_list` (it emits `EXT-X-DISCONTINUITY`, which Safari refuses). `-hls_playlist_type event` (not `vod` — ffmpeg delays the m3u8 until the transcode finishes).
- **Safari stall at `currentTime 0` (ROOT CAUSE)**: ffmpeg's MPEG-TS muxer adds ~1.4s of `initial_offset`, so `seg_00000.ts` starts at 1.4s (a hole [0,1.4] → Safari/iOS hang at t=0). **`-muxdelay 0 -muxpreload 0`** zeroes it at the muxer (`setpts`/`asetpts=PTS-STARTPTS` only zeroes the filter — the muxer re-adds it afterwards). Guard: `TestEncodeSpecZeroesPTSBothModes`.
- **Seekable source required**: ffmpeg reads the torrent through a loopback HTTP server with Range (`serveSource`), not a pipe — otherwise MP4s with `moov` at the end break. Seek+Read are atomic under a mutex (STSC/STCO race).
- **Media token**: `<video>/<track>` can't send headers → they use `?token=`. The middleware only accepts `?token=` on media routes (`/api/stream/*`, `/api/subtitles/download/*`, `/api/local/file`, `/api/local/hls/*`).
- **VOD/seek**: `JACKUI_HLS_VOD_MODE` = `off` | `hlsjs` (VOD only for hls.js clients) | `all` (default — VOD for ALL clients incl. Safari native; the #61 stall root cause was fixed via muxdelay). `ForceVOD` applies to local files and fully-downloaded torrents. When off/unsupported it falls back to the stable EVENT/live path.
- **`dbutil.ParseTime`** to read SQLite timestamps (modernc sometimes emits RFC3339) — don't use `time.Parse` with a single layout.
- **The downloads worker is async**: `internal/downloads/worker.go` does `EnsureActive`+`GotInfo` (up to 90s) in a separate goroutine (`pending`/`retries` maps under a mutex), with up to `maxInitRetries=3` in-memory retries before marking `failed`. A dead magnet no longer freezes the other downloads. `Stop()` cancels in-flight work via context.
- **`UpdateName` after metadata**: the download row is created with the search title; the real name (`t.Name()`) comes later and is persisted via `store.UpdateName` so the boot-time `RegisterDownload` in `NewWorker` protects the right path from LRU.
- **Tracker merge in grouping** (`web/src/lib/group.ts`): when grouping results by `infoHash` (or `name|size` fallback), the `tr=` of ALL magnets in the bucket are folded into the primary's magnet — more peers on Play/Download with no backend change (anacrolix already honours multiple `tr=`).
- **Local transcode reuses the `HLSSessionManager`**: `internal/handlers/local_play.go` runs ffprobe; if container/codec doesn't match the browser → HLS via the SAME manager as the torrents. `/api/local/hls/` is in the `isMediaPath` whitelist (auth/middleware.go) so `<video>` can use `?token=`.

## CI/CD & quality gates

- **Jenkins**: multibranch job `jackui-mb` validates PRs; the `jackui` job builds the main branch via SCM polling (no webhook) and deploys. Pipeline: Go tests+coverage → frontend build → semver from Conventional Commits → **SonarQube gate (blocks)** → SBOM→Dependency-Track → native build+push → Trivy (fails on CRITICAL) → deploy → version tag.
- **Sonar gate**: `new_coverage ≥ 80%` (Go only — `web/**` and `cmd/**` are coverage-excluded), `new_violations = 0`, cognitive complexity ≤ 15 (S3776). ⚠ The PR analysis does NOT flag everything the MAIN analysis flags (S3776 has bitten) — validate locally on the diff before pushing: `golangci-lint run --new-from-rev=gitea/main` (gocognit min 16) + coverage of new functions + `eslint-plugin-sonarjs` cognitive-complexity on changed `.tsx`.
- **A failed gate on main silently skips Build&Push/Deploy** — prod stays on the old version (this already masked a shipped fix).
- **Jenkins deploy only swaps the image**: the prod `docker-compose.yml` is a customized copy on the server — new env vars in the repo compose do NOT reach prod by themselves.
- Never commit to `main` — always a PR (Gitea API; see the `gitea-pr` skill). Worktrees branch from updated `origin/main`.

## Conventions

- Comments only where the WHY isn't obvious. Errors return JSON `{"error": "..."}`.
- SQLite stores: `MaxOpenConns(1)`, migrations with `IF NOT EXISTS` / `hasColumn`.
- Test with `net/http/httptest` — no external deps. `make test` must stay green.
- New UI strings use `t()` with keys added to BOTH `web/src/locales/pt.json` and `en.json`. Backend errors stay in English (other packages match on `err.Error()` substrings).
- Don't fatten god-files (`PlayerModal`, `DownloadsPage`, `SettingsPage`, `AIBenchmarkCard`) — new components/logic go in their own files.
- Deploy: `make deploy-auto` (default, no VPN); `-vpn` is opt-in.
- **RTK summarises `git diff`** by default. When you need the raw output for `git apply` / parsing, use `rtk proxy git diff ...` — otherwise `git apply` spits out "No valid patches in input". Same for `go test`/`curl` when output looks truncated (`rtk proxy go test`, `/usr/bin/curl`).
