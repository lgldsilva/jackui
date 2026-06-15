import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Music2, Loader2, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import NavHeader from './NavHeader'
import { musicTrending, MusicAlbum } from '../api/music'
import { newTabProps, searchHref } from '../lib/cardNav'

// albumQuery seeds the search with "Artist Album" — the cleanest signal for the
// torrent search (the backend already separates artist/album, no fragile split).
function albumQuery(a: MusicAlbum): string {
  return `${a.artist} ${a.name}`.trim()
}

// AlbumCard renders one trending album as a clickable square cover. Mirrors the
// PosterCard pattern (full-bleed button overlay + new-tab support) but square,
// since it's NOT a TmdbMatch (no rating/direction/kind).
function AlbumCard({ a, onClick }: { readonly a: MusicAlbum; readonly onClick: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="group relative flex flex-col text-left rounded-lg overflow-hidden bg-surface-secondary border border-default hover:border-green-500/50 transition-colors">
      <button
        {...newTabProps(searchHref(albumQuery(a)), onClick)}
        title={t('discover.search_album', { name: a.name })}
        aria-label={t('discover.search_album', { name: a.name })}
        className="absolute inset-0 z-[1]"
      />
      <div className="aspect-square bg-surface relative">
        {a.artwork
          ? <img src={a.artwork} alt={a.name} loading="lazy" className="w-full h-full object-cover" />
          : <div className="w-full h-full flex items-center justify-center"><Music2 className="w-10 h-10 text-text-muted" /></div>}
        <div className="absolute inset-0 flex items-center justify-center bg-black/50 opacity-0 group-hover:opacity-100 group-active:opacity-100 transition-opacity">
          <Search className="w-7 h-7 text-green-400" />
        </div>
      </div>
      <div className="p-2">
        <p className="text-xs text-text-primary line-clamp-2" title={a.name}>{a.name}</p>
        <p className="text-[10px] text-text-muted line-clamp-1">{a.artist}</p>
      </div>
    </div>
  )
}

// MusicDiscoverView replaces the TMDB trending grid when Música mode is on (TMDB
// has no music). It shows Apple's keyless top-albums feed; clicking an album
// seeds the search ("Artist Album"), reusing the exact same ?q= pipeline as the
// film discover. Lives in its own file so DiscoverPage just early-returns into it.
export function MusicDiscoverView() {
  const { t } = useTranslation()
  const [albums, setAlbums] = useState<MusicAlbum[] | null>(null)
  const navigate = useNavigate()

  useEffect(() => {
    setAlbums(null)
    musicTrending({ limit: 48 }).then(setAlbums).catch(() => setAlbums([]))
  }, [])

  let body: React.ReactNode
  if (albums === null) {
    body = <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-text-muted" /></div>
  } else if (albums.length === 0) {
    body = (
      <div className="text-center py-20 text-text-muted">
        <Music2 className="w-16 h-16 mx-auto mb-4 opacity-30" />
        <p>{t('discover.music_empty')}</p>
      </div>
    )
  } else {
    body = (
      <div className="grid grid-cols-2 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8 gap-3">
        {albums.map((a, i) => (
          <AlbumCard key={`${a.artist}-${a.name}-${i}`} a={a} onClick={() => navigate(searchHref(albumQuery(a)))} />
        ))}
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        <h1 className="text-xl font-semibold text-text-primary flex items-center gap-2">
          <Music2 className="w-5 h-5 text-purple-400" /> {t('discover.music_trending')}
        </h1>
        {body}
      </main>
    </div>
  )
}
