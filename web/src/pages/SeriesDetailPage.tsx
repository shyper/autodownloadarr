import { useState, type FormEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useParams } from 'react-router'
import {
  useDeleteSeries,
  useDownloadEpisodeRelease,
  useEpisode,
  useEpisodeDownloads,
  useSearchEpisode,
  useSeries,
  useShow,
  useSkipEpisodeRelease,
  useUpdateSeries,
} from '../api/series'
import type { DeleteTrigger, Episode, EpisodeState, SeasonInfo, SeriesDetail } from '../api/types'
import { DownloadProgress, Poster, Problem, Rejection, StateBadge, WatchProgress } from '../components/MovieBits'
import { SeasonPicker } from '../components/SeasonPicker'
import { TriggerFields } from '../components/TriggerFields'
import { Alert, Badge, Button, Card, Checkbox, Spinner } from '../components/ui'
import { formatBytes, formatDateTime } from '../format'
import { errorMessage } from '../i18n'

export function SeriesDetailPage() {
  const { id } = useParams()
  const series = useSeries(Number(id))
  const { t } = useTranslation()
  if (series.isPending) return <Spinner />
  if (series.isError) return <Alert>{errorMessage(t, series.error)}</Alert>
  return <Detail s={series.data} />
}

const code = (season: number, episode: number) => `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`

/** Wide screens show the settings open, phones start collapsed. */
const wide = () => typeof window !== 'undefined' && window.matchMedia?.('(min-width: 768px)').matches

function Detail({ s }: { s: SeriesDetail }) {
  const { t } = useTranslation()
  const seasons = [...new Set(s.episodes.map((e) => e.season))].sort((a, b) => a - b)
  const openSeason = s.current?.season ?? seasons.find((n) => n > 0) ?? seasons[0]

  return (
    <div className="space-y-5">
      <Link to="/series" className="inline-flex min-h-11 items-center text-sm text-muted hover:text-fg">
        ← {t('series.title')}
      </Link>
      <Header s={s} />
      <StatusLine s={s} />
      <SettingsSection s={s} />
      {seasons.map((season) => (
        <SeasonSection key={season} season={season} episodes={s.episodes.filter((e) => e.season === season)} defaultOpen={season === openSeason} />
      ))}
      <DeleteSeries s={s} />
    </div>
  )
}

function Header({ s }: { s: SeriesDetail }) {
  const { t } = useTranslation()
  const [more, setMore] = useState(false)
  return (
    <div className="flex gap-4 md:gap-6">
      <div className="w-24 shrink-0 sm:w-32 md:w-44">
        <Poster url={s.posterUrl} title={s.title} />
      </div>
      <div className="min-w-0 space-y-2">
        <h1 className="text-xl font-semibold sm:text-2xl">
          {s.title} {s.year > 0 && <span className="text-muted">({s.year})</span>}
        </h1>
        <div className="flex flex-wrap gap-2">
          {s.keep && <Badge tone="ok">{t('movies.kept')}</Badge>}
          {s.paused && <Badge tone="muted">{t('series.paused')}</Badge>}
          {(s.seasons.length === 0 || s.futureSeasons) && <Badge tone="accent">{t('series.subscribed')}</Badge>}
          {s.status && <Badge tone="muted">{s.status}</Badge>}
        </div>
        {s.overview && (
          <div className="max-w-3xl text-sm text-muted">
            <p className={more ? '' : 'line-clamp-3'}>{s.overview}</p>
            {s.overview.length > 180 && (
              <button type="button" className="mt-1 min-h-8 text-accent hover:underline" onClick={() => setMore((m) => !m)}>
                {more ? t('series.less') : t('series.more')}
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

/** Where the viewer stands: current episode, ready episodes, next broadcast. */
function StatusLine({ s }: { s: SeriesDetail }) {
  const { t, i18n } = useTranslation()
  const ready = s.counts.available ?? 0
  const parts: string[] = []
  if (s.current) {
    parts.push(
      s.current.progress > 0
        ? t('series.status.watching', { code: code(s.current.season, s.current.episode), percent: Math.round(s.current.progress * 100) })
        : t('series.status.next', { code: code(s.current.season, s.current.episode) }),
    )
  }
  parts.push(t('series.ready', { count: ready }))
  if (s.upcoming?.airDate) {
    parts.push(
      t('series.status.upcoming', {
        code: code(s.upcoming.season, s.upcoming.episode),
        date: new Date(s.upcoming.airDate).toLocaleDateString(i18n.resolvedLanguage),
      }),
    )
  }
  return (
    <Card className="p-4">
      <p className="text-sm">
        {parts.map((p, i) => (
          <span key={i}>
            {i > 0 && <span className="text-muted"> · </span>}
            {p}
          </span>
        ))}
      </p>
    </Card>
  )
}

/** A collapsible card with a title, a short summary and its content. */
function Section({ title, summary, defaultOpen, children }: { title: string; summary?: string; defaultOpen: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <details open={open} onToggle={(e) => setOpen(e.currentTarget.open)} className="group rounded-2xl border border-line bg-surface shadow-sm">
      <summary className="flex min-h-12 cursor-pointer list-none items-center gap-3 px-4 py-3 [&::-webkit-details-marker]:hidden">
        <span className="font-semibold">{title}</span>
        <span className="min-w-0 flex-1 truncate text-right text-xs text-muted">{summary}</span>
        <span aria-hidden="true" className="text-muted transition-transform group-open:rotate-180">
          ▾
        </span>
      </summary>
      {open && <div className="border-t border-line px-4 py-4">{children}</div>}
    </details>
  )
}

function SettingsSection({ s }: { s: SeriesDetail }) {
  const { t } = useTranslation()
  const trigger =
    s.deleteTrigger === 'progress'
      ? t('series.summary.progress', { value: s.triggerValue })
      : s.deleteTrigger === 'minutes'
        ? t('series.summary.minutes', { value: s.triggerValue })
        : t('series.summary.watched')
  return (
    <Section title={t('series.settings')} summary={`${t('series.summary.window', { count: s.windowSize })} · ${trigger}`} defaultOpen={wide()}>
      <SettingsForm s={s} />
    </Section>
  )
}

function SettingsForm({ s }: { s: SeriesDetail }) {
  const { t } = useTranslation()
  const update = useUpdateSeries()
  const show = useShow(s.tmdbId)
  const [windowSize, setWindowSize] = useState(s.windowSize)
  const [trigger, setTrigger] = useState<DeleteTrigger>(s.deleteTrigger)
  const [value, setValue] = useState(s.triggerValue)
  const [keep, setKeep] = useState(s.keep)
  const [paused, setPaused] = useState(s.paused)
  const [seasons, setSeasons] = useState<number[]>(s.seasons)
  const [future, setFuture] = useState(s.futureSeasons)

  // All seasons TMDB knows, not only those loaded so far; the episode list
  // is the fallback while TMDB is not reachable.
  const known: SeasonInfo[] =
    show.data?.seasons ??
    [...new Set(s.episodes.map((e) => e.season))].map((n) => ({
      number: n,
      name: '',
      episodeCount: s.episodes.filter((e) => e.season === n).length,
    }))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    update.mutate({ id: s.id, settings: { seasons, futureSeasons: future, windowSize, deleteTrigger: trigger, triggerValue: value, keep, paused } })
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      <p className="text-sm text-muted">{t('series.settingsHint')}</p>
      {update.isError && <Alert>{errorMessage(t, update.error)}</Alert>}
      {update.isSuccess && <Alert tone="success">{t('settings.saved')}</Alert>}
      <SeasonPicker
        seasons={known}
        loading={show.isPending && s.episodes.length === 0}
        selected={seasons}
        future={future}
        onChange={(sel, f) => {
          setSeasons(sel)
          setFuture(f)
        }}
      />
      <TriggerFields windowSize={windowSize} trigger={trigger} value={value} onWindow={setWindowSize} onTrigger={setTrigger} onValue={setValue} />
      <div className="flex flex-col gap-3 sm:flex-row sm:gap-6">
        <Checkbox label={t('series.keep')} hint={t('series.keepHint')} checked={keep} onChange={(e) => setKeep(e.target.checked)} />
        <Checkbox label={t('series.pause')} hint={t('series.pauseHint')} checked={paused} onChange={(e) => setPaused(e.target.checked)} />
      </div>
      <div className="flex justify-end">
        <Button type="submit" className="w-full sm:w-auto" loading={update.isPending}>
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}

const summaryStates: EpisodeState[] = ['available', 'downloading', 'watched', 'deleted', 'failed']

function SeasonSection({ season, episodes, defaultOpen }: { season: number; episodes: Episode[]; defaultOpen: boolean }) {
  const { t } = useTranslation()
  const summary = summaryStates
    .map((st) => [st, episodes.filter((e) => e.state === st).length] as const)
    .filter(([, n]) => n > 0)
    .map(([st, n]) => `${n} ${t(`movies.state.${st}`)}`)
    .join(' · ')
  return (
    <Section title={season === 0 ? t('series.specials') : t('series.season', { number: season })} summary={summary} defaultOpen={defaultOpen}>
      <ul className="-my-2 divide-y divide-line">
        {episodes.map((e) => (
          <EpisodeRow key={e.id} e={e} />
        ))}
      </ul>
    </Section>
  )
}

function EpisodeRow({ e }: { e: Episode }) {
  const { t, i18n } = useTranslation()
  const downloads = useEpisodeDownloads()
  const [open, setOpen] = useState(false)
  const dl = downloads.data?.find((d) => d.episodeId === e.id)
  const lang = i18n.resolvedLanguage

  return (
    <li className="space-y-2 py-2">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex min-h-12 w-full items-center gap-3 rounded-lg text-left focus-visible:outline-2 focus-visible:outline-accent"
      >
        <span className="w-14 shrink-0 font-mono text-xs text-muted">{code(e.season, e.episode)}</span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{e.title || '–'}</span>
          <span className="block text-xs text-muted">
            {e.airDate && new Date(e.airDate).toLocaleDateString(lang)}
            {e.deleteAt && <> · {t('movies.deletesAt', { date: formatDateTime(e.deleteAt, lang) })}</>}
          </span>
          <span className="mt-1 block sm:hidden">
            <StateBadge state={e.state} />
          </span>
        </span>
        <span className="hidden sm:inline-flex">
          <StateBadge state={e.state} />
        </span>
        <span aria-hidden="true" className={`text-muted transition-transform ${open ? 'rotate-180' : ''}`}>
          ▾
        </span>
      </button>
      {e.state === 'downloading' && dl && <DownloadProgress download={dl} />}
      {e.state === 'available' && <WatchProgress progress={e.progress} runtime={e.runtime} />}
      {e.lastError && (e.state === 'waiting' || e.state === 'failed') && (
        <p className="text-xs text-muted">
          <Problem value={e.lastError} />
          {e.nextSearchAt && <> · {t('movies.nextSearch')}: {formatDateTime(e.nextSearchAt, lang)}</>}
        </p>
      )}
      {open && <EpisodeDetails e={e} />}
    </li>
  )
}

function EpisodeDetails({ e }: { e: Episode }) {
  const { t } = useTranslation()
  const ep = useEpisode(e.id, true)
  const search = useSearchEpisode()
  const skip = useSkipEpisodeRelease()
  const pick = useDownloadEpisodeRelease()
  const canSearch = ['waiting', 'failed', 'deleted', 'pending', 'skipped'].includes(e.state)
  const error = search.error ?? skip.error ?? pick.error

  return (
    <div className="space-y-3 rounded-xl bg-surface-2 p-3 text-sm">
      {error && <Alert>{errorMessage(t, error)}</Alert>}
      {(canSearch || e.state === 'downloading') && (
        <div className="flex flex-col gap-2 sm:flex-row">
          {canSearch && (
            <Button variant="secondary" className="w-full sm:w-auto" loading={search.isPending} onClick={() => search.mutate(e.id)}>
              {t('series.searchEpisode')}
            </Button>
          )}
          {e.state === 'downloading' && (
            <Button variant="secondary" className="w-full sm:w-auto" loading={skip.isPending} onClick={() => skip.mutate(e.id)}>
              {t('movies.actions.skip')}
            </Button>
          )}
        </div>
      )}
      {ep.isPending ? (
        <Spinner />
      ) : ep.isError ? (
        <Alert>{errorMessage(t, ep.error)}</Alert>
      ) : (
        <>
          {ep.data.releases.length === 0 ? (
            <p className="text-muted">{t('movies.releases.none')}</p>
          ) : (
            <ul className="space-y-2">
              {ep.data.releases.map((r) => (
                <li key={r.id} className={`space-y-2 rounded-lg border border-line bg-surface p-3 ${r.rejected || r.blocklisted ? 'text-muted' : ''}`}>
                  <p className="line-clamp-2 font-mono text-xs break-all" title={r.title}>
                    {r.title}
                  </p>
                  <div className="flex flex-wrap items-center gap-2 text-xs">
                    <span>{formatBytes(r.size)}</span>
                    {r.blocklisted ? (
                      <Badge tone="danger">{t('movies.releases.blocklisted')}</Badge>
                    ) : r.rejected ? (
                      <Rejection value={r.rejected} />
                    ) : (
                      <Badge tone="ok">{t('movies.releases.ok')}</Badge>
                    )}
                  </div>
                  <Button
                    variant="ghost"
                    className="w-full sm:w-auto"
                    loading={pick.isPending && pick.variables?.releaseId === r.id}
                    onClick={() => pick.mutate({ id: e.id, releaseId: r.id })}
                  >
                    {t('movies.releases.download')}
                  </Button>
                </li>
              ))}
            </ul>
          )}
          {ep.data.attempts.length > 0 && (
            <ul className="space-y-1 border-t border-line pt-2">
              {ep.data.attempts.map((a) => (
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
        </>
      )}
    </div>
  )
}

function DeleteSeries({ s }: { s: SeriesDetail }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const remove = useDeleteSeries()
  const confirmDelete = () => {
    if (!window.confirm(t('series.confirmDelete', { title: s.title }))) return
    remove.mutate(s.id, { onSuccess: () => navigate('/series') })
  }
  return (
    <div className="space-y-2 pt-2">
      {remove.isError && <Alert>{errorMessage(t, remove.error)}</Alert>}
      <Button variant="ghost" className="w-full sm:w-auto" loading={remove.isPending} onClick={confirmDelete}>
        {t('series.delete')}
      </Button>
    </div>
  )
}
