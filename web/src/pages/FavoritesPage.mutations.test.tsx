import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, waitFor } from '@testing-library/react'
import type { RefObject } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { deferred } from '../lib/deferred.testutil'
import { setRevealHidden } from '../lib/reveal'

const m = vi.hoisted(() => ({
  notify: vi.fn(),
  favorites: vi.fn(), folders: vi.fn(),
  remove: vi.fn(), removeBatch: vi.fn(),
  folderCreate: vi.fn(), folderRename: vi.fn(), folderDelete: vi.fn(),
  setFolder: vi.fn(), setFolderBatch: vi.fn(),
}))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    favoritesList: (...a: unknown[]) => m.favorites(...a),
    folderList: (...a: unknown[]) => m.folders(...a),
    downloadsList: vi.fn().mockResolvedValue([]),
    favoriteRemove: (...a: unknown[]) => m.remove(...a),
    favoriteRemoveBatch: (...a: unknown[]) => m.removeBatch(...a),
    folderCreate: (...a: unknown[]) => m.folderCreate(...a),
    folderRename: (...a: unknown[]) => m.folderRename(...a),
    folderDelete: (...a: unknown[]) => m.folderDelete(...a),
    favoriteSetFolder: (...a: unknown[]) => m.setFolder(...a),
    favoriteSetFolderBatch: (...a: unknown[]) => m.setFolderBatch(...a),
  }
})
vi.mock('../auth/AuthContext', async () => {
  const actual = await vi.importActual<typeof import('../auth/AuthContext')>('../auth/AuthContext')
  return { ...actual, useAuth: () => ({ isAdmin: false }) }
})
vi.mock('../components/PlayerProvider', () => ({ usePlayer: () => ({ playSingle: vi.fn() }) }))
vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/DownloadModal', () => ({ default: () => null }))
vi.mock('../components/TorrentContentsModal', () => ({ default: () => null }))
vi.mock('../components/ConfirmDialog', () => ({ useConfirm: () => vi.fn(async () => true) }))
vi.mock('../components/Toast', () => ({ useToast: () => ({ notify: m.notify, notifyError: vi.fn() }) }))

// Thin stand-ins for the presentational children: they show what the page
// passes down and expose one button per page handler.
type Fav = { name: string; folderId: number | null }
vi.mock('../components/favorites/FavoriteCard', () => ({
  default: ({ fav, onRemove, onMoveToFolder, onToggleSelected }: {
    fav: Fav; onRemove: () => void; onMoveToFolder: (id: number | null) => void; onToggleSelected: () => void
  }) => (
    <div>
      <span data-testid="fav">{`${fav.name}@${fav.folderId ?? 'root'}`}</span>
      <button onClick={onRemove}>remove {fav.name}</button>
      <button onClick={() => onMoveToFolder(5)}>file {fav.name}</button>
      <button onClick={onToggleSelected}>select {fav.name}</button>
    </div>
  ),
}))
type Node = { folder: { id: number; name: string }; children: Node[] }
const flat = (nodes: Node[]): Node['folder'][] => nodes.flatMap(n => [n.folder, ...flat(n.children)])
vi.mock('../components/favorites/FolderSidebar', () => ({
  default: (p: {
    tree: Node[]; newFolderInput: RefObject<HTMLInputElement>
    onCreateRoot: () => void; onRename: (id: number, name: string) => void
    onDeleteFolder: (id: number) => void; onCreateSub: (id: number) => void
  }) => (
    <div>
      <input aria-label="new folder name" ref={p.newFolderInput} />
      <button onClick={p.onCreateRoot}>create root</button>
      {flat(p.tree).map(f => (
        <div key={f.id}>
          <span data-testid="folder">{f.name}</span>
          <button onClick={() => p.onRename(f.id, ' Renamed ')}>rename {f.name}</button>
          <button onClick={() => p.onRename(f.id, '  ')}>blank {f.name}</button>
          <button onClick={() => p.onDeleteFolder(f.id)}>delete {f.name}</button>
          <button onClick={() => p.onCreateSub(f.id)}>new sub of {f.name}</button>
        </div>
      ))}
    </div>
  ),
}))
vi.mock('../components/favorites/MobileFolderSheet', () => ({
  default: (p: { tree: Node[]; onCreateRoot: () => void; onRename: (f: Node['folder']) => void }) => (
    <div>
      <button onClick={p.onCreateRoot}>mobile create root</button>
      {flat(p.tree).map(f => <button key={f.id} onClick={() => p.onRename(f)}>mobile rename {f.name}</button>)}
    </div>
  ),
}))
vi.mock('../components/favorites/MultiSelectBar', () => ({
  default: (p: { count: number; onMoveToFolder: (id: number | null) => void; onDeleteSelected: () => void }) => (
    <div>
      <span>{p.count} selected</span>
      <button onClick={() => p.onMoveToFolder(5)}>file selected</button>
      <button onClick={p.onDeleteSelected}>delete selected</button>
    </div>
  ),
}))

import FavoritesPage from './FavoritesPage'

const fav = (name: string, folderId: number | null = null) => ({
  name, infoHash: name, magnet: '', userId: 1, favoritedAt: '2026-01-01T00:00:00Z', reason: 'manual' as const, folderId,
})
const folder = (id: number, name: string, parentId: number | null = null) => ({
  id, userId: 1, name, parentId, position: id, createdAt: '',
})
const favNames = () => screen.queryAllByTestId('fav').map(el => el.textContent)
const folderNames = () => screen.queryAllByTestId('folder').map(el => el.textContent)
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() })
const click = async (label: string) => { fireEvent.click(screen.getByText(label)); await flush() }

describe('FavoritesPage — mutations', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    localStorage.clear()
    m.favorites.mockResolvedValue([fav('A'), fav('B')])
    m.folders.mockResolvedValue([folder(5, 'Films')])
    render(<MemoryRouter><FavoritesPage /></MemoryRouter>)
    await waitFor(() => expect(favNames()).toEqual(['A@root', 'B@root']))
  })
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); setRevealHidden(false) })

  describe('patch the list in place when the curtain did not move', () => {
    it('removes one favorite', async () => {
      m.remove.mockResolvedValue(undefined)
      await click('remove A')
      expect(m.remove).toHaveBeenCalledWith('A')
      expect(favNames()).toEqual(['B@root'])
      expect(m.favorites).toHaveBeenCalledTimes(1)
    })

    it('moves one favorite into a folder', async () => {
      m.setFolder.mockResolvedValue(undefined)
      await click('file A')
      expect(m.setFolder).toHaveBeenCalledWith('A', 5)
      expect(favNames()).toEqual(['A@5', 'B@root'])
    })

    it('creates a root folder from the sidebar input and clears it', async () => {
      m.folderCreate.mockResolvedValue(folder(6, 'Series'))
      const input = screen.getByLabelText<HTMLInputElement>('new folder name')
      input.value = ' Series '
      await click('create root')
      expect(m.folderCreate).toHaveBeenCalledWith('Series', null)
      expect(folderNames()).toEqual(['Films', 'Series'])
      expect(input.value).toBe('')
    })

    it('does not create a root folder from a blank name', async () => {
      await click('create root')
      expect(m.folderCreate).not.toHaveBeenCalled()
    })

    it('creates a root folder from the mobile prompt', async () => {
      vi.stubGlobal('prompt', () => ' Docs ')
      m.folderCreate.mockResolvedValue(folder(7, 'Docs'))
      await click('mobile create root')
      expect(m.folderCreate).toHaveBeenCalledWith('Docs', null)
      expect(folderNames()).toEqual(['Films', 'Docs'])
    })

    it('creates a subfolder under its parent', async () => {
      vi.stubGlobal('prompt', () => 'Noir')
      m.folderCreate.mockResolvedValue(folder(8, 'Noir', 5))
      await click('new sub of Films')
      expect(m.folderCreate).toHaveBeenCalledWith('Noir', 5)
      expect(folderNames()).toEqual(['Films', 'Noir'])
    })

    it('renames a folder with the trimmed name and ignores a blank one', async () => {
      m.folderRename.mockResolvedValue(undefined)
      await click('blank Films')
      expect(m.folderRename).not.toHaveBeenCalled()
      await click('rename Films')
      expect(m.folderRename).toHaveBeenCalledWith(5, 'Renamed')
      expect(folderNames()).toEqual(['Renamed'])
    })

    it('renames a folder from the mobile prompt', async () => {
      vi.stubGlobal('prompt', () => 'Movies')
      m.folderRename.mockResolvedValue(undefined)
      await click('mobile rename Films')
      expect(folderNames()).toEqual(['Movies'])
    })

    it('deletes a folder and re-reads the favorites it contained', async () => {
      m.folderDelete.mockResolvedValue(undefined)
      m.favorites.mockResolvedValue([fav('A'), fav('B'), fav('C')])
      await click('delete Films')
      expect(m.folderDelete).toHaveBeenCalledWith(5)
      expect(folderNames()).toEqual([])
      expect(favNames()).toEqual(['A@root', 'B@root', 'C@root'])
    })

    it('moves the selected favorites, leaving the ones the server rejected', async () => {
      m.setFolderBatch.mockResolvedValue({ affected: 1, total: 2, failed: ['B'] })
      await click('select A')
      await click('select B')
      expect(screen.getByText('2 selected')).toBeInTheDocument()
      await click('file selected')
      expect(m.setFolderBatch).toHaveBeenCalledWith(['A', 'B'], 5)
      expect(favNames()).toEqual(['A@5', 'B@root'])
      expect(m.notify).toHaveBeenCalledWith('1 favorite(s) could not be updated.', 'error')
      expect(screen.queryByText(/selected$/)).not.toBeInTheDocument()
    })

    it('moves nothing when the batch move request fails', async () => {
      m.setFolderBatch.mockRejectedValue(new Error('offline'))
      await click('select A')
      await click('file selected')
      expect(favNames()).toEqual(['A@root', 'B@root'])
      expect(m.notify).toHaveBeenCalledWith('1 favorite(s) could not be updated.', 'error')
    })

    it('deletes the selected favorites, keeping the ones the server rejected', async () => {
      m.removeBatch.mockResolvedValue({ affected: 1, total: 2, failed: ['B'] })
      await click('select A')
      await click('select B')
      await click('delete selected')
      expect(m.removeBatch).toHaveBeenCalledWith(['A', 'B'])
      expect(favNames()).toEqual(['B@root'])
      expect(m.notify).toHaveBeenCalledWith('1 favorite(s) could not be updated.', 'error')
    })

    it('deletes every selected favorite without a toast when none fails', async () => {
      m.removeBatch.mockResolvedValue({ affected: 2, total: 2, failed: [] })
      await click('select A')
      await click('select B')
      await click('delete selected')
      expect(favNames()).toEqual([])
      expect(m.notify).not.toHaveBeenCalled()
    })
  })

  describe('reload instead of patching when the curtain flipped mid-request', () => {
    // The request is held open, the curtain flips, and the list the server
    // returns afterwards is the only truth: the local patch must not run.
    const flipDuring = async (label: string, mock: typeof m.remove, value: unknown) => {
      const pending = deferred<unknown>()
      mock.mockReturnValue(pending.promise)
      await click(label)
      m.favorites.mockResolvedValue([fav('Server list')])
      m.folders.mockResolvedValue([folder(9, 'Server folder')])
      await act(async () => { setRevealHidden(true) })
      await flush()
      const loadsBefore = m.favorites.mock.calls.length
      await act(async () => { pending.resolve(value) })
      await flush()
      expect(m.favorites.mock.calls.length).toBeGreaterThan(loadsBefore)
      expect(favNames()).toEqual(['Server list@root'])
      expect(folderNames()).toEqual(['Server folder'])
    }

    it('after removing a favorite', () => flipDuring('remove A', m.remove, undefined))
    it('after renaming a folder', () => flipDuring('rename Films', m.folderRename, undefined))
    it('after deleting a folder', () => flipDuring('delete Films', m.folderDelete, undefined))
    it('after creating a root folder from the mobile prompt', () => {
      vi.stubGlobal('prompt', () => 'Docs')
      return flipDuring('mobile create root', m.folderCreate, folder(7, 'Docs'))
    })
    it('after a batch move', async () => {
      await click('select A')
      await flipDuring('file selected', m.setFolderBatch, { affected: 1, total: 1, failed: [] })
    })
    it('after a batch delete', async () => {
      await click('select A')
      await flipDuring('delete selected', m.removeBatch, { affected: 1, total: 1, failed: [] })
    })
  })

  describe('load results', () => {
    it('treats an empty (null) response as no favorites', async () => {
      m.favorites.mockResolvedValue(null)
      m.folders.mockResolvedValue(null)
      await act(async () => { setRevealHidden(true) })
      await flush()
      expect(favNames()).toEqual([])
      expect(screen.getByText('No favorites yet')).toBeInTheDocument()
    })

    it('shows the load error of the current request only', async () => {
      const stale = deferred<unknown[]>()
      m.favorites.mockReturnValueOnce(stale.promise)
      await act(async () => { setRevealHidden(true) })
      m.favorites.mockResolvedValue([fav('A')])
      await act(async () => { setRevealHidden(false) })
      await flush()
      await act(async () => { stale.reject(new Error('stale failure')) })
      await flush()
      expect(screen.queryByText(/stale failure/)).not.toBeInTheDocument()
      expect(favNames()).toEqual(['A@root'])

      m.favorites.mockRejectedValue(new Error('db down'))
      await act(async () => { setRevealHidden(true) })
      await flush()
      expect(screen.getByText('Error: db down')).toBeInTheDocument()
    })
  })
})
