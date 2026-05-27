// Tiny localStorage wrapper. All keys live under "jackui:" namespace.

const PREFIX = 'jackui:'

export function load<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(PREFIX + key)
    if (raw === null) return fallback
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

export function save<T>(key: string, value: T): void {
  try {
    localStorage.setItem(PREFIX + key, JSON.stringify(value))
  } catch {
    // Quota exceeded or storage unavailable — silently ignore.
  }
}

export function remove(key: string): void {
  try {
    localStorage.removeItem(PREFIX + key)
  } catch { /* ignore */ }
}

// Append to a bounded MRU list (most-recent-first, deduped, capped).
export function pushMRU(key: string, value: string, cap = 8): string[] {
  const v = value.trim()
  if (!v) return load<string[]>(key, [])
  const current = load<string[]>(key, [])
  const next = [v, ...current.filter(x => x !== v)].slice(0, cap)
  save(key, next)
  return next
}
