import { api } from './http'

// ─── Library (per-user history of streamed torrents) ───────────────────────

export type LibraryEntry = {
  id: number
  userId: number
  infoHash: string
  magnet: string
  name: string
  primaryFileIndex: number
  // The file the user actually last watched (multi-file torrents). -1 = unknown.
  lastFileIndex: number
  totalSize: number
  resumeSeconds: number
  durationSeconds: number
  kind: string
  lastPlayedAt: string
  addedAt: string
}

export const libraryList = async (opts: { limit?: number; all?: boolean; revealHidden?: boolean } = {}): Promise<LibraryEntry[]> => {
  const p = new URLSearchParams()
  if (opts.limit) p.set('limit', String(opts.limit))
  if (opts.all) p.set('all', '1')
  // revealHidden forces inclusion of entries behind the hidden curtain for THIS
  // request only (the backend's RevealHidden middleware also honors ?revealHidden=1,
  // not just the global X-JackUI-Reveal-Hidden header). Used by the deep-link play
  // gate to tell "hidden item" apart from "not in library" without flipping the
  // global curtain.
  if (opts.revealHidden) p.set('revealHidden', '1')
  const { data } = await api.get<LibraryEntry[]>(`/library?${p}`)
  return data
}

export const libraryGet = async (id: number): Promise<LibraryEntry> => {
  const { data } = await api.get<LibraryEntry>(`/library/${id}`)
  return data
}

export const libraryUpdateResume = async (id: number, resumeSeconds: number, durationSeconds = 0, fileIndex?: number): Promise<void> => {
  await api.patch(`/library/${id}`, { resumeSeconds, durationSeconds, fileIndex })
}

export const libraryDelete = async (id: number): Promise<void> => {
  await api.delete(`/library/${id}`)
}

export const libraryDeleteAll = async (): Promise<{ deleted: number }> => {
  const { data } = await api.delete<{ deleted: number }>('/library')
  return data
}
