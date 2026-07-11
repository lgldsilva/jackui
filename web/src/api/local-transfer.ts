// Throughput snapshot de um arquivo local em reprodução (caso rclone/Drive: o
// play busca silenciosamente pela rede). Extraído de local.ts (#417 follow-up).
import { api } from './http'
import { localQS } from './local-base'

// LocalTransfer is the throughput snapshot for a playing local file, used to
// show "downloading X MB/s" / "waiting for data" — the rclone/Drive case where
// a play silently fetches over the network.
export type LocalTransfer = {
  key?: string
  bytesRead: number
  ratePerSec: number
  size: number
  active: boolean
  stalled: boolean
}

// localTransferStatus polls the read throughput for a playing local file. It is
// cheap (no ffprobe) so the player can call it every couple of seconds.
export const localTransferStatus = async (mount: string, path: string): Promise<LocalTransfer | null> => {
  try {
    const { data, status } = await api.get<LocalTransfer>(
      `/local/transfer-status?${localQS(mount, path)}`,
      { validateStatus: () => true },
    )
    return status === 200 ? data : null
  } catch {
    return null
  }
}
