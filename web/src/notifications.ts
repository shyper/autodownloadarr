import type { TFunction } from 'i18next'
import type { NotificationEvent, NotificationSettings } from './api/types'

export function notificationDraft(initial: NotificationSettings, provider?: 'telegram'): NotificationSettings {
  if (provider && initial.provider !== provider) {
    return { enabled: false, provider, url: '', chatId: '', hasToken: false, useBotToken: true, disabledEvents: initial.disabledEvents ?? [] }
  }
  return { ...initial, provider: initial.provider || 'webhook', useBotToken: initial.useBotToken ?? false, disabledEvents: initial.disabledEvents ?? [] }
}

/** Switches one notification kind on or off. */
export function toggleNotificationEvent(disabled: NotificationEvent[], event: NotificationEvent, on: boolean): NotificationEvent[] {
  const rest = disabled.filter((e) => e !== event)
  return on ? rest : [...rest, event]
}

export function notificationUrlLabel(t: TFunction, provider: NotificationSettings['provider']) {
  return t([`settings.notifications.url.${provider}`, 'settings.notifications.urlLabel'])
}

/** Never carry credentials between providers; saved credentials remain usable when switching back. */
export function notificationProviderDraft(initial: NotificationSettings, current: NotificationSettings & { token?: string }, provider: NotificationSettings['provider']) {
  const saved = provider === initial.provider
  return {
    ...current, provider, token: '', userKey: '',
    hasToken: saved && initial.hasToken,
    hasUserKey: saved && !!initial.hasUserKey,
    useBotToken: saved && provider === 'telegram' && initial.useBotToken,
    device: saved ? initial.device : '',
  }
}
