import i18n, { type TFunction } from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'
import { ApiError } from '../api/client'
import de from './locales/de.json'
import en from './locales/en.json'

export const languages = [
  { code: 'en', label: 'English' },
  { code: 'de', label: 'Deutsch' },
] as const

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: { en: { translation: en }, de: { translation: de } },
    supportedLngs: languages.map((l) => l.code),
    fallbackLng: 'en',
    load: 'languageOnly',
    interpolation: { escapeValue: false }, // React escapes already
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: 'autodownloadarr.language',
      caches: ['localStorage'],
    },
  })

const syncHtmlLang = (lng: string) => document.documentElement.setAttribute('lang', lng)
syncHtmlLang(i18n.resolvedLanguage ?? 'en')
i18n.on('languageChanged', syncHtmlLang)

/** Translates an error from the API into a message for the user. */
export function errorMessage(t: TFunction, err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'too_many_attempts') {
      return t('errors.too_many_attempts', { minutes: Math.max(1, Math.ceil((err.retryAfter ?? 60) / 60)) })
    }
    return t(`errors.${err.code}`, { defaultValue: t('errors.unknown') })
  }
  return t('errors.unknown')
}

export default i18n
