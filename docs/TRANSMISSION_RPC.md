# Transmission RPC — `*arr` compatibility

## Overview

JackUI can expose a `/transmission/rpc` endpoint compatible with the Transmission RPC
protocol, so **Sonarr**, **Radarr**, **Prowlarr** (and any Transmission-RPC client) can
treat JackUI as if it were a Transmission daemon.

This removes the need to run a separate Transmission — JackUI manages the downloads
directly through its internal worker (anacrolix/torrent), sharing the same storage.

## Enabling the endpoint (opt-in — default OFF)

> [!WARNING]
> The RPC layer is **not exposed by default** — it's a sensitive RPC surface, so it
> ships disabled. Until enabled, `POST /transmission/rpc` returns **404** and the `*arr`
> apps can't connect.

To turn it on, set the env var on JackUI and redeploy:

```yaml
# docker-compose.yml (jackui service)
environment:
  - JACKUI_TRANSMISSION_RPC_ENABLED=1   # accepts "1" or "true"
```

Then `make deploy-auto`. Confirm it's live:

```bash
# 409 = enabled (handshake); 404 = still disabled (flag not applied / no redeploy)
curl -s -o /dev/null -w '%{http_code}\n' -X POST http://jackui:8989/transmission/rpc \
  -d '{"method":"session-get"}'
```

The endpoint only mounts when the flag is on **and** the downloads/streamer stores
exist (always the case in a normal deploy).

## Architecture

```
Sonarr/Radarr/Prowlarr
        │
        ▼  POST /transmission/rpc
┌───────────────────────────────┐
│  internal/transmissionrpc/    │ ← Gin handler (outside /api, no JWT)
│  ┌─────────────────────────┐  │
│  │  dispatch():            │  │
│  │  session-get            │  │
│  │  session-stats          │  │
│  │  torrent-add            │  │
│  │  torrent-get            │  │
│  │  torrent-set            │  │
│  │  torrent-remove         │  │
│  │  port-test              │  │
│  └─────────────────────────┘  │
└──────────────┬────────────────┘
               │
    ┌──────────┴──────────┐
    ▼                     ▼
internal/downloads/     internal/streamer/
(SQLite store)         (anacrolix/torrent)
```

## Implemented RPC methods

### session-get
Returns session configuration. Used by the `*arr` apps to test connectivity.

**Fields:** 61 fields, same names as Transmission 4.1.1 (rpc-version 19). Includes
`version`, `rpc-version`, `rpc-version-semver`, `download-dir`,
`download-dir-free-space`, `seedRatioLimit`, `units`, `speed-limit-*`, `peer-limit-*`,
`queue-*`, `script-torrent-*`, etc.

### session-stats
Returns session statistics. Used by Prowlarr to test connectivity.

**Fields:** `activeTorrentCount`, `downloadSpeed`, `uploadSpeed`, `pausedTorrentCount`,
`torrentCount`, `cumulative-stats`, `current-stats`.

### torrent-add
Adds a torrent to download. Accepts:

| Type | Example |
|------|---------|
| Magnet URI | `magnet:?xt=urn:btih:<hash>&dn=...` |
| Bare infohash | `abcdef0123456789abcdef0123456789abcdef01` |
| .torrent URL | `https://example.com/file.torrent` |

**Params:** `filename` (required), `download-dir` (→ mapped to category), `paused`
(start paused).

**Response:** `{ "torrent-added": { "id", "hashString", "name" } }`

### torrent-get
Lists torrents with selectable fields. Used by the `*arr` apps to poll progress.

**50 supported fields:** `id`, `hashString`, `name`, `status` (TR_STATUS_*),
`totalSize`, `percentDone`, `rateDownload`, `rateUpload`, `downloadDir`, `addedDate`,
`doneDate`, `error`, `errorString`, `leftUntilDone`, `haveValid`, `peersConnected`,
`eta`, `isFinished`, `isStalled`, `labels`, `trackers`, `uploadRatio`, `queuePosition`,
`bandwidthPriority`, `recheckProgress`, `secondsDownloading`, `secondsSeeding`,
`files`, `fileStats`, etc.

**Filter:** `ids` (optional) — array of IDs or a single ID.

### torrent-set
Modifies properties of existing torrents.

**Commands:** `paused` (pause/resume), `labels` (→ category), `seedRatioLimit`,
`seedRatioMode`, `bandwidthPriority`.

### torrent-remove
Removes torrents from the download queue.

**Params:** `ids` (required), `delete-local-data` (optional, not implemented — on-disk
data stays until the streamer's LRU evicts it).

### Other methods

| Method | Behaviour |
|--------|-----------|
| `port-test` | Returns `{ "port-is-open": true }` |
| `blocklist-update` | No-op, returns `{ "blocklist-size": 0 }` |
| `free-space` | Returns free space in `download-dir` |
| `torrent-set-location` | No-op (accepted without error) |
| `torrent-rename-path` | No-op (accepted without error) |

## Authentication

The endpoint follows the Transmission handshake:

1. Request without `X-Transmission-Session-Id` → **HTTP 409** with the
   `X-Transmission-Session-Id` and `X-Transmission-Rpc-Version` headers.
2. Client retries with the header → request processed.

When JackUI auth is **enabled** (`JACKUI_AUTH_ENABLED=1`):
- The client must send **Basic Auth** (a JackUI username/password).
- The session-id is only issued after valid credentials.
- Downloads are associated with the authenticated user's **userID**.

When auth is **disabled**:
- Any request is accepted (there's no auth store to validate against).
- All downloads are associated with **userID 0** (system).
- To associate downloads with a specific user, **enable auth**
  (`JACKUI_AUTH_ENABLED=1`) — the RPC endpoint uses the same auth store.

## Status mapping

| JackUI status | Transmission status | Code |
|---------------|--------------------|------|
| `queued` | TR_STATUS_DOWNLOAD_WAIT | 3 |
| `downloading` | TR_STATUS_DOWNLOAD | 4 |
| `completed` | TR_STATUS_SEED | 6 |
| `paused` | TR_STATUS_STOPPED | 0 |
| `failed` | TR_STATUS_STOPPED | 0 |

## Internal worker integration

`torrent-add` creates a row in `internal/downloads/` with `FileIndex = -2`
(`FileIndexWholeTorrent`) so an *arr client gets **Transmission semantics**: the
ENTIRE release is downloaded, not just one file. This matters for season packs and
any multi-file release — Sonarr/Radarr import every file, so fetching only the
"best file" would import broken. When the worker (`worker.go`) processes it:

1. Calls `streamer.EnsureActive()` to add it to the anacrolix swarm.
2. Waits for metadata (GotInfo).
3. Calls `t.DownloadAll()` — marks every piece of every file as wanted.
4. Tracks aggregate progress over the whole torrent.
5. On completion, `moveCompletedTorrent()` moves the full directory tree to the
   download dir (preserving the torrent's internal structure).

> The JackUI **UI** download path is unchanged: a play/download started inside
> JackUI still uses `FileIndexAuto` (-1) → `pickBestFile()` (single best media
> file) for streaming, or an explicit `FileIndex` when the user picks one file.
> Only the Transmission-RPC entry point downloads the whole torrent.

## Configuring Sonarr/Radarr

Settings → Download Clients → **+** → **Transmission**:

| Field | Value |
|-------|-------|
| **Type** | Transmission |
| **Host** | JackUI host/IP (e.g. `jackui` on the NPM bridge, or the server IP) |
| **Port** | `8989` (JackUI's port — **not** a separate Transmission port) |
| **Url Base** | `/transmission/` — the `*arr` POSTs to `<UrlBase>rpc`, yielding `/transmission/rpc`. ⚠️ Do **not** enter `/transmission/rpc` here (it becomes `/transmission/rpc/rpc` → 404). |
| **Use SSL** | only if you reach JackUI over HTTPS (e.g. via NPM with TLS) |
| **Username / Password** | a JackUI user's credentials **if** `JACKUI_AUTH_ENABLED=1`; blank if auth is off |
| **Category** | e.g. `tv-sonarr` / `radarr` (becomes the `download-dir` → subfolder/category) |

Click **Test** — the `*arr` performs the 409 → session-id handshake and a
`session-get`. Green = OK. "Unable to connect" is almost always the flag being off
(404) or a wrong Url Base.

## Configuring Prowlarr

Prowlarr uses `session-stats`/`session-get` only to test connectivity — same fields as
Sonarr/Radarr (Settings → Apps, or as a test download client). The actual download flow
happens in Sonarr/Radarr, which receive the release from Prowlarr.

## Files created/modified

| File | Kind | Description |
|------|------|-------------|
| `internal/transmissionrpc/handler.go` | New | Gin handler + RPC methods |
| `internal/transmissionrpc/*_test.go` | New | Unit tests |
| `cmd/server/main.go` | Modified | Init + route registration (behind the opt-in flag) |
| `internal/downloads/worker.go` | Modified | `FileIndex=-1` support + `pickBestFile()` |
| `internal/downloads/store.go` | Modified | `SetFileIndex()` |
| `docs/TRANSMISSION_RPC.md` | New | This document |

## Testing

```bash
# transmissionrpc package tests
go test ./internal/transmissionrpc/... -v

# worker tests (include pickBestFile)
go test ./internal/downloads/... -v

# curl against a running server
curl -s -X POST http://localhost:8989/transmission/rpc \
  -H "Content-Type: application/json" \
  -d '{"method":"session-get"}'

# full handshake (409 → session-id → request)
SESSION_ID=$(curl -s -D - -X POST http://localhost:8989/transmission/rpc \
  -d '{"method":"session-get"}' 2>&1 | grep X-Transmission-Session-Id \
  | awk '{print $2}' | tr -d '\r')
curl -s -X POST http://localhost:8989/transmission/rpc \
  -H "Content-Type: application/json" \
  -H "X-Transmission-Session-Id: $SESSION_ID" \
  -d '{"method":"session-get"}'
```

## Limitations / TODO

- [ ] `uploadRatio` / `uploadedEver` return 0 (we don't track per-torrent upload).
- [ ] `torrentFile` returns empty (we don't store `.torrent` files on disk).
- [ ] `torrent-set` doesn't persist per-download `seedRatioLimit` (accepted without error).
- [ ] `torrent-remove` with `delete-local-data=true` doesn't delete on-disk data.
- [ ] `files` `begin_piece`/`end_piece` are generic (don't reflect the real torrent layout).
- [ ] Frequent `*arr` polling (~every 10s) over `ListAll()` + `streamer.Get()` can be
      costly with many torrents — consider caching the response.
