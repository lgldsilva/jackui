import { createContext, useCallback, useContext, useEffect, useRef, useState, ReactNode } from 'react'
import { transfersList, type TransferSnapshot } from '../api/transfers'
import { useAuth } from '../auth/AuthContext'

// TransfersProvider polls GET /api/transfers and exposes the live move/copy jobs
// to the global Transfers dock. Polling is self-driving: fast (~1s) while any job
// is present, slow (~5s) when idle — background polling is required because the
// post-download move starts server-side with no frontend action to trigger it.

const POLL_ACTIVE_MS = 1000
const POLL_IDLE_MS = 5000

type TransfersAPI = {
  readonly transfers: readonly TransferSnapshot[]
  /** Force an immediate refresh — call right after kicking off a move/promote. */
  readonly bump: () => void
}

const Ctx = createContext<TransfersAPI | null>(null)

export function TransfersProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  const [transfers, setTransfers] = useState<TransferSnapshot[]>([])
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const stopped = useRef(false)

  const poll = useCallback(async () => {
    if (stopped.current) return
    let next = POLL_IDLE_MS
    try {
      const list = await transfersList()
      if (stopped.current) return
      setTransfers(list)
      // Keep polling fast while anything is still running.
      next = list.some((t) => t.status === 'running') ? POLL_ACTIVE_MS : POLL_IDLE_MS
    } catch {
      // transient (e.g. token refresh) — back off to idle and try again
    }
    if (!stopped.current) {
      timer.current = setTimeout(poll, next)
    }
  }, [])

  const bump = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    void poll()
  }, [poll])

  useEffect(() => {
    stopped.current = false
    if (!user) {
      // Logged out: stop polling and clear any leftover jobs.
      setTransfers([])
      return () => { stopped.current = true }
    }
    void poll()
    return () => {
      stopped.current = true
      if (timer.current) clearTimeout(timer.current)
    }
  }, [user, poll])

  return <Ctx.Provider value={{ transfers, bump }}>{children}</Ctx.Provider>
}

export function useTransfers(): TransfersAPI {
  const ctx = useContext(Ctx)
  if (!ctx) return { transfers: [], bump: () => {} }
  return ctx
}

// useTrackedJobs lets a modal show live progress for the move/copy jobs IT just
// started, without the backend having to return job IDs: call start() right
// before kicking off the operation (it snapshots the existing job IDs + forces an
// immediate poll), then render `jobs` — the transfers of `kind` that appeared
// AFTER the snapshot. reset() clears the baseline (e.g. when the modal closes).
export function useTrackedJobs(kind: string) {
  const { transfers, bump } = useTransfers()
  const baseline = useRef<Set<string> | null>(null)
  const start = useCallback(() => {
    baseline.current = new Set(transfers.map((t) => t.id))
    bump()
  }, [transfers, bump])
  const reset = useCallback(() => { baseline.current = null }, [])
  const base = baseline.current
  const jobs = base ? transfers.filter((t) => t.kind === kind && !base.has(t.id)) : []
  return { start, reset, jobs, bump }
}
