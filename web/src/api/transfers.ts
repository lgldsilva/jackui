import { api } from './http'

// ─── Global file-movement progress ─────────────────────────────────────────
// One consistent shape for every move/copy in JackUI: the post-download move,
// Local-tab moves, and promote/AI-rename. Mirrors internal/transfer.Snapshot.

export type TransferStatus = 'queued' | 'running' | 'done' | 'failed' | 'canceled'

export type TransferKind = 'download-move' | 'local-move' | 'promote' | 'ai-rename'

export type TransferSnapshot = {
  id: string
  label: string
  kind: TransferKind | string
  status: TransferStatus
  filesDone: number
  filesTotal: number
  bytesDone: number
  bytesTotal: number
  ratePerSec: number
  etaSeconds: number
  progress: number // 0..1
  error?: string
  startedAt: string
}

// transfersList polls the single dock endpoint: active + recently-finished jobs.
export const transfersList = async (): Promise<TransferSnapshot[]> => {
  const { data } = await api.get<{ transfers: TransferSnapshot[] }>('/transfers')
  return data.transfers ?? []
}

// transferCancel aborts an in-flight move/copy (the dock's stop button). The
// backend cancels the job's context so the copy/retries stop and it flips to
// "canceled".
export const transferCancel = async (id: string): Promise<void> => {
  await api.delete(`/transfers/${encodeURIComponent(id)}`)
}
