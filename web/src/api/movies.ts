import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'
import type { Download, Movie, MovieDetail, SearchResult } from './types'

export const movieKeys = {
  all: ['movies'] as const,
  list: ['movies', 'list'] as const,
  detail: (id: number) => ['movies', 'detail', id] as const,
  search: (q: string) => ['movies', 'search', q] as const,
  downloads: ['downloads'] as const,
}

export function useMovies() {
  return useQuery({ queryKey: movieKeys.list, queryFn: () => api<Movie[]>('/movies') })
}

export function useMovie(id: number) {
  return useQuery({ queryKey: movieKeys.detail(id), queryFn: () => api<MovieDetail>(`/movies/${id}`) })
}

export function useMovieSearch(q: string) {
  return useQuery({
    queryKey: movieKeys.search(q),
    queryFn: ({ signal }) => api<SearchResult[]>(`/movies/search?q=${encodeURIComponent(q)}`, { signal }),
    enabled: q.trim().length >= 2,
    staleTime: 5 * 60_000,
  })
}

/** Running downloads; kept up to date by server events (see useServerEvents). */
export function useDownloads() {
  return useQuery({ queryKey: movieKeys.downloads, queryFn: () => api<Download[]>('/downloads') })
}

function useMovieMutation<V, R>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSettled: () => qc.invalidateQueries({ queryKey: movieKeys.all }),
  })
}

export const useRequestMovie = () =>
  useMovieMutation((v: { tmdbId: number; profileId?: number }) => api<Movie>('/movies', { method: 'POST', body: v }))

export const useSearchAgain = () => useMovieMutation((id: number) => api<Movie>(`/movies/${id}/search`, { method: 'POST' }))

export const useKeepMovie = () =>
  useMovieMutation((v: { id: number; keep: boolean }) => api<Movie>(`/movies/${v.id}/keep`, { method: 'POST', body: { keep: v.keep } }))

export const useSkipRelease = () => useMovieMutation((id: number) => api<void>(`/movies/${id}/skip`, { method: 'POST' }))

export const useDownloadRelease = () =>
  useMovieMutation((v: { id: number; releaseId: number }) =>
    api<Movie>(`/movies/${v.id}/releases/${v.releaseId}/download`, { method: 'POST' }),
  )

export function useDeleteMovie() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { id: number; forget: boolean }) =>
      api<void>(`/movies/${v.id}${v.forget ? '?forget=1' : ''}`, { method: 'DELETE' }),
    onSuccess: (_, v) => {
      void qc.invalidateQueries({ queryKey: movieKeys.list })
      if (!v.forget) void qc.invalidateQueries({ queryKey: movieKeys.detail(v.id) })
    },
  })
}

export const useRemoveBlocklist = () => useMovieMutation((id: number) => api<void>(`/blocklist/${id}`, { method: 'DELETE' }))
