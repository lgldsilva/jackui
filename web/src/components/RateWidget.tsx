import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowDownCircle } from 'lucide-react'
import { streamRate } from '../api/client'
import { formatRate } from '../lib/format'

/**
 * Global download-rate widget for the header. Hidden when no torrents are active.
 *
 * Adaptive polling: 2s while something is actually downloading/seeding, but only
 * every 20s when idle (the common case — nothing to show). Paused while the tab
 * is hidden. This kills the constant /api/stream/rate chatter on idle screens
 * while still updating quickly during playback.
 */
export default function RateWidget() {
  const [rate, setRate] = useState({ downRate: 0, upRate: 0, activeTorrents: 0 })
  const timerRef = useRef<ReturnType<typeof globalThis.setTimeout> | null>(null)

  useEffect(() => {
    let cancelled = false

    const clear = () => {
      if (timerRef.current) { globalThis.clearTimeout(timerRef.current); timerRef.current = null }
    }
    const schedule = (ms: number) => {
      if (cancelled || document.hidden) return
      timerRef.current = globalThis.setTimeout(tick, ms)
    }
    const tick = async () => {
      try {
        const r = await streamRate()
        if (cancelled) return
        setRate(r)
        schedule(r.activeTorrents > 0 ? 2000 : 20000)
      } catch {
        // 401 / network error — keep last value, retry slowly.
        if (!cancelled) schedule(20000)
      }
    }

    const handleVisibilityChange = () => {
      if (document.hidden) clear()
      else if (!timerRef.current) tick() // resume immediately on focus
    }
    document.addEventListener('visibilitychange', handleVisibilityChange)
    tick()
    return () => {
      cancelled = true
      clear()
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [])

  if (rate.activeTorrents === 0) return null

  return (
    <Link
      to="/library"
      className="header-link tabular-nums text-[11px] text-emerald-300 hover:!text-emerald-200"
      title={`${rate.activeTorrents} torrent(s) ativo(s) — ${formatRate(rate.upRate)} upload`}
    >
      <ArrowDownCircle className="w-4 h-4" />
      <span>{formatRate(rate.downRate)}</span>
    </Link>
  )
}
