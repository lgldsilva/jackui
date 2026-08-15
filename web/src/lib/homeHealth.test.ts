import { describe, expect, it } from 'vitest'
import { allHomeSectionsFailed, failedHomeSections, HOME_SECTIONS, preserveOnFailure } from './homeHealth'

describe('home health aggregation', () => {
  it('keeps the failed sections visible for a partial outage', () => {
    const results = HOME_SECTIONS.map(section => ({
      section,
      status: section === 'trending' ? 'rejected' as const : 'fulfilled' as const,
    }))

    expect(failedHomeSections(results)).toEqual(['trending'])
    expect(allHomeSectionsFailed(results)).toBe(false)
  })

  it('only treats Home as globally failed when every rail failed', () => {
    const results = HOME_SECTIONS.map(section => ({ section, status: 'rejected' as const }))

    expect(failedHomeSections(results)).toEqual([...HOME_SECTIONS])
    expect(allHomeSectionsFailed(results)).toBe(true)
  })

  it('keeps previously loaded content when a retry rejects a rail', () => {
    expect(preserveOnFailure(['old'], { status: 'rejected', reason: new Error('offline') })).toEqual(['old'])
    expect(preserveOnFailure(['old'], { status: 'fulfilled', value: ['new'] })).toEqual(['new'])
  })
})
