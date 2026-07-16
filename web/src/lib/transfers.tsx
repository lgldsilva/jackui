import { createContext, useCallback, useContext, useEffect, useRef, useState, ReactNode } from 'react'
import { transfersList, transferCancel, type TransferSnapshot } from '../api/transfers'
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
  /** Cancel an in-flight move/copy (the dock's stop button). */
  readonly cancel: (id: string) => void
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
      // Keep polling fast while anything is still running OR queued.
      next = list.some((t) => t.status === 'running' || t.status === 'queued') ? POLL_ACTIVE_MS : POLL_IDLE_MS
    } catch {
      // transient (e.g. token refresh) — back off to idle and try again
    }
    // Pausa enquanto a aba está oculta: a doca de transferências em background
    // não precisa rodar se ninguém está olhando. O visibilitychange retoma no
    // foco; bump() também força um poll imediato após qualquer move/promote do
    // usuário. timer=null sinaliza "pausado" pro resume não duplicar.
    if (!stopped.current && !document.hidden) {
      timer.current = setTimeout(poll, next)
    } else {
      timer.current = null
    }
  }, [])

  const firePoll = useCallback(() => {
    poll().catch(() => { /* next interval retries */ })
  }, [poll])

  const bump = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    firePoll()
  }, [firePoll])

  // Optimistically drop the job from the dock, tell the backend to abort, then
  // refresh (the job will come back as "canceled" briefly, then prune).
  const cancel = useCallback((id: string) => {
    setTransfers((prev) => prev.filter((t) => t.id !== id))
    transferCancel(id).catch(() => {}).finally(() => bump())
  }, [bump])

  useEffect(() => {
    stopped.current = false
    if (!user) {
      // Logged out: stop polling and clear any leftover jobs.
      setTransfers([])
      return () => { stopped.current = true }
    }
    firePoll()
    // Retoma o poll ao voltar pra aba (quando estava pausado: timer.current null).
    const onVisible = () => {
      if (!document.hidden && !timer.current && !stopped.current) firePoll()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      stopped.current = true
      if (timer.current) { clearTimeout(timer.current); timer.current = null }
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [user, firePoll])

  return <Ctx.Provider value={{ transfers, bump, cancel }}>{children}</Ctx.Provider>
}

export function useTransfers(): TransfersAPI {
  const ctx = useContext(Ctx)
  if (!ctx) return { transfers: [], bump: () => {}, cancel: () => {} }
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
