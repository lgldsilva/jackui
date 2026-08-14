import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SkipLink from './SkipLink'

afterEach(cleanup)

describe('SkipLink', () => {
  it('moves focus to the current main landmark', () => {
    const main = document.createElement('main')
    main.id = 'main-content'
    main.tabIndex = -1
    main.scrollIntoView = vi.fn()
    document.body.append(main)

    render(<SkipLink />)
    fireEvent.click(screen.getByRole('link'))

    expect(main).toHaveFocus()
    expect(main.scrollIntoView).toHaveBeenCalledWith({ block: 'start' })
    main.remove()
  })
})
