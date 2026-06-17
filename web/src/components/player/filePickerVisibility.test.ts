import { describe, it, expect } from 'vitest'
import { computeFilePickerState } from './filePickerVisibility'
import type { TorrentInfo } from '../../api/client'

describe('computeFilePickerState', () => {
  it('deve lidar graciosamente com info sendo null no carregamento inicial', () => {
    const state = computeFilePickerState({
      info: null,
      minimized: false,
      sidebarOpen: true,
      aggregateMode: false,
    })

    expect(state.showFilePicker).toBe(false)
    expect(state.showReopenTab).toBe(false)
    expect(state.fileCount).toBe(0)
  })

  it('deve retornar os estados corretos para múltiplos arquivos', () => {
    const mockInfo = {
      infoHash: 'hash',
      name: 'Test Torrent',
      files: [
        { index: 0, path: 'file1.mp4', size: 100, isVideo: true },
        { index: 1, path: 'file2.mp4', size: 200, isVideo: true },
      ],
      totalSize: 300,
    } as unknown as TorrentInfo

    const state = computeFilePickerState({
      info: mockInfo,
      minimized: false,
      sidebarOpen: true,
      aggregateMode: false,
    })

    expect(state.showFilePicker).toBe(true)
    expect(state.showReopenTab).toBe(false)
    expect(state.fileCount).toBe(2)
  })
})
