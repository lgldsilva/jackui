# Architecture

The canonical "what is this thing and how is it shaped" doc. The README is the
operational summary; this page is for someone reading the code. Long-form rationale
for the tricky decisions lives next door in [design-decisions.md](design-decisions.md).

## Purpose

JackUI turns a torrent into something a browser can play **before the download
finishes**: it exposes pieces as a seekable HTTP source, transcodes incompatible
codecs on demand, and serves the result as progressive MP4 or HLS. Everything (API +
the React UI) ships as a single Go binary.

## Data flow — the streaming path

```
search ─► Jackett ─► results (grouped by infoHash) ─► user hits Play
                                                          │
                                       streamer.Add(magnet)│
                                                          ▼
                                   anacrolix/torrent client ──► peers
                                                          │ pieces
                                                          ▼
                                   disk cache (LRU, favourites pinned)
                                                          │
                  ┌───────────────────────────────────────┤
                  │ probe (ffprobe): codec/container OK for browser?
        yes ──────┤                                       │────── no
                  ▼                                        ▼
   GET /api/stream/:hash  (HTTP + Range, direct play)   transcode/ (ffmpeg)
                  │                                        │  NVENC/VAAPI/QSV/x264
                  ▼                                        ▼
            <video> src                          Safari? ─► HLS (.m3u8 + .ts)
                                                 else    ─► progressive MP4
```

The torrent is **not** piped into ffmpeg. The streamer runs a loopback HTTP server
with Range support (`serveSource`) and ffmpeg reads from it like any seekable file —
otherwise MP4s with `moov` at the end break. Seek+Read are atomic under a mutex.

## Component map

```
cmd/server/main.go   Gin wiring: builds appDeps, registers /api/* routes, mounts the
                     SPA fallback, and starts background workers (downloads, watchlist).
ui/embed.go          //go:embed all:dist — the Vite build is compiled into the binary.
web/src/             React 18 + TS + Vite + Tailwind. PlayerProvider sits ABOVE the
                     router so playback survives navigation. The iOS audio path
                     (dedicated <audio>, direct-play) is written up in
                     IOS_AUDIO.md; the frontend→backend N+1 backlog in
                     PERFORMANCE.md.
```

> Frontend deep-dives: **[IOS_AUDIO.md](IOS_AUDIO.md)** (the iOS/Safari audio
> path + the silent-switch lesson) and **[PERFORMANCE.md](PERFORMANCE.md)** (the
> N+1 batch backlog — list-in-client = one batch call).

| Package | Responsibility |
|---|---|
| `internal/streamer` | The core. anacrolix client; `Add`, `FileReader`, ffprobe, the disk cache + LRU eviction, favourites, swarm-health probes, the loopback `serveSource`, the SSRF guard for `.torrent` fetches. |
| `internal/transcode` | ffmpeg pipeline + the `HLSSessionManager` (HLS sessions, seek-restart). Encoder selection (NVENC/VAAPI/QSV/libx264). |
| `internal/handlers` | HTTP handlers: search (+SSE), config, stream, subtitles, local-file play, artwork resolve, `classify`, diag. |
| `internal/jackett` | Jackett search client. Strips the API key from download links and re-injects it server-side. |
| `internal/config` | `config.yaml` load/save + env overrides; stream perf knobs. |
| `internal/downloads` | Background download queue + async worker (`EnsureActive`+`GotInfo` in a goroutine). The **aggregate-by-torrent model** lives in `aggregate.go` (see below). |
| `internal/downloader` | qBittorrent / Transmission client adapters. |
| `internal/transmissionrpc` | Transmission-RPC compatibility layer so `*arr` apps treat JackUI as a Transmission daemon (opt-in). |
| `internal/tmdb` | TMDB enrichment (posters, metadata, trending) with SQLite cache. |
| `internal/imagesearch` | Keyless web image search (DuckDuckGo→Bing) — last resort for artwork TMDB can't cover. |
| `internal/ai` | OpenAI-compatible chain for release-name cleanup, with fallback + circuit breaker + a benchmark. |
| `internal/subtitles` | Embedded probe, sidecar, and OpenSubtitles. |
| `internal/local` | Browsable read-only mounts (the local-files page). |
| `internal/auth` | JWT (rotated refresh tokens), users, sessions, `AdminOnly`. |
| `internal/history` / `library` / `playlists` / `watchlist` | SQLite stores. |
| `internal/middleware` | Cross-cutting Gin middleware (incognito flag, media-token `?token=` auth). |
| `internal/parser` / `renamer` / `dbutil` | Release-name parsing, library renaming, SQLite time helpers. |
| `electron/` | Optional desktop wrapper (Electron main + preload) bundling the Go server. |

## The downloads subsystem (aggregate-by-torrent)

The store persists **one row per `(user_id, info_hash, file_index)`** — a single-file
download, a selected file inside a multi-file pack, or the whole-torrent sentinel.
anacrolix, however, treats the torrent as **one unit**. Driving the queue per row (one
`EnsureActive` / `VerifyFile` / progress-sample / stall-cycle **per file**) was O(files)
in CPU/RAM and OOM'd on big season packs.

`internal/downloads/aggregate.go` resolves the rows sharing a `(user_id, info_hash)` into
a runtime **`Group`** (`GroupRows`). The scheduler and worker act on the group, never the
row:

- the **scheduler** counts **one slot per torrent** — `MaxActive`/`PerUserMax` are
  torrents, not files (`scheduler.go`);
- the **worker** activates the torrent **once** per group, marks only the selected files
  wanted via anacrolix **file priorities** (deselected/removed → `PiecePriorityNone`),
  samples progress **once**, and runs **one** completion-move + **one** AI-rename + **one**
  stall cycle for the whole torrent.

No new table and no migration: a single-file download is a group of one, and the
**whole-torrent sentinel `FileIndexWholeTorrent = -2`** (`store.go`) is an aggregate row
covering all N files (`DownloadAll()`). **At enqueue**, picking ALL files
(`isWholeTorrentSelection`) creates a single `-2` row — a 778-file pack = 1 row — while a
real subset goes per-file via `POST /api/downloads/batch`. The UI groups by `infoHash` →
**one card per torrent** (counts are per-torrent via `countTorrents`).

**`GET /api/downloads` must stay cheap.** Many rows are selected files of the same
torrent, so `enrichETAList` (`internal/handlers/downloads.go`) dedupes by `info_hash` and
calls `Streamer.LiveStats(hash)` — **O(1)** per torrent (cached rate sample +
`Stats().ConnectedSeeders`, no file walk). The old per-row `s.Get`→`buildInfo` was
O(rows×files) and made the endpoint take 2–17 s on a big pack.

## Storage architecture

Two roots, deliberately separated to reduce I/O contention:

| Root (env) | Holds | Why separate |
|---|---|---|
| **`JACKUI_CONFIG_DIR`** → `/data` | `jackui.db` (history) + `auth.db` | Irreplaceable user state; low write volume. |
| **`JACKUI_CACHE_DIR`** → `/data/streams` | Piece cache **+** 7 streamer SQLite stores (favorites, metadata-cache, library, playlists, downloads, tmdb, watchlist, ai-benchmark) | High write volume (pieces); cache is reconstructable. |
| **`JACKUI_STORAGE_DIR`** → `/mnt/storage` | Browsable mounts (read-only) + the "promote" destination | Shared media library. |

> [!NOTE]
> The 7 streamer stores currently live in the cache dir. Splitting them out via a
> dedicated `JACKUI_STATE_DIR` is on the roadmap — losing the cache should never lose
> favourites/library/playlists.

SQLite stores all use `MaxOpenConns(1)` and `IF NOT EXISTS` / `hasColumn` migrations.
Read timestamps with `dbutil.ParseTime` (modernc sometimes emits RFC3339) — never a
single `time.Parse` layout.

## Request & auth model

- `/api/*` is the API; everything else falls back to the embedded SPA.
- Auth (when enabled) is JWT via header. But `<video>`/`<track>` can't send headers,
  so **media routes accept a scoped `?token=`** — the middleware only honours
  `?token=` on `/api/stream/*`, `/api/subtitles/download/*`, `/api/local/file`,
  `/api/local/hls/*`.
- `AdminOnly` guards config writes, user admin, benchmarks, all-users download views.
- The Transmission-RPC endpoint (`/transmission/rpc`) lives **outside** `/api` and is
  opt-in; it does its own session-id/Basic-Auth handshake.

## Cross-cutting invariants

1. **VOD is the default; live/EVENT is the last resort.** Seekbar playback (VOD) is
   the goal. EVENT/live is only for unknown-duration / no-data-ahead cases. Never
   "fix" a VOD bug by switching to live — fix the VOD path.
2. **Safari means HLS.** Progressive MP4 over chunked transfer is rejected
   (`SRC_NOT_SUPPORTED`). No `append_list` (Safari rejects the `EXT-X-DISCONTINUITY`).
   `-hls_playlist_type event`, and `-muxdelay 0 -muxpreload 0` to kill the MPEG-TS
   `initial_offset` that strands Safari at `t=0`. See design-decisions.
3. **The ffmpeg source must be seekable** — served over loopback HTTP with Range, not
   a pipe. Seek+Read atomic under mutex.
4. **A dead magnet must not freeze other downloads, and one torrent is one unit.** The
   worker is async with bounded retries (init runs in a goroutine, so a stuck `GotInfo`
   doesn't block the queue). The scheduler/worker operate **per torrent group**
   (`internal/downloads/aggregate.go`), never per file — see *The downloads subsystem*.
5. **Incognito skips writes silently** — handlers consult `middleware.IsIncognito(c)`
   and no-op history/library writes.

## Configuration

See the README's [Configuration](../README.md#configuration) table. Config is
`config.yaml` + env overrides (env wins); `internal/config` does the merge and the
frontend shows which keys are environment-managed.

## Reading order (for a new contributor)

1. `README.md` — what it is, how to run it.
2. This file — the shape.
3. `cmd/server/main.go` — how everything is wired together (`appDeps`, route groups).
4. `internal/streamer/streamer.go` — the heart: Add, cache, the loopback source.
5. `internal/transcode/` — the ffmpeg/HLS pipeline (read [design-decisions.md](design-decisions.md) first; the gotchas there explain the flags).
6. `internal/downloads/aggregate.go` — the queue's mental model (torrent = one unit, rows aggregated at runtime).
7. `internal/handlers/` — pick the handler for the feature you're touching.
8. `web/src/components/PlayerProvider.tsx` + `PlayerModal.tsx` — the playback UI.
