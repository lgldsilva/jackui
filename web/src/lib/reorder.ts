// reorder moves the item at index `from` to index `to`, returning a NEW array
// (the input is untouched). Out-of-range or no-op moves return the list as-is
// (a shallow copy). Used to drag-reorder the search tabs.
export function reorder<T>(list: readonly T[], from: number, to: number): T[] {
  const out = [...list]
  if (from === to || from < 0 || to < 0 || from >= out.length || to >= out.length) {
    return out
  }
  const [moved] = out.splice(from, 1)
  out.splice(to, 0, moved)
  return out
}
