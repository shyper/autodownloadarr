import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { ApiError } from '../api/client'
import { useDownloads, useMovieSearch, useMovies, useRequestMovie } from '../api/movies'
import type { Movie, SearchResult } from '../api/types'
import { DownloadProgress, Poster, Problem, StateBadge } from '../components/MovieBits'
import { Alert, Badge, Button, Card, Input, Spinner } from '../components/ui'
import { formatDateTime } from '../format'
import { errorMessage } from '../i18n'

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

export function MoviesPage() {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const q = useDebounced(query, 400)
  const movies = useMovies()

  return (
    <div className="space-y-8">
      <div className="space-y-3">
        <h1 className="text-2xl font-semibold">{t('movies.title')}</h1>
        <Input
          type="search"
          aria-label={t('movies.searchLabel')}
          placeholder={t('movies.searchPlaceholder')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="max-w-xl py-2.5 text-base"
        />
      </div>
      {q.trim().length >= 2 && <SearchResults q={q} />}

      <section className="space-y-4">
        <h2 className="text-lg font-semibold">{t('movies.mine')}</h2>
        {movies.isPending ? (
          <Spinner />
        ) : movies.data?.length ? (
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
            {movies.data.map((m) => (
              <MovieCard key={m.id} movie={m} />
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted">{t('movies.none')}</p>
        )}
      </section>
    </div>
  )
}

function SearchResults({ q }: { q: string }) {
  const { t } = useTranslation()
  const search = useMovieSearch(q)
  const request = useRequestMovie()

  if (search.isPending) return <Spinner />
  if (search.isError) {
    const notConfigured = search.error instanceof ApiError && search.error.code === 'not_configured'
    return (
      <Alert>
        {notConfigured ? (
          <>
            {t('movies.tmdbMissing')}{' '}
            <Link to="/settings/general" className="underline">
              {t('nav.settings')}
            </Link>
          </>
        ) : (
          errorMessage(t, search.error)
        )}
      </Alert>
    )
  }
  if (!search.data.length) return <p className="text-sm text-muted">{t('movies.noResults')}</p>
  return (
    <section className="space-y-3">
      {request.isError && <Alert>{errorMessage(t, request.error)}</Alert>}
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        {search.data.map((r) => (
          <SearchCard key={r.tmdbId} result={r} busy={request.isPending && request.variables?.tmdbId === r.tmdbId} onRequest={() => request.mutate({ tmdbId: r.tmdbId })} />
        ))}
      </div>
    </section>
  )
}

function SearchCard({ result, busy, onRequest }: { result: SearchResult; busy: boolean; onRequest: () => void }) {
  const { t } = useTranslation()
  const requested = result.movieId && result.state !== 'deleted'
  return (
    <Card className="flex flex-col gap-3 p-3">
      <Poster url={result.posterUrl} title={result.title} />
      <div className="min-w-0 flex-1">
        <p className="line-clamp-2 text-sm font-medium" title={result.title}>
          {result.title}
        </p>
        <p className="text-xs text-muted">{result.year || '–'}</p>
      </div>
      {requested ? (
        <Link to={`/movies/${result.movieId}`} className="text-sm font-medium text-accent hover:underline">
          {result.state ? <StateBadge state={result.state} /> : null}
        </Link>
      ) : (
        <Button onClick={onRequest} loading={busy}>
          {t('movies.request')}
        </Button>
      )}
    </Card>
  )
}

function MovieCard({ movie }: { movie: Movie }) {
  const { t, i18n } = useTranslation()
  const downloads = useDownloads()
  const dl = downloads.data?.find((d) => d.movieId === movie.id)
  return (
    <Link to={`/movies/${movie.id}`} className="group rounded-2xl focus-visible:outline-2 focus-visible:outline-accent">
      <Card className="flex h-full flex-col gap-2 p-3 transition-colors group-hover:border-accent/50">
        <Poster url={movie.posterUrl} title={movie.title} className={movie.state === 'deleted' ? 'opacity-40' : ''} />
        <p className="line-clamp-2 text-sm font-medium">
          {movie.title} {movie.year > 0 && <span className="text-muted">({movie.year})</span>}
        </p>
        <div className="flex flex-wrap gap-1">
          <StateBadge state={movie.state} />
          {movie.keep && <Badge tone="ok">{t('movies.kept')}</Badge>}
        </div>
        {movie.state === 'downloading' && dl && <DownloadProgress download={dl} />}
        {movie.state === 'watched' && movie.deleteAt && !movie.keep && (
          <p className="text-xs text-muted">{t('movies.deletesAt', { date: formatDateTime(movie.deleteAt, i18n.resolvedLanguage) })}</p>
        )}
        {(movie.state === 'failed' || movie.state === 'waiting') && movie.lastError && (
          <p className="text-xs text-muted">
            <Problem value={movie.lastError} />
          </p>
        )}
      </Card>
    </Link>
  )
}
