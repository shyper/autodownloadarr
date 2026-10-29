import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { useHistory, useQueue } from '../api/downloads'
import { useDownloads, useMovies } from '../api/movies'
import { useEpisodeDownloads, useSeriesList } from '../api/series'
import type { HistoryEntry } from '../api/types'
import { DownloadProgress, Problem } from '../components/MovieBits'
import { Alert, Badge, Button, Card, Select, Spinner } from '../components/ui'
import { formatBytes, formatDateTime } from '../format'
import { errorMessage } from '../i18n'

const code = (season: number, episode: number) => `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`

export function DownloadsPage() {
  const { t } = useTranslation()
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">{t('downloads.title')}</h1>
      <Active />
      <Queue />
      <History />
    </div>
  )
}

/** Running downloads of movies and episodes, with live progress. */
function Active() {
  const { t } = useTranslation()
  const movies = useMovies()
  const series = useSeriesList()
  const movieDownloads = useDownloads()
  const episodeDownloads = useEpisodeDownloads()
  const items = [
    ...(movieDownloads.data ?? []).map((d) => {
      const m = movies.data?.find((x) => x.id === d.movieId)
      return { key: `m${d.movieId}`, to: `/movies/${d.movieId}`, label: m ? `${m.title}${m.year ? ` (${m.year})` : ''}` : '', download: d }
    }),
    ...(episodeDownloads.data ?? []).map((d) => ({
      key: `e${d.episodeId}`,
      to: `/series/${d.seriesId}`,
      label: `${series.data?.find((s) => s.id === d.seriesId)?.title ?? ''} · ${code(d.season, d.episode)}`,
      download: d,
    })),
  ]
  return (
    <Card className="space-y-4">
      <h2 className="font-semibold">{t('downloads.active')}</h2>
      {items.length === 0 ? (
        <p className="text-sm text-muted">{t('dashboard.noDownloads')}</p>
      ) : (
        <ul className="space-y-4">
          {items.map((a) => (
            <li key={a.key} className="space-y-1">
              <Link to={a.to} className="text-sm font-medium hover:underline">
                {a.label}
              </Link>
              <p className="font-mono text-xs break-all text-muted">{a.download.releaseTitle}</p>
              <DownloadProgress download={a.download} />
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}

function Queue() {
  const { t } = useTranslation()
  const queue = useQueue()
  if (!queue.data?.length) return null
  return (
    <Card className="space-y-3">
      <h2 className="font-semibold">{t('downloads.queue')}</h2>
      <p className="text-sm text-muted">{t('downloads.queueHint')}</p>
      <ol className="list-inside list-decimal space-y-1 text-sm">
        {queue.data.map((q) => (
          <li key={`${q.kind}${q.movieId ?? q.episodeId}`}>
            <Link to={q.kind === 'movie' ? `/movies/${q.movieId}` : `/series/${q.seriesId}`} className="hover:underline">
              {q.label}
            </Link>
          </li>
        ))}
      </ol>
    </Card>
  )
}

const resultTone = { success: 'ok', failed: 'danger', cancelled: 'muted', running: 'accent' } as const

function History() {
  const { t } = useTranslation()
  const [kind, setKind] = useState('')
  const [result, setResult] = useState('')
  const history = useHistory(kind, result)
  const items = history.data?.pages.flatMap((p) => p.items) ?? []

  return (
    <Card className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <h2 className="font-semibold">{t('downloads.history')}</h2>
        <div className="grid w-full grid-cols-2 gap-2 sm:w-auto">
          <Select aria-label={t('downloads.filterKind')} value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="">{t('downloads.allKinds')}</option>
            <option value="movie">{t('nav.movies')}</option>
            <option value="episode">{t('nav.series')}</option>
          </Select>
          <Select aria-label={t('downloads.filterResult')} value={result} onChange={(e) => setResult(e.target.value)}>
            <option value="">{t('downloads.allResults')}</option>
            {(['success', 'failed', 'cancelled', 'running'] as const).map((r) => (
              <option key={r} value={r}>
                {t(`movies.attempts.result.${r}`)}
              </option>
            ))}
          </Select>
        </div>
      </div>
      {history.isError && <Alert>{errorMessage(t, history.error)}</Alert>}
      {history.isPending ? (
        <Spinner />
      ) : items.length === 0 ? (
        <p className="text-sm text-muted">{t('downloads.empty')}</p>
      ) : (
        <ul className="divide-y divide-line">
          {items.map((h) => (
            <HistoryRow key={h.id} h={h} />
          ))}
        </ul>
      )}
      {history.hasNextPage && (
        <div className="flex justify-center">
          <Button variant="secondary" loading={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>
            {t('downloads.more')}
          </Button>
        </div>
      )}
    </Card>
  )
}

function HistoryRow({ h }: { h: HistoryEntry }) {
  const { t, i18n } = useTranslation()
  const lang = i18n.resolvedLanguage
  const to = h.kind === 'movie' ? (h.movieId ? `/movies/${h.movieId}` : '') : h.seriesId ? `/series/${h.seriesId}` : ''
  const seconds = h.finishedAt ? (new Date(h.finishedAt).getTime() - new Date(h.startedAt).getTime()) / 1000 : 0
  return (
    <li className="space-y-1 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={resultTone[h.result]}>{t(`movies.attempts.result.${h.result}`)}</Badge>
        <span className="text-xs text-muted">{h.kind === 'movie' ? t('downloads.kindMovie') : t('downloads.kindEpisode')}</span>
        {to ? (
          <Link to={to} className="min-w-0 text-sm font-medium hover:underline">
            {h.label}
          </Link>
        ) : (
          <span className="min-w-0 text-sm font-medium">
            {h.label} <span className="text-xs font-normal text-muted">({t('downloads.removed')})</span>
          </span>
        )}
      </div>
      <p className="line-clamp-2 font-mono text-xs break-all text-muted" title={h.releaseTitle}>
        {h.releaseTitle}
      </p>
      <p className="text-xs text-muted">
        {formatDateTime(h.startedAt, lang)}
        {h.size > 0 && <> · {formatBytes(h.size)}</>}
        {seconds > 0 && <> · {t('downloads.duration', { minutes: Math.max(1, Math.round(seconds / 60)) })}</>}
        {h.indexerName && <> · {h.indexerName}</>}
        {h.errorKind && (
          <>
            {' '}
            · <Problem value={h.errorKind} />
          </>
        )}
      </p>
    </li>
  )
}
