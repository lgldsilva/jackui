// GroupHeader — section header (icon + label) inside the downloads tabs.
export function GroupHeader({ icon, label, color }: { readonly icon: React.ReactNode; readonly label: string; readonly color: string }) {
  return (
    <div className={`flex items-center gap-2 text-xs font-medium uppercase tracking-wider px-1 ${color}`}>
      {icon}{label}
    </div>
  )
}
