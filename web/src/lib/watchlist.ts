const GiB = 1024 * 1024 * 1024

type AutoFilters = {
  minResolution: string
  maxSizeBytes: number
  codec: string
}

// autoFilterSummary builds the compact "Auto · 1080p+ · x265 · ≤8GB" chip text
// shown on watchlist cards with auto-download enabled.
export function autoFilterSummary(w: AutoFilters, label: string): string {
  const parts = [label]
  if (w.minResolution) parts.push(`${w.minResolution}+`)
  if (w.codec) parts.push(w.codec)
  if (w.maxSizeBytes > 0) parts.push(`≤${Math.round((w.maxSizeBytes / GiB) * 10) / 10}GB`)
  return parts.join(' · ')
}
