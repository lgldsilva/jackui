import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowDownCircle } from 'lucide-react'
import { streamRate } from '../api/client'
import { formatRate } from '../lib/format'

/**
 * Global download-rate widget for the header. Hidden when no torrents are active.
 *
 * Polling cadence: 2s while the tab is visible, paused while hidden — avoids
 * pointless background requests on mobile.
 */
export default function RateWidget() {
  const [rate, setRate] = useState({ downRate: 0, upRate: 0, activeTorrents: 0 })
  const intervalRef = useRef<number | null>(null)

  useEffect(() => {
    let cancelled = false

    const tick = async () => {
      try {
        const r = await streamRate()
        if (!cancelled) setRate(r)
      } catch {
        // 401 / network error — silently keep last value (likely not logged in)
      }
    }

    const start = () => {
      if (intervalRef.current) return
      tick()
      intervalRef.current = window.setInterval(tick, 2000)
    }
    const stop = () => {
      if (intervalRef.current) {
        window.clearInterval(intervalRef.current)
        intervalRef.current = null
      }
    }

    const onVisibility = () => {
      if (document.hidden) stop()
      else start()
    }
    document.addEventListener('visibilitychange', onVisibility)
    start()
    return () => {
      cancelled = true
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
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
