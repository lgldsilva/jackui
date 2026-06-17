import { api } from './http'

// ─── Global file-movement progress ─────────────────────────────────────────
// One consistent shape for every move/copy in JackUI: the post-download move,
// Local-tab moves, and promote/AI-rename. Mirrors internal/transfer.Snapshot.

export type TransferStatus = 'running' | 'done' | 'failed'

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
