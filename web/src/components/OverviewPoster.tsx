import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import type { OverviewTitle } from '../dashboard'
import type { TrendingTitle } from '../api/types'
import { Poster } from './MovieBits'
import { Badge } from './ui'

export function OverviewPoster({ item, showPlayable, onChoose }: {
  item: OverviewTitle; showPlayable: boolean; onChoose: (item: TrendingTitle) => void
}) {
  const { t } = useTranslation()
  const active = !!item.download || item.state === 'downloading'
  const pending = !active && (item.state === 'queued' || item.state === 'searching')
  const ready = item.ready && !active && !pending
  const playable = !ready && showPlayable && item.playable
  const p = item.download?.progress
  const ratio = p?.checking ? (p.checkTotal ? (p.checked ?? 0) / p.checkTotal : 0) : p && p.totalBytes > 0 ? p.doneBytes / p.totalBytes : 0
  const percent = Math.round(Math.max(0, Math.min(1, ratio)) * 100)
  const speed = p && p.elapsed > 0 ? p.doneBytes / (p.elapsed / 1e9) : 0
  const minutes = p && speed > 0 ? Math.ceil(Math.max(0, p.totalBytes - p.doneBytes) / speed / 60) : 0
  const status = active ? t('dashboard.downloading') : pending ? t('dashboard.pending') : item.state ? t(`movies.state.${item.state}`) : t('dashboard.notRequested')
  const label = `${item.title} · ${status}${playable ? ` · ${t('downloads.playable')}` : ''}`
  const content = <>
    <Poster url={item.posterUrl} title={item.title} className={`transition-transform group-hover:scale-[1.02] ${pending || item.state === 'deleted' ? 'grayscale opacity-70' : ''}`} />
    <span className="absolute top-2 left-2"><Badge tone={item.mediaType === 'tv' ? 'muted' : 'accent'}>{t(item.mediaType === 'tv' ? 'dashboard.seriesType' : 'dashboard.movieType')}</Badge></span>
    {(ready || playable) && <span className={`absolute top-2 right-2 grid size-6 place-items-center rounded-full shadow ${ready ? 'bg-ok-soft text-ok' : 'bg-accent-soft text-accent'}`} title={ready ? t('movies.state.available') : t('downloads.playable')}>
      {ready ? <svg viewBox="0 0 24 24" className="size-4" fill="none" stroke="currentColor" strokeWidth="2.5" aria-hidden="true"><path d="m5 12 4 4L19 6" /></svg>
        : <svg viewBox="0 0 24 24" className="size-4" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z" /><circle cx="12" cy="12" r="3" /></svg>}
    </span>}
    {(active || pending || item.state === 'failed' || item.state === 'waiting') && <span className="absolute inset-x-0 bottom-0 space-y-2 bg-surface/95 p-2 text-fg">
      <Badge tone={pending ? 'warn' : item.state === 'failed' ? 'danger' : 'accent'}>{status}</Badge>
      {(active || pending) && <>
        <span className="flex justify-between gap-1 text-[11px]"><span>{p?.checking ? t('dashboard.checking') : `${percent} %`}</span><span>{minutes > 0 && !p?.checking ? t('dashboard.remaining', { minutes }) : pending ? t('dashboard.queue') : ''}</span></span>
        <span className="block h-1 overflow-hidden rounded-full bg-line" role="progressbar" aria-label={t('dashboard.progress', { title: item.title })} aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}><span className="block h-full rounded-full bg-accent" style={{ width: `${percent}%` }} /></span>
      </>}
    </span>}
  </>
  const className = 'group relative block aspect-[2/3] w-full overflow-hidden rounded-lg border border-line bg-surface-2 text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'
  return item.href
    ? <Link to={item.href} aria-label={label} title={item.title} className={className}>{content}</Link>
    : <button type="button" aria-label={label} title={item.title} className={className} onClick={() => { onChoose(item); window.scrollTo({ top: 0, behavior: 'auto' }) }}>{content}</button>
}
