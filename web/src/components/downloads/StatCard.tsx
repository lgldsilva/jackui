// StatCard — cartão de resumo (download/upload/peers/fila) no topo do dashboard
// de downloads. Puramente apresentacional: recebe tudo via props.
export function StatCard({ icon, label, value, subtitle, gradient, iconColor, pulse }: {
  readonly icon: React.ReactNode
  readonly label: string
  readonly value: string
  readonly subtitle?: string
  readonly gradient: string
  readonly iconColor: string
  readonly pulse?: boolean
}) {
  return (
    <div className={`
      relative overflow-hidden rounded-xl border border-default/50
      bg-gradient-to-br ${gradient} backdrop-blur-sm
      p-4 flex flex-col gap-1
    `}>
      <div className="flex items-center gap-2">
        <span className={`${iconColor} ${pulse ? 'animate-pulse' : ''}`}>{icon}</span>
        <span className="text-xs text-text-secondary uppercase tracking-wider font-medium">{label}</span>
      </div>
      <span className="text-xl font-bold text-text-primary tracking-tight">{value}</span>
      {subtitle && <span className="text-xs text-text-muted">{subtitle}</span>}
    </div>
  )
}
