import { useTranslation } from 'react-i18next'

/** Move keyboard focus to the current route's content landmark. */
export function focusMainContent(): boolean {
  const target = document.getElementById('main-content')
  if (!(target instanceof HTMLElement)) return false

  target.focus({ preventScroll: true })
  target.scrollIntoView?.({ block: 'start' })
  return true
}

export default function SkipLink() {
  const { t } = useTranslation()

  return (
    <a
      className="skip-link"
      href="#main-content"
      onClick={event => {
        if (focusMainContent()) event.preventDefault()
      }}
    >
      {t('common.skipToContent')}
    </a>
  )
}
