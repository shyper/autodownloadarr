import { useTranslation } from 'react-i18next'
import type { Download, EpisodeState, Progress } from '../api/types'
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

export function Poster({ url, title, className = '' }: { url?: string; title: string; className?: string }) {
  return url ? (
    <img src={url} alt="" loading="lazy" className={`aspect-[2/3] w-full rounded-lg bg-surface-2 object-cover ${className}`} />
  ) : (
    <div className={`flex aspect-[2/3] w-full items-center justify-center rounded-lg bg-surface-2 p-2 text-center text-xs text-muted ${className}`}>
      {title}
    </div>
  )
}
