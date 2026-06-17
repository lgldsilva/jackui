// Gate for `?play=<infoHash>` deep links against the hidden curtain (easter egg).
//
// The player mirrors playback into the URL (`/?play=HASH`). A hash played while
// the curtain was OPEN ends up in the URL; after a reload — which resets the
// in-memory curtain to CLOSED — that same URL would silently re-play hidden
// content. shouldBlockHiddenDeepLink() lets the caller refuse exactly those:
// items that exist ONLY behind the curtain.
//
// `visible`  = library list fetched WITHOUT the curtain (backend drops hidden).
// `revealed` = same list fetched WITH the curtain forced (?revealHidden=1).
//
// Block only when the hash is absent from `visible` but present in `revealed`
// (i.e. it's a hidden entry). A hash visible without the curtain, or absent even
// with it (a genuine non-library magnet / shared link), is allowed to play.
export function shouldBlockHiddenDeepLink(
  hash: string,
  visible: ReadonlyArray<{ infoHash: string }>,
  revealed: ReadonlyArray<{ infoHash: string }>,
): boolean {
  if (!hash) return false
  const inVisible = visible.some(e => e.infoHash === hash)
  if (inVisible) return false
  return revealed.some(e => e.infoHash === hash)
}
