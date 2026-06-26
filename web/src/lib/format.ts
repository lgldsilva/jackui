// Format helpers shared across player UI / library / cache stats.

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'] as const

export function bytesUnit(bytes: number): number {
  if (!bytes || bytes <= 0) return 0
  const k = 1024
  return Math.min(UNITS.length - 1, Math.floor(Math.log(bytes) / Math.log(k)))
}

export function formatBytesAs(bytes: number, unitIndex: number): string {
  const targetUnit = UNITS[unitIndex] ?? 'B'
  if (!bytes || bytes <= 0) return `0 ${targetUnit}`
  const val = bytes / Math.pow(1024, unitIndex)
  return `${Number.parseFloat(val.toFixed(2))} ${targetUnit}`
}

export function formatBytes(bytes: number): string {
  const i = bytesUnit(bytes)
  return formatBytesAs(bytes, i)
}

export function formatRate(bytesPerSec: number): string {
  if (!bytesPerSec || bytesPerSec <= 0) return '0 KB/s'
  return `${formatBytes(bytesPerSec)}/s`
}

export function formatDuration(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds <= 0) return '0:00'
  const s = Math.floor(totalSeconds)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (h > 0) return `${h}:${m.toString().padStart(2, '0')}:${sec.toString().padStart(2, '0')}`
  return `${m}:${sec.toString().padStart(2, '0')}`
}

// Compact ETA-style duration: "45s" | "12m" | "2h 13m" | "5h". Used in card
// chips where space is tight. Ceils to avoid showing "0s remaining".
export function formatDurationShort(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds <= 0) return ''
  if (totalSeconds < 60) return `${Math.ceil(totalSeconds)}s`
  if (totalSeconds < 3600) return `${Math.ceil(totalSeconds / 60)}m`
  const hours = Math.floor(totalSeconds / 3600)
  const mins = Math.floor((totalSeconds % 3600) / 60)
  return mins > 0 ? `${hours}h ${mins}m` : `${hours}h`
}

// Relative date in pt-BR: "5m atrás" | "3h atrás" | "ontem" | "4d atrás" |
// "12 mai" (fallback to short locale date for >7d). Used in History/Favorites
// cards. Granularity drops to minutes under 1h so freshly-added items don't
// all read "agora".
export function formatDate(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const diffMs = Date.now() - d.getTime()
  const diffH = diffMs / 3_600_000
  if (diffH < 1) {
    const m = Math.floor(diffH * 60)
    return m <= 0 ? 'agora' : `${m}m atrás`
  }
  if (diffH < 24) return `${Math.floor(diffH)}h atrás`
  if (diffH < 48) return 'ontem'
  if (diffH < 168) return `${Math.floor(diffH / 24)}d atrás`
  return d.toLocaleDateString('pt-BR', { day: '2-digit', month: 'short' })
}

export function formatBytesPair(downloaded: number, total: number): string {
  const u = bytesUnit(total)
  return `${formatBytesAs(downloaded, u)} / ${formatBytesAs(total, u)}`
}
