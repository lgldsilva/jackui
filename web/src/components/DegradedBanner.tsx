import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getStatus } from '../api/client'
import { DEGRADED_POLL_MS, isReadOnlyDegraded } from '../lib/degraded'
import { StatusBanner } from './StatusBanner'

/** Read-only notice for an edge node whose primary database is unreachable. */
export function DegradedBanner() {
  const { t } = useTranslation()
  const [degraded, setDegraded] = useState(false)

  useEffect(() => {
    let alive = true
    const poll = () => {
      getStatus()
        .then((s) => { if (alive) setDegraded(isReadOnlyDegraded(s)) })
        .catch(() => { /* node unreachable: other error states cover it */ })
    }
    poll()
    const id = globalThis.setInterval(poll, DEGRADED_POLL_MS)
    return () => { alive = false; globalThis.clearInterval(id) }
  }, [])

  if (!degraded) return null
  return (
    <div className="sticky top-0 z-[80] px-3 pt-2">
      <StatusBanner variant="warning" title={t('degraded.title')}>
        {t('degraded.body')}
      </StatusBanner>
    </div>
  )
}
