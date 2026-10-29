import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { useOnboarding } from '../api/config'
import { useMe, useSystemStatus, useWarnings } from '../api/hooks'
import { useDownloads, useMovies } from '../api/movies'
import { useEpisodeDownloads, useSeriesList } from '../api/series'
import type { EpisodeDownload, MovieState } from '../api/types'
import { DownloadProgress } from '../components/MovieBits'
import { Alert, Card } from '../components/ui'

const counted: MovieState[] = ['downloading', 'available', 'watched', 'waiting', 'failed']

export function DashboardPage() {
  const { t } = useTranslation()
  const { data: user } = useMe()
  const { data: status } = useSystemStatus()
  const onboarding = useOnboarding(user?.role === 'admin')
  const warnings = useWarnings()
  const movies = useMovies()
  const downloads = useDownloads()
  const series = useSeriesList()
  const episodeDownloads = useEpisodeDownloads()
  const readyEpisodes = series.data?.reduce((n, s) => n + (s.counts.available ?? 0), 0) ?? 0

  const count = (s: MovieState) => movies.data?.filter((m) => m.state === s).length ?? 0
  const titleOf = (id: number) => movies.data?.find((m) => m.id === id)?.title ?? ''
  const episodeLabel = (d: EpisodeDownload) =>
    `${series.data?.find((s) => s.id === d.seriesId)?.title ?? ''} · S${String(d.season).padStart(2, '0')}E${String(d.episode).padStart(2, '0')}`
  const active = [
    ...(downloads.data ?? []).map((d) => ({ key: `m${d.movieId}`, to: `/movies/${d.movieId}`, label: titleOf(d.movieId), download: d })),
    ...(episodeDownloads.data ?? []).map((d) => ({ key: `e${d.episodeId}`, to: `/series/${d.seriesId}`, label: episodeLabel(d), download: d })),
  ]

  return (
    <div className="space-y-8">
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

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-5">
        {counted.map((s) => (
          <Link key={s} to="/movies">
            <Card className="space-y-1 p-4 hover:border-accent/50">
              <p className="text-2xl font-semibold">{count(s)}</p>
              <p className="text-xs text-muted">{t(`movies.state.${s}`)}</p>
            </Card>
          </Link>
        ))}
      </div>

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

      <div className="grid gap-4 md:grid-cols-2">
        <Card className="space-y-2">
          <div className="flex items-center justify-between gap-2">
            <h2 className="font-semibold">{t('nav.series')}</h2>
            <Link to="/series" className="text-sm text-accent hover:underline">
              {t('dashboard.openSeries')}
            </Link>
          </div>
          <p className="text-sm text-muted">
            {t('dashboard.seriesSummary', { count: series.data?.length ?? 0, ready: readyEpisodes })}
          </p>
        </Card>
        <Card className="space-y-2">
          <h2 className="font-semibold">{t('dashboard.system.title')}</h2>
          <dl className="grid grid-cols-2 gap-y-1 text-sm">
            <dt className="text-muted">{t('dashboard.system.version')}</dt>
            <dd className="font-mono">{status?.version}</dd>
          </dl>
        </Card>
      </div>
    </div>
  )
}
