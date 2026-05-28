import { useEffect, useRef, useState } from 'react'
import { ArrowUp, Loader2 } from 'lucide-react'
import { streamHealth, StreamHealth } from '../api/client'

// SeedBadge shows a torrent's swarm health on a card: a dot (green = available,
// gray = no seeds, amber = checking) plus the seed count and how long ago it was
// checked. It fetches lazily (only when scrolled into view) and, when the server
// reports a stale snapshot is being re-probed, polls once more to pick up the
// fresh numbers. The server caps/queues the actual swarm probing.

function relTime(iso?: string): string {
  if (!iso) return ''
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 90) return 'agora'
  const m = s / 60
  if (m < 90) return `há ${Math.round(m)}min`
  const h = m / 60
  if (h < 36) return `há ${Math.round(h)}h`
  return `há ${Math.round(h / 24)}d`
}

interface Props {
  infoHash?: string
  magnet?: string
  className?: string
}

export default function SeedBadge({ infoHash, magnet, className = '' }: Props) {
  const ref = useRef<HTMLSpanElement>(null)
  const [health, setHealth] = useState<StreamHealth | null>(null)
  const fetchedRef = useRef(false)

  useEffect(() => {
    if (!infoHash) return
    const el = ref.current
    if (!el) return
    let cancelled = false

    const fetchOnce = async () => {
      const h = await streamHealth(infoHash, magnet)
      if (cancelled) return
      setHealth(h)
      // If the server kicked a background re-probe, grab the fresh result shortly.
      if (h.refreshing) {
        setTimeout(async () => {
          if (cancelled) return
          const h2 = await streamHealth(infoHash, magnet)
          if (!cancelled) setHealth(h2)
        }, 9000)
      }
    }

    if (typeof IntersectionObserver === 'undefined') {
      if (!fetchedRef.current) { fetchedRef.current = true; fetchOnce() }
      return () => { cancelled = true }
    }
    const obs = new IntersectionObserver((entries, observer) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        observer.disconnect()
        if (!fetchedRef.current) { fetchedRef.current = true; fetchOnce() }
        return
      }
    }, { rootMargin: '120px' })
    obs.observe(el)
    return () => { cancelled = true; obs.disconnect() }
  }, [infoHash, magnet])

  if (!infoHash) return null

  // States: loading/checking (amber), known available (green), known none (gray).
  const checking = health === null || (!health.known && health.refreshing)
  const seeders = health?.seeders ?? 0
  const available = !!health?.available
  const dot = checking ? 'bg-amber-400' : available ? 'bg-green-500' : 'bg-gray-600'
  const title = checking
    ? 'Verificando seeds no swarm...'
    : health?.known
      ? `${seeders} seeds / ${health?.peers ?? 0} peers · verificado ${relTime(health?.checkedAt)}${health?.active ? ' (ao vivo)' : ''}`
      : 'Disponibilidade desconhecida'

  return (
    <span
      ref={ref}
      title={title}
      className={`inline-flex items-center gap-1 text-[10px] text-gray-400 ${className}`}
    >
      <span className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${dot} ${checking ? 'animate-pulse' : ''}`} />
      {checking ? (
        <Loader2 className="w-3 h-3 animate-spin text-gray-500" />
      ) : health?.known ? (
        <span className="flex items-center gap-0.5 tabular-nums">
          <ArrowUp className="w-3 h-3 text-green-500" />{seeders}
        </span>
      ) : (
        <span className="text-gray-600">—</span>
      )}
    </span>
  )
}
