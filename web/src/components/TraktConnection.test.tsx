import i18next from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router'
import { expect, it, vi } from 'vitest'
import type { TraktStatus } from '../api/trakt'
import de from '../i18n/locales/de.json'
import en from '../i18n/locales/en.json'
import { TraktConnection } from './TraktConnection'

const state = vi.hoisted(() => ({ data: null as TraktStatus | null }))
vi.mock('../i18n', () => ({ errorMessage: () => 'Request failed' }))
vi.mock('../api/trakt', () => ({
  useTrakt: () => ({ data: state.data }),
  useConnectTrakt: () => ({ mutate: vi.fn() }),
  useDisconnectTrakt: () => ({ mutate: vi.fn() }),
  useSaveTrakt: () => ({ mutate: vi.fn() }),
  useSyncTrakt: () => ({ mutate: vi.fn() }),
  useTraktLists: () => ({ data: [{ name: 'My films', ids: { trakt: 7 }, item_count: 3 }], refetch: vi.fn() }),
}))
const connected: TraktStatus = { configured: true, connected: true, linked: true, syncing: false, username: 'trakt-user', enabled: true, watchlist: true, listIds: [7, 99], pollMinutes: 17, lastSyncAt: 0, nextSyncAt: 0, lastError: '', requested: 0, skipped: 0, failed: 0 }
async function render(lng: string) {
  const i18n = i18next.createInstance()
  await i18n.init({ lng, resources: { de: { translation: de }, en: { translation: en } } })
  return renderToStaticMarkup(<I18nextProvider i18n={i18n}><MemoryRouter><TraktConnection /></MemoryRouter></I18nextProvider>)
}
it.each(['de', 'en'])('shows a configurable per-account interval, list selection and unavailable lists in %s', async (lng) => {
  state.data = connected
  const markup = await render(lng)
  expect(markup).toContain('id="trakt-interval"')
  expect(markup).toContain('value="17"')
  expect(markup).toContain('My films')
  expect(markup).toContain('99')
  expect(markup).toContain('trakt-user')
  expect(markup).not.toContain('trakt.interval')
})
it('disables manual synchronization while a durable task is queued or running', async () => {
  state.data = { ...connected, syncing: true }
  const markup = await render('en')
  expect(markup).toMatch(/<button[^>]*disabled=""[^>]*>Sync now<\/button>/)
  expect(markup).toContain('Synchronization is queued or running')
})
it('shows setup guidance without connection controls when the app is not configured', async () => {
  state.data = { ...connected, configured: false, connected: false, linked: false, username: '' }
  const markup = await render('en')
  expect(markup).toContain('An administrator must configure')
  expect(markup).not.toContain('id="trakt-interval"')
  expect(markup).not.toContain('>Connect Trakt</button>')
})
