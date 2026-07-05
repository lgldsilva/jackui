# Design decisions

Why the tricky parts are the way they are — and what *not* to "fix". These are the
decisions that cost real debugging; each one is backed by the concrete symptom that
forced it. If you're about to change one of these, read its **Why** first.

Format per entry: **Decision** → Why → How → Trade-offs.

---

## Playback: VOD is the default, live is the last resort

**Decision.** Serve seekable VOD (a real seekbar) by default. Only fall back to
EVENT/live when the duration is unknown or there's no data buffered ahead.

**Why.** A live stream has no seekbar and can't jump around — terrible for watching a
movie you're streaming off a torrent. The temptation, when VOD misbehaves, is to
"just switch to live" because live hides the symptom. That trades the whole UX for a
band-aid.

**How.** With pieces on disk, always VOD. The unstable VOD/seek path is behind the
`hlsVODEnabled` flag in `internal/transcode/hls.go`; when off it falls back to the
stable EVENT/live path.

**Trade-offs.** VOD over a partially-downloaded torrent is harder to get right
(seek into not-yet-downloaded ranges). We accept that complexity instead of
degrading playback.

> ⚠️ **Never fix a VOD bug by switching to live.** Make the VOD path work.

---

## Safari gets HLS, never progressive MP4

**Decision.** Safari/iOS `<video>` is served HLS (`.m3u8` + `.ts`); other browsers get
direct-play or progressive transcode.

**Why.** Safari rejects progressive MP4 delivered over chunked transfer with
`SRC_NOT_SUPPORTED`. HLS is the only path its `<video>` accepts for our use case.

**How.** `-hls_playlist_type event` (**not** `vod` — with `vod` ffmpeg delays the
`.m3u8` until the transcode finishes). No `append_list` (it emits
`EXT-X-DISCONTINUITY`, which Safari refuses).

**Trade-offs.** Two delivery paths to maintain (MP4 + HLS). Worth it — there is no
single path that satisfies both Safari and the rest.

---

## `-muxdelay 0 -muxpreload 0` — the Safari `t=0` stall

**Decision.** Pass `-muxdelay 0 -muxpreload 0` to the HLS muxer.

**Why (the receipt).** Safari/iOS would stall at `currentTime 0`. Root cause: the
ffmpeg MPEG-TS muxer adds ~1.4s of `initial_offset`, so `seg_00000.ts` starts at 1.4s,
leaving a hole `[0, 1.4]` that Safari hangs on.

**How.** `-muxdelay 0 -muxpreload 0` zeroes it at the muxer. Note `setpts`/
`asetpts=PTS-STARTPTS` only zeroes the *filter* — the muxer re-adds the offset
afterwards, so the filter alone is not enough. Guarded by
`TestEncodeSpecZeroesPTSBothModes`.

**Trade-offs.** None meaningful; it's a correctness fix.

---

## Playback: the iOS-audio path (dedicated `<audio>`, direct-play, native gesture)

**Decision.** iOS audio plays through a **dedicated native `<audio controls>`**
element (`SimpleAudioPlayer`), fed a **direct** bytes-with-Range URL
(`useAudioDirectUrl`, never HLS), with `preload='none'` on WebKit, an internal
`blessedRef` for auto-advance, and a 6h media-token. **No** Web Audio (EQ/gapless
off on WebKit), no custom start overlay, no `v.load()`.

**Why (the receipt).** A long saga where the headline symptom — "no sound on
iPhone" — turned out to be the **hardware silent/ringer switch** muting
inline-`<video>` audio, not our code. The definitive fix was to move the audio
path OFF `<video>` onto a bare `<audio>`, which plays on the **media channel**
(the switch does not mute it). Separately: `preload='auto'` froze the element at
`readyState 2`; a media-token refresh reloaded the element (AbortError); and
EQ/gapless use `createMediaElementSource`, which stalls the element on a
suspended-AudioContext WebKit.

**How / full write-up.** See **[IOS_AUDIO.md](IOS_AUDIO.md)** — the canonical doc
with the gotcha table (G1-G6), the current architecture, what's disabled and why,
the re-enable table, and a "Don't fix this" checklist.

**Trade-offs.** No auto-advance gapless/crossfade or EQ on iOS (the `<audio>`
path is deliberately Web-Audio-free); those stay a future opt-in.

---

## ffmpeg reads a seekable HTTP source, not a pipe

**Decision.** ffmpeg reads the torrent through a loopback HTTP server with Range
support (`serveSource`), not via a pipe.

**Why.** MP4 files with `moov` at the end require seeking to the tail before playback.
A pipe isn't seekable, so those files break. Seek+Read also race on STSC/STCO if not
serialised.

**How.** `serveSource` exposes the torrent file over `127.0.0.1` with Range; Seek and
Read are atomic under a mutex.

**Trade-offs.** An extra loopback hop, but it's the only way to give ffmpeg a seekable
source over an incomplete torrent.

---

## Media auth via `?token=`, not just headers

**Decision.** Media routes accept a scoped `?token=` query param in addition to the
JWT header.

**Why.** `<video>` and `<track>` elements can't attach Authorization headers.

**How.** The middleware only honours `?token=` on media paths: `/api/stream/*`,
`/api/subtitles/download/*`, `/api/local/file`, `/api/local/hls/*`. Everything else is
header-only.

**Trade-offs.** A token in a URL is slightly more exposable (logs/history); scoping it
to media paths bounds the blast radius.

---

## SQLite (modernc, pure-Go), one writer

**Decision.** All state is SQLite via `modernc.org/sqlite` (cgo-free), with
`MaxOpenConns(1)`.

**Why.** No external DB to run; the binary stays self-contained and cross-compiles
cleanly (no cgo). `MaxOpenConns(1)` sidesteps SQLite's writer-concurrency footguns.

**How.** Migrations are idempotent (`IF NOT EXISTS` / `hasColumn`). Read timestamps
with `dbutil.ParseTime` — modernc sometimes emits RFC3339, so a single `time.Parse`
layout silently fails.

**Trade-offs.** Single-writer throughput, mitigated by splitting state across separate
DB files (see ARCHITECTURE → Storage).

---

## The downloads worker is async and per-item

**Decision.** Background downloads initialise (`EnsureActive`+`GotInfo`, up to 90s) in
a separate goroutine, per item, with bounded in-memory retries (`maxInitRetries=3`).

**Why (the receipt).** A single dead magnet used to block the whole queue while it
waited for metadata. One bad magnet must not freeze the others.

**How.** `internal/downloads/worker.go` tracks `pending`/`retries` under a mutex;
`Stop()` cancels in-flight work via context. The row is created with the search title,
then `store.UpdateName` persists the real `t.Name()` once metadata arrives, so the
boot-time `RegisterDownload` protects the right path from LRU eviction.

**Trade-offs.** More moving parts than a serial queue; necessary for resilience.

---

## Tracker merge when grouping results

**Decision.** When grouping results by `infoHash` (or `name|size` fallback), fold the
`tr=` trackers from *all* magnets in the bucket into the primary magnet.

**Why.** More trackers → more peers on Play/Download, for free.

**How.** Done in `web/src/lib/group.ts`; no backend change needed (anacrolix already
honours multiple `tr=`).

**Trade-offs.** None — anacrolix dedupes trackers.

---

## VPN overlay is opt-in, not default

**Decision.** `make deploy-auto` deploys **without** a VPN. The gluetun overlay is the
`-vpn` opt-in.

**Why (the receipt).** Routing everything through gluetun
(`network_mode: container:gluetun`) cut peer connectivity on many torrents — fewer
seeds, stalled streams.

**How.** The `-vpn` targets layer in `docker-compose.gluetun.yml`. Default reaches
JackUI at `jackui:8989` on the shared bridge, exiting via the host's real IP.

**Trade-offs.** No VPN privacy by default — a deliberate choice favouring
connectivity. Opt in when you actually want VPN egress.

---

## Local transcode reuses the torrent HLS manager

**Decision.** Playing a local file (`/api/local/play`) reuses the **same**
`HLSSessionManager` the torrents use.

**Why.** MKV/HEVC/AC3/DTS local files need the exact same transcode path as torrents;
duplicating it would mean two pipelines to keep in sync.

**How.** `internal/handlers/local_play.go` ffprobes the file; if the container/codec
doesn't match the browser, it goes HLS via the shared manager. `/api/local/hls/` is in
the `isMediaPath` whitelist so `<video>` can use `?token=`.

**Trade-offs.** None — it's deliberate reuse.

---

## CI builds natively on the target arch

**Decision.** Build the production image **natively** on the target architecture, not
by emulating amd64 on the arm64 CI host.

**Why (the receipt).** The CI host was arm64 at the time; emulating amd64 builds
under qemu OOM-killed the build. Separately, qemu binfmt is **not auto-registered after
a reboot**, which breaks any `docker run --platform linux/amd64` stage (e.g. the Sonar
scanner) until it's re-registered.

**How.** Native build on the target. `qemu-user-static` is installed on the CI host so
binfmt survives reboots; the host also has swap + `oom_score_adj` protection on the CI
services so a runaway can't freeze it.

**Trade-offs.** Cross-arch images need the right host; in exchange the build is fast
and doesn't thrash.

---

## A torrent is ONE unit, not N downloads

**Decision.** The scheduler and worker reason about a *torrent*, not a file. Rows stay
per-(user, info_hash, file_index) for the file SELECTION and per-file progress, but are
folded at runtime into a per-(user, info_hash) **group** that is the unit of work: one
slot, one `EnsureActive`, one verify, one progress sample, one completion move +
AI-rename, one stall cycle.

**Why (the receipt).** Driving per row was O(N) in CPU/RAM: a ~389-file pack ran 389
`EnsureActive`/`VerifyFile`/stall cycles and **OOM'd**; one `MaxActive` slot per file
starved every other download.

**How.** `internal/downloads/aggregate.go` derives the group (`GroupRows`/`grpKey`);
the scheduler counts ONE slot per group; `reconcileGroup` marks only the SELECTED files
wanted via anacrolix **file priorities** (rest cancelled). A whole-torrent group (the
`FileIndexWholeTorrent = -2` sentinel) uses `t.DownloadAll`. The UI groups rows into ONE
card per torrent. **At enqueue**, picking ALL files (`isWholeTorrentSelection`) creates a
single `-2` row (a 778-file pack = 1 row); only a real subset goes per-file via
`downloadBatchCreate`. (#347/#348/#356)

---

## `GET /api/downloads` is O(unique torrents), never O(files)

**Decision.** Enriching the list with live rate/seeders/ETA does O(1) work per torrent
and dedupes by info_hash — never walks `t.Files()` per row.

**Why (the receipt).** `s.Get`→`buildInfo` walks every file under the client lock; per
row that was O(rows×files) and `GET /api/downloads` took **2–17s** on a 778-file pack —
the queue page hung and looked empty.

**How.** `enrichETAList` (`internal/handlers/downloads.go`) dedupes by info_hash and calls
`Streamer.LiveStats(hash)` — **O(1)**: the cached rate sample + `Stats().ConnectedSeeders`,
NO file walk (vs `Get`/`buildInfo`). The counter likewise counts torrents (`countTorrents`),
not rows. (#355)

---

## Graceful shutdown is bounded by a hard watchdog (`os.Exit` backstop)

**Decision.** SIGTERM and the gluetun forwarded-port `restart` signal run the SAME
graceful shutdown (stop HTTP, drain in-flight transfers, run cleanups in reverse order)
— but `runCleanup` is wrapped in a **20s watchdog that `os.Exit(0)`s** if cleanup blocks.

**Why (the receipt).** We tried "graceful, no `os.Exit`" first — and it bit us: when the
VPN network dropped, the anacrolix/DHT teardown **blocked forever** ("error announcing to
DHT: nothing resolved"), the HTTP server was already down (eternal **502**), the process
never exited, and `restart: unless-stopped` never recreated it. A real outage. Graceful
is right, but it MUST be bounded.

**How.** `cmd/server/main.go`: `runCleanup` runs the cleanups in a goroutine; on
`cleanupHardDeadline` (20s) it logs and `os.Exit(0)` so Docker recycles into a fresh
instance. The next boot reconciles (`RescueStuckMoving`, `resumeSeeding`, piece verify),
so forcing exit past a stuck cleanup is safe.

**Trade-offs.** A wedged cleanup loses up to ~that window of clean teardown, but the
alternative (observed) is an unkillable 502. Normal shutdowns finish well inside 20s. (#355)

---

## Seed completed torrents from the cached metainfo, in place

**Decision.** Re-activate a COMPLETED download for seeding from the metainfo we already
cached by info_hash — never re-fetch a `.torrent` over HTTP.

**Why (the receipt).** The old path re-fetched the `.torrent` by URL and died with
`auto-seed failed: .torrent URL 404` (Jackett `/dl/...` links expire); and the file lives
in bulk (moved out of the cache on completion), so re-downloading would be wrong.

**How.** The seed paths use `Download.SeedSource()` → a bare info_hash magnet;
`streamer.resolveMagnet` is **cache-first** (`loadCachedMetainfo` → `addCachedMetainfo`
with `relocatedStorage` rooted at the file's real bulk location) so anacrolix verifies +
SEEDS in place. (#351)

---

## Local files: local disk serves directly; only REMOTE mounts get read-ahead/cache

**Decision.** A local-disk file is served via `http.ServeFile` (kernel sendfile + page
cache = instant Range seeks). The whole-file LRU cache and 16 MB read-ahead are reserved
for slow/remote mounts.

**Why (the receipt).** The synchronous 16 MB `fillAt` on every seek added ~1s of
first-byte latency on local disk for zero benefit; on an rclone/FUSE mount it's the
difference between seekable playback and intermittent EIO.

**How.** `serveLocalFileMetered` (`internal/handlers/local.go`) checks `isRemoteFS(abs)`:
local (and a remote file already pulled into the localcache) → `http.ServeFile`;
remote/FUSE → the metered `localstream.Session` with read-ahead. (#350)

---

## Cap hardware-decode sessions; spill the rest to software decode

**Decision.** Bound how many concurrent ffmpeg sessions hold a CUDA decoder; sessions over
the cap (or after a CUDA-OOM) run SOFTWARE decode feeding the same NVENC encoder.

**Why (the receipt).** Each `-hwaccel cuda` decode allocates a CUVID decoder in VRAM; with
~7 concurrent HLS sessions the GPU hit `CUDA_ERROR_OUT_OF_MEMORY` and the next play/seek
failed in the player.

**How.** `internal/transcode/gpusem.go`: `gpuSem` grants HW-decode slots up to the cap
(default 3; 0 = unlimited); `hls.go` detects CUDA-OOM in ffmpeg stderr and relaunches in
software decode. The NVENC *encode* still runs on the GPU. (#349)

---

## What we are explicitly NOT doing (yet)

- **No public-internet hardening.** JackUI assumes a reverse proxy + trusted network.
- **No multi-writer SQLite / external DB.** Single-writer is fine at home scale.
- **No mermaid diagrams.** ASCII box-and-arrow only — renders everywhere, diffs cleanly.
- *(Done since: react-i18next ships pt/en; and the streamer now reconciles on-disk pieces
  — the worker `VerifyFile`s before `f.Download()` and streaming reconciles via
  `verifyFilePieces`, so a completed download seeds/plays in place without re-downloading.)*

## Mistakes-to-avoid checklist

- [ ] Don't switch VOD → live to dodge a seek bug. Fix VOD.
- [ ] Don't serve Safari progressive MP4. Don't use `append_list` or `-hls_playlist_type vod`.
- [ ] Don't drop `-muxdelay 0 -muxpreload 0` thinking the `setpts` filter covers it.
- [ ] Don't feed ffmpeg a pipe — it must be a seekable HTTP source.
- [ ] Don't read SQLite timestamps with a single `time.Parse` layout — use `dbutil.ParseTime`.
- [ ] Don't make the downloads queue serial — one dead magnet would block everything.
- [ ] Don't run repeated heavy `docker run --platform linux/amd64` scans on the CI host
      via `timeout ssh` — the remote container is orphaned on timeout and can OOM the host.
- [ ] Don't point a new env var at an empty cache dir on deploy — it holds the streamer
      DBs (favourites/library/playlists), not just pieces.
- [ ] Don't drive a multi-file torrent per file — one slot/verify/stall cycle PER TORRENT
      (group by info_hash). Per-file driving OOM'd on season packs.
- [ ] Don't `s.Get`/`buildInfo` per download row to enrich a list — use `LiveStats` (O(1))
      and dedupe by info_hash (`buildInfo` is O(files)).
- [ ] Don't let cleanup block shutdown forever — `runCleanup` has a 20s watchdog that
      `os.Exit`s; a stuck anacrolix/DHT teardown once hung the process at an eternal 502.
- [ ] Don't re-fetch a `.torrent` by URL to re-seed — resolve the cached metainfo by
      info_hash (`SeedSource` → bare magnet) and seed in place from bulk storage.
- [ ] Don't read-ahead / pre-cache a local-disk file — that's only for remote/FUSE mounts
      (`isRemoteFS`); local goes straight to `http.ServeFile`.
- [ ] Don't run unbounded `-hwaccel cuda` decoders — cap them and spill to software decode
      (recover from CUDA-OOM) so the Nth concurrent stream doesn't fail.
- [ ] Don't add a prod env var only to the repo compose — prod is a hand-maintained
      compose file on the server; edit it too or the change won't take effect.
