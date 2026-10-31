import { useEffect, useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate } from 'react-router'
import { ApiError } from '../api/client'
import { useOnboarding } from '../api/config'
import { useTrending } from '../api/discover'
import { useQueue } from '../api/downloads'
import { useMe, useWarnings } from '../api/hooks'
import { useDownloads, useMovieSearch, useMovies, useRequestMovie } from '../api/movies'
import { useEpisodeDownloads, useSeriesList, useShowSearch } from '../api/series'
import type { TrendingTitle } from '../api/types'
import { OverviewPoster } from '../components/OverviewPoster'
import { Alert, Button, Card, Input, Spinner } from '../components/ui'
import { overviewTitles, recentlyDownloaded, recentlyRequested, type OverviewTitle } from '../dashboard'
import { errorMessage } from '../i18n'
import { RequestPanel } from './SeriesPage'

export function DashboardPage() {
  const { t, i18n } = useTranslation()
  const { data: user } = useMe()
  const onboarding = useOnboarding(user?.role === 'admin')
  const warnings = useWarnings()
  const movies = useMovies()
  const series = useSeriesList()
  const downloads = useDownloads()
  const episodes = useEpisodeDownloads()
  const queue = useQueue()
  const trending = useTrending(i18n.resolvedLanguage?.split('-')[0] ?? 'en')
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [chosen, setChosen] = useState<TrendingTitle | null>(null)
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(query.trim()), 350)
    return () => window.clearTimeout(timer)
  }, [query])
  const movieSearch = useMovieSearch(debounced)
  const seriesSearch = useShowSearch(debounced)
  const titles = overviewTitles(movies.data ?? [], series.data ?? [], downloads.data ?? [], episodes.data ?? [], queue.data ?? [])
  const activeCount = titles.filter((item) => item.download || item.state === 'downloading').length
  const pendingCount = titles.filter((item) => !item.download && (item.state === 'queued' || item.state === 'searching')).length
  const downloadCaption = [activeCount ? t('dashboard.loading', { count: activeCount }) : '', pendingCount ? t('dashboard.pendingCount', { count: pendingCount }) : ''].filter(Boolean).join(' · ')
  const local = new Map(titles.map((item) => [item.key, item]))
  const annotate = (item: TrendingTitle): OverviewTitle => local.get(`${item.mediaType}-${item.tmdbId}`) ?? { ...item, key: `${item.mediaType}-${item.tmdbId}` }
  const searching = debounced.length >= 2
  const searchTitles = searching ? [
    ...(movieSearch.data ?? []).map((m) => annotate({ ...m, mediaType: 'movie', year: m.year ?? 0, overview: m.overview ?? '' })),
    ...(seriesSearch.data ?? []).map((s) => annotate({ ...s, mediaType: 'tv', year: s.year ?? 0, overview: s.overview ?? '' })),
  ] : []
  const errors = [movies.error, series.error, downloads.error, episodes.error, queue.error]
  return (
    <div className="space-y-8">
      <h1 className="sr-only">{t('nav.dashboard')}</h1>
      {warnings.data?.map((w) => <Alert key={w.code}>{t(`warnings.${w.code}`, { detail: w.detail, defaultValue: w.detail })}</Alert>)}
      {onboarding.data && !onboarding.data.finished && (
        <Card className="flex flex-wrap items-center justify-between gap-3 border-accent/40">
          <p className="text-sm">{t('dashboard.setupPending')}</p>
          <Link to="/setup" className="text-sm font-medium text-accent hover:underline">{t('dashboard.continueSetup')}</Link>
        </Card>
      )}
      <Input type="search" aria-label={t('dashboard.search')} placeholder={t('dashboard.search')} value={query} onChange={(e) => setQuery(e.target.value)} className="rounded-full px-5 py-3" />
      {chosen && (chosen.mediaType === 'tv'
        ? <RequestPanel key={`tv-${chosen.tmdbId}`} show={chosen} onClose={() => setChosen(null)} />
        : <MovieRequest key={`movie-${chosen.tmdbId}`} movie={chosen} onClose={() => setChosen(null)} />)}
      {errors.find(Boolean) && <Alert>{errorMessage(t, errors.find(Boolean))}</Alert>}
      {searching && (
        <PosterShelf title={t('dashboard.results')} items={searchTitles} onChoose={setChosen}>
          {movieSearch.isPending || seriesSearch.isPending ? <Spinner /> : null}
          {movieSearch.error && <Alert>{errorMessage(t, movieSearch.error)}</Alert>}
          {seriesSearch.error && <Alert>{errorMessage(t, seriesSearch.error)}</Alert>}
        </PosterShelf>
      )}
      <PosterShelf title={t('dashboard.recentlyDownloaded')} caption={downloadCaption} items={recentlyDownloaded(titles)} onChoose={setChosen} showPlayable>
        {movies.isPending || series.isPending ? <Spinner /> : null}
      </PosterShelf>
      <PosterShelf title={t('dashboard.recentlyRequested')} items={recentlyRequested(titles)} onChoose={setChosen}>
        {movies.isPending || series.isPending ? <Spinner /> : null}
      </PosterShelf>
      <PosterShelf title={t('dashboard.trending')} caption={t('dashboard.thisWeek')} items={(trending.data ?? []).map(annotate)} onChoose={setChosen}>
        {trending.isPending && <Spinner />}
        {trending.error && <Alert>{trending.error instanceof ApiError && trending.error.code === 'not_configured' ? t('movies.tmdbMissing') : errorMessage(t, trending.error)}</Alert>}
      </PosterShelf>
    </div>
  )
}

function MovieRequest({ movie, onClose }: { movie: TrendingTitle; onClose: () => void }) {
  const { t } = useTranslation()
  const request = useRequestMovie()
  const navigate = useNavigate()
  return (
    <Card className="space-y-4 border-accent/40">
      <div className="flex items-start justify-between gap-4">
        <div><h2 className="text-lg font-semibold">{movie.title}</h2><p className="text-sm text-muted">{movie.overview}</p></div>
        <Button variant="ghost" onClick={onClose}>{t('common.cancel')}</Button>
      </div>
      {request.error && <Alert>{errorMessage(t, request.error)}</Alert>}
      <Button loading={request.isPending} onClick={() => request.mutate({ tmdbId: movie.tmdbId }, { onSuccess: (result) => { onClose(); void navigate(`/movies/${result.id}`) } })}>{t('movies.request')}</Button>
    </Card>
  )
}

function PosterShelf({ title, caption, items, onChoose, showPlayable = false, children }: {
  title: string; caption?: string; items: OverviewTitle[]; onChoose: (item: TrendingTitle) => void; showPlayable?: boolean; children?: ReactNode
}) {
  const { t } = useTranslation()
  const id = useId()
  const [page, setPage] = useState(0)
  const pages = Math.max(1, Math.ceil(items.length / 6))
  const current = Math.min(page, pages - 1)
  return (
    <section aria-labelledby={id} className="space-y-3">
      <div className="flex items-end justify-between gap-3">
        <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 id={id} className="text-xl font-semibold tracking-tight">{title}</h2>
          {caption && <span className="text-xs text-muted">{caption}</span>}
        </div>
        <div className="flex shrink-0 gap-1">
          <button type="button" disabled={current === 0} aria-label={t('dashboard.previous', { section: title })} onClick={() => setPage(current - 1)} className="grid size-11 place-items-center rounded-full border border-line text-xl text-muted hover:bg-surface-2 disabled:opacity-30">‹</button>
          <button type="button" disabled={current >= pages - 1} aria-label={t('dashboard.next', { section: title })} onClick={() => setPage(current + 1)} className="grid size-11 place-items-center rounded-full border border-line text-xl text-muted hover:bg-surface-2 disabled:opacity-30">›</button>
        </div>
      </div>
      {children}
      {items.length ? (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6" aria-live="polite">
          {items.slice(current * 6, current * 6 + 6).map((item) => <OverviewPoster key={item.key} item={item} showPlayable={showPlayable} onChoose={onChoose} />)}
        </div>
      ) : <p className="text-sm text-muted">{t('dashboard.empty')}</p>}
    </section>
  )
}
