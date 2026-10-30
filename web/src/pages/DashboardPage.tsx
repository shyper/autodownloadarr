import { Queue } from './DownloadsPage'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { useOnboarding } from '../api/config'
import { useMe, useWarnings } from '../api/hooks'
import { useDownloads, useMovies } from '../api/movies'
import { useEpisodeDownloads, useSeriesList } from '../api/series'
import type { EpisodeDownload } from '../api/types'
import { DownloadProgress, WatchProgress } from '../components/MovieBits'
import { Alert, Badge, Card } from '../components/ui'

const code = (season: number, episode: number) => `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`

export function DashboardPage() {
  const { t } = useTranslation()
  const { data: user } = useMe()
  const onboarding = useOnboarding(user?.role === 'admin')
  const warnings = useWarnings()

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold">{t('dashboard.greeting', { name: user?.username })}</h1>
        <p className="text-sm text-muted">{t('dashboard.subtitle')}</p>
      </div>

      {warnings.data?.map((w) => (
        <Alert key={w.code}>{t(`warnings.${w.code}`, { detail: w.detail, defaultValue: w.detail })}</Alert>
      ))}

      {onboarding.data && !onboarding.data.finished && (
        <Card className="flex flex-wrap items-center justify-between gap-3 border-accent/40">
          <p className="text-sm">{t('dashboard.setupPending')}</p>
          <Link to="/setup" className="text-sm font-medium text-accent hover:underline">
            {t('dashboard.continueSetup')}
          </Link>
        </Card>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        <MoviesCard />
        <SeriesCard />
      </div>

      <DownloadsCard />
      <Queue />
    </div>
  )
}

/** A dashboard card: title with a link, a line of figures and a list. */
function SummaryCard({ title, to, figures, children }: { title: string; to: string; figures: ReactNode; children?: ReactNode }) {
  const { t } = useTranslation()
  return (
    <Card className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <h2 className="font-semibold">{title}</h2>
        <Link to={to} className="text-sm text-accent hover:underline">
          {t('dashboard.open')}
        </Link>
      </div>
      <div className="flex flex-wrap gap-2">{figures}</div>
      {children}
    </Card>
  )
}

function MoviesCard() {
  const { t } = useTranslation()
  const movies = useMovies()
  const list = movies.data ?? []
  const count = (...states: string[]) => list.filter((m) => states.includes(m.state)).length
  const ready = count('available')
  const loading = count('downloading', 'queued', 'searching')
  const problems = count('failed', 'waiting')
  const watching = list.filter((m) => m.state === 'available' && m.progress > 0 && m.progress < 1)

  return (
    <SummaryCard
      title={t('nav.movies')}
      to="/movies"
      figures={
        list.length === 0 ? (
          <span className="text-sm text-muted">{t('dashboard.noMovies')}</span>
        ) : (
          <>
            <Badge tone="ok">{t('dashboard.moviesReady', { count: ready })}</Badge>
            {loading > 0 && <Badge>{t('dashboard.loading', { count: loading })}</Badge>}
            {problems > 0 && <Badge tone="danger">{t('dashboard.problems', { count: problems })}</Badge>}
          </>
        )
      }
    >
      {watching.length > 0 && (
        <ul className="space-y-3 border-t border-line pt-3">
          {watching.slice(0, 3).map((m) => (
            <li key={m.id} className="space-y-1">
              <Link to={`/movies/${m.id}`} className="text-sm font-medium hover:underline">
                {m.title} {m.year > 0 && <span className="text-muted">({m.year})</span>}
              </Link>
              <WatchProgress progress={m.progress} runtime={m.runtime} />
            </li>
          ))}
        </ul>
      )}
    </SummaryCard>
  )
}

function SeriesCard() {
  const { t } = useTranslation()
  const series = useSeriesList()
  const list = series.data ?? []
  const sum = (state: 'available' | 'downloading' | 'queued' | 'searching' | 'failed') =>
    list.reduce((n, s) => n + (s.counts[state] ?? 0), 0)
  const ready = sum('available')
  const loading = sum('downloading') + sum('queued') + sum('searching')
  const problems = sum('failed')
  const watching = list.filter((s) => s.current && s.current.progress > 0 && s.current.progress < 1)

  return (
    <SummaryCard
      title={t('nav.series')}
      to="/series"
      figures={
        list.length === 0 ? (
          <span className="text-sm text-muted">{t('dashboard.noSeries')}</span>
        ) : (
          <>
            <Badge tone="muted">{t('dashboard.seriesCount', { count: list.length })}</Badge>
            <Badge tone="ok">{t('series.ready', { count: ready })}</Badge>
            {loading > 0 && <Badge>{t('dashboard.loading', { count: loading })}</Badge>}
            {problems > 0 && <Badge tone="danger">{t('dashboard.problems', { count: problems })}</Badge>}
          </>
        )
      }
    >
      {watching.length > 0 && (
        <ul className="space-y-3 border-t border-line pt-3">
          {watching.slice(0, 3).map((s) => (
            <li key={s.id} className="space-y-1">
              <Link to={`/series/${s.id}`} className="text-sm font-medium hover:underline">
                {s.title} · {code(s.current!.season, s.current!.episode)}
              </Link>
              <WatchProgress progress={s.current!.progress} runtime={0} />
            </li>
          ))}
        </ul>
      )}
    </SummaryCard>
  )
}

/** Running downloads of movies and episodes. */
function DownloadsCard() {
  const { t } = useTranslation()
  const movies = useMovies()
  const series = useSeriesList()
  const downloads = useDownloads()
  const episodeDownloads = useEpisodeDownloads()
  const titleOf = (id: number) => movies.data?.find((m) => m.id === id)?.title ?? ''
  const episodeLabel = (d: EpisodeDownload) => `${series.data?.find((s) => s.id === d.seriesId)?.title ?? ''} · ${code(d.season, d.episode)}`
  const active = [
    ...(downloads.data ?? []).map((d) => ({ key: `m${d.movieId}`, to: `/movies/${d.movieId}`, label: titleOf(d.movieId), download: d })),
    ...(episodeDownloads.data ?? []).map((d) => ({ key: `e${d.episodeId}`, to: `/series/${d.seriesId}`, label: episodeLabel(d), download: d })),
  ]

  return (
    <Card className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="font-semibold">{t('dashboard.downloads')}</h2>
        <Link to="/downloads" className="text-sm text-accent hover:underline">
          {t('dashboard.allDownloads')}
        </Link>
      </div>
      {active.length ? (
        <ul className="space-y-4">
          {active.map((a) => (
            <li key={a.key} className="space-y-1">
              <Link to={a.to} className="text-sm font-medium hover:underline">
                {a.label}
              </Link>
              <p className="font-mono text-xs break-all text-muted">{a.download.releaseTitle}</p>
              <DownloadProgress download={a.download} />
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted">{t('dashboard.noDownloads')}</p>
      )}
    </Card>
  )
}
