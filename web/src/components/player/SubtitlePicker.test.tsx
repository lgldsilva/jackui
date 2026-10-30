// Lane W2: the SubtitlePicker surfaces the virtual auto subtitle through the
// auto badge, and the pipeline failure as a quiet hint (toast-free) — the hint
// hides while the auto pick is active, and neither shows without an event.
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import i18n from '../../lib/i18n'
import { SubtitlePicker } from './SubtitlePicker'

beforeAll(async () => {
  await i18n.changeLanguage('en-US')
})

afterEach(cleanup)

const AUTO_BADGE = 'PT-BR (auto)'
const FAILED_HINT = 'No automatic subtitle found'

function renderPicker(over: Partial<Parameters<typeof SubtitlePicker>[0]> = {}) {
  return render(
    <SubtitlePicker
      handleCustomSubtitleUpload={() => {}}
      customSubName={null}
      clearCustomSub={() => {}}
      subLoading={false}
      subError=""
      subResults={[]}
      subActive={null}
      pickSubtitle={() => {}}
      setSubActive={() => {}}
      autoSub={null}
      autoFailed={false}
      {...over}
    />,
  )
}

describe('SubtitlePicker auto badge (lane W2)', () => {
  it('shows the auto badge while the virtual auto subtitle is applied', () => {
    renderPicker({ autoSub: { id: '77', language: 'pt-BR' } })
    expect(screen.getByText(AUTO_BADGE)).toBeInTheDocument()
    expect(screen.queryByText(FAILED_HINT)).not.toBeInTheDocument()
  })

  it('shows the quiet failed hint only when the pipeline failed and no auto pick is active', () => {
    renderPicker({ autoFailed: true })
    expect(screen.getByText(FAILED_HINT)).toBeInTheDocument()
    expect(screen.queryByText(AUTO_BADGE)).not.toBeInTheDocument()
  })

  it('prefers the badge over the hint when the auto pick is active', () => {
    renderPicker({ autoSub: { id: '77', language: 'pt-BR' }, autoFailed: true })
    expect(screen.getByText(AUTO_BADGE)).toBeInTheDocument()
    expect(screen.queryByText(FAILED_HINT)).not.toBeInTheDocument()
  })

  it('shows neither without an auto event', () => {
    renderPicker()
    expect(screen.queryByText(AUTO_BADGE)).not.toBeInTheDocument()
    expect(screen.queryByText(FAILED_HINT)).not.toBeInTheDocument()
  })
})
