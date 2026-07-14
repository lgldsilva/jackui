// ffprobe de tracks embutidos (áudio/legenda/capítulos). Extraído de stream.ts (R3).
import { api } from './http'
import { isLocalHash, localQS, parseLocalHash } from './local-base'
import type { StreamProbe } from './stream-types'

export const streamProbe = async (hash: string, fileIdx: number): Promise<StreamProbe> => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    const { data } = await api.get<StreamProbe>(`/local/probe?${localQS(loc.mount, loc.path)}`)
    return data
  }
  const { data } = await api.get<StreamProbe>(`/stream/probe/${hash}/${fileIdx}`)
  return data
}
