import { useTranslation } from 'react-i18next'
import type { Download, EpisodeState, Progress, Release } from '../api/types'
import type { ReleaseMark } from '../releases'
import { formatBytes } from '../format'
import { Badge, ProgressBar, type BadgeTone } from './ui'

const stateTone: Record<EpisodeState, BadgeTone> = {
  pending: 'muted',
  skipped: 'muted',
  searching: 'accent',
  waiting: 'warn',
  queued: 'accent',
  downloading: 'accent',
  available: 'ok',
  watched: 'muted',
  deleted: 'muted',
  failed: 'danger',
}

export function StateBadge({ state }: { state: EpisodeState }) {
  const { t } = useTranslation()
  return <Badge tone={stateTone[state]}>{t(`movies.state.${state}`)}</Badge>
}

/** Translates "code" or "code: detail" from the server. */
export function Problem({ value }: { value: string }) {
  const { t } = useTranslation()
  if (!value) return null
  const [code = '', ...rest] = value.split(':')
  const detail = rest.join(':').trim()
  const text = t(`problems.${code.trim()}`, { defaultValue: value })
  return (
    <span>
      {text}
      {detail && text !== value && <span className="text-muted"> ({detail})</span>}
    </span>
  )
}

export function Rejection({ value }: { value: string }) {
  const { t } = useTranslation()
  const [code = '', ...rest] = value.split(':')
  const detail = rest.join(':').trim()
  return (
    <span>
      {t(`reject.${code.trim()}`, { defaultValue: code })}
      {detail && <span className="text-muted"> ({detail})</span>}
    </span>
  )
}

export function DownloadProgress({ download }: { download: Pick<Download, 'progress'> & { progress: Progress } }) {
  const { t } = useTranslation()
  const p = download.progress
  if (p.checking) {
    const checked = p.checkTotal ? (p.checked ?? 0) / p.checkTotal : 0
    return (
      <div className="space-y-1">
        <ProgressBar value={checked} />
        <p className="text-xs text-muted">{t('movies.checking', { percent: Math.round(checked * 100) })}</p>
      </div>
    )
  }
  const ratio = p.totalBytes > 0 ? p.doneBytes / p.totalBytes : 0
  const seconds = p.elapsed / 1e9
  const speed = seconds > 0 ? p.doneBytes / seconds : 0
  return (
    <div className="space-y-1">
      <ProgressBar value={ratio} />
      <p className="flex flex-wrap justify-between gap-x-3 text-xs text-muted">
        <span>
          {Math.round(ratio * 100)} % · {formatBytes(p.doneBytes)} / {formatBytes(p.totalBytes)}
        </span>
        <span>
          {formatBytes(speed)}/s
          {p.failedSegments > 0 && <> · {t('movies.missingArticles', { count: p.failedSegments })}</>}
        </span>
      </p>
    </div>
  )
}

/** The status of a release: its mark if it has one, else the verdict of the profile. */
export function ReleaseStatus({ r, mark }: { r: Release; mark: ReleaseMark }) {
  const { t } = useTranslation()
  if (mark?.kind === 'loaded') return <Badge tone="ok">{t('movies.releases.loaded')}</Badge>
  if (mark?.kind === 'loading') return <Badge tone="accent">{t('movies.releases.loading')}</Badge>
  if (mark?.kind === 'failed')
    return (
      <span className="inline-flex flex-wrap items-center gap-1">
        <Badge tone="danger">{t('movies.releases.failed')}</Badge>
        {mark.errorKind && (
          <span className="text-muted">
            <Problem value={mark.errorKind} />
          </span>
        )}
      </span>
    )
  if (r.blocklisted) return <Badge tone="danger">{t('movies.releases.blocklisted')}</Badge>
  if (r.rejected) return <Rejection value={r.rejected} />
  return <Badge tone="muted">{t('movies.releases.ok')}</Badge>
}

/** How far a title has been watched, with the time left. */
export function WatchProgress({ progress, runtime }: { progress: number; runtime: number }) {
  const { t } = useTranslation()
  if (progress <= 0 || progress >= 1) return null
  const left = Math.round((1 - progress) * runtime)
  return (
    <div className="flex items-center gap-2">
      <ProgressBar value={progress} />
      <span className="text-xs whitespace-nowrap text-muted">
        {Math.round(progress * 100)} %{runtime > 0 && <> · {t('movies.timeLeft', { time: formatMinutes(left) })}</>}
      </span>
    </div>
  )
}

function formatMinutes(min: number) {
  return min >= 60 ? `${Math.floor(min / 60)} h ${String(min % 60).padStart(2, '0')} min` : `${min} min`
}

export function Poster({ url, title, className = '' }: { url?: string; title: string; className?: string }) {
  return url ? (
    <img src={url} alt="" loading="lazy" className={`aspect-[2/3] w-full rounded-lg bg-surface-2 object-cover ${className}`} />
  ) : (
    <div className={`flex aspect-[2/3] w-full items-center justify-center rounded-lg bg-surface-2 p-2 text-center text-xs text-muted ${className}`}>
      {title}
    </div>
  )
}
