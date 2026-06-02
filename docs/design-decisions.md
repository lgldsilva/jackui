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

**Why (the receipt).** The CI host (oracle-desktop) is arm64; emulating amd64 builds
under qemu OOM-killed the build. Separately, qemu binfmt is **not auto-registered after
a reboot**, which breaks any `docker run --platform linux/amd64` stage (e.g. the Sonar
scanner) until it's re-registered.

**How.** Native build on the target. `qemu-user-static` is installed on the CI host so
binfmt survives reboots; the host also has swap + `oom_score_adj` protection on the CI
services so a runaway can't freeze it.

**Trade-offs.** Cross-arch images need the right host; in exchange the build is fast
and doesn't thrash.

---

## What we are explicitly NOT doing (yet)

- **No public-internet hardening.** JackUI assumes a reverse proxy + trusted network.
- **No multi-writer SQLite / external DB.** Single-writer is fine at home scale.
- **No mermaid diagrams.** ASCII box-and-arrow only — renders everywhere, diffs cleanly.
- **No i18n yet.** UI is Portuguese-only; see the README roadmap.
- **The streamer does not yet reconcile pieces with already-downloaded files** — a
  completed download still re-streams through the cache rather than reading the file.

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
