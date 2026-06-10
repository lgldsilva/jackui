import { Quality } from '../api/client'

type Props = {
  readonly quality?: Quality
  readonly compact?: boolean
}

const RESOLUTION_COLORS: Record<string, string> = {
  '2160p': 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30',
  '1080p': 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/30',
  '720p':  'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300 border-cyan-500/30',
  '480p':  'bg-gray-500/20 text-text-primary border-strong/30',
}

const SOURCE_COLORS: Record<string, string> = {
  'BluRay':   'bg-amber-500/20 text-amber-700 dark:text-amber-300 border-amber-500/30',
  'WEB-DL':   'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/30',
  'WEBRip':   'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400 border-emerald-500/20',
  'HDTV':     'bg-slate-500/20 text-slate-700 dark:text-slate-300 border-slate-500/30',
  'DVDRip':   'bg-stone-500/20 text-stone-700 dark:text-stone-300 border-stone-500/30',
  'CAM':      'bg-red-500/20 text-red-700 dark:text-red-300 border-red-500/30',
  'TS':       'bg-red-500/20 text-red-700 dark:text-red-300 border-red-500/30',
}

function Badge({ text, className, title }: { readonly text: string; readonly className: string; readonly title?: string }) {
  return (
    <span
      title={title || text}
      className={`text-[10px] uppercase font-medium border px-1.5 py-0.5 rounded whitespace-nowrap ${className}`}
    >
      {text}
    </span>
  )
}

function buildCompactBadges(quality: Quality): React.ReactNode[] {
  const badges: React.ReactNode[] = []
  if (quality.resolution) {
    const cls = RESOLUTION_COLORS[quality.resolution] || 'bg-gray-600/20 text-text-primary border-strong/30'
    badges.push(<Badge key="res" text={quality.resolution} className={cls} />)
  }
  if (quality.hdr) {
    badges.push(<Badge key="hdr" text="HDR" className="bg-yellow-500/20 text-yellow-700 dark:text-yellow-300 border-yellow-500/30" title="HDR" />)
  }
  if (quality.dv) {
    badges.push(<Badge key="dv" text="DV" className="bg-yellow-500/20 text-yellow-700 dark:text-yellow-300 border-yellow-500/30" title="Dolby Vision" />)
  }
  if (quality.source) {
    const cls = SOURCE_COLORS[quality.source] || 'bg-gray-600/20 text-text-primary border-strong/30'
    badges.push(<Badge key="src" text={quality.source} className={cls} />)
  }
  if (quality.codec) {
    badges.push(<Badge key="codec" text={quality.codec} className="bg-indigo-500/20 text-indigo-700 dark:text-indigo-300 border-indigo-500/30" />)
  }
  if (quality.remux) {
    badges.push(<Badge key="rmx" text="REMUX" className="bg-pink-500/20 text-pink-700 dark:text-pink-300 border-pink-500/30" />)
  }
  return badges
}

function buildExtendedBadges(quality: Quality): React.ReactNode[] {
  const badges: React.ReactNode[] = []
  if ((quality.audio?.length ?? 0) > 0) {
    for (const a of (quality.audio ?? []).slice(0, 2)) {
      badges.push(<Badge key={`a-${a}`} text={a} className="bg-rose-500/15 text-rose-700 dark:text-rose-300 border-rose-500/25" />)
    }
  }
  if (quality.dubbed) {
    badges.push(<Badge key="dub" text="DUB" className="bg-orange-500/20 text-orange-700 dark:text-orange-300 border-orange-500/30" title="Dublado" />)
  }
  if (quality.repack) {
    badges.push(<Badge key="rep" text="REPACK" className="bg-gray-500/20 text-text-primary border-strong/30" />)
  }
  if (quality.proper) {
    badges.push(<Badge key="pro" text="PROPER" className="bg-gray-500/20 text-text-primary border-strong/30" />)
  }
  if (quality.extended) {
    badges.push(<Badge key="ext" text="EXT" className="bg-gray-500/20 text-text-primary border-strong/30" title="Extended/Director's Cut" />)
  }
  if (quality.year) {
    badges.push(<Badge key="yr" text={String(quality.year)} className="bg-surface-tertiary/40 text-text-secondary border-strong/30" />)
  }
  return badges
}

export default function QualityBadges({ quality, compact = false }: Props) {
  if (!quality) return null

  const badges = buildCompactBadges(quality)
  if (!compact) {
    badges.push(...buildExtendedBadges(quality))
  }

  if (badges.length === 0) return null

  return <div className="flex flex-wrap gap-1 items-center">{badges}</div>
}
