import i18next from 'i18next'
import { expect, it } from 'vitest'
import en from './i18n/locales/en.json'
import de from './i18n/locales/de.json'
import { scopeLabel } from './scopes'

it.each(['de', 'en'])('translates OAuth scope names containing colons in %s', async (lng) => {
  const instance = i18next.createInstance()
  await instance.init({ lng, resources: { en: { translation: en }, de: { translation: de } } })
  const messages = lng === 'de' ? de : en
  for (const [scope, label] of Object.entries(messages.integrations.scopes)) {
    expect(scopeLabel(instance.t.bind(instance), scope)).toBe(label)
  }
  expect(scopeLabel(instance.t.bind(instance), 'unknown:scope')).toBe('unknown:scope')
})
