import { useMe } from '../api/hooks'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useParams } from 'react-router'
import {
  useDeleteMovie,
  useDownloadRelease,
  useDownloads,
  useKeepMovie,
  useMovie,
  useRemoveBlocklist,
  useSearchAgain,
  useSkipRelease,
} from '../api/movies'
import type { MovieDetail } from '../api/types'
import { DownloadProgress, Poster, Problem, ReleaseStatus, StateBadge, WatchProgress } from '../components/MovieBits'
import { Alert, Badge, Button, Card, SectionTitle, Spinner } from '../components/ui'
import { formatBytes, formatDateTime } from '../format'
import { errorMessage } from '../i18n'
import { releaseMark, releaseRowTone } from '../releases'

export function MovieDetailPage() {
  const { id } = useParams()
  const movie = useMovie(Number(id))
  const { t } = useTranslation()
  if (movie.isPending) return <Spinner />
  if (movie.isError) return <Alert>{errorMessage(t, movie.error)}</Alert>
  return <Detail m={movie.data} />
}

function Detail({ m }: { m: MovieDetail }) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const lang = i18n.resolvedLanguage
  const downloads = useDownloads()
  const keep = useKeepMovie()
  const search = useSearchAgain()
  const { data: user } = useMe()
  const canManage = user?.role === 'admin' || (!!user && user.id === m.requestedBy)
  const skip = useSkipRelease()
  const remove = useDeleteMovie()
  const pick = useDownloadRelease()
  const unblock = useRemoveBlocklist()
  const dl = downloads.data?.find((d) => d.movieId === m.id)
  const error = keep.error ?? search.error ?? skip.error ?? remove.error ?? pick.error ?? unblock.error

  const confirmDelete = (forget: boolean) => {
    if (!window.confirm(t(forget ? 'movies.confirmForget' : 'movies.confirmDelete', { title: m.title }))) return
    void remove.mutateAsync({ id: m.id, forget }).then(() => { if (forget) navigate('/movies') }).catch(() => {})
  }

  return (
    <div className="space-y-6">
      <Link to="/movies" className="text-sm text-muted hover:text-fg">
        ← {t('movies.title')}
      </Link>
      <div className="grid gap-6 md:grid-cols-[200px_1fr]">
        <Poster url={m.posterUrl} title={m.title} className="max-w-[200px]" />
        <div className="space-y-4">
          <div className="space-y-2">
            <h1 className="text-2xl font-semibold">
              {m.title} {m.year > 0 && <span className="text-muted">({m.year})</span>}
            </h1>
            <div className="flex flex-wrap gap-2">
              <StateBadge state={m.state} />
              {m.genres?.map((genre) => <Badge key={genre} tone="muted">{genre}</Badge>)}
              {m.keep && <Badge tone="ok">{t('movies.kept')}</Badge>}
              {m.runtime > 0 && <Badge tone="muted">{t('movies.runtime', { minutes: m.runtime })}</Badge>}
            </div>
            {m.overview && <p className="max-w-3xl text-sm text-muted">{m.overview}</p>}
            {m.state === 'available' && (
              <div className="max-w-md">
                <WatchProgress progress={m.progress} runtime={m.runtime} />
              </div>
            )}
          </div>
          {error && <Alert>{errorMessage(t, error)}</Alert>}
          <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[auto_1fr] sm:items-baseline">
            {m.requestedByName && (
              <>
                <dt className="text-muted">{t('movies.requestedByLabel')}</dt>
                <dd>{m.requestedByName}</dd>
              </>
            )}
            {m.releaseTitle && (
              <>
                <dt className="text-muted">{t('movies.release')}</dt>
                <dd className="font-mono text-xs break-all">{m.releaseTitle}</dd>
              </>
            )}
            {m.lastError && (
              <>
                <dt className="text-muted">{t('movies.problem')}</dt>
                <dd>
                  <Problem value={m.lastError} />
                </dd>
              </>
            )}
            {m.nextSearchAt && (
              <>
                <dt className="text-muted">{t('movies.nextSearch')}</dt>
                <dd>{formatDateTime(m.nextSearchAt, lang)}</dd>
              </>
            )}
            {m.watchedAt && (
              <>
                <dt className="text-muted">{t('movies.watchedAt')}</dt>
                <dd>{formatDateTime(m.watchedAt, lang)}</dd>
              </>
            )}
            {m.deleteAt && !m.keep && (
              <>
                <dt className="text-muted">{t('movies.deleteAt')}</dt>
                <dd>{formatDateTime(m.deleteAt, lang)}</dd>
              </>
            )}
          </dl>
          {m.state === 'downloading' && dl && (
            <div className="max-w-xl">
              <DownloadProgress download={dl} />
            </div>
          )}
          {canManage && <div className="flex flex-wrap gap-2">
            {m.state !== 'deleted' && (
              <Button variant="secondary" loading={keep.isPending} onClick={() => keep.mutate({ id: m.id, keep: !m.keep })}>
                {m.keep ? t('movies.actions.unkeep') : t('movies.actions.keep')}
              </Button>
            )}
            {(m.state === 'waiting' || m.state === 'failed') && (
              <Button loading={search.isPending} onClick={() => search.mutate(m.id)}>
                {t('movies.actions.search')}
              </Button>
            )}
            {m.state === 'downloading' && (
              <Button variant="secondary" loading={skip.isPending} onClick={() => skip.mutate(m.id)}>
                {t('movies.actions.skip')}
              </Button>
            )}
            {m.state !== 'deleted' && (
              <Button variant="ghost" onClick={() => confirmDelete(false)}>
                {t('movies.actions.delete')}
              </Button>
            )}
            <Button variant="ghost" onClick={() => confirmDelete(true)}>
              {t('movies.actions.forget')}
            </Button>
          </div>}
        </div>
      </div>

      <Card className="space-y-3">
        <SectionTitle title={t('movies.releases.title')} description={t('movies.releases.description')} />
        {m.releases.length === 0 ? (
          <p className="text-sm text-muted">{t('movies.releases.none')}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead className="text-xs text-muted">
                <tr>
                  <th className="py-2 pr-3 font-medium">{t('movies.releases.name')}</th>
                  <th className="py-2 pr-3 font-medium">{t('movies.releases.size')}</th>
                  <th className="py-2 pr-3 font-medium">{t('movies.releases.score')}</th>
                  <th className="py-2 pr-3 font-medium">{t('movies.releases.status')}</th>
                  <th />
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {m.releases.map((r) => {
                  const mark = releaseMark(r, m, m.attempts)
                  return (
                  <tr key={r.id} className={releaseRowTone(r, mark)}>
                    <td className="py-2 pr-3">
                      <p className="font-mono text-xs break-all">{r.title}</p>
                      <p className="text-xs text-muted">
                        {r.indexerName}
                        {r.passworded && <> · {t('movies.releases.passworded')}</>}
                      </p>
                    </td>
                    <td className="py-2 pr-3 whitespace-nowrap">{formatBytes(r.size)}</td>
                    <td className="py-2 pr-3">{r.rejected ? '–' : r.score}</td>
                    <td className="py-2 pr-3 text-xs">
                      <ReleaseStatus r={r} mark={mark} />
                    </td>
                    <td className="py-2 text-right">
                      {canManage && mark?.kind !== 'loaded' && mark?.kind !== 'loading' && (
                        <Button variant="ghost" loading={pick.isPending && pick.variables?.releaseId === r.id} onClick={() => pick.mutate({ id: m.id, releaseId: r.id })}>
                          {t('movies.releases.download')}
                        </Button>
                      )}
                    </td>
                  </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <div className="grid gap-6 lg:grid-cols-2">
        <Card className="space-y-3">
          <SectionTitle title={t('movies.attempts.title')} />
          {m.attempts.length === 0 ? (
            <p className="text-sm text-muted">{t('movies.attempts.none')}</p>
          ) : (
            <ul className="space-y-3">
              {m.attempts.map((a) => (
                <li key={a.id} className="space-y-1 text-sm">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge tone={a.result === 'success' ? 'ok' : a.result === 'failed' ? 'danger' : 'muted'}>
                      {t(`movies.attempts.result.${a.result}`)}
                    </Badge>
                    <span className="text-xs text-muted">{formatDateTime(a.startedAt, lang)}</span>
                  </div>
                  <p className="font-mono text-xs break-all">{a.releaseTitle}</p>
                  {a.errorKind && (
                    <p className="text-xs">
                      <Problem value={a.errorKind} />
                      {a.message && <span className="block text-muted break-all">{a.message}</span>}
                    </p>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Card>
        <Card className="space-y-3">
          <SectionTitle title={t('movies.blocklist.title')} description={t('movies.blocklist.description')} />
          {m.blocklist.length === 0 ? (
            <p className="text-sm text-muted">{t('movies.blocklist.none')}</p>
          ) : (
            <ul className="space-y-3">
              {m.blocklist.map((b) => (
                <li key={b.id} className="flex items-start gap-3 text-sm">
                  <div className="min-w-0 flex-1">
                    <p className="font-mono text-xs break-all">{b.title}</p>
                    <p className="text-xs text-muted">
                      <Problem value={b.reason} />
                    </p>
                  </div>
                  {user?.role === 'admin' && <Button variant="ghost" onClick={() => unblock.mutate(b.id)}>
                    {t('movies.blocklist.remove')}
                  </Button>}
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  )
}
