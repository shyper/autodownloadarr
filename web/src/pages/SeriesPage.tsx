import { useEffect, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { ApiError } from '../api/client'
import { useGeneral } from '../api/config'
import { useMe } from '../api/hooks'
import { useRequestSeries, useSeriesList, useShow, useShowSearch } from '../api/series'
import type { DeleteTrigger, Series, ShowResult } from '../api/types'
import { Poster } from '../components/MovieBits'
import { SeasonPicker } from '../components/SeasonPicker'
import { TriggerFields } from '../components/TriggerFields'
import { Alert, Badge, Button, Card, Input, Spinner } from '../components/ui'
import { errorMessage } from '../i18n'

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

export function SeriesPage() {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const q = useDebounced(query, 400)
  const list = useSeriesList()

  return (
    <div className="space-y-8">
      <div className="space-y-3">
        <h1 className="text-2xl font-semibold">{t('series.title')}</h1>
        <Input
          type="search"
          aria-label={t('series.searchLabel')}
          placeholder={t('series.searchPlaceholder')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="max-w-xl py-2.5 text-base"
        />
      </div>
      {q.trim().length >= 2 && <ShowResults q={q} />}
      <section className="space-y-4">
        <h2 className="text-lg font-semibold">{t('series.mine')}</h2>
        {list.isPending ? (
          <Spinner />
        ) : list.data?.length ? (
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
            {list.data.map((s) => (
              <SeriesCard key={s.id} series={s} />
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted">{t('series.none')}</p>
        )}
      </section>
    </div>
  )
}

function ShowResults({ q }: { q: string }) {
  const { t } = useTranslation()
  const search = useShowSearch(q)
  const [chosen, setChosen] = useState<ShowResult | null>(null)

  if (search.isPending) return <Spinner />
  if (search.isError) {
    const notConfigured = search.error instanceof ApiError && search.error.code === 'not_configured'
    return <Alert>{notConfigured ? t('movies.tmdbMissing') : errorMessage(t, search.error)}</Alert>
  }
  if (!search.data.length) return <p className="text-sm text-muted">{t('movies.noResults')}</p>
  return (
    <section className="space-y-4">
      {chosen && <RequestPanel show={chosen} onClose={() => setChosen(null)} />}
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        {search.data.map((r) => (
          <Card key={r.tmdbId} className="flex flex-col gap-3 p-3">
            <Poster url={r.posterUrl} title={r.title} />
            <div className="min-w-0 flex-1">
              <p className="line-clamp-2 text-sm font-medium">{r.title}</p>
              <p className="text-xs text-muted">{r.year || '–'}</p>
            </div>
            {r.seriesId ? (
              <Link to={`/series/${r.seriesId}`} className="text-sm font-medium text-accent hover:underline">
                {t('series.open')}
              </Link>
            ) : (
              <Button onClick={() => setChosen(r)}>{t('movies.request')}</Button>
            )}
          </Card>
        ))}
      </div>
    </section>
  )
}

/** Options for a new series: seasons, window and delete trigger. */
export function RequestPanel({ show, onClose }: { show: ShowResult; onClose: () => void }) {
  const { t } = useTranslation()
  const { data: me } = useMe()
  const general = useGeneral()
  const details = useShow(show.tmdbId)
  const request = useRequestSeries()
  const [picked, setPicked] = useState<number[]>([])
  const [future, setFuture] = useState(false)
  const [windowSize, setWindowSize] = useState<number | null>(null)
  const [trigger, setTrigger] = useState<DeleteTrigger | null>(null)
  const [value, setValue] = useState<number | null>(null)
  const isAdmin = me?.role === 'admin'
  const defaults = general.data
  const win = windowSize ?? defaults?.seriesWindow ?? 3
  const trig = trigger ?? defaults?.seriesTrigger ?? 'progress'
  const val = value ?? defaults?.seriesTriggerValue ?? 10
  const submit = (e: FormEvent) => {
    e.preventDefault()
    request.mutate(
      { tmdbId: show.tmdbId, seasons: picked, futureSeasons: future, windowSize: win, deleteTrigger: trig, triggerValue: val },
      { onSuccess: onClose },
    )
  }

  return (
    <Card className="space-y-4 border-accent/40">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h3 className="font-semibold">{show.title}</h3>
          <p className="text-sm text-muted">{t('series.request.description')}</p>
        </div>
        <Button variant="ghost" onClick={onClose}>
          {t('common.cancel')}
        </Button>
      </div>
      {request.isError && <Alert>{errorMessage(t, request.error)}</Alert>}
      <form onSubmit={submit} className="space-y-4">
        <SeasonPicker
          seasons={details.data?.seasons ?? []}
          loading={details.isPending}
          selected={picked}
          future={future}
          onChange={(s, f) => {
            setPicked(s)
            setFuture(f)
          }}
        />
        <TriggerFields
          windowSize={win}
          trigger={trig}
          value={val}
          onWindow={setWindowSize}
          onTrigger={setTrigger}
          onValue={setValue}
        />
        {!isAdmin && <p className="text-xs text-muted">{t('series.request.defaultsHint')}</p>}
        <div className="flex justify-end">
          <Button type="submit" loading={request.isPending}>
            {t('movies.request')}
          </Button>
        </div>
      </form>
    </Card>
  )
}

function SeriesCard({ series }: { series: Series }) {
  const { t } = useTranslation()
  const ready = series.counts.available ?? 0
  const loading = (series.counts.downloading ?? 0) + (series.counts.queued ?? 0) + (series.counts.searching ?? 0)
  return (
    <Link to={`/series/${series.id}`} className="group rounded-2xl focus-visible:outline-2 focus-visible:outline-accent">
      <Card className="flex h-full flex-col gap-2 p-3 transition-colors group-hover:border-accent/50">
        <Poster url={series.posterUrl} title={series.title} />
        <p className="line-clamp-2 text-sm font-medium">
          {series.title} {series.year > 0 && <span className="text-muted">({series.year})</span>}
        </p>
        {series.requestedByName && (
          <p className="truncate text-xs text-muted">{t('series.requestedBy', { name: series.requestedByName })}</p>
        )}
        <div className="flex flex-wrap gap-1">
          <Badge tone="ok">{t('series.ready', { count: ready })}</Badge>
          {loading > 0 && <Badge>{t('series.loading', { count: loading })}</Badge>}
          {(series.counts.failed ?? 0) > 0 && <Badge tone="danger">{t('movies.state.failed')}</Badge>}
          {series.paused && <Badge tone="muted">{t('series.paused')}</Badge>}
        </div>
        {series.current && series.current.progress > 0 && (
          <p className="text-xs text-muted">
            {t('series.status.watching', {
              code: `S${String(series.current.season).padStart(2, '0')}E${String(series.current.episode).padStart(2, '0')}`,
              percent: Math.round(series.current.progress * 100),
            })}
          </p>
        )}
      </Card>
    </Link>
  )
}
