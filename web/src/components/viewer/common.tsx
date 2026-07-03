import { Loader2, AlertCircle } from 'lucide-react'

// Re-export do formatador canônico (variante com '—' para zero/ausente), que era
// duplicado aqui. Mantém o nome `formatBytes` pros viewers que já importam daqui.
export { formatBytesOrDash as formatBytes } from '../../lib/format'

// Shared loading/error states for the viewer family — keeps each viewer free
// of copy-pasted spinner markup.

export function ViewerLoading({ hint }: { readonly hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-12 text-text-muted gap-2">
      <Loader2 className="w-6 h-6 animate-spin" />
      {hint && <p className="text-xs">{hint}</p>}
    </div>
  )
}

export function ViewerError({ message }: { readonly message: string }) {
  return (
    <div className="p-4 text-sm text-red-400 flex items-center gap-2">
      <AlertCircle className="w-4 h-4 flex-shrink-0" /> {message}
    </div>
  )
}

