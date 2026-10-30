import type { Attempt, EpisodeState, Release } from './api/types'

export type ReleaseMark = { kind: 'loaded' | 'loading' } | { kind: 'failed'; errorKind: string } | null

/**
 * What happened with a release of a movie or episode: the one on disk (or
 * being downloaded), or one whose last attempt failed.
 */
export function releaseMark(r: Release, current: { releaseKey: string; state: EpisodeState }, attempts: Attempt[]): ReleaseMark {
  if (r.releaseKey && r.releaseKey === current.releaseKey) {
    if (current.state === 'available' || current.state === 'watched') return { kind: 'loaded' }
    if (current.state === 'downloading') return { kind: 'loading' }
  }
  const last = attempts.find((a) => r.releaseKey && a.releaseKey === r.releaseKey) // newest first
  if (last?.result === 'failed') return { kind: 'failed', errorKind: last.errorKind }
  return null
}

/** Row or card classes for a release: highlighted when loaded, dimmed when unusable. */
export function releaseTone(r: Release, mark: ReleaseMark) {
  if (mark?.kind === 'loaded' || mark?.kind === 'loading') return 'border-accent bg-accent-soft/40'
  if (mark?.kind === 'failed' || r.blocklisted || r.rejected) return 'border-line text-muted'
  return 'border-line'
}

/** Table row classes for a release (no border). */
export function releaseRowTone(r: Release, mark: ReleaseMark) {
  if (mark?.kind === 'loaded' || mark?.kind === 'loading') return 'bg-accent-soft/40'
  if (mark?.kind === 'failed' || r.blocklisted || r.rejected) return 'text-muted'
  return ''
}
