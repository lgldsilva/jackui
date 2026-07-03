// EmptyState — estado vazio (ícone + título + descrição) das abas de downloads.
export function EmptyState({ icon, title, description }: { readonly icon: React.ReactNode; readonly title: string; readonly description: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="text-text-muted mb-3">{icon}</div>
      <h3 className="text-lg font-semibold text-text-secondary mb-1">{title}</h3>
      <p className="text-sm text-text-muted max-w-md">{description}</p>
    </div>
  )
}
