import { useRef, useEffect, useCallback, type Dispatch, type SetStateAction } from 'react'
import { useTranslation } from 'react-i18next'
import { SearchResult, withToken } from '../../api/client'
import { isIncognito } from '../../lib/incognito'
import { openSearchStream, type SearchStreamHandle } from '../../lib/searchStream'
import { appendResult, setErrorMsg, type TabState } from '../../lib/searchTabs'

// Gerencia as conexões SSE de busca (uma por aba) e expõe start/stop/close.
// Extraído do SearchPage (god-file): o Map de EventSources, o handleSearch e o
// stopSearch viviam soltos no componente. `closeStream` é usado por quem fecha
// uma aba para cancelar a busca em voo daquela aba.
export function useSearchStreams(
  tabs: TabState[],
  updateTab: (id: string, patch: Partial<TabState>) => void,
  setTabs: Dispatch<SetStateAction<TabState[]>>,
) {
  const { t } = useTranslation()
  const esMap = useRef<Map<string, SearchStreamHandle>>(new Map())

  useEffect(() => {
    return () => { esMap.current.forEach(es => es.close()) }
  }, [])

  const closeStream = useCallback((id: string) => {
    const es = esMap.current.get(id)
    if (es) { es.close(); esMap.current.delete(id) }
  }, [])

  const handleSearch = useCallback((tabId: string, queryOverride?: string) => {
    const tab = tabs.find(t => t.id === tabId)
    // queryOverride lets a caller (e.g. the Discover page seeding via ?q=) run a
    // search before the tab's query state has propagated through a re-render.
    const q = (queryOverride ?? tab?.query ?? '').trim()
    if (!tab || !q) return

    const existing = esMap.current.get(tabId)
    if (existing) { existing.close(); esMap.current.delete(tabId) }

    updateTab(tabId, { results: [], error: '', summary: null, phase: 'cache' })

    const params = new URLSearchParams({ q })
    if (tab.selectedIndexers.length > 0 && tab.selectedIndexers[0] !== 'all')
      params.set('indexers', tab.selectedIndexers.join(','))
    if (tab.selectedCategory && tab.selectedCategory !== 'all')
      params.set('category', tab.selectedCategory)
    if (isIncognito()) params.set('incognito', '1')

    // EventSource can't set Authorization header — inject Bearer as query token
    // instead (the middleware's extractToken() reads ?token= as a fallback).
    // openSearchStream owns the connection: a drop before `done` reconnects
    // with backoff while the tab stays in 'live' (the backend's cache phase
    // re-emits what already arrived; appendResult dedupes the replay). Only
    // after the retry budget is exhausted does the tab go to 'error'.
    const handle = openSearchStream(withToken(`/api/search/stream?${params}`), {
      onResult: (result) => setTabs(prev => appendResult(prev, tabId, result as SearchResult)),
      onLive: () => updateTab(tabId, { phase: 'live' }),
      onServerError: (message) => setTabs(prev => setErrorMsg(prev, tabId, message)),
      onDone: (summary) => {
        esMap.current.delete(tabId)
        updateTab(tabId, { summary: summary as TabState['summary'], phase: 'done' })
      },
      onGiveUp: () => {
        esMap.current.delete(tabId)
        updateTab(tabId, { phase: 'error', error: t('search.connection_lost') })
      },
    })
    esMap.current.set(tabId, handle)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabs, updateTab])

  // Abort the in-flight search for a tab. Closing the EventSource cancels the
  // request on the backend (the SSE handler watches c.Request.Context()), so the
  // indexers stop being polled. Partial results already received stay on screen;
  // the phase goes to 'done' (not 'error') since the stop was intentional.
  const stopSearch = useCallback((tabId: string) => {
    const es = esMap.current.get(tabId)
    if (es) { es.close(); esMap.current.delete(tabId) }
    updateTab(tabId, { phase: 'done' })
  }, [updateTab])

  return { handleSearch, stopSearch, closeStream }
}
