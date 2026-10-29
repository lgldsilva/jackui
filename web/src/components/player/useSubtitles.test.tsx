// Lane W2 integration: useSubtitles wires the auto pipeline into the virtual
// auto subtitle state. Ready → autoSub set with the toast fired ONCE per file,
// sidecar/embedded slots untouched (no synthetic index), the choice saved to
// localStorage stays all-null (the auto pick is NEVER persisted), a quota
// failure toasts info while other failures stay quiet, and ANY manual pick
// replaces/disables the auto pick for the session.
import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '../../lib/i18n'

const mocks = vi.hoisted(() => ({
  streamProbe: vi.fn(),
  streamSidecars: vi.fn(),
  subtitlesAuto: vi.fn(),
  subtitlesSearch: vi.fn(),
  localSubtrackBlobURL: vi.fn(),
  subtitleAutoStatus: vi.fn(),
  getSubtitleSettings: vi.fn(),
  isLocalHash: vi.fn(() => false),
  notify: vi.fn(),
  notifyError: vi.fn(),
}))

vi.mock('../../api/client', async (importOriginal) => ({
  // Real module otherwise (URL builders, types) — only the network surface is faked.
  ...(await importOriginal<typeof import('../../api/client')>()),
  streamProbe: mocks.streamProbe,
  streamSidecars: mocks.streamSidecars,
  subtitlesAuto: mocks.subtitlesAuto,
  subtitlesSearch: mocks.subtitlesSearch,
  localSubtrackBlobURL: mocks.localSubtrackBlobURL,
  subtitleAutoStatus: mocks.subtitleAutoStatus,
  getSubtitleSettings: mocks.getSubtitleSettings,
  isLocalHash: mocks.isLocalHash,
}))

// Toast is a module-level pub/sub — swap it for a spy recorder.
vi.mock('../Toast', () => ({
  useToast: () => ({ notify: mocks.notify, notifyError: mocks.notifyError }),
}))

import type { SearchResult, Subtitle, TorrentInfo } from '../../api/client'
import { load } from '../../lib/storage'
import { useSubtitles } from './useSubtitles'

const HASH = 'a'.repeat(40)
const SETTINGS_ON = { default_languages: 'pt-BR,pt', auto_fetch: true, burn_default: false }
const AUTO_APPLIED_TOAST = 'PT-BR subtitle applied'
const AUTO_QUOTA_TOAST = 'OpenSubtitles quota exhausted — try later'

function torrentInfo(): TorrentInfo {
  return {
    infoHash: HASH,
    name: 'Show S01E01',
    totalSize: 100,
    // Two video files: the per-file reset test switches to index 1.
    files: [
      { index: 0, path: 'Show S01E01.mp4', size: 1, isVideo: true, downloaded: 1, progress: 1, priority: 'normal' },
      { index: 1, path: 'Show S01E02.mp4', size: 1, isVideo: true, downloaded: 1, progress: 1, priority: 'normal' },
    ],
    peers: 1,
    seeders: 2,
    downRate: 0,
    upRate: 0,
    progress: 1,
    primaryFile: 0,
  }
}

type SubsOpts = Parameters<typeof useSubtitles>[0]

// Minimal SearchResult fixture for the manual-panel tests (openSubtitlePanel
// needs a non-null result to fire; only `title` is really consumed).
function searchResult(): SearchResult {
  return {
    title: 'Show S01E01',
    tracker: 'TestTracker',
    categoryId: 5000,
    category: 'TV',
    size: 100,
    seeders: 5,
    leechers: 0,
    age: '1d',
    magnetUri: `magnet:?xt=urn:btih:${HASH}`,
    link: 'https://tracker.test/t/1',
    infoHash: HASH,
    publishDate: '2024-01-01',
  }
}

function makeOpts(over: Partial<SubsOpts> = {}): SubsOpts {
  return {
    // The offset effect walks video.textTracks — an empty iterable keeps every
    // path safe under jsdom (which has no TextTrack support).
    videoRef: { current: { textTracks: [] } as unknown as HTMLVideoElement },
    info: torrentInfo(),
    selectedFile: 0,
    serverReady: true,
    result: null,
    mediaToken: 'tok',
    setProbe: vi.fn(),
    autoFetchEnabled: true,
    ...over,
  }
}

// Resolve every pending microtask chain (settings read → status tick → apply).
async function drain() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
}

async function renderSubs(over: Partial<SubsOpts> = {}) {
  const opts = makeOpts(over)
  const utils = renderHook(({ v }) => useSubtitles(v), { initialProps: { v: opts } })
  await drain()
  return { ...utils, opts }
}

beforeAll(async () => {
  await i18n.changeLanguage('en-US')
})

beforeEach(() => {
  localStorage.clear()
  mocks.streamProbe.mockReset().mockResolvedValue({ audio: [], subtitles: [] })
  mocks.streamSidecars.mockReset().mockResolvedValue([])
  mocks.subtitlesAuto.mockReset()
  mocks.subtitlesSearch.mockReset()
  mocks.localSubtrackBlobURL.mockReset()
  mocks.subtitleAutoStatus.mockReset()
  mocks.getSubtitleSettings.mockReset().mockResolvedValue(SETTINGS_ON)
  mocks.isLocalHash.mockClear()
  mocks.notify.mockReset()
  mocks.notifyError.mockReset()
})

afterEach(cleanup)

describe('useSubtitles auto pipeline (lane W2)', () => {
  it('applies the ready answer as the virtual auto subtitle: dedicated state, one success toast, nothing persisted', async () => {
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'ready', subtitle: { id: '77', language: 'pt-BR' } })
    const { result, opts, rerender } = await renderSubs()

    expect(result.current.autoSub).toEqual({ id: '77', language: 'pt-BR' })
    // Dedicated state: no synthetic sidecar/embedded index was touched.
    expect(result.current.sidecarIdx).toBeNull()
    expect(result.current.embeddedSub).toBeNull()
    expect(result.current.subActive).toBeNull()
    // Toast fires once per file — not per poll, not per rerender.
    expect(mocks.notify).toHaveBeenCalledTimes(1)
    expect(mocks.notify).toHaveBeenCalledWith(AUTO_APPLIED_TOAST, 'success')

    // The auto pick NEVER lands in the per-file choice persistence.
    const saved = load<Record<string, unknown> | null>(`sub.${HASH}.0`, null)
    expect(saved).toEqual({ external: null, embedded: null, sidecar: null, offset: 0 })
    expect(JSON.stringify(saved)).not.toContain('77')

    rerender({ v: opts })
    await drain()
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(1)
    expect(mocks.notify).toHaveBeenCalledTimes(1)
  })

  it('toasts info once on a quota failure and sets the quiet failed flag', async () => {
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'failed', reason: 'quota' })
    const { result } = await renderSubs()

    expect(result.current.autoSub).toBeNull()
    expect(result.current.autoFailed).toBe(true)
    expect(mocks.notify).toHaveBeenCalledTimes(1)
    expect(mocks.notify).toHaveBeenCalledWith(AUTO_QUOTA_TOAST, 'info')
  })

  it('stays quiet on other failures (hint only, no toast)', async () => {
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'failed', reason: 'no_results' })
    const { result } = await renderSubs()

    expect(result.current.autoSub).toBeNull()
    expect(result.current.autoFailed).toBe(true)
    expect(mocks.notify).not.toHaveBeenCalled()
  })

  it('a manual search pick replaces the auto pick and disables the pipeline for the session', async () => {
    mocks.subtitleAutoStatus.mockResolvedValue({ status: 'ready', subtitle: { id: '77', language: 'pt-BR' } })
    const { result, opts, rerender } = await renderSubs()
    expect(result.current.autoSub).toEqual({ id: '77', language: 'pt-BR' })
    const callsAfterApply = mocks.subtitleAutoStatus.mock.calls.length

    const picked: Subtitle = {
      id: '42',
      language: 'pt-BR',
      release: 'Show.S01E01',
      url: '/api/subtitles/download/42',
      uploaderName: 'os-user',
      downloads: 10,
      hearingImpaired: false,
      trusted: true,
    }
    act(() => result.current.pickSubtitle(picked))
    await drain()

    expect(result.current.autoSub).toBeNull()
    expect(result.current.subActive).toBe('42')
    expect(result.current.autoFailed).toBe(false)
    // The pick itself persisted — a real manual choice wins.
    const saved = load<{ external: string | null } | null>(`sub.${HASH}.0`, null)
    expect(saved?.external).toBe('42')
    // No further status polls and no replacement toast after the manual pick.
    rerender({ v: opts })
    await drain()
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(callsAfterApply)
    expect(mocks.notify).toHaveBeenCalledTimes(1)
  })

  it('an explicit user setter (remove/switch) drops the auto pick for the session', async () => {
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'ready', subtitle: { id: '77', language: 'pt-BR' } })
    const { result } = await renderSubs()
    expect(result.current.autoSub).not.toBeNull()

    act(() => result.current.setSubActive(null))
    await drain()

    expect(result.current.autoSub).toBeNull()
    // Removing counts as a manual decision: the persisted choice stays all-null.
    const saved = load<{ external: string | null } | null>(`sub.${HASH}.0`, null)
    expect(saved?.external).toBeNull()
  })

  it('does not even read the pipeline when auto_fetch is off (fail closed)', async () => {
    await renderSubs({ autoFetchEnabled: false })
    expect(mocks.getSubtitleSettings).not.toHaveBeenCalled()
    expect(mocks.subtitleAutoStatus).not.toHaveBeenCalled()
  })

  it('arbitrates the probe auto-pick against the pipeline: an embedded pt pick landing AFTER the ready answer leaves exactly ONE active', async () => {
    // The designed RF7 replay race: the server-cached status answers ready
    // first, the ffprobe (embedded pt track) resolves later. Both auto-picks
    // fire through RAW setters — without arbitration both would stay active.
    let resolveStatus!: (v: { status: string; subtitle?: { id: string; language: string } }) => void
    mocks.subtitleAutoStatus.mockImplementationOnce(() => new Promise(res => { resolveStatus = res }))
    let resolveProbe!: (v: { audio: unknown[]; subtitles: Array<Record<string, unknown>> }) => void
    mocks.streamProbe.mockImplementationOnce(() => new Promise(res => { resolveProbe = res }))

    const { result } = await renderSubs()
    expect(result.current.autoSub).toBeNull()

    // 1. The pipeline answers ready → the virtual auto subtitle applies.
    await act(async () => { resolveStatus({ status: 'ready', subtitle: { id: '77', language: 'pt-BR' } }) })
    expect(result.current.autoSub).toEqual({ id: '77', language: 'pt-BR' })
    expect(result.current.embeddedSub).toBeNull()

    // 2. The probe lands late with an embedded pt track → it wins the display,
    //    the auto entry yields (cleared WITHOUT counting as a manual pick).
    await act(async () => { resolveProbe({ audio: [], subtitles: [{ index: 3, language: 'pt', image: false }] }) })

    expect(result.current.embeddedSub).toBe(3)
    expect(result.current.autoSub).toBeNull()
    expect(result.current.autoSource).toBe('embedded')
    // Exactly one decision, no re-poll, and the arbitration itself is silent:
    // the success toast fired once (for the apply), never again.
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(1)
    expect(mocks.notify).toHaveBeenCalledTimes(1)
  })

  it('a file switch resets the auto state so the next file polls fresh', async () => {
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'ready', subtitle: { id: '77', language: 'pt-BR' } })
    const { result, rerender } = await renderSubs()
    expect(result.current.autoSub).toEqual({ id: '77', language: 'pt-BR' })

    act(() => result.current.resetSubtitlesForFile())
    await drain()

    expect(result.current.autoSub).toBeNull()
    expect(result.current.autoFailed).toBe(false)

    // New file (index 1): the effect re-arms for the new session key.
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'ready', subtitle: { id: '88', language: 'pt-BR' } })
    rerender({ v: makeOpts({ selectedFile: 1 }) })
    await drain()
    expect(result.current.autoSub).toEqual({ id: '88', language: 'pt-BR' })
  })

  // Cross-cutting review finding 5 (RF2): the manual panel must NOT hardcode
  // 'pt-BR,pt' — omitting the param lets the backend default (settings-driven
  // DefaultLanguages) apply, so an admin-configured language list reaches the
  // in-app picker too.
  it('manual panel auto-search sends no langs param (settings-driven default, RF2)', async () => {
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'none' })
    mocks.subtitlesAuto.mockResolvedValueOnce({ osHash: 'x', osSize: 1, file: 'f', results: [] })
    const { result } = await renderSubs({ result: searchResult() })

    await act(async () => { await result.current.openSubtitlePanel() })

    expect(mocks.subtitlesAuto).toHaveBeenCalledTimes(1)
    // Exact arity: a third (langs) argument would pin the old hardcode.
    expect(mocks.subtitlesAuto).toHaveBeenCalledWith(HASH, 0)
  })

  it('manual panel fallback title search also omits langs (settings-driven default, RF2)', async () => {
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'none' })
    mocks.subtitlesAuto.mockRejectedValueOnce(new Error('auto search down'))
    const { result } = await renderSubs({ result: searchResult() })

    await act(async () => { await result.current.openSubtitlePanel() })

    expect(mocks.subtitlesSearch).toHaveBeenCalledTimes(1)
    // Deep equality without a langs key: 'Show S01E01' parses to season 1,
    // episode 1, cleanQuery 'Show'; an explicit langs here would fail the match.
    expect(mocks.subtitlesSearch).toHaveBeenCalledWith('Show', { season: 1, episode: 1 })
  })
})
