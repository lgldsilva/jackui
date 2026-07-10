import { useCallback, useEffect, useState } from 'react'
import { LocalEntry, localSetHidden, localListHidden } from '../../api/client'

// Hidden curtain (global easter egg): hidden entries drop from the list unless
// it's open. hiddenSet (paths in the active mount) flags which rows are hidden
// so the row shows a "Mostrar" action + indicator while revealed.
export function useHiddenEntries(activeMount: string, revealHidden: boolean, refresh: () => void) {
  const [hiddenSet, setHiddenSet] = useState<Set<string>>(new Set())

  // Which entries in this mount are hidden — flags them + offers "Mostrar" while
  // the curtain is open (closed → they're filtered server-side, empty set is ok).
  const loadHidden = useCallback(() => {
    if (!activeMount) { setHiddenSet(new Set()); return }
    localListHidden()
      .then((paths) => setHiddenSet(new Set(paths.filter((p) => p.mount === activeMount).map((p) => p.path))))
      .catch(() => setHiddenSet(new Set()))
  }, [activeMount])
  useEffect(() => {
    loadHidden()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeMount, revealHidden])

  const handleToggleHidden = useCallback(async (e: LocalEntry) => {
    await localSetHidden(activeMount, e.path, !hiddenSet.has(e.path))
    loadHidden()
    refresh()
  }, [activeMount, hiddenSet, loadHidden, refresh])

  return { hiddenSet, handleToggleHidden }
}
