import { useTranslation } from 'react-i18next'
import { languages } from '../i18n'

export function LanguageSelect() {
  const { t, i18n } = useTranslation()
  return (
    <label className="inline-flex items-center gap-2 text-sm text-muted">
      <span className="sr-only">{t('nav.language')}</span>
      <svg viewBox="0 0 20 20" className="size-4" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
        <circle cx="10" cy="10" r="7.5" />
        <path d="M2.5 10h15M10 2.5c2 2.2 3 4.7 3 7.5s-1 5.3-3 7.5c-2-2.2-3-4.7-3-7.5s1-5.3 3-7.5Z" />
      </svg>
      <select
        value={i18n.resolvedLanguage}
        onChange={(e) => void i18n.changeLanguage(e.target.value)}
        className="rounded-md border border-line bg-surface px-2 py-1 text-sm text-fg focus:border-accent focus:outline-none"
      >
        {languages.map((l) => (
          <option key={l.code} value={l.code}>
            {l.label}
          </option>
        ))}
      </select>
    </label>
  )
}
