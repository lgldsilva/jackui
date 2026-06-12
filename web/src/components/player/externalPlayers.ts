// Catalogue of "Open in" external-player targets shown in the player's
// ExternalPlayerMenu. Each entry's `build` derives the player's link from the
// SAME media URLs computeMediaUrls already produces — no new URL/scheme logic
// lives here, so each item behaves EXACTLY like the standalone button it
// replaced. `build` returning '' (or null) means "not applicable for this
// media" and the item is hidden (matches the old `iinaURL && ...` guards).
//
// Pure + data-only on purpose: ExternalPlayerMenu stays presentational and the
// per-player wiring is unit-testable without React.

// The subset of computeMediaUrls' return value the players need. Keeping it
// narrow (not the whole MediaUrls bag) makes the test inputs obvious.
export type ExternalPlayerURLs = {
  // .m3u playlist link (universal VLC: desktop/iOS/Android). Always built when
  // a file is selected; '' otherwise.
  readonly vlcURL: string
  // iina://weblink?url=… (macOS). '' when no file selected.
  readonly iinaURL: string
  // infuse://x-callback-url/play?url=… (iOS/macOS/Apple TV). '' when no file.
  readonly infuseURL: string
  // Absolute direct-play HTTP URL with ?token=, no transcode — the value
  // "Copiar URL" puts on the clipboard so any other player can ingest it. '' if
  // there's nothing to stream.
  readonly directURL: string
}

// kind drives how ExternalPlayerMenu activates the item:
//  - 'link'      → navigate to build()'s href (open the app via its scheme).
//  - 'clipboard' → copy build()'s string instead of navigating.
export type ExternalPlayerKind = 'link' | 'clipboard'

export type ExternalPlayer = {
  readonly id: string
  // i18n key under player.external.* for the visible label.
  readonly labelKey: string
  // i18n key under player.external.*_hint for the tooltip/description.
  readonly hintKey: string
  readonly kind: ExternalPlayerKind
  // Tailwind classes for the per-player accent (kept from the old buttons so
  // the colours survive the consolidation).
  readonly accent: string
  // Derives the href (link) or payload (clipboard) from the media URLs. Empty
  // string ⇒ hide this item for the current media.
  readonly build: (urls: ExternalPlayerURLs) => string
}

// EXTERNAL_PLAYERS preserves the order the buttons used to appear in (VLC,
// IINA, Infuse) and appends "Copiar URL" last. Adding a player = one entry.
export const EXTERNAL_PLAYERS: readonly ExternalPlayer[] = [
  {
    id: 'vlc',
    labelKey: 'player.external.vlc',
    hintKey: 'player.external.vlc_hint',
    kind: 'link',
    accent: 'text-orange-600 dark:text-orange-300',
    build: (u) => u.vlcURL,
  },
  {
    id: 'iina',
    labelKey: 'player.external.iina',
    hintKey: 'player.external.iina_hint',
    kind: 'link',
    accent: 'text-blue-600 dark:text-blue-300',
    build: (u) => u.iinaURL,
  },
  {
    id: 'infuse',
    labelKey: 'player.external.infuse',
    hintKey: 'player.external.infuse_hint',
    kind: 'link',
    accent: 'text-pink-600 dark:text-pink-300',
    build: (u) => u.infuseURL,
  },
  {
    id: 'copy',
    labelKey: 'player.external.copy_url',
    hintKey: 'player.external.copy_url_hint',
    kind: 'clipboard',
    accent: 'text-text-primary',
    build: (u) => u.directURL,
  },
]

// availableExternalPlayers keeps only the items whose build() yields a
// non-empty value for the current media — i.e. the ones that used to render.
export function availableExternalPlayers(urls: ExternalPlayerURLs): ExternalPlayer[] {
  return EXTERNAL_PLAYERS.filter((p) => p.build(urls) !== '')
}

// resolveExternalPlayer picks the player to use for the primary (split-button)
// action: the remembered `preferredId` if it's still available, else the first
// available one (VLC by default). Returns null when nothing is playable. Keeps
// the "remember the last choice" decision pure/testable.
export function resolveExternalPlayer(
  urls: ExternalPlayerURLs,
  preferredId: string | null,
): ExternalPlayer | null {
  const available = availableExternalPlayers(urls)
  if (available.length === 0) return null
  if (preferredId) {
    const match = available.find((p) => p.id === preferredId)
    if (match) return match
  }
  return available[0]
}
