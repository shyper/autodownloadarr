import i18next from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'
import { expect, it, vi } from 'vitest'
import de from '../../i18n/locales/de.json'
import en from '../../i18n/locales/en.json'
import { NotificationSettings } from './NotificationSettings'

vi.mock('../../i18n', () => ({ errorMessage: () => 'Request failed' }))

vi.mock('../../api/config', () => ({
  useNotification: () => ({ data: { enabled: true, provider: 'pushover', hasToken: true, hasUserKey: true, device: 'phone', disabledEvents: [] } }),
  useSaveNotification: () => ({ mutate: vi.fn() }),
  useTestNotification: () => ({ mutate: vi.fn() }),
}))

it.each(['de', 'en'])('renders the translated Pushover form with masked saved credentials in %s', async (lng) => {
  const i18n = i18next.createInstance()
  await i18n.init({ lng, resources: { de: { translation: de }, en: { translation: en } } })
  const markup = renderToStaticMarkup(<I18nextProvider i18n={i18n}><NotificationSettings /></I18nextProvider>)
  expect(markup).toContain('<option value="pushover" selected="">Pushover</option>')
  expect(markup.match(/type="password"/g)).toHaveLength(2)
  expect(markup).toContain('id="notification-pushover-device"')
  expect(markup).toContain('value="phone"')
  expect(markup).not.toContain('id="notification-url"')
  expect(markup).not.toContain('settings.notifications.')
})
