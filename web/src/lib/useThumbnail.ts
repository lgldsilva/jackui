import { useEffect, useRef, useState } from 'react'
import { TmdbMatch, tmdbMatch } from '../api/client'

// useThumbnail wraps the existing tmdbMatch() helper so cards across the app
// fetch a poster lazily (only once their root node enters the viewport) and
// share the module-scoped session cache + in-flight dedupe that lives in
// api/client.ts. This means N visible cards with the same cleaned title only
// trigger one HTTP request even before the server's 30-day cache fires.
//
// Usage:
//   const { ref, match, loaded } = useThumbnail<HTMLDivElement>(item.title)
//   return <div ref={ref}>{match?.posterUrl ? <img src=...> : <FallbackIcon />}</div>
//
// We deliberately do NOT use a React Query / SWR dep — the project doesn't ship
// one and the duplicate-request guard already lives in api/client.ts. Adding a
// hook layer here keeps the per-card boilerplate to one line.
export type UseThumbnailResult<T extends HTMLElement> = {
  ref: React.RefObject<T>
  match: TmdbMatch | null
  // `loaded` flips true once a fetch attempt completed (success OR null result).
  // Useful to swap fallback icons in only after we know there's no poster
  // instead of flashing them while the request is still in flight.
  loaded: boolean
}

export function useThumbnail<T extends HTMLElement = HTMLDivElement>(
  title: string | undefined | null,
): UseThumbnailResult<T> {
  const ref = useRef<T>(null)
  const [match, setMatch] = useState<TmdbMatch | null>(null)
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    if (!title) { setLoaded(true); return }
    const el = ref.current
    if (!el) return

    let cancelled = false
    const fire = () => {
      tmdbMatch(title).then(m => {
        if (cancelled) return
        if (m) setMatch(m)
        setLoaded(true)
      }).catch(() => {
        if (!cancelled) setLoaded(true)
      })
    }

    // IntersectionObserver may not be available in some test/jsdom contexts.
    // Fall back to firing immediately so the hook still works (the dedupe
    // cache prevents stampeding).
    if (typeof IntersectionObserver === 'undefined') {
      fire()
      return () => { cancelled = true }
    }
    const obs = new IntersectionObserver((entries, observer) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        observer.disconnect()
        fire()
        return
      }
    }, { rootMargin: '120px' /* prefetch slightly before the card scrolls in */ })
    obs.observe(el)
    return () => { cancelled = true; obs.disconnect() }
  }, [title])

  return { ref, match, loaded }
}
