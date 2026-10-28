import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useParams } from 'react-router'
import {
  useDeleteSeries,
  useDownloadEpisodeRelease,
  useEpisode,
  useEpisodeDownloads,
  useSearchEpisode,
  useSeries,
  useSkipEpisodeRelease,
  useUpdateSeries,
} from '../api/series'
import type { DeleteTrigger, Episode, SeriesDetail } from '../api/types'
import { DownloadProgress, Poster, Problem, Rejection, StateBadge } from '../components/MovieBits'
import { Alert, Badge, Button, Card, Checkbox, ProgressBar, SectionTitle, Spinner } from '../components/ui'
import { formatBytes, formatDateTime } from '../format'
import { errorMessage } from '../i18n'
import { TriggerFields } from '../components/TriggerFields'

export function SeriesDetailPage() {
  const { id } = useParams()
  const series = useSeries(Number(id))
  const { t } = useTranslation()
  if (series.isPending) return <Spinner />
  if (series.isError) return <Alert>{errorMessage(t, series.error)}</Alert>
  return <Detail s={series.data} />
}

function Detail({ s }: { s: SeriesDetail }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const remove = useDeleteSeries()
  const seasons = [...new Set(s.episodes.map((e) => e.season))].sort((a, b) => a - b)

  const confirmDelete = () => {
    if (!window.confirm(t('series.confirmDelete', { title: s.title }))) return
    remove.mutate(s.id, { onSuccess: () => navigate('/series') })
  }

  return (
    <div className="space-y-6">
      <Link to="/series" className="text-sm text-muted hover:text-fg">
        ← {t('series.title')}
      </Link>
      <div className="grid gap-6 md:grid-cols-[180px_1fr]">
        <Poster url={s.posterUrl} title={s.title} className="max-w-[180px]" />
        <div className="space-y-3">
          <h1 className="text-2xl font-semibold">
            {s.title} {s.year > 0 && <span className="text-muted">({s.year})</span>}
          </h1>
          <div className="flex flex-wrap gap-2">
            <Badge tone="ok">{t('series.ready', { count: s.counts.available ?? 0 })}</Badge>
            {s.keep && <Badge tone="ok">{t('movies.kept')}</Badge>}
            {s.paused && <Badge tone="muted">{t('series.paused')}</Badge>}
            {s.status && <Badge tone="muted">{s.status}</Badge>}
          </div>
          {s.overview && <p className="max-w-3xl text-sm text-muted">{s.overview}</p>}
          {remove.isError && <Alert>{errorMessage(t, remove.error)}</Alert>}
          <Button variant="ghost" onClick={confirmDelete}>
            {t('series.delete')}
          </Button>
        </div>
      </div>

      <SettingsCard s={s} allSeasons={seasons} />

      {seasons.map((season) => (
        <Card key={season} className="space-y-3">
          <h2 className="font-semibold">{t('series.season', { number: season })}</h2>
          <ul className="divide-y divide-line">
            {s.episodes
              .filter((e) => e.season === season)
              .map((e) => (
                <EpisodeRow key={e.id} e={e} />
              ))}
          </ul>
        </Card>
      ))}
    </div>
  )
}

function SettingsCard({ s, allSeasons }: { s: SeriesDetail; allSeasons: number[] }) {
  const { t } = useTranslation()
  const update = useUpdateSeries()
  const [windowSize, setWindowSize] = useState(s.windowSize)
  const [trigger, setTrigger] = useState<DeleteTrigger>(s.deleteTrigger)
  const [value, setValue] = useState(s.triggerValue)
  const [keep, setKeep] = useState(s.keep)
  const [paused, setPaused] = useState(s.paused)
  const [seasons, setSeasons] = useState<number[]>(s.seasons)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    update.mutate({ id: s.id, settings: { seasons, windowSize, deleteTrigger: trigger, triggerValue: value, keep, paused } })
  }

  return (
    <Card>
      <form onSubmit={submit} className="space-y-4">
        <SectionTitle title={t('series.settings')} description={t('series.settingsHint')} />
        {update.isError && <Alert>{errorMessage(t, update.error)}</Alert>}
        {update.isSuccess && <Alert tone="success">{t('settings.saved')}</Alert>}
        <TriggerFields windowSize={windowSize} trigger={trigger} value={value} onWindow={setWindowSize} onTrigger={setTrigger} onValue={setValue} />
        <div className="flex flex-wrap gap-x-5 gap-y-2">
          <Checkbox label={t('series.request.allSeasons')} checked={seasons.length === 0} onChange={() => setSeasons([])} />
          {allSeasons
            .filter((n) => n > 0)
            .map((n) => (
              <Checkbox
                key={n}
                label={t('series.season', { number: n })}
                checked={seasons.includes(n)}
                onChange={(e) => setSeasons(e.target.checked ? [...seasons, n].sort((a, b) => a - b) : seasons.filter((x) => x !== n))}
              />
            ))}
        </div>
        <div className="flex flex-wrap gap-x-6 gap-y-2">
          <Checkbox label={t('series.keep')} hint={t('series.keepHint')} checked={keep} onChange={(e) => setKeep(e.target.checked)} />
          <Checkbox label={t('series.pause')} hint={t('series.pauseHint')} checked={paused} onChange={(e) => setPaused(e.target.checked)} />
        </div>
        <div className="flex justify-end">
          <Button type="submit" loading={update.isPending}>
            {t('common.save')}
          </Button>
        </div>
      </form>
    </Card>
  )
}

function EpisodeRow({ e }: { e: Episode }) {
  const { t, i18n } = useTranslation()
  const downloads = useEpisodeDownloads()
  const search = useSearchEpisode()
  const skip = useSkipEpisodeRelease()
  const [open, setOpen] = useState(false)
  const dl = downloads.data?.find((d) => d.episodeId === e.id)
  const canSearch = ['waiting', 'failed', 'deleted', 'pending', 'skipped'].includes(e.state)
  const error = search.error ?? skip.error

  return (
    <li className="space-y-2 py-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="w-16 font-mono text-xs text-muted">
          S{String(e.season).padStart(2, '0')}E{String(e.episode).padStart(2, '0')}
        </span>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">{e.title || '–'}</p>
          <p className="text-xs text-muted">
            {e.airDate && new Date(e.airDate).toLocaleDateString(i18n.resolvedLanguage)}
            {e.deleteAt && <> · {t('movies.deletesAt', { date: formatDateTime(e.deleteAt, i18n.resolvedLanguage) })}</>}
          </p>
        </div>
        <StateBadge state={e.state} />
        {canSearch && (
          <Button variant="ghost" loading={search.isPending} onClick={() => search.mutate(e.id)}>
            {t('series.searchEpisode')}
          </Button>
        )}
        {e.state === 'downloading' && (
          <Button variant="ghost" loading={skip.isPending} onClick={() => skip.mutate(e.id)}>
            {t('movies.actions.skip')}
          </Button>
        )}
        <Button variant="ghost" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
          {t('series.details')}
        </Button>
      </div>
      {error && <Alert>{errorMessage(t, error)}</Alert>}
      {e.state === 'downloading' && dl && <DownloadProgress download={dl} />}
      {(e.state === 'available' || e.state === 'watched') && e.progress > 0 && (
        <div className="flex items-center gap-2">
          <ProgressBar value={e.progress} />
          <span className="text-xs whitespace-nowrap text-muted">{Math.round(e.progress * 100)} %</span>
        </div>
      )}
      {e.lastError && (e.state === 'waiting' || e.state === 'failed') && (
        <p className="text-xs text-muted">
          <Problem value={e.lastError} />
          {e.nextSearchAt && <> · {t('movies.nextSearch')}: {formatDateTime(e.nextSearchAt, i18n.resolvedLanguage)}</>}
        </p>
      )}
      {open && <EpisodeDetails id={e.id} />}
    </li>
  )
}

function EpisodeDetails({ id }: { id: number }) {
  const { t } = useTranslation()
  const ep = useEpisode(id, true)
  const pick = useDownloadEpisodeRelease()
  if (ep.isPending) return <Spinner />
  if (ep.isError) return <Alert>{errorMessage(t, ep.error)}</Alert>
  const d = ep.data
  return (
    <div className="space-y-3 rounded-xl bg-surface-2 p-3 text-sm">
      {d.releases.length === 0 ? (
        <p className="text-muted">{t('movies.releases.none')}</p>
      ) : (
        <ul className="space-y-2">
          {d.releases.map((r) => (
            <li key={r.id} className={`flex flex-wrap items-center gap-2 ${r.rejected || r.blocklisted ? 'text-muted' : ''}`}>
              <span className="min-w-0 flex-1 font-mono text-xs break-all">{r.title}</span>
              <span className="text-xs">{formatBytes(r.size)}</span>
              <span className="text-xs">
                {r.blocklisted ? (
                  <Badge tone="danger">{t('movies.releases.blocklisted')}</Badge>
                ) : r.rejected ? (
                  <Rejection value={r.rejected} />
                ) : (
                  <Badge tone="ok">{t('movies.releases.ok')}</Badge>
                )}
              </span>
              <Button variant="ghost" loading={pick.isPending && pick.variables?.releaseId === r.id} onClick={() => pick.mutate({ id, releaseId: r.id })}>
                {t('movies.releases.download')}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {d.attempts.length > 0 && (
        <ul className="space-y-1 border-t border-line pt-2">
          {d.attempts.map((a) => (
            <li key={a.id} className="text-xs">
              <Badge tone={a.result === 'success' ? 'ok' : a.result === 'failed' ? 'danger' : 'muted'}>{t(`movies.attempts.result.${a.result}`)}</Badge>{' '}
              <span className="font-mono break-all">{a.releaseTitle}</span>
              {a.errorKind && (
                <span className="text-muted">
                  {' '}
                  · <Problem value={a.errorKind} />
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
