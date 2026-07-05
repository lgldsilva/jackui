# iOS Audio — the playback path, the silent-switch lesson, and what stays off

> The canonical write-up of how audio plays on iOS/Safari in JackUI, why the
> design is shaped this way, and which features are deliberately disabled.
> Long-form sibling of [design-decisions.md](design-decisions.md); linked from
> the `web/` row in [ARCHITECTURE.md](ARCHITECTURE.md).

---

## TL;DR — read this first

**The "no sound on iPhone" bug was the SILENT/RINGER SWITCH, not our code —
and the definitive fix has since shipped: audio now plays through a dedicated
`<audio>` element (the media channel), which the silent switch does NOT mute.**

Historically JackUI played iOS audio through the inline `<video>` element. iOS
routes inline-`<video>` audio to the **ringer channel**, which the hardware
silent switch mutes. So a phone with the switch ON played everything *muted* —
and looked exactly like a broken player. **Playback always worked. It was muted
by hardware.**

A ~12-patch saga (tap-to-play, `blessed`, `load()`, media-token cache,
burst-defer, preload juggling, simplifications) chased this ghost. Apple staff
gave the *identical* diagnosis for the canonical "Web Audio produces no sound on
iOS" report (Apple Developer Forum thread 126136: *"The mute switch was
enabled… iOS is now honoring the mute switch"*).

**The fix that closed it (the one the old re-enable table called "THE definitive
fix"):** the audio path was moved OFF `<video>` onto a **dedicated native
`<audio>` element** — `SimpleAudioPlayer` (`web/src/components/player/SimpleAudioPlayer.tsx`).
A bare `<audio>` plays on the **media channel**, which the silent switch does
*not* mute. Together with the earlier real fixes — the media-token cache (stops
reload-on-token-refresh) and the playlist persistence (stops reload on sidebar
open/close and on the 1s poll) — that is what actually made iOS audio reliable.
Most of the rest of the saga was fighting a symptom that was hardware.

---

## The iOS gotchas (why every decision below exists)

These are **timeless WebKit behaviours** — they constrained the old `<video>`
path and they still shape the current `<audio>` one.

| # | Gotcha | Consequence |
|---|--------|-------------|
| G1 | **Silent switch mutes inline-`<video>` audio.** iOS sends `<video>` (and audio played through a muted/inline `<video>`) to the **ringer** channel. A dedicated `<audio>` plays on the **media** channel. | Switch ON + `<video>` → silent playback that looks broken. A dedicated `<audio>` is NOT muted by the switch — **this is why the current path uses `<audio>`.** |
| G2 | **`createMediaElementSource` makes the Web Audio graph the ONLY output.** | With a **suspended** `AudioContext` (iOS only unlocks inside a gesture) the element stalls at **readyState 2** and goes mute, even for direct files. |
| G3 | **Web Audio output is ALSO muted by the silent switch by default** (WebKit bug 237322 — Web Audio uses the ambient channel, unlike a bare `<audio>`/`<video>` on the media channel). | Once you tap with `createMediaElementSource`, audio follows the *stricter* Web Audio silent-switch rule. Official fix: `navigator.audioSession.type='playback'` (iOS 17+, experimental — feature-detect). |
| G4 | **Programmatic autoplay requires a real user gesture.** | First track needs a tap. `play()` outside a gesture → AbortError, no sound. The **native `<audio controls>` play button IS that gesture** — no custom overlay needed. |
| G5 | **The per-element play grant PERSISTS across `src` swaps.** | After the first gesture-`play()` on an element, subsequent programmatic `play()` on the *same* element are allowed — this is what makes auto-advance possible (`blessedRef` in `SimpleAudioPlayer`). |
| G6 | **`preload='auto'` parks the element at readyState 2.** | Pre-fetching OUTSIDE a gesture fires `suspend` (NETWORK_IDLE); a later `play()` does NOT resume the fetch → stuck, no sound. `preload='none'` + gesture-`play()` is the canonical iOS pattern (still used by `SimpleAudioPlayer` on WebKit). |

> The historical reverts **conflated G1 and G6**: the "no sound" (G1, silent
> switch) and the "freeze at rs2" (G6, preload prefetch) were two different
> problems blamed on Web Audio. Both were root-caused independently; G1 is now
> structurally solved by the dedicated-`<audio>` move.

---

## Architecture of the iOS audio path (current)

Audio plays through a **dedicated native `<audio controls>`** element, NOT the
inline `<video>`. The switch happens in `PlayerModal` on the `audioMode` prop:
`audioMode` → `renderAudioBody()` mounts the audio stack; otherwise
`VideoPlayerElement` handles video (and keeps the old `blessed`/HLS machinery
for the video path only).

The audio stack (`renderAudioBody`, `web/src/components/PlayerModal.tsx`):

1. **`AudioCoverArt`** — the album cover box.
2. **`SimpleAudioPlayer`** (`SimpleAudioPlayer.tsx`) — a native `<audio controls>`
   whose `src` is a **direct** URL and nothing else.
3. **`SimpleAudioControls`** (`SimpleAudioControls.tsx`) — ⏮⏭ + shuffle/repeat +
   `N / M` position. These are **track-switching** buttons only (the native
   `<audio>` transport has no prev/next); there is **no** Web Audio, no EQ, no
   custom seekbar.

### 1. Dedicated `<audio>` on the media channel — the G1 fix

- A bare `<audio>` element plays on the **media** channel; the hardware silent
  switch does not mute it (G1). Moving off inline-`<video>` is what closed the
  "no sound with the switch on" bug for good.
- This is the "dedicated `<audio>` element" the historical
  [re-enable table](#re-enable-table) flagged as *the definitive fix* — now
  shipped.

### 2. Direct-play only — `useAudioDirectUrl` (never HLS)

- `useAudioDirectUrl(info, selectedFile, mediaToken)`
  (`useAudioDirectUrl.ts`) resolves the RAW bytes-with-Range endpoint, source
  agnostic:
  - **local** (rclone/disk): `/api/local/file?mount=…&path=…`
  - **torrent**: `streamFileURL(hash, fileIndex, token)`
- It **NEVER returns HLS/transcode** — audio always direct-plays. (HLS-on-WebKit
  is the one case Web Audio can't tap; direct-play is also what keeps the path
  simple.) A codec the browser can't decode is a separate, rare concern, not the
  default audio path.

### 3. Native play button = the gesture — no overlay, no `load()`

- `SimpleAudioPlayer` renders `<audio controls>`. On iOS the user taps the
  **native** play button, which is the real user gesture WebKit requires (G4) —
  so there is **no custom `StartAudioOverlay`, no `v.load()`, no gesture state
  machine**. The `src` is declarative; tapping play does a clean load+play iOS
  honours.
- (`StartAudioOverlay` / `shouldShowStartAudioOverlay` still exist in the tree
  but are **no longer wired into the audio path** — the native controls replaced
  them. Treat them as legacy.)

### 4. Preload by platform (`none` on WebKit) — load-bearing, KEEP

- `preload={isWebKit ? 'none' : 'metadata'}` in `SimpleAudioPlayer`.
- On WebKit, `preload='none'` means the element never pre-fetches, so it is never
  parked at rs2 (G6); the gesture's `play()` does a clean load+play. Do NOT set
  `'auto'`/`'metadata'` on WebKit — it re-introduces the rs2 stall.

### 5. `blessedRef` / auto-advance — ENABLED

- Apple grants programmatic `play()` per-element AFTER the first gesture-play,
  and the grant **persists across `src` swaps** (G5).
- `SimpleAudioPlayer` tracks this with an internal `blessedRef` (set true on the
  first `'playing'` event). When `src` changes AND the element is blessed, it
  calls `el.play()` for the next track; before the first play it does NOT
  autoplay (the user taps). An `attachedSrcRef` guard stops a re-render on the
  same `src` from re-firing.
- The `PlayerModal` also keeps a top-level `blessed` state, but it now only
  serves the **video** element (`disableNativeAutoplay = isIOS() && !blessed`)
  and the **playlist-burst defer** (`resolveEnabled = !iosAudio || blessed`) —
  not the audio element, which is self-contained.

### 6. Media-token cache — ENABLED, critical to iOS

- `fetchMediaToken`: a long-TTL (**6h**) JWT `scope='media'`, fetched **once** on
  player open (effect keyed on `result?.infoHash`), stored in `mediaToken`
  state, reused in ALL media URLs (`<audio src>` via `useAudioDirectUrl`,
  `<track src>`, cover art).
- **Why:** the regular 15-min access token's background refresh would change the
  URL query string → the element reloads and resets playback to 0 → on iOS that
  **aborts the pending gesture-`play()`** (AbortError, commit 59a9fe7). The
  long-TTL token keeps the media URL stable for the whole session.

### 7. Playlist aggregation + persistence — KEEP

- The aggregated track list (`usePlaylistTracks`) must NOT re-resolve all ~47
  tracks on every sidebar toggle or poll tick.
- The skeleton-rebuild effect depends on `signature` (playlist item hashes) +
  `enabled` — a local item starts `'ready'` from its pseudo-hash with **no
  backend call** (`skeletonGroup`); the list persists across sidebar open/close.
- The current-group seed effect keys on `currentSig` (hash + file count) via a
  **ref**, NOT on the `currentInfo` object — which the player replaces every ~1s
  poll tick, previously re-seeding and visibly "reloading" the list every second.
- Background resolution stays gated by `enabled` + `resolveEnabled` and throttled
  by `ACTIVATE_CONCURRENCY=2`. On iOS the burst waits for `blessed` so it does
  not compete with the current track's byte-stream. `localPlayBatch` pre-warms
  the direct-play URLs for a whole local folder in one request (see
  [PERFORMANCE.md](PERFORMANCE.md) #1).

---

## What's DISABLED on iOS (and why)

### EQ + spectrum visualizer (Web Audio) — DISABLED on WebKit

- Enabling EQ/visualizer means `createMediaElementSource`, which makes the Web
  Audio graph the sole output and stalls a suspended-context WebKit element at
  rs2 (G2), and puts audio back on the stricter silent-switch rule (G3).
- **Status:** kept off on Safari/iOS. The current `<audio>` path is deliberately
  Web-Audio-free — it trades EQ for rock-solid, silent-switch-immune playback.
- **Re-enablable for direct-play** (see [re-enable table](#re-enable-table)) only
  with device testing; it is no longer the priority now that plain playback works.

### Gapless / crossfade engine — DISABLED on WebKit, KEEP OFF

- The dual-`<audio>` A/B ping-pong via GainNode used the **same**
  `createMediaElementSource` → same rs2 freeze (G2). It was reproduced in prod by
  **three** independent attempts and removed in the simplification (the
  `AudioTransportBar`/engine are gone — only comments remain).
- **Verdict: KEEP DISABLED.** Re-introducing `createMediaElementSource`
  re-introduces the freeze. A "gapless-lite" (a 2nd `<audio>` + swap on
  `'ended'`, dry cut, no crossfade, no Web Audio) is *possible* as a NEW opt-in
  path, but is not a reactivation of the engine.

---

## What's ACTIVE on iOS

| Feature | Status | Why kept |
|---|---|---|
| Dedicated `<audio>` (`SimpleAudioPlayer`) | ENABLED | Media channel → silent switch does NOT mute (the G1 fix). |
| Direct-play via `useAudioDirectUrl` | ENABLED | Raw bytes+Range, source-agnostic; never HLS → no Web-Audio-can't-tap case. |
| Native play button = gesture | ENABLED | `<audio controls>` play IS the iOS gesture (G4) — no overlay, no `load()`. |
| `preload='none'` on WebKit | ENABLED | Avoids the rs2 stall (G6) on first play. |
| `blessedRef` / auto-advance | ENABLED | First track tapped, all subsequent auto-advance (per-element grant persists, G5). |
| Media-token cache (6h JWT) | ENABLED | Stable media URL → no reload/AbortError on token refresh. |
| Playlist persistence + `localPlayBatch` | ENABLED | List survives sidebar toggle + 1s poll; folder URLs pre-warmed in one call. |
| EQ / Web Audio | DISABLED | `createMediaElementSource` freezes element at rs2 on suspended ctx (G2). |
| Gapless engine | DISABLED | Same Web Audio constraint. Keep off. |

### Removed (do NOT bring back)

- **`<video>` for the iOS audio path** — replaced by the dedicated `<audio>`
  (media channel). Routing music back through `<video>` re-arms the silent-switch
  mute (G1).
- **`StartAudioOverlay` for audio** — the native `<audio controls>` play button
  is the gesture; the custom overlay is redundant.
- **`v.load()` before `play()`** (40951f1) — band-aid for the `preload='auto'`
  suspended state; with `preload='none'` the element is never parked, so `load()`
  is redundant and harmful. Removed in 7d6ae73.
- **Web Audio engine / `AudioTransportBar`** — removed in the simplification; the
  transport is now the native controls + `SimpleAudioControls` (prev/next only).

---

## Re-enable table

| What | Re-enable? | Risk | How / test |
|---|---|---|---|
| **Dedicated `<audio>` element for iOS** | ✅ DONE | — | Shipped as `SimpleAudioPlayer`. The residual silent-switch mute (G1) is fixed: audio plays on the media channel; switch ON no longer mutes. |
| EQ + visualizer (direct-play only) | MAYBE, needs device test | MED | Gate EQ to `direct && !isSafariBrowser()` initially. Test on a real iPhone, **silent switch OFF**: sound after tap, rs4 not rs2, no AbortError; EQ/visualizer light up after first interaction. Lower priority now that plain playback is solid. |
| `navigator.audioSession.type='playback'` | AFTER EQ works | LOW | Feature-detect; makes EQ'd audio ignore the silent switch (G3). Test: switch ON mid-play → audio keeps playing. |
| Gapless / crossfade engine | NO — keep disabled | HIGH | Same `createMediaElementSource` rs2 freeze (G2). A "gapless-lite" (dry-cut, no Web Audio) is a separate opt-in feature, not a reactivation. |

---

## Don't "fix" this checklist

- [ ] Don't read "no sound on iPhone" as a code bug before checking the **silent switch** (G1) — and remember the audio path already sidesteps it via `<audio>`.
- [ ] Don't route iOS music audio back through `<video>` — it re-arms the silent-switch mute (G1). Keep it on the dedicated `<audio>` (media channel).
- [ ] Don't feed HLS into the audio path — `useAudioDirectUrl` is direct-only by design.
- [ ] Don't re-enable the Web Audio **gapless engine** on iOS — `createMediaElementSource` freezes the element at readyState 2 (G2). Confirmed in prod 3×.
- [ ] Don't re-add a custom `StartAudioOverlay`/`v.load()` for audio — the native `<audio controls>` play button is the gesture; `preload='none'` is the root-cause fix.
- [ ] Don't set `preload='auto'`/`'metadata'` on WebKit `<audio>` — it re-introduces the rs2 stall (G6).
- [ ] Don't use the 15-min access token in media URLs — its refresh reloads the element and aborts the gesture-`play()` on iOS. Use the 6h media token.
- [ ] Don't let the playlist gate oscillate per-track (no `currentTime>1`, no `enabled`-bound `signature`) — it reloads the list.
- [ ] Don't test "audio works on iPhone" with the silent switch in an unknown state — control it explicitly.
