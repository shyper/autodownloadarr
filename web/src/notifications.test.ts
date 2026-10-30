import i18next from 'i18next'
import { expect, it } from 'vitest'
import type { NotificationSettings } from './api/types'
import de from './i18n/locales/de.json'
import en from './i18n/locales/en.json'
import { notificationDraft, notificationUrlLabel } from './notifications'

const empty: NotificationSettings = { enabled: false, provider: '', url: '', chatId: '', hasToken: false, useBotToken: false }

it.each(['de', 'en'])('renders a translated URL label for unconfigured notifications in %s', async (lng) => {
  const instance = i18next.createInstance()
  await instance.init({ lng, resources: { de: { translation: de }, en: { translation: en } } })
  const draft = notificationDraft(empty)
  expect(draft.provider).toBe('webhook')
  const messages = lng === 'de' ? de : en
  expect(notificationUrlLabel(instance.t.bind(instance), draft.provider)).toBe(messages.settings.notifications.url.webhook)
  expect(notificationUrlLabel(instance.t.bind(instance), '')).toBe(messages.settings.notifications.urlLabel)
  expect(notificationUrlLabel(instance.t.bind(instance), 'discord')).toBe(messages.settings.notifications.url.discord)
  expect(notificationUrlLabel(instance.t.bind(instance), 'ntfy')).toBe(messages.settings.notifications.url.ntfy)
})

it('starts Telegram setup with the shared bot token and without enabling or inheriting another channel', () => {
  const existing: NotificationSettings = { ...empty, enabled: true, provider: 'discord', url: 'https://example.com/private-webhook' }
  const draft = notificationDraft(existing, 'telegram')
  expect(draft).toMatchObject({ enabled: false, provider: 'telegram', useBotToken: true, url: '', chatId: '' })
  expect(existing.provider).toBe('discord')
})

it('preserves existing Telegram settings and the selected credential source', () => {
  const existing: NotificationSettings = { ...empty, enabled: true, provider: 'telegram', chatId: '42', hasToken: true }
  expect(notificationDraft(existing, 'telegram')).toEqual(existing)
})
