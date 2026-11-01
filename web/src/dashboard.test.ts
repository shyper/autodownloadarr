import { describe, expect, it } from 'vitest'
import { overviewTitles, recentlyDownloaded, recentlyRequested, type OverviewTitle } from './dashboard'
import type { Series } from './api/types'

const title = (key: string, state: OverviewTitle['state'], date: string): OverviewTitle => ({
  key, tmdbId: 1, title: key, mediaType: 'movie', overview: '', year: 2024, state, requestedAt: date, availableAt: date,
  ready: state === 'available' || state === 'watched',
})

describe('overview shelves', () => {
  it('puts active downloads first, then the actual queue, then newest completions', () => {
    const items = [
      title('old', 'available', '2024-01-01'),
      { ...title('queue-later', 'queued', '2024-06-01'), queuePosition: 2 },
      title('deleted', 'deleted', '2024-07-01'),
      title('new', 'watched', '2024-02-01'),
      { ...title('queue-first', 'queued', '2024-05-01'), queuePosition: 0 },
      title('active', 'downloading', '2024-01-01'),
      title('failed', 'failed', '2024-08-01'),
    ]
    expect(recentlyDownloaded(items).map((m) => m.key)).toEqual(['active', 'queue-first', 'queue-later', 'new', 'old'])
    expect(recentlyRequested(items)[0]?.key).toBe('failed')
    expect(items[0]?.key).toBe('old')
  })

  it('keeps removed movies out of the downloaded shelf but in the requested one', () => {
    const items = [title('expired', 'expired', '2024-06-01'), title('deleted', 'deleted', '2024-05-01'), title('ready', 'available', '2024-01-01')]
    expect(recentlyDownloaded(items).map((m) => m.key)).toEqual(['ready'])
    expect(recentlyRequested(items).map((m) => m.key)).toEqual(['expired', 'deleted', 'ready'])
  })

  it('uses completion time rather than request time for finished titles', () => {
    const earlyRequest = { ...title('early-request', 'available', '2024-01-01'), availableAt: '2024-06-01' }
    const lateRequest = title('late-request', 'available', '2024-05-01')
    expect(recentlyDownloaded([lateRequest, earlyRequest])[0]?.key).toBe('early-request')
    expect(recentlyRequested([earlyRequest, lateRequest])[0]?.key).toBe('late-request')
  })

  it('marks expired series as expired', () => {
    const series = { id: 8, tmdbId: 80, title: 'Old show', counts: { expired: 2, deleted: 6 }, createdAt: '2024-01-01', expiredAt: '2024-02-01' } as Series
    const items = overviewTitles([], [series], [], [], [])
    expect(items[0]).toMatchObject({ state: 'expired', ready: false, playable: false })
    expect(recentlyDownloaded(items)).toEqual([])
  })

  it('preserves partial series availability while a new episode downloads', () => {
    const series = { id: 7, tmdbId: 70, title: 'Show', counts: { available: 1, downloading: 1 }, createdAt: '2024-01-01' } as Series
    const items = overviewTitles([], [series], [], [], [])
    expect(items[0]).toMatchObject({ state: 'downloading', playable: true, ready: true, href: '/series/7' })
  })
})
