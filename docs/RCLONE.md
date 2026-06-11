# rclone mounts (Google Drive & co.)

JackUI browses and streams any path you expose as an **external mount**
(`external.mounts` / `JACKUI_EXTERNAL_MOUNTS`). When that path is an
**rclone mount** (e.g. a Google Drive remote mounted via FUSE), every read is a
network fetch from the provider, so tuning matters: an untuned mount makes
"play from Gdrive" slow to start and stutter mid-playback.

This doc explains the split of responsibilities and the rclone flags that pair
well with what JackUI already does.

## Who does what

```
            ┌──────────────────────── JackUI ────────────────────────┐
 provider   │  rclone mount (FUSE)        internal/localstream        │
 (Drive) ───┼─►  VFS cache + chunked   ─►  aligned read-ahead +       ─► ffmpeg / <video>
            │     ranged downloads         throughput metering        │
            └─────────────────────────────────────────────────────────┘
```

- **rclone** owns the transport: it turns the kernel's read requests into
  ranged HTTP downloads, caches chunks on disk, and (optionally) reads ahead.
- **JackUI** (`internal/localstream`) wraps the file it serves/transcodes with a
  metered, read-ahead `Session`: it coalesces many small reads into a few large
  aligned ones (`JACKUI_LOCAL_READAHEAD_MB`, default 16 MiB) and measures the
  real byte rate so the player can show **"downloading X MB/s"**
  (`GET /api/local/transfer-status`).
- **Duplicate detection** (`/api/local/duplicates`) never downloads whole files:
  it groups by size, then fingerprints by hashing only the size + first/last
  64 KiB via ranged reads — rclone serves those as partial fetches.

## Recommended mount flags (Google Drive)

```sh
rclone mount gdrive: /mnt/gdrive \
  --vfs-cache-mode full \              # cache reads on disk → random ffmpeg seeks hit the cache
  --vfs-cache-max-size 50G \           # cap the on-disk cache (needs free space!)
  --vfs-cache-max-age 24h \
  --vfs-read-chunk-size 64M \          # larger chunks = fewer API round-trips (pairs with JackUI read-ahead)
  --vfs-read-chunk-size-limit 512M \
  --vfs-read-ahead 128M \              # rclone-side read-ahead, complements JackUI's
  --buffer-size 64M \
  --dir-cache-time 12h \               # don't re-list directories constantly
  --drive-chunk-size 64M \
  --allow-other --uid <jackui> --gid <jackui>
```

Notes:

- **`--vfs-cache-mode full`** is the single biggest win for transcoding: ffmpeg
  seeks to the `moov`/index at the end of MP4/MKV, and a cached file answers
  those seeks locally instead of re-fetching. Without it, seeks re-download.
  It needs disk space — size `--vfs-cache-max-size` to your cache disk.
- **Drive API quota**: large chunk sizes and read-ahead increase throughput but
  also burn quota; if you hit `rateLimitExceeded`, lower `--vfs-read-chunk-size`
  / `--drive-chunk-size` or set `--tpslimit`.
- A **stale mount** (network blip, token expiry) makes `statfs` fail; JackUI's
  disk-space readout degrades gracefully (shows 0/0) rather than erroring.

## JackUI-side knobs

| Setting | Env | Default | Effect |
|---|---|---|---|
| Local read-ahead | `JACKUI_LOCAL_READAHEAD_MB` | 16 | Read-ahead buffer for local/rclone files (direct-play + HLS source). Bigger = smoother on slow mounts, more RAM per stream. |
| HLS VOD mode | `JACKUI_HLS_VOD_MODE` | `all` | `off`/`hlsjs`/`all` — enables the seekbar (finite-VOD HLS) for transcoded playback. The local-file path also reuses the play-time ffprobe duration, skipping the 30s seekable probe that otherwise runs on every HLS session (a big chunk of "Gdrive play is slow to load"). |

## Why not call the provider's stored hash directly?

Google Drive stores an MD5 per object that `rclone hashsum` can read without
downloading — but only when you talk to rclone as a *remote* (`gdrive:path`).
JackUI sees a generic filesystem path through FUSE and has no rclone remote
mapping, so it can't ask for that hash portably. The size + head/tail fingerprint
is the filesystem-generic equivalent: it avoids full downloads and works the
same on rclone, NFS, SMB, or a local disk.
