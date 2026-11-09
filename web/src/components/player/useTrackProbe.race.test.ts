import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { StreamProbe, TorrentInfo } from '../../api/client'
import { useTrackProbe } from './playerHooks'

const probes = new Map<number, (p: StreamProbe) => void>()
vi.mock('../../api/client', async (orig) => ({
  ...(await orig<typeof import('../../api/client')>()),
  streamProbe: (_hash: string, file: number) => new Promise((r) => probes.set(file, r)),
  streamSidecars: () => Promise.resolve([]),
}))

afterEach(() => { cleanup(); probes.clear() })

const info = { infoHash: 'a'.repeat(40) } as TorrentInfo
const probeOf = (lang: string) => ({ audio: [], subtitles: [{ index: 3, language: lang }] } as unknown as StreamProbe)

describe('useTrackProbe', () => {
  // Regression: ep1's probe resolving after the user moved to ep2 was applied
  // to ep2 (tracks + auto-picked subtitle persisted under ep2's key).
  it('drops a probe that resolves after the file changed', async () => {
    const setProbe = vi.fn()
    const setEmbeddedSub = vi.fn()
    const base = {
      info, serverReady: true, subActive: null, embeddedSub: null,
      setProbe, setEmbeddedSub, setAutoSource: vi.fn(), setSidecars: vi.fn(), setSidecarIdx: vi.fn(),
    }
    const { rerender } = renderHook((p: { file: number }) => useTrackProbe({ ...base, selectedFile: p.file }), {
      initialProps: { file: 1 },
    })
    rerender({ file: 2 })

    await act(async () => { probes.get(1)!(probeOf('por')) })
    expect(setProbe).not.toHaveBeenCalled()
    expect(setEmbeddedSub).not.toHaveBeenCalled()

    await act(async () => { probes.get(2)!(probeOf('eng')) })
    expect(setProbe).toHaveBeenCalledTimes(1)
    expect(setProbe.mock.calls[0][0].subtitles[0].language).toBe('eng')
  })
})
