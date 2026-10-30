import type { TFunction } from 'i18next'
import type { NotificationSettings } from './api/types'

export function notificationDraft(initial: NotificationSettings, provider?: 'telegram'): NotificationSettings {
  if (provider && initial.provider !== provider) {
    return { enabled: false, provider, url: '', chatId: '', hasToken: false, useBotToken: true }
  }
  return { ...initial, provider: initial.provider || 'webhook', useBotToken: initial.useBotToken ?? false }
}

export function notificationUrlLabel(t: TFunction, provider: NotificationSettings['provider']) {
  return t([`settings.notifications.url.${provider}`, 'settings.notifications.urlLabel'])
}
