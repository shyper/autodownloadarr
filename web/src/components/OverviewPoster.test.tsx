import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router'
import { beforeAll, expect, it } from 'vitest'
import type { OverviewTitle } from '../dashboard'
import de from '../i18n/locales/de.json'
import { OverviewPoster } from './OverviewPoster'

const i18n = createInstance()
beforeAll(async () => { await i18n.init({ lng: 'de', resources: { de: { translation: de } } }) })

function render(overrides: Partial<OverviewTitle>) {
  const item: OverviewTitle = { key: 'movie-1', tmdbId: 1, title: 'Film', mediaType: 'movie', year: 2024, overview: '', href: '/movies/1', ...overrides }
  return renderToStaticMarkup(<I18nextProvider i18n={i18n}><MemoryRouter><OverviewPoster item={item} showPlayable onChoose={() => {}} /></MemoryRouter></I18nextProvider>)
}

it('shows an eye for playable downloads and only a check for completed movies', () => {
  const downloading = render({ state: 'downloading', playable: true })
  expect(downloading).toContain('title="Jetzt schon anschaubar"')
  expect(downloading).toContain('Lädt')
  const completed = render({ state: 'available', ready: true, playable: true })
  expect(completed).toContain('title="Bereit zum Schauen"')
  expect(completed).not.toContain('title="Jetzt schon anschaubar"')
  expect(completed).not.toContain('role="progressbar"')
})

it('shows queued covers in grayscale with an empty progress bar and a translated badge', () => {
  const pending = render({ state: 'queued' })
  expect(pending).toContain('grayscale')
  expect(pending).toContain('Ausstehend')
  expect(pending).toContain('aria-valuenow="0"')
  expect(pending).not.toContain('title="Jetzt schon anschaubar"')
})
