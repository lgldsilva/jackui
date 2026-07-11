import { useState, useEffect, useMemo } from 'react'
import type { Indexer } from '../../api/client'
import { load, save } from '../../lib/storage'
import type { TabState } from '../../lib/searchTabs'

// Coleta e persiste indexadores autodescobertos a partir dos resultados de busca
// e devolve a lista unificada (configurados + descobertos, deduplicada por nome).
// Extraído do SearchPage (god-file): mantém o estado + os dois effects + o memo
// num só lugar, recebendo as abas e os indexadores configurados por parâmetro.
export function useDiscoveredIndexers(tabs: TabState[], indexers: Indexer[]): Indexer[] {
  const [discoveredIndexers, setDiscoveredIndexers] = useState<Indexer[]>([])

  // Carrega indexadores autodescobertos persistidos
  useEffect(() => {
    setDiscoveredIndexers(load<Indexer[]>('discoveredIndexers', []))
  }, [])

  // Coleta novos indexadores a partir dos resultados de busca
  useEffect(() => {
    if (tabs.length === 0) return
    const allResults = tabs.flatMap(t => t.results)
    if (allResults.length === 0) return

    const discoveredMap = new Map<string, Indexer>()
    discoveredIndexers.forEach(idx => discoveredMap.set(idx.id, idx))

    let mutated = false
    allResults.forEach(r => {
      const id = r.trackerId || r.tracker.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-')
      if (!id) return
      if (discoveredMap.has(id)) return  // already known by this ID

      // When a real trackerId arrives, evict any stale entry with the same
      // display name but a different (possibly synthetic) ID. Without this,
      // re-discovering "Amigos Share Club" under its real Jackett ID leaves
      // the old synthetic-id entry, causing the same indexer to appear twice.
      if (r.trackerId) {
        const nameLower = r.tracker.toLowerCase()
        for (const [staleId, stale] of discoveredMap) {
          if (stale.name.toLowerCase() === nameLower && staleId !== id) {
            discoveredMap.delete(staleId)
            break
          }
        }
      }

      discoveredMap.set(id, {
        id,
        name: r.tracker,
        description: `Descoberto via busca (${r.tracker})`,
        configured: true,
        language: '',
        type: ''
      })
      mutated = true
    })

    if (mutated) {
      const nextList = Array.from(discoveredMap.values())
      setDiscoveredIndexers(nextList)
      save('discoveredIndexers', nextList)
    }
  }, [tabs, discoveredIndexers])

  return useMemo(() => {
    const map = new Map<string, Indexer>()
    indexers.forEach(i => map.set(i.id, i))
    discoveredIndexers.forEach(i => map.set(i.id, i))
    // Final dedup by display name: prevents a stale entry (old synthetic ID)
    // from appearing alongside a newer entry with the same name but a real ID.
    const byName = new Map<string, Indexer>()
    for (const idx of map.values()) {
      const key = idx.name.toLowerCase().trim()
      const existing = byName.get(key)
      if (existing) {
        // Prefer the entry whose ID is NOT the synthetic derivation of the name
        const synthetic = idx.name.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-')
        if (existing.id === synthetic && idx.id !== synthetic) {
          byName.set(key, idx)
        }
      } else {
        byName.set(key, idx)
      }
    }
    return Array.from(byName.values())
  }, [indexers, discoveredIndexers])
}
