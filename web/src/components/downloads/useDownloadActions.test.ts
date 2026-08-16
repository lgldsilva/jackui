import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, cleanup } from '@testing-library/react'
import { useDownloadActions } from './useDownloadActions'
import { newPendingDeletes } from '../../lib/downloadsReconcile'
import type { DownloadEntry } from '../../api/client'

const mocks = {
  downloadPause: vi.fn(),
  downloadResume: vi.fn(),
  downloadDelete: vi.fn(),
  downloadBatchPause: vi.fn(),
  downloadBatchResume: vi.fn(),
  downloadBatchDelete: vi.fn(),
  downloadBatchStopSeed: vi.fn(),
  downloadStopSeed: vi.fn(),
  downloadSetPriority: vi.fn(),
  downloadPauseAll: vi.fn(),
  downloadResumeAll: vi.fn(),
  streamPause: vi.fn(),
  streamResume: vi.fn(),
  streamDrop: vi.fn(),
  streamDropBatch: vi.fn(),
  streamSetPriority: vi.fn(),
  streamSetLimits: vi.fn(),
  streamPauseAll: vi.fn(),
  streamResumeAll: vi.fn(),
}

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    downloadPause: (...args: unknown[]) => mocks.downloadPause(...args),
    downloadResume: (...args: unknown[]) => mocks.downloadResume(...args),
    downloadDelete: (...args: unknown[]) => mocks.downloadDelete(...args),
    downloadBatchPause: (...args: unknown[]) => mocks.downloadBatchPause(...args),
    downloadBatchResume: (...args: unknown[]) => mocks.downloadBatchResume(...args),
    downloadBatchDelete: (...args: unknown[]) => mocks.downloadBatchDelete(...args),
    downloadBatchStopSeed: (...args: unknown[]) => mocks.downloadBatchStopSeed(...args),
    downloadStopSeed: (...args: unknown[]) => mocks.downloadStopSeed(...args),
    downloadSetPriority: (...args: unknown[]) => mocks.downloadSetPriority(...args),
    downloadPauseAll: (...args: unknown[]) => mocks.downloadPauseAll(...args),
    downloadResumeAll: (...args: unknown[]) => mocks.downloadResumeAll(...args),
    streamPause: (...args: unknown[]) => mocks.streamPause(...args),
    streamResume: (...args: unknown[]) => mocks.streamResume(...args),
    streamDrop: (...args: unknown[]) => mocks.streamDrop(...args),
    streamDropBatch: (...args: unknown[]) => mocks.streamDropBatch(...args),
    streamSetPriority: (...args: unknown[]) => mocks.streamSetPriority(...args),
    streamSetLimits: (...args: unknown[]) => mocks.streamSetLimits(...args),
    streamPauseAll: (...args: unknown[]) => mocks.streamPauseAll(...args),
    streamResumeAll: (...args: unknown[]) => mocks.streamResumeAll(...args),
  }
})

const confirmMock = vi.fn()
vi.mock('../ConfirmDialog', () => ({ useConfirm: () => confirmMock }))

const notifyMock = vi.fn()
const notifyErrorMock = vi.fn()
vi.mock('../Toast', () => ({ useToast: () => ({ notify: notifyMock, notifyError: notifyErrorMock }) }))

const dl = (over: Partial<DownloadEntry> & { id: number }): DownloadEntry => ({
  status: 'downloading',
  infoHash: 'hash',
  fileIndex: 0,
  name: 'x',
  filePath: '',
  fileSize: 1000,
  bytesDownloaded: 0,
  progress: 0,
  magnet: '',
  createdAt: '',
  userId: 1,
  ...over,
} as DownloadEntry)

describe('useDownloadActions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    confirmMock.mockResolvedValue(true)
    Object.values(mocks).forEach(fn => fn.mockResolvedValue({ failed: [] }))
  })

  afterEach(cleanup)

  const setup = (overrides: Partial<Parameters<typeof useDownloadActions>[0]> = {}) => {
    const items = [dl({ id: 1, infoHash: 'a' }), dl({ id: 2, infoHash: 'b', status: 'paused' })]
    const state = {
      items,
      setItems: vi.fn(),
      selected: new Set<number>(),
      setSelected: vi.fn(),
      setBusyID: vi.fn(),
      setBusyHash: vi.fn(),
      setBulkBusy: vi.fn(),
      setPromoteTargets: vi.fn(),
      pendingDeletesRef: { current: newPendingDeletes() },
      reloadDownloadsRef: { current: vi.fn().mockResolvedValue(undefined) },
      loadTorrents: vi.fn().mockResolvedValue(undefined),
      loadLimits: vi.fn(),
      mountedRef: { current: true },
      limitDownKB: '',
      limitUpKB: '',
      setLimitsSaving: vi.fn(),
      setLimitsMsg: vi.fn(),
      completedDownloads: items.filter(d => d.status === 'completed'),
      downloadsByStatus: {
        downloading: items.filter(d => d.status === 'downloading' || d.status === 'queued'),
        paused: items.filter(d => d.status === 'paused'),
        completed: [],
        failed: [],
      },
      queuedDownloads: items.filter(d => d.status === 'queued'),
      ...overrides,
    }
    const { result } = renderHook(() => useDownloadActions(state))
    return { result, state }
  }

  it('onDelete pausa, dropa stream e remove otimista', async () => {
    const { result, state } = setup()
    await act(async () => { await result.current.onDelete(1) })

    expect(mocks.downloadPause).toHaveBeenCalledWith(1)
    expect(mocks.streamDrop).toHaveBeenCalledWith('a')
    expect(mocks.downloadDelete).toHaveBeenCalledWith(1)
    expect(state.setItems).toHaveBeenCalled()
    expect(state.reloadDownloadsRef.current).toHaveBeenCalled()
    expect(state.loadTorrents).toHaveBeenCalled()
  })

  it('onDelete restaura a linha e notifica em caso de erro', async () => {
    mocks.downloadDelete.mockRejectedValue(new Error('boom'))
    const { result, state } = setup()
    await act(async () => { await result.current.onDelete(1) })

    expect(state.pendingDeletesRef.current.ids.has(1)).toBe(false)
    expect(state.reloadDownloadsRef.current).toHaveBeenCalled()
    expect(notifyErrorMock).toHaveBeenCalled()
  })

  it('runBatchDelete chama batch pause, streamDropBatch e delete', async () => {
    const { result, state } = setup({ selected: new Set([1]) })
    await act(async () => { await result.current.onBatchDelete() })

    expect(mocks.downloadBatchPause).toHaveBeenCalledWith([1])
    expect(mocks.streamDropBatch).toHaveBeenCalledWith(['a'])
    expect(mocks.downloadBatchDelete).toHaveBeenCalledWith([1])
    expect(state.setSelected).toHaveBeenCalledWith(new Set())
  })

  it('runBatchDelete notifica falhas parciais e restaura só os falhos', async () => {
    mocks.downloadBatchDelete.mockResolvedValue({ failed: [1] })
    const { result, state } = setup({ selected: new Set([1]) })
    await act(async () => { await result.current.onBatchDelete() })

    expect(state.pendingDeletesRef.current.ids.has(1)).toBe(false)
    expect(notifyMock).toHaveBeenCalled()
  })

  it('busy flags são setados e resetados mesmo em erro', async () => {
    mocks.downloadPause.mockRejectedValue(new Error('boom'))
    const { result, state } = setup()
    await act(async () => {
      await expect(result.current.onPause(1)).rejects.toThrow('boom')
    })

    expect(state.setBusyID).toHaveBeenCalledWith(1)
    expect(state.setBusyID).toHaveBeenLastCalledWith(null)
  })

  // "Parar" precisa remover o item da lista na hora (otimista) e blindá-lo
  // contra polls stale de 2s — mesmo mecanismo do delete. O bug original: o
  // stop-seed não tocava em setItems/pendingDeletes e o card continuava na
  // lista até o backend parar de retorná-lo.
  it('onStopSeed remove otimistamente da lista', async () => {
    const { result, state } = setup()
    await act(async () => { await result.current.onStopSeed(1, 'x') })

    expect(mocks.downloadStopSeed).toHaveBeenCalledWith(1)
    expect(state.setItems).toHaveBeenCalled()
    expect(state.pendingDeletesRef.current.ids.has(1)).toBe(true)
    expect(state.reloadDownloadsRef.current).toHaveBeenCalled()
    expect(state.loadTorrents).toHaveBeenCalled()
  })

  it('onStopSeed restaura a linha e notifica em caso de erro', async () => {
    mocks.downloadStopSeed.mockRejectedValue(new Error('boom'))
    const { result, state } = setup()
    await act(async () => { await result.current.onStopSeed(1, 'x') })

    expect(state.pendingDeletesRef.current.ids.has(1)).toBe(false)
    expect(state.reloadDownloadsRef.current).toHaveBeenCalled()
    expect(notifyErrorMock).toHaveBeenCalled()
  })

  it('onStopSeedMany remove otimistamente os alvos', async () => {
    const { result, state } = setup()
    const targets = [dl({ id: 3, infoHash: 'c', status: 'completed' })]
    await act(async () => { await result.current.onStopSeedMany(targets) })

    expect(mocks.downloadBatchStopSeed).toHaveBeenCalledWith([3])
    expect(state.setItems).toHaveBeenCalled()
    expect(state.pendingDeletesRef.current.ids.has(3)).toBe(true)
  })
})
