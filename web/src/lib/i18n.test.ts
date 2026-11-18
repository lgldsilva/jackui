import { describe, it, expect } from 'vitest'
import i18n from './i18n'

describe('i18n runtime (i18next 27 / react-i18next 18)', () => {
  it('loads both locales', async () => {
    await i18n.changeLanguage('pt-BR')
    expect(i18n.hasResourceBundle('pt-BR', 'translation')).toBe(true)
    expect(i18n.hasResourceBundle('en-US', 'translation')).toBe(true)
  })

  it('renders plural and interpolated keys in both languages', async () => {
    for (const lng of ['pt-BR', 'en-US']) {
      await i18n.changeLanguage(lng)
      const one = i18n.t('downloads.clear_failed_message', { count: 1 })
      const many = i18n.t('downloads.clear_failed_message', { count: 5 })
      expect(one).not.toBe('downloads.clear_failed_message')
      expect(many).toContain('5')
      expect(many).not.toBe(one)
    }
  })

  it('does not HTML-escape interpolated values', async () => {
    await i18n.changeLanguage('en-US')
    const out = i18n.t('downloads.clear_failed_message', { count: 2 })
    expect(out).not.toContain('&#x2F;')
  })
})
