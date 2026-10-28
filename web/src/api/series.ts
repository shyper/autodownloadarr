import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'
import type { DeleteTrigger, Episode, EpisodeDetail, EpisodeDownload, Series, SeriesDetail, SeriesSettings, ShowResult } from './types'

export const seriesKeys = {
  all: ['series'] as const,
  list: ['series', 'list'] as const,
  detail: (id: number) => ['series', 'detail', id] as const,
  episode: (id: number) => ['series', 'episode', id] as const,
  search: (q: string) => ['series', 'search', q] as const,
  show: (tmdbId: number) => ['series', 'show', tmdbId] as const,
  downloads: ['series', 'downloads'] as const,
}

export function useSeriesList() {
  return useQuery({ queryKey: seriesKeys.list, queryFn: () => api<Series[]>('/series') })
}

export function useSeries(id: number) {
  return useQuery({ queryKey: seriesKeys.detail(id), queryFn: () => api<SeriesDetail>(`/series/${id}`) })
}

export function useEpisode(id: number, enabled: boolean) {
  return useQuery({ queryKey: seriesKeys.episode(id), queryFn: () => api<EpisodeDetail>(`/episodes/${id}`), enabled })
}

export function useShowSearch(q: string) {
  return useQuery({
    queryKey: seriesKeys.search(q),
    queryFn: ({ signal }) => api<ShowResult[]>(`/series/search?q=${encodeURIComponent(q)}`, { signal }),
    enabled: q.trim().length >= 2,
    staleTime: 5 * 60_000,
  })
}

export function useShow(tmdbId: number | null) {
  return useQuery({
    queryKey: seriesKeys.show(tmdbId ?? 0),
    queryFn: () => api<ShowResult>(`/series/shows/${tmdbId}`),
    enabled: tmdbId !== null,
  })
}

export function useEpisodeDownloads() {
  return useQuery({ queryKey: seriesKeys.downloads, queryFn: () => api<EpisodeDownload[]>('/series/downloads') })
}

function useSeriesMutation<V, R>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient()
  return useMutation({ mutationFn: fn, onSettled: () => qc.invalidateQueries({ queryKey: seriesKeys.all }) })
}

export type SeriesRequest = {
  tmdbId: number
  seasons: number[]
  windowSize: number
  deleteTrigger: DeleteTrigger
  triggerValue: number
}

export const useRequestSeries = () => useSeriesMutation((v: SeriesRequest) => api<Series>('/series', { method: 'POST', body: v }))

export const useUpdateSeries = () =>
  useSeriesMutation((v: { id: number; settings: SeriesSettings }) => api<SeriesDetail>(`/series/${v.id}`, { method: 'PUT', body: v.settings }))

export const useDeleteSeries = () => useSeriesMutation((id: number) => api<void>(`/series/${id}`, { method: 'DELETE' }))

export const useSearchEpisode = () => useSeriesMutation((id: number) => api<Episode>(`/episodes/${id}/search`, { method: 'POST' }))

export const useSkipEpisodeRelease = () => useSeriesMutation((id: number) => api<void>(`/episodes/${id}/skip`, { method: 'POST' }))

export const useDownloadEpisodeRelease = () =>
  useSeriesMutation((v: { id: number; releaseId: number }) =>
    api<Episode>(`/episodes/${v.id}/releases/${v.releaseId}/download`, { method: 'POST' }),
  )
