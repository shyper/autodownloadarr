import { useQuery } from '@tanstack/react-query'
import { api } from './client'
import type { TrendingTitle } from './types'

export function useTrending(language: string) {
  return useQuery({
    queryKey: ['discover', 'trending', language],
    queryFn: ({ signal }) => api<TrendingTitle[]>(`/discover/trending?language=${encodeURIComponent(language)}`, { signal }),
    staleTime: 15 * 60_000,
  })
}
