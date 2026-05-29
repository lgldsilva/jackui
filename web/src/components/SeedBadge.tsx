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
  const [probing, setProbing] = useState(false)
  const fetchedRef = useRef(false)

  // On view: PEEK only (persisted/live — never touches the swarm). The probe is
  // strictly on click (verify()), so scrolling a list costs nothing on the swarm.
  useEffect(() => {
    if (!infoHash) return
    const el = ref.current
    if (!el) return
    let cancelled = false
    const peek = async () => {
      const h = await streamHealth(infoHash, magnet, false)
      if (!cancelled) setHealth(h)
    }
    if (typeof IntersectionObserver === 'undefined') {
      if (!fetchedRef.current) { fetchedRef.current = true; peek() }
      return () => { cancelled = true }
    }
    const obs = new IntersectionObserver((entries, observer) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        observer.disconnect()
        if (!fetchedRef.current) { fetchedRef.current = true; peek() }
        return
      }
    }, { rootMargin: '120px' })
    obs.observe(el)
    return () => { cancelled = true; obs.disconnect() }
  }, [infoHash, magnet])

  if (!infoHash) return null

  // Explicit, user-triggered swarm probe (adds the torrent briefly to count peers).
  const verify = async (e?: React.MouseEvent) => {
    e?.stopPropagation(); e?.preventDefault()
    if (probing) return
    setProbing(true)
    const h = await streamHealth(infoHash, magnet, true)
    setHealth(h)
    if (h.refreshing) {
      setTimeout(async () => { setHealth(await streamHealth(infoHash, magnet, false)); setProbing(false) }, 9000)
    } else {
      setProbing(false)
    }
  }

  const known = !!health?.known
  const seeders = health?.seeders ?? 0
  const available = !!health?.available
  let dot: string
  if (probing) {
    dot = 'bg-amber-400'
  } else if (known) {
    dot = available ? 'bg-green-500' : 'bg-gray-600'
  } else {
    dot = 'bg-gray-700'
  }
  let title: string
  if (probing) {
    title = 'Verificando seeds no swarm…'
  } else if (known) {
    const liveSuffix = health?.active ? ' (ao vivo)' : ''
    title = `${seeders} seeds / ${health?.peers ?? 0} peers · verificado ${relTime(health?.checkedAt)}${liveSuffix}`
  } else {
    title = 'Clique para verificar seeds'
  }

  return (
    <span
      ref={ref}
      onClick={verify}
      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); verify() } }}
      title={title}
      role="button"
      tabIndex={0}
      className={`inline-flex items-center gap-1 text-[10px] text-gray-400 cursor-pointer hover:text-gray-200 ${className}`}
    >
      <span className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${dot} ${probing ? 'animate-pulse' : ''}`} />
      {probing ? (
        <Loader2 className="w-3 h-3 animate-spin text-gray-500" />
      ) : known ? (
        <span className="flex items-center gap-0.5 tabular-nums">
          <ArrowUp className="w-3 h-3 text-green-500" />{seeders}
        </span>
      ) : (
        <span className="text-gray-500">seeds?</span>
      )}
    </span>
  )
}
