import { Loader2 } from 'lucide-react'

interface LoadingStateProps {
  readonly size?: 'sm' | 'default'
  readonly label?: string
}

export function LoadingState({ size = 'default', label }: LoadingStateProps) {
  const sizeClass = size === 'sm' ? 'w-5 h-5' : 'w-8 h-8'
  const paddingClass = size === 'sm' ? 'py-16' : 'py-20'

  return (
    <div
      role="status"
      aria-live="polite"
      aria-busy="true"
      className={`flex flex-col items-center justify-center ${paddingClass} text-center text-text-muted`}
    >
      <Loader2 className={`${sizeClass} animate-spin`} aria-hidden />
      {label && <p className="text-sm mt-3 text-text-secondary">{label}</p>}
    </div>
  )
}
