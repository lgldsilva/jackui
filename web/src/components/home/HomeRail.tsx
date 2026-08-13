import { Link } from 'react-router-dom'
import { ChevronRight } from 'lucide-react'

type HomeRailProps = {
  readonly title: string
  readonly href?: string
  readonly seeAllLabel?: string
  readonly children: React.ReactNode
  readonly empty?: boolean
}

export function HomeRail({ title, href, seeAllLabel, children, empty }: HomeRailProps) {
  if (empty) return null
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-end justify-between gap-3 px-1">
        <h2 className="text-lg font-semibold text-text-primary tracking-tight">{title}</h2>
        {href && (
          <Link
            to={href}
            className="text-xs text-text-secondary hover:text-green-400 flex items-center gap-0.5"
          >
            {seeAllLabel} <ChevronRight className="w-3.5 h-3.5" />
          </Link>
        )}
      </div>
      <div className="flex gap-3 overflow-x-auto pb-2 snap-x snap-mandatory [scrollbar-width:thin]">
        {children}
      </div>
    </section>
  )
}

export function homeCardClass(): string {
  return 'snap-start shrink-0 w-36 sm:w-40'
}
