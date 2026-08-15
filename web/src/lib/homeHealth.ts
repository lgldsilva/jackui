export const HOME_SECTIONS = ['continue', 'recent', 'recommended', 'trending', 'music'] as const

export type HomeSection = typeof HOME_SECTIONS[number]

export type HomeSectionResult = {
  readonly section: HomeSection
  readonly status: 'fulfilled' | 'rejected'
}

export function failedHomeSections(results: readonly HomeSectionResult[]): HomeSection[] {
  return results.filter(result => result.status === 'rejected').map(result => result.section)
}

export function allHomeSectionsFailed(results: readonly HomeSectionResult[]): boolean {
  return results.length === HOME_SECTIONS.length && results.every(result => result.status === 'rejected')
}

export function preserveOnFailure<T>(previous: T, result: PromiseSettledResult<T>): T {
  return result.status === 'fulfilled' ? result.value : previous
}
