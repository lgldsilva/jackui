import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { StreamProbe, TorrentInfo } from '../../api/client'
import { useTrackProbe } from './playerHooks'

const probes = new Map<number, (p: StreamProbe) => void>()
const sidecars = new Map<number, (l: unknown[]) => void>()
vi.mock('../../api/client', async (orig) => ({
  ...(await orig<typeof import('../../api/client')>()),
  streamProbe: (_hash: string, file: number) => new Promise((r) => probes.set(file, r)),
  streamSidecars: (_hash: string, file: number) => new Promise((r) => sidecars.set(file, r)),
}))

afterEach(() => { cleanup(); probes.clear(); sidecars.clear() })

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

  it('drops a sidecar list for the previous file and auto-picks pt for the current one', async () => {
    const setSidecars = vi.fn()
    const setSidecarIdx = vi.fn()
    const base = {
      info, serverReady: true, subActive: null, embeddedSub: null,
      setProbe: vi.fn(), setEmbeddedSub: vi.fn(), setAutoSource: vi.fn(), setSidecars, setSidecarIdx,
    }
    const { rerender } = renderHook((p: { file: number }) => useTrackProbe({ ...base, selectedFile: p.file }), {
      initialProps: { file: 1 },
    })
    rerender({ file: 2 })
    await act(async () => { sidecars.get(1)!([{ index: 9, language: 'pt' }]) })
    expect(setSidecars).not.toHaveBeenCalled()

    await act(async () => { sidecars.get(2)!([{ index: 4, language: 'por' }]) })
    expect(setSidecars).toHaveBeenCalledTimes(1)
    expect(setSidecarIdx).toHaveBeenCalledWith(4)
  })
})
