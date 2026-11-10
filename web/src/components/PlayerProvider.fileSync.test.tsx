import { act, cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter, useLocation, useNavigate, type NavigateFunction } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PlayerModalProps } from './player/playerTypes'

const modal = vi.hoisted(() => ({ props: null as PlayerModalProps | null }))
vi.mock('./PlayerModal', () => ({
  default: (p: PlayerModalProps) => { modal.props = p; return <div data-testid="player" /> },
}))
vi.mock('../api/client', async (orig) => ({
  ...(await orig<typeof import('../api/client')>()),
  streamAdd: vi.fn().mockResolvedValue({}),
  libraryList: vi.fn().mockResolvedValue([]),
}))

import PlayerProvider, { usePlayer } from './PlayerProvider'

const HASH = 'a'.repeat(40)
let loc = ''
let navigate: NavigateFunction = () => {}
let api: ReturnType<typeof usePlayer> | null = null

function Probe() {
  const l = useLocation()
  loc = l.search
  navigate = useNavigate()
  api = usePlayer()
  return null
}

function mount(initial = '/') {
  render(
    <MemoryRouter initialEntries={[initial]}>
      <PlayerProvider><Probe /></PlayerProvider>
    </MemoryRouter>,
  )
}

const result = { title: 'Show', infoHash: HASH, magnetUri: `magnet:?xt=urn:btih:${HASH}` } as never

describe('PlayerProvider ?f= sync within one torrent (#200)', () => {
  afterEach(() => { cleanup(); modal.props = null; localStorage.clear() })

  it('writes ?f= when the user switches file inside the open player', async () => {
    mount()
    await act(async () => { api!.playSingle(result, 0) })
    await screen.findByTestId('player')
    expect(new URLSearchParams(loc).get('f')).toBe('0')

    await act(async () => { modal.props!.onFileSelected?.(3) })
    const q = new URLSearchParams(loc)
    expect(q.get('play')).toBe(HASH)
    expect(q.get('f')).toBe('3')
  })

  it('switches the open player to a ?f= changed externally for the same torrent', async () => {
    mount()
    await act(async () => { api!.playSingle(result, 0) })
    await screen.findByTestId('player')

    await act(async () => { navigate(`/?play=${HASH}&f=5`) })
    expect(modal.props!.initialFileIndex).toBe(5)
    expect(modal.props!.result?.infoHash).toBe(HASH)
  })
})
