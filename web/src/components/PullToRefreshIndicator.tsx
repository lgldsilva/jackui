import { RefreshCw } from 'lucide-react'

type Props = {
  readonly pull: number
  readonly progress: number
  readonly refreshing: boolean
}

/**
 * Renders the pull-to-refresh indicator at the top of the page.
 * Uses `transform: translateY` to follow the finger; the icon rotates with progress.
 */
export default function PullToRefreshIndicator({ pull, progress, refreshing }: Props) {
  if (pull === 0 && !refreshing) return null

  const visible = refreshing ? 60 : pull
  const rotation = refreshing ? 0 : progress * 180

  return (
    <div
      className="fixed top-0 left-0 right-0 flex justify-center pointer-events-none z-40 transition-transform"
      style={{
        transform: `translateY(${Math.min(visible, 100) - 40}px)`,
        opacity: refreshing ? 1 : Math.min(1, progress * 1.5),
      }}
    >
      <div className="bg-surface-secondary/95 border border-default rounded-full p-2 shadow-lg backdrop-blur-sm">
        <RefreshCw
          className={`w-5 h-5 ${progress >= 1 || refreshing ? 'text-green-400' : 'text-text-secondary'} ${refreshing ? 'animate-spin' : ''}`}
          style={{ transform: refreshing ? undefined : `rotate(${rotation}deg)` }}
        />
      </div>
    </div>
  )
}
