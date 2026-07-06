import { describe, it, expect, vi } from 'vitest'
import { autoDownloadNextFile } from './playerEffects'
import type { TorrentInfo, StreamFile } from '../../api/client'

function mkInfo(files: StreamFile[], hash = 'abc123'): TorrentInfo {
  return {
    infoHash: hash,
    name: 'Test Torrent',
    totalSize: files.reduce((s, f) => s + f.size, 0),
    files,
    peers: 5,
    seeders: 3,
    downRate: 100_000,
    upRate: 0,
    progress: 0,
    primaryFile: 0,
  }
}

function mkFile(overrides: Partial<StreamFile> & { index: number }): StreamFile {
  return {
    index: overrides.index,
    path: overrides.path ?? `File${overrides.index}.mkv`,
    size: overrides.size ?? 1_000_000,
    isVideo: overrides.isVideo ?? true,
    downloaded: overrides.downloaded ?? 0,
    progress: overrides.progress ?? 0,
    priority: overrides.priority ?? 'normal',
  }
}

describe('autoDownloadNextFile', () => {
  it('no-ops when info is null', () => {
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: null, selectedFile: 0, nextIdx: 1, doneRef: { current: new Set() }, incognito: false, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('no-ops when selectedFile is negative', () => {
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo([]), selectedFile: -1, nextIdx: 1, doneRef: { current: new Set() }, incognito: false, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('no-ops when nextIdx is negative (last file)', () => {
    const files = [mkFile({ index: 0, downloaded: 1_000_000, size: 1_000_000 })]
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: -1, doneRef: { current: new Set() }, incognito: false, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('no-ops when incognito is true', () => {
    const files = [
      mkFile({ index: 0, downloaded: 1_000_000, size: 1_000_000 }),
      mkFile({ index: 1 }),
    ]
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef: { current: new Set() }, incognito: true, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('no-ops when infoHash is a local pseudo-hash', () => {
    const files = [
      mkFile({ index: 0, downloaded: 1_000_000, size: 1_000_000 }),
      mkFile({ index: 1 }),
    ]
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files, 'local-mount1-/path'), selectedFile: 0, nextIdx: 1, doneRef: { current: new Set() }, incognito: false, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('no-ops when current file is not found in info.files', () => {
    const files = [mkFile({ index: 1 })]
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef: { current: new Set() }, incognito: false, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('no-ops when current file is not fully downloaded', () => {
    const files = [
      mkFile({ index: 0, downloaded: 500_000, size: 1_000_000 }),
      mkFile({ index: 1 }),
    ]
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef: { current: new Set() }, incognito: false, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('no-ops when next file is already fully downloaded', () => {
    const files = [
      mkFile({ index: 0, downloaded: 1_000_000, size: 1_000_000 }),
      mkFile({ index: 1, downloaded: 2_000_000, size: 2_000_000 }),
    ]
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef: { current: new Set() }, incognito: false, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('no-ops when nextIdx is already in doneRef (prevents duplicate enqueue)', () => {
    const files = [
      mkFile({ index: 0, downloaded: 1_000_000, size: 1_000_000 }),
      mkFile({ index: 1 }),
    ]
    const doneRef = { current: new Set([1]) }
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef, incognito: false, onEnqueue })
    expect(onEnqueue).not.toHaveBeenCalled()
  })

  it('calls onEnqueue and marks doneRef when current file is fully downloaded', () => {
    const files = [
      mkFile({ index: 0, downloaded: 1_000_000, size: 1_000_000 }),
      mkFile({ index: 1 }),
    ]
    const doneRef = { current: new Set<number>() }
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef, incognito: false, onEnqueue })
    expect(onEnqueue).toHaveBeenCalledTimes(1)
    expect(onEnqueue).toHaveBeenCalledWith(1)
    expect(doneRef.current.has(1)).toBe(true)
  })

  it('fires only once per next file (doneRef guard)', () => {
    const files = [
      mkFile({ index: 0, downloaded: 1_000_000, size: 1_000_000 }),
      mkFile({ index: 1 }),
    ]
    const doneRef = { current: new Set<number>() }
    const onEnqueue = vi.fn()
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef, incognito: false, onEnqueue })
    // Second call with the same props should no-op.
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef, incognito: false, onEnqueue })
    expect(onEnqueue).toHaveBeenCalledTimes(1)
  })

  it('enqueues next-of-next when the user advances (new selectedFile, fresh nextIdx)', () => {
    // Scenario: watching files in order 0→1→2.
    // File 0 finishes downloading → auto-enqueue file 1.
    // User finishes watching file 0 → advances to file 1 → file 1 streams.
    // File 1 finishes downloading → auto-enqueue file 2.
    const files = [
      mkFile({ index: 0, downloaded: 1_000_000, size: 1_000_000 }),
      mkFile({ index: 1, downloaded: 500_000, size: 2_000_000 }),   // still downloading
      mkFile({ index: 2, downloaded: 0, size: 1_500_000 }),         // not started
    ]
    const doneRef = { current: new Set<number>() }
    const onEnqueue = vi.fn()
    // File 0 completes → enqueue file 1.
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 0, nextIdx: 1, doneRef, incognito: false, onEnqueue })
    expect(onEnqueue).toHaveBeenCalledTimes(1)
    expect(onEnqueue).toHaveBeenCalledWith(1)
    // Later, file 1 completes → enqueue file 2 (different nextIdx, not in doneRef).
    // Update file 1's download progress to "done".
    files[1].downloaded = 2_000_000
    autoDownloadNextFile({ info: mkInfo(files), selectedFile: 1, nextIdx: 2, doneRef, incognito: false, onEnqueue })
    expect(onEnqueue).toHaveBeenCalledTimes(2)
    expect(onEnqueue).toHaveBeenLastCalledWith(2)
  })
})
