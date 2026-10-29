import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { api } from './client'
import type { HistoryPage, QueueEntry } from './types'

export const downloadKeys = {
  queue: ['downloads', 'queue'] as const,
  history: (kind: string, result: string) => ['downloads', 'history', kind, result] as const,
}

/** Movies and episodes waiting for a free download slot. */
export function useQueue() {
  return useQuery({ queryKey: downloadKeys.queue, queryFn: () => api<QueueEntry[]>('/downloads/queue'), refetchInterval: 30_000 })
}

/** Past and running download attempts, newest first, 50 per page. */
export function useHistory(kind: string, result: string) {
  return useInfiniteQuery({
    queryKey: downloadKeys.history(kind, result),
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const q = new URLSearchParams()
      if (kind) q.set('kind', kind)
      if (result) q.set('result', result)
      if (pageParam) q.set('before', String(pageParam))
      return api<HistoryPage>(`/downloads/history?${q}`)
    },
    getNextPageParam: (last) => last.next,
  })
}
