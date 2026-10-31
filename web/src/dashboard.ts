import type { Download, EpisodeDownload, Movie, MovieState, QueueEntry, Series, TrendingTitle } from './api/types'

export type OverviewTitle = TrendingTitle & {
  key: string
  href?: string
  state?: MovieState
  requestedAt?: string
  availableAt?: string
  ready?: boolean
  playable?: boolean
  download?: Download | EpisodeDownload
  queuePosition?: number
}

export function overviewTitles(movies: Movie[], series: Series[], downloads: Download[], episodes: EpisodeDownload[], queue: QueueEntry[]): OverviewTitle[] {
  return [
    ...movies.map((m): OverviewTitle => ({
      ...m, mediaType: 'movie', key: `movie-${m.tmdbId}`, href: `/movies/${m.id}`, requestedAt: m.createdAt,
      ready: m.state === 'available' || m.state === 'watched',
      download: downloads.find((d) => d.movieId === m.id),
      playable: downloads.some((d) => d.movieId === m.id && d.playable),
      queuePosition: queue.findIndex((q) => q.kind === 'movie' && q.movieId === m.id),
    })),
    ...series.map((s): OverviewTitle => {
      const active = episodes.filter((d) => d.seriesId === s.id).sort((a, b) => a.season - b.season || a.episode - b.episode)
      const ready = (s.counts.available ?? 0) + (s.counts.watched ?? 0) > 0
      const queued = (s.counts.queued ?? 0) + (s.counts.searching ?? 0) > 0
      return {
        ...s, mediaType: 'tv', key: `tv-${s.tmdbId}`, href: `/series/${s.id}`, requestedAt: s.createdAt,
        availableAt: s.latestAvailableAt, ready, playable: ready || active.some((d) => d.playable), download: active[0],
        state: active.length || s.counts.downloading ? 'downloading' : queued ? 'queued' : ready ? 'available' : s.counts.failed ? 'failed' : 'waiting',
        queuePosition: queue.findIndex((q) => q.kind === 'episode' && q.seriesId === s.id),
      }
    }),
  ]
}

const date = (value?: string) => value ? Date.parse(value) || 0 : 0
const pending = (m: OverviewTitle) => m.state === 'queued' || m.state === 'searching'
const rank = (m: OverviewTitle) => m.download || m.state === 'downloading' ? 0 : pending(m) ? 1 : 2

export function recentlyDownloaded(titles: OverviewTitle[]): OverviewTitle[] {
  return titles.filter((m) => m.state !== 'deleted' && (rank(m) < 2 || m.ready)).sort((a, b) => {
    const group = rank(a) - rank(b)
    if (group) return group
    if (rank(a) === 1) {
      const position = (a.queuePosition === undefined || a.queuePosition < 0 ? Infinity : a.queuePosition) -
        (b.queuePosition === undefined || b.queuePosition < 0 ? Infinity : b.queuePosition)
      if (Number.isFinite(position) && position !== 0) return position
      if (position === Infinity || position === -Infinity) return position
    }
    return date(rank(a) === 2 ? b.availableAt : b.requestedAt) - date(rank(a) === 2 ? a.availableAt : a.requestedAt) || a.key.localeCompare(b.key)
  })
}

export function recentlyRequested(titles: OverviewTitle[]): OverviewTitle[] {
  return [...titles].sort((a, b) => date(b.requestedAt) - date(a.requestedAt) || a.key.localeCompare(b.key))
}
