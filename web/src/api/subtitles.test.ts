import { vi, describe, it, expect, beforeEach } from 'vitest'

const getMock = vi.fn()
vi.mock('./http', () => ({
  api: { get: (...a: unknown[]) => getMock(...a) },
  withToken: (u: string, override?: string) =>
    `${u}${u.includes('?') ? '&' : '?'}token=${override ?? 'tok'}`,
}))

import { buildLocalHash, setLocalViewAsUser } from './local-base'
import { subtitleAutoStatus } from './subtitles'
// Locale parity check for the lane's new i18n keys (JSON import = validity proof).
import pt from '../locales/pt.json'
import en from '../locales/en.json'

const HASH = 'a'.repeat(40)

describe('subtitleAutoStatus URL routing', () => {
  beforeEach(() => {
    getMock.mockReset()
    getMock.mockResolvedValue({ data: { status: 'none' } })
  })

  it('builds /stream/:hash/:idx/subtitles/auto for torrent hashes', async () => {
    await subtitleAutoStatus(HASH, 2)
    expect(getMock).toHaveBeenCalledWith(`/stream/${HASH}/2/subtitles/auto`, { signal: undefined })
  })

  it('keeps the local pseudo-hash in the path for local files', async () => {
    const local = buildLocalHash('Movies', 'Movie 2024/a.mkv')
    await subtitleAutoStatus(local, 0)
    expect(getMock).toHaveBeenCalledWith(`/local/subtitles/auto/${local}/0`, { signal: undefined })
  })

  it('carries the admin view-as ?user= on the local branch (same as every other local call)', async () => {
    const local = buildLocalHash('Movies', 'Movie 2024/a.mkv')
    setLocalViewAsUser('alice')
    try {
      await subtitleAutoStatus(local, 0)
      expect(getMock).toHaveBeenCalledWith(`/local/subtitles/auto/${local}/0?user=alice`, { signal: undefined })
    } finally {
      setLocalViewAsUser('')
    }
  })

  it('ignores the view-as override on the torrent branch', async () => {
    setLocalViewAsUser('alice')
    try {
      await subtitleAutoStatus(HASH, 2)
      expect(getMock).toHaveBeenCalledWith(`/stream/${HASH}/2/subtitles/auto`, { signal: undefined })
    } finally {
      setLocalViewAsUser('')
    }
  })

  it('passes the abort signal through to the request', async () => {
    const controller = new AbortController()
    await subtitleAutoStatus(HASH, 1, controller.signal)
    const config = getMock.mock.calls[0][1] as { signal?: AbortSignal }
    expect(config.signal).toBe(controller.signal)
  })
})

describe('subtitleAutoStatus response mapping', () => {
  beforeEach(() => {
    getMock.mockReset()
  })

  it('maps ready: tokenizes vttUrl and normalizes empty reason to undefined', async () => {
    getMock.mockResolvedValueOnce({
      data: {
        status: 'ready',
        reason: '',
        subtitle: {
          id: '4242',
          language: 'pt-BR',
          vttUrl: '/api/subtitles/download/4242',
          hearingImpaired: true,
          forced: false,
        },
      },
    })
    const result = await subtitleAutoStatus(HASH, 0)
    expect(result).toEqual({
      status: 'ready',
      reason: undefined,
      subtitle: {
        id: '4242',
        language: 'pt-BR',
        vttUrl: '/api/subtitles/download/4242?token=tok',
        hearingImpaired: true,
        forced: false,
      },
    })
  })

  it('maps pending/none without inventing subtitle or reason', async () => {
    getMock.mockResolvedValueOnce({ data: { status: 'pending' } })
    const pending = await subtitleAutoStatus(HASH, 0)
    expect(pending.status).toBe('pending')
    expect(pending.reason).toBeUndefined()
    expect(pending.subtitle).toBeUndefined()
  })

  it('keeps failure reasons for failed pipeline states', async () => {
    getMock.mockResolvedValueOnce({ data: { status: 'failed', reason: 'quota' } })
    const failed = await subtitleAutoStatus(HASH, 0)
    expect(failed).toEqual({ status: 'failed', reason: 'quota', subtitle: undefined })
  })

  it('propagates HTTP errors instead of fabricating a status (404 inactive torrent)', async () => {
    getMock.mockRejectedValueOnce(new Error('Request failed with status code 404'))
    await expect(subtitleAutoStatus(HASH, 0)).rejects.toThrow('404')
  })

  it('propagates abort rejections to the caller', async () => {
    const controller = new AbortController()
    getMock.mockRejectedValueOnce(new DOMException('Aborted', 'AbortError'))
    await expect(subtitleAutoStatus(HASH, 0, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    })
  })
})

describe('i18n keys for subtitle auto-status (lanes W1/W2)', () => {
  const playerAutoKeys = ['autoBadge', 'autoApplied', 'autoFailed', 'autoQuota']
  const settingsSubKeys = [
    'title',
    'defaultLanguages',
    'defaultLanguagesHelp',
    'autoFetch',
    'autoFetchHelp',
    'burnDefault',
    'burnDefaultHelp',
  ]
  const ptPlayer = pt.player.subtitles as unknown as Record<string, unknown>
  const enPlayer = en.player.subtitles as unknown as Record<string, unknown>
  const ptSettings = pt.settings.subtitles as unknown as Record<string, unknown>
  const enSettings = en.settings.subtitles as unknown as Record<string, unknown>

  it('adds the frozen player.subtitles auto keys to BOTH locales with the frozen copy', () => {
    expect(playerAutoKeys.filter((k) => k in ptPlayer).sort()).toEqual([...playerAutoKeys].sort())
    expect(playerAutoKeys.filter((k) => k in enPlayer).sort()).toEqual([...playerAutoKeys].sort())
    expect(ptPlayer.autoBadge).toBe('PT-BR (auto)')
    expect(enPlayer.autoBadge).toBe('PT-BR (auto)')
    expect(ptPlayer.autoApplied).toBe('Legenda PT-BR aplicada')
    expect(enPlayer.autoApplied).toBe('PT-BR subtitle applied')
  })

  it('adds settings.subtitles with identical key sets and non-empty copy', () => {
    expect(Object.keys(ptSettings).sort()).toEqual([...settingsSubKeys].sort())
    expect(Object.keys(enSettings).sort()).toEqual([...settingsSubKeys].sort())
    expect(Object.keys(ptSettings).length).toBe(7)
    for (const k of settingsSubKeys) {
      expect(String(ptSettings[k]).length).toBeGreaterThan(0)
      expect(String(enSettings[k]).length).toBeGreaterThan(0)
    }
    // Card heading added by lane W2 (SettingsPage subtitles card).
    expect(ptSettings.title).toBe('Legendas')
    expect(enSettings.title).toBe('Subtitles')
  })
})
