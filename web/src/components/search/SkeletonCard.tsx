// Animated placeholder card shown while the search hasn't returned results yet.
// Lives in its own file to avoid bloating the SearchPage (god-file).
export function SkeletonCard() {
  return (
    <div className="card animate-pulse flex flex-col gap-3">
      <div className="h-4 bg-surface-tertiary rounded w-3/4" />
      <div className="h-3 bg-surface-tertiary rounded w-1/4" />
      <div className="grid grid-cols-2 gap-2">
        <div className="h-3 bg-surface-tertiary rounded" />
        <div className="h-3 bg-surface-tertiary rounded" />
        <div className="h-3 bg-surface-tertiary rounded" />
        <div className="h-3 bg-surface-tertiary rounded" />
      </div>
      <div className="flex gap-2 pt-1 border-t border-default">
        <div className="h-7 bg-surface-tertiary rounded flex-1" />
        <div className="h-7 bg-surface-tertiary rounded flex-1" />
      </div>
    </div>
  )
}
