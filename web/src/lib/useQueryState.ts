// URL query-string state, generalized from the LocalPage `updateNavigation`
// pattern. Putting "what's open" (active tab, filters, open item) in the URL makes
// browser back/forward, reload and PWA reopen restore it for free — the
// RouteRestorer already persists pathname+search.
//
// CRITICAL: every write merges over `globalThis.location.search` (the LIVE query,
// not the snapshot this component rendered with) and only sets/deletes the named
// keys. This preserves params written by other effects in the same tick — above
// all the PlayerProvider's `?play=&f=&t=`. Rebuilding the query from scratch would
// strip `?play=`, the provider would close() the player, and its unmount cleanup
// would streamDrop the torrent mid-transcode (corrupt packets → SRC_NOT_SUPPORTED).

import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

export type QuerySetter = (
  updates: Record<string, string | null>,
  opts?: { replace?: boolean },
) => void

// mergeQuery applies updates over an existing query string and returns the new one:
// set a key to a non-empty value, or DELETE it when the value is null/'' (so the
// URL stays clean). Pure + exported so the merge semantics — including "preserves
// untouched keys like ?play=" — are unit-tested without a DOM.
export function mergeQuery(currentSearch: string, updates: Record<string, string | null>): string {
  const params = new URLSearchParams(currentSearch)
  for (const [k, v] of Object.entries(updates)) {
    if (v === null || v === '') params.delete(k)
    else params.set(k, v)
  }
  return params.toString()
}

// pickEnum returns raw when it's one of the allowed values, else the fallback.
// Pure + exported so the validation is unit-tested directly.
export function pickEnum<T extends string>(raw: string | null, allowed: readonly T[], fallback: T): T {
  return raw != null && (allowed as readonly string[]).includes(raw) ? (raw as T) : fallback
}

// useQuerySetter returns an atomic multi-key setter. Use it when one handler must
// change several params at once (e.g. "clear all filters") — calling several
// single-key useQueryParam setters in the same tick would each read a stale
// location.search and clobber the others.
export function useQuerySetter(): QuerySetter {
  const [, setSearchParams] = useSearchParams()
  return useCallback<QuerySetter>((updates, opts) => {
    setSearchParams(mergeQuery(globalThis.location.search, updates), { replace: opts?.replace ?? true })
  }, [setSearchParams])
}

// useQueryParam is a useState-like [value, setValue] backed by one query param.
// Reading goes through useSearchParams so the component re-renders on URL change
// (back/forward works). `fallback` is returned when the param is absent, and
// writing the fallback DELETES the param so the URL stays clean (/downloads, not
// /downloads?tab=all). Default replace:true (filters/tabs don't grow history);
// pass {replace:false} for "open an item/modal" so Back closes it.
export function useQueryParam(
  key: string,
  fallback = '',
  opts?: { replace?: boolean },
): [string, (v: string) => void] {
  const [searchParams] = useSearchParams()
  const setQuery = useQuerySetter()
  const value = searchParams.get(key) ?? fallback
  const setValue = useCallback((v: string) => {
    setQuery({ [key]: v === fallback ? null : v }, { replace: opts?.replace ?? true })
  }, [key, fallback, opts?.replace, setQuery])
  return [value, setValue]
}

// useEnumQueryParam validates the raw value against an allowed set, so a
// hand-edited or stale URL (?tab=garbage) falls back instead of selecting a
// non-existent tab.
export function useEnumQueryParam<T extends string>(
  key: string,
  allowed: readonly T[],
  fallback: T,
  opts?: { replace?: boolean },
): [T, (v: T) => void] {
  const [raw, setRaw] = useQueryParam(key, fallback, opts)
  return [pickEnum(raw, allowed, fallback), setRaw as (v: T) => void]
}
