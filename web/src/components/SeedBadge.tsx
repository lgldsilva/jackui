import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUp, Loader2 } from 'lucide-react'
import i18n from '../lib/i18n'
import { streamHealth, StreamHealth } from '../api/client'

// SeedBadge shows a torrent's swarm health on a card: a dot (green = available,
// gray = no seeds, amber = checking) plus the seed count and how long ago it was
// checked. It fetches lazily (only when scrolled into view) and, when the server
// reports a stale snapshot is being re-probed, polls once more to pick up the
// fresh numbers. The server caps/queues the actual swarm probing.

function relTime(iso?: string): string {
  if (!iso) return ''
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 90) return i18n.t('misc.rel_now')
  const m = s / 60
  if (m < 90) return i18n.t('misc.rel_min', { n: Math.round(m) })
  const h = m / 60
  if (h < 36) return i18n.t('misc.rel_hour', { n: Math.round(h) })
  return i18n.t('misc.rel_day', { n: Math.round(h / 24) })
}

type Props = {
  readonly infoHash?: string
  readonly magnet?: string
  readonly className?: string
  // Increment to force a swarm re-probe from a parent (e.g. "atualizar seeds"
  // by folder). Same effect as a user click; ignored while already probing.
  readonly refreshSignal?: number
  // When true, automatically scrape (real tracker numbers) on first view if no
  // snapshot is cached yet — used on search cards so the count isn't just the
  // indexer's. Off elsewhere to keep scrolling cheap.
  readonly autoProbe?: boolean
}

export default function SeedBadge({ infoHash, magnet, className = '', refreshSignal, autoProbe = false }: Props) {
  const { t } = useTranslation()
  const ref = useRef<HTMLButtonElement>(null)
  const [health, setHealth] = useState<StreamHealth | null>(null)
  const [probing, setProbing] = useState(false)
  const fetchedRef = useRef(false)
  const probingRef = useRef(false)

  // Explicit swarm scrape. Triggered by a user click, a parent's refreshSignal,
  // or autoProbe-on-view. probingRef guards overlap. Stable across renders so the
  // effects below can depend on it.
  const runProbe = useCallback(async () => {
    if (!infoHash || probingRef.current) return
    probingRef.current = true
    setProbing(true)
    const h = await streamHealth(infoHash, magnet, true)
    setHealth(h)
    if (h.refreshing) {
      setTimeout(async () => { setHealth(await streamHealth(infoHash, magnet, false)); probingRef.current = false; setProbing(false) }, 9000)
    } else {
      probingRef.current = false
      setProbing(false)
    }
  }, [infoHash, magnet])

  // On view: PEEK (persisted/live — cheap). With autoProbe, if nothing is cached
  // yet, kick a real scrape so search cards show the tracker's number, not the
  // indexer's. Already-cached cards just show the cache (no scrape on scroll).
  useEffect(() => {
    if (!infoHash) return
    const el = ref.current
    if (!el) return
    let cancelled = false
    const peek = async () => {
      const h = await streamHealth(infoHash, magnet, false)
      if (cancelled) return
      setHealth(h)
      if (autoProbe && !h.known) {
        runProbe().catch(() => { /* fire-and-forget probe */ })
      }
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
  }, [infoHash, magnet, autoProbe, runProbe])

  // Batch refresh from a parent: re-probe when the signal changes (skips the
  // initial undefined/0 so it never probes on mount).
  useEffect(() => {
    if (refreshSignal) {
      runProbe().catch(() => { /* fire-and-forget probe */ })
    }
  }, [refreshSignal, runProbe])

  if (!infoHash) return null

  const verify = (e?: React.MouseEvent) => {
    e?.stopPropagation(); e?.preventDefault()
    runProbe().catch(() => { /* fire-and-forget probe */ })
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
    dot = 'bg-surface-tertiary'
  }
  let title: string
  if (probing) {
    title = t('misc.seed_checking')
  } else if (known) {
    const liveSuffix = health?.active ? t('misc.seed_live_suffix') : ''
    title = `${t('misc.seed_summary', { seeders, peers: health?.peers ?? 0, time: relTime(health?.checkedAt) })}${liveSuffix}`
  } else {
    title = t('misc.seed_click')
  }

  return (
    <button
      ref={ref}
      type="button"
      onClick={verify}
      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); verify() } }}
      title={title}
      className={`inline-flex items-center gap-1 text-[10px] text-text-secondary cursor-pointer hover:text-text-primary ${className}`}
    >
      <span className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${dot} ${probing ? 'animate-pulse' : ''}`} />
{(() => {
        if (probing) return <Loader2 className="w-3 h-3 animate-spin text-text-muted" />
        if (known) return <span className="flex items-center gap-0.5 tabular-nums"><ArrowUp className="w-3 h-3 text-green-500" />{seeders}</span>
        return <span className="text-text-muted">seeds?</span>
      })()}
    </button>
  )
}
