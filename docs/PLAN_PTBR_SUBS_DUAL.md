# Plan: PT-BR Subtitles by Default + Dual-Audio Release Preference

Status: **IMPLEMENTED (Phases 1–3)** — merged on `feature/ptbr-subs-dual`; see "Implementation record" at the bottom for deviations.
Created: 2026-10-29
Scope: Phases 1–3. Phase 4 items are v2 backlog.

---

## 1. Context

JackUI streams torrents while they download and transcodes on the fly. Users who watch
PT-BR content today must manually search and pick a subtitle for every item, and cannot
steer search results towards dual-audio (PT-BR) releases.

### 1.1 What already exists (do NOT rebuild)

| Capability | Where |
|---|---|
| OpenSubtitles REST v1 client: search by query / `moviehash`+`moviebytesize` / `imdb_id` / season+episode, `moviehash_match=include`, `order_by=download_count`, login, download → VTT conversion, disk cache, response size caps | `internal/subtitles/opensubtitles.go` |
| HTTP API | `GET /api/subtitles/search?q&season&episode&langs` and `POST /api/subtitles/download/:fileId` (`internal/handlers/subtitles.go`) |
| Sidecar subtitles inside torrents and local files (srt/ass/vtt/ssa/sub) | `web/src/api/subtitles.ts` (`streamSidecars`, `streamSidecarURL`), `/api/stream/sidecar/{hash}/{fileIdx}`, `/api/local/sidecars` |
| Player wiring: `SubtitlePicker`, custom subtitle upload, burn-subtitle pipeline (ffmpeg CPU overlay), `EmbeddedTracksPanel` | `web/src/components/player/PlayerControlsPanel.tsx`, `PlayerProvider.tsx`, `internal/transcode/pipeline.go` |
| Release parsing: `Parse(title) → Quality`, `parseAudioTracks`, `parseGroup`, season/episode detection | `internal/parser/quality.go` |
| Content identification: TMDB integration + content ID | `internal/tmdb`, `internal/contentid` |
| HTTP retry policy | `internal/httpretry` |

### 1.2 Gaps (this plan)

1. No automatic subtitle fetch — search/download is fully manual today.
2. No default language (`pt-BR,pt`).
3. Fetched subtitle is not auto-selected in the player.
4. Search results carry no audio-language flags; no dual-audio preference or filter.
5. No OpenSubtitles file-hash computation on the backend.

---

## 2. Goals & Non-goals

**Goals**
- G1: Playing anything automatically offers (and by default applies) PT-BR subtitles — zero clicks.
- G2: Search results expose audio-language flags; a "prefer dual PT-BR" toggle boosts matching
  releases; an optional "dual only" hard filter exists (OFF by default).
- G3: Optional auto-burn of the selected PT-BR subtitle when a session transcodes (OFF by default).

**Non-goals (v2 backlog)**
- Additional providers (Podnapisi, Addic7ed…).
- Embedded subtitle streams exposed as HLS WebVTT renditions (`-c:s webvtt` master-playlist work).
- PGS/VobSub OCR.
- Languages beyond a configured list (design must not hardcode pt-BR).

---

## 3. Locked decisions

| # | Decision | Value | Rationale |
|---|---|---|---|
| D1 | Default languages | `pt-BR,pt` | pt-BR first, pt as fallback — both accepted by OS `languages` param |
| D2 | Dual preference | rank boost **within the same quality tier**; hard filter exists but OFF | dual releases are rare for fresh content; a hard filter would zero results |
| D3 | Auto-burn | OFF by default, per-user setting | burn forces the CPU overlay path; must stay explicit |
| D4 | Providers | OpenSubtitles only | native client already exists; Podnapisi = v2 |
| D5 | Boost semantics | flags outrank equal-quality competitors, never a higher quality tier | quality is the primary user expectation |

---

## 4. Requirements

### 4.1 Functional

| ID | Requirement | Acceptance criteria |
|---|---|---|
| RF1 | Auto-fetch PT-BR subtitle when a stream/download session starts | With API key configured: subtitle applied without any user action in ≤5 s p95; video start is not blocked; zero calls when `auto_fetch=false` |
| RF2 | Default language from settings | `SubtitlesSearch` without `langs=` uses `pt-BR,pt`; changeable in Settings |
| RF3 | Auto-selection in player | Fetched pt-BR subtitle becomes the active track; a later manual pick wins for the rest of the session |
| RF4 | Audio flags on search results | Table-driven fixtures of real release names (see §8.1) pass with ≥95% accuracy; results payload includes `audioFlags` |
| RF5 | Prefer-dual toggle + "dual only" filter | With *prefer*: dual releases rank above equal-quality non-dual; with *only*: zero non-dual results; state persists |
| RF6 | Auto-burn (optional) | Setting on + transcode active + pt-BR available → burned output shows the subtitle; off → unchanged behavior |
| RF7 | Dedup/persistence | Second play of the same item makes 0 OpenSubtitles calls; DB link survives restart |
| RF8 | Embedded text subtitles (SRT/ASS) keep working via `EmbeddedTracksPanel`; PGS marked "burn-only" | PGS never rendered as `<track>` |

### 4.2 Non-functional

| ID | Requirement |
|---|---|
| RNF1 | OpenSubtitles free quota is small → one search + one download per (hash, language); backoff via `httpretry`; graceful degradation when quota exhausted (warning + sidecars still work) |
| RNF2 | Auto-fetch is asynchronous with session-scoped context; cancellation on session end; video start latency unaffected (<50 ms added) |
| RNF3 | Signal cascade (§5.2): OS hash requires a **complete** file; in-progress torrents fall back to tmdb/imdb + parsed title, refined when the download completes |
| RNF4 | Formats: SRT→VTT (existing); ASS/SSA→VTT loses styling (documented); PGS/VobSub not convertible (burn-only) |
| RNF5 | Security: API key/token never logged; `fileID` path-validated (`fileIDPattern`); cache paths validated |
| RNF6 | Backwards compatibility: existing endpoints unchanged; new endpoints additive |
| RNF7 | Quality gates: coverage ≥90% on new packages, Sonar gate green, i18n keys added to `web/src/locales/{pt,en}.json` |

---

## 5. Detailed design

### 5.1 OpenSubtitles file hash — `internal/subtitles/oshash.go` (new)

Implement the OpenSubtitles hash: `size` as 16-char little-endian decimal string +
first 64 KiB + last 64 KiB → MD5 → 32-char hex. Streaming reads (open, seek), no
full-file buffering. **Complete files only** (see cascade below).

### 5.2 Signal cascade for auto-fetch

```
1. Complete file available on disk (local stream, library, finished download)
     → real path → OS hash → SearchAuto{MovieHash, MovieBytesize, Languages}
       (frame-exact match; strongest signal)
2. Torrent in progress (last piece may be missing)
     → internal/contentid + internal/tmdb → imdbid
     → SearchAuto{IMDB, Season, Episode, Query=<parsed title>, Languages}
3. On download completion → recompute real hash → refine result → persist
```

Rationale: `moviehash_match` is the only frame-exact signal and requires the full file;
progressive streaming cannot compute it, hence the cascade.

### 5.3 AutoFetcher — `internal/subtitles/auto.go` (new)

```go
type AutoFetcher struct{ /* client, db, settings, session registry */ }
func (a *AutoFetcher) Start(s SessionHandle)   // idempotent per (hash, fileIdx)
func (a *AutoFetcher) Cancel(sessionKey string)
```

- Hook into session creation (`internal/streamer/common.go`) and local playback start
  (`internal/localstream`).
- Persistence: table `subtitle_links`
  `(info_hash, file_index, status, file_id, language, vtt_path, created_at)`
  (SQLite via `internal/db`, Postgres migration note in `docs/POSTGRES_MIGRATION.md`).
- Exactly one search + ≤1 download per `(hash, language)`; cache dir reuse (VTTs are
  already content-addressed by file ID).
- The fetched VTT is registered as a **virtual sidecar** on the session (same shape the
  player already consumes from `streamSidecars`) and surfaced to the web via the
  existing session status/poll payload (add `subtitleAuto: {status, subtitle?}`).
- Failure states stored (`no_results`, `quota`, `no_api_key`, `error`) so the UI can
  show a quiet hint instead of retrying forever.

### 5.4 Player auto-selection (web)

- `PlayerProvider` consumes `subtitleAuto` and merges the virtual sidecar into the
  subtitle list; selection rule: pt-BR > pt > none, `hearingImpaired` and `forced`
  sorted last.
- `SubtitlePicker` shows a `PT-BR (auto)` tag; a manual pick disables auto-selection
  for that session.
- Toast: "Legenda PT-BR aplicada" / "PT-BR subtitle applied".

### 5.5 Audio flags — `internal/parser/audioflags.go` (new)

```go
type AudioFlags struct{ Dual, Dub, PTBR, Multi, Legendado, Latino bool }
func DetectAudioFlags(title string) AudioFlags
```

Case-insensitive, word-bounded regexes:
- `dual` → Dual · `dublado` → Dub · `pt[ -]?br` → PTBR · `legendado` → Legendado
- `multi` → Multi · `latino` → Latino
- **`MULTi` does NOT set Dual/PTBR** (typically FR/EN); **`LATINO` is Spanish**, not PT-BR.

Search handlers annotate each result with `audioFlags` and apply the toggle
(§5.6). The `jackett.Result` struct stays untouched — annotation happens in the
handler/response layer.

### 5.6 Preference & filter

- Toggle state: `prefer_dual` (default **off**) and `dual_only` (default **off**),
  persisted client-side (localStorage) + sent as query params to search endpoints.
- *prefer*: comparator = quality tier desc → flags desc (Dual‖Dub‖PTBR) → existing
  secondary sort. **Never promotes across a quality tier** (D5).
- *only*: filters out results without `Dual|Dub|PTBR`.
- Result cards show `DUAL` / `PT-BR` badges.

### 5.7 Auto-burn (transcode)

Setting `subtitles.burn_default` (off). When on, the active pt-BR subtitle is passed to
the existing burn/overlay path (`burnSubTrack` → `internal/transcode/pipeline.go`).
Subtitle streams remain dropped from HLS (`-sn`) — burn is the only transcode path.

### 5.8 New API surface (additive)

| Method | Path | Body/Query | Returns |
|---|---|---|---|
| GET | `/api/settings/subtitles` | — | `{default_languages, auto_fetch, burn_default}` |
| POST | `/api/settings/subtitles` | same shape | saved |
| GET | `/api/stream/{hash}/{fileIdx}/subtitles/auto` | — | `{status: none\|pending\|ready\|failed, reason?, subtitle?}` |
| GET | `/api/subtitles/search` (existing) | + `prefer_dual`, `dual_only` | records gain `audioFlags` |

Existing `/api/subtitles/*`, sidecar and local endpoints are untouched.

---

## 6. Compatibility matrix

| Scenario | Works? | Notes |
|---|---|---|
| Completed torrent / local file playback | ✅ | Real OS hash → frame-exact match |
| Torrent still downloading (stream-while-downloading) | ⚠️ | Cascade via imdbid/query; refined on completion (§5.2) |
| Local library files (`localstream`) | ✅✅ | Complete file → hash directly (best case) |
| SRT / VTT | ✅ | SRT→VTT conversion exists |
| ASS/SSA | ✅* | Converts; styling lost (documented) |
| PGS/VobSub (4K remuxes) | ⚠️ | Burn-only; never a `<track>` |
| Direct play (no transcode) | ✅ | `<track>` sidecar — already supported |
| HLS transcode | ✅ | Burn (existing pipeline) or sidecar `<track>` (hls.js/Safari) |
| `MULTi` / `LATINO` releases | ⚠️ | Not PT-BR — must not be flagged (false-positive guard, §5.5) |
| Anime absolute numbering (One Piece 1000+) | ⚠️ | OS `episode_number` accepts absolute; add fixture, tolerate failure |
| OS free quota exhausted | ⚠️ | `status=failed(quota)`; no automatic retry (decision persisted — manual flow still works; a later settings-language change re-searches once); sidecars unaffected |
| Electron / Safari / mobile web | ✅ | `<track>` + native HLS already handled by the player |

---

## 7. Edge cases

1. `MULTi.VF2` (French) — must not be flagged dual-PT (RF4 fixture).
2. `DUAL` without PT-BR (e.g., dual RU/EN) — flag is honest (`Dual` only); OS result
   language is the source of truth for the subtitle itself.
3. UTF-8 BOM on SRT — already stripped (`utf8BOM`).
4. Hearing-impaired / forced — sorted last in auto-selection.
5. Release names containing the word "dual" as part of a group name — word-boundary
   regex + `looksLikeFalseGroup`-style guard.
6. Same movie, multiple files (collections) — auto-fetch keyed by `(info_hash, file_index)`.
7. OS API schema drift — tolerate unknown JSON fields (decoder already ignores them);
   pin `User-Agent: JackUI v1.0`.

---

## 8. Test plan

- **Parser**: table-driven fixtures of real release names → expected flags (≥95% accuracy
  gate; include `DUAL-PiA`, `DUAL-SiGLA`, `MULTi.VF2`, `ENG.LATINO`, `YTS`, `DUBLADO`).
- **AutoFetcher**: `httptest` OS server (pattern from `opensubtitles_test.go`) — happy
  path, no-results, quota, cancel-on-session-end, dedup (second call = no HTTP).
- **oshash**: golden vectors (empty file, <64 KiB file, large file), streaming correctness.
- **Handlers**: settings round-trip, `prefer_dual`/`dual_only` param handling, additive
  response fields.
- **Web (vitest)**: auto-select logic, badge render, toggle persistence.
- Coverage ≥90% on new/changed packages; Sonar quality gate green.

---

## 9. Phases & acceptance

| Phase | Content | Acceptance |
|---|---|---|
| **P1 — backend** | `oshash.go`, `auto.go`, settings endpoints, `subtitle_links` table, session hook, `subtitles/auto` endpoint | RF1, RF2, RF7 pass; existing tests green |
| **P2 — player/UI** | `subtitleAuto` merge + auto-select + badge + toasts; i18n | RF3 pass; vitest green |
| **P3 — dual preference** | `audioflags.go`, search annotation, toggle + filter + badges | RF4, RF5 pass; parser fixtures ≥95% |
| **P4 — v2 backlog** | Podnapisi provider; embedded-subtitle HLS WebVTT renditions; PGS OCR; settings UI page | — |

Effort estimate: P1 ≈ 1 session · P2 ≈ ½ session · P3 ≈ 1 session.

---

## 10. Risks

| Risk | Mitigation |
|---|---|
| OS free quota too small for auto-fetch at scale | Dedup by hash; one download per title; quota status surfaced; providers are pluggable (v2) |
| Frame-exact hash impossible mid-download | Cascade (§5.2) + refinement on completion — UX shows "pending" state |
| ASS styling loss | Accepted; burn path preserves original ASS when selected |
| OS API schema drift | Tolerant decoder, pinned UA, `httpretry` policy |
| False-positive dual flags | Fixture-gated regex + word boundaries + guard tests |

---

## 11. Implementation record (2026-10-29)

Delivered on `feature/ptbr-subs-dual` (P1 backend, P2 player, P3 dual-audio). Deviations from
this plan, all review-driven (oracle + 9 independent lane reviews):

1. **§5.1/§5.2 corrected**: no new `internal/subtitles/oshash.go` — `internal/streamer/oshash.go`
   already implements the (correct, uint64-block-sum) OS hash. Cascade tier-1 now attempts
   `Streamer.OSHash` on ACTIVE torrents with a ~15s budget (parity with the manual flow; the
   reader pulls start/end pieces on demand), degrading to imdb/query on timeout/error/<64KiB.
   The original §5.1 hash description (16-char LE decimal + MD5) described a different scheme
   and was never implemented.
2. **§5.7 corrected**: external-subtitle burn is NOT the embedded-overlay path. Additive
   `Options.SubBurnFile` uses the libass `subtitles=` filter, forces CPU x264, keeps `-sn`;
   the client sends an OpenSubtitles FILE ID (`?burnsub=`), never a path; a typed 400
   (no libass / not cached / cache disabled) and a player-side `burnFailed` latch guarantee
   playback never hard-fails over a subtitle (falls back to the unburned `<track>`).
3. **Virtual sidecar**: implemented as dedicated player state (`autoSub`), NOT merged into
   `streamSidecars` output — avoids synthetic-index collisions and accidental persistence;
   auto picks are never written to localStorage and re-apply from the server cache on replay
   (0 OpenSubtitles calls, RF7).
4. **`subtitle_links`**: added `languages` column (one re-search when the language setting
   changes) + `LinksStore.MarkRefined` (refine failures consume their single attempt, no
   cross-restart re-search loops). Local files persist under the web's synthetic `local:` hash.
5. **Status semantics**: rows with status=ready are reported even when `auto_fetch` is off
   (previously fetched subtitles remain usable); only NEW fetches are gated.
6. **Manual-disable scope**: per-file (a manual pick disables auto for that episode; the next
   episode re-enables auto) — accepted reading of "wins for the session".
7. **D3 amended**: `burn_default` (and the other subtitle settings) are GLOBAL config, not
   per-user; GET lives in the `api` group, POST in the `adminAPI` (AdminOnly) group, and the
   GET response is an explicit 3-field struct (never the config struct with the API key).
8. **API surface**: `GET /api/stream/:hash/:file/subtitles/auto` +
   `GET /api/local/subtitles/auto/:hash/:file` (new, additive); the manual
   `/api/subtitles/auto/:hash/:file` flow is untouched; RF2 threads `default_languages` into
   `SubtitlesSearch` as an optional settings source.
9. **P2 extras**: `hearingImpaired`/`forced` ordering enforced (clean < forced < HI < HI+forced);
   probe/auto arbitration (embedded pick clears the auto pick); offset/Safari activation
   coverage for the auto track; settings card lives in the STREAM tab (no subtitles section
   existed).
10. **P4 backlog additions** from reviews: local row key collision across users on
    UserSubpath mounts (same relative path, different content); double LinksStore read on the
    refine gate; pre-existing `Access-Control-Allow-Origin: *` on SubtitlesDownload (matches
    the app-wide CORS policy; dedicated follow-up, out of scope here); hardcoded "PT-BR" copy
    in badge/toast when the applied language is `pt`; duplicate settings GET in the player;
    VAAPI hosts: burn forces sw-frames into `h264_vaapi` (mirrors the pre-existing embedded
    burn branch — ops awareness on AMD/Intel hosts).
11. **Second-model audit fixes** (deepseek-v4-pro, post-PR): `cfg.Subtitles.*` reads/writes
    fully synchronized (the POST previously mutated shared state racing 60s-lived AutoFetcher
    readers — torn-string panic risk; proven clean under `go test -race`); a FAILED
    languages-change re-search no longer destroys a working `ready` row (prior decision
    restored on failure); `cfg.Save` failure now rolls back in-memory values (house pattern);
    local auto-status rejects `:file != 0` (bogus dedup rows + duplicate quota spend). The
    §6 "quota → retry next day" line was corrected — there is no automatic retry.
12. **CI gate note**: the ARM CI stack enforces `gofmt -l .` repo-wide — lane-local gates ran
    vet/tests but not a global gofmt; fixed in `style:` commit (5 files).
