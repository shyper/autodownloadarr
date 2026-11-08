import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'
import { useMe } from './hooks'

export type TraktSettings = { enabled: boolean; watchlist: boolean; listIds: number[]; pollMinutes: number }
export type TraktStatus = TraktSettings & {
  configured: boolean; connected: boolean; linked: boolean; syncing: boolean; username: string
  lastSyncAt: number; nextSyncAt: number; lastError: string; requested: number; skipped: number; failed: number
}
export type TraktList = { name: string; item_count: number; ids: { trakt: number } }
export type TraktApp = { clientId: string; redirectUri: string }
const key = (id?: number) => ['trakt', 'account', id]
const listKey = (id?: number) => ['trakt', 'lists', id]
export function useTrakt() {
  const { data: user } = useMe()
  return useQuery({ queryKey: key(user?.id), queryFn: ({ signal }) => api<TraktStatus>('/trakt', { signal }), enabled: !!user, refetchInterval: (q) => q.state.data?.syncing ? 2000 : 30000 })
}
export function useTraktLists(enabled: boolean) {
  const { data: user } = useMe()
  return useQuery({ queryKey: listKey(user?.id), queryFn: ({ signal }) => api<TraktList[]>('/trakt/lists', { signal }), enabled: enabled && !!user, retry: false })
}
export const useConnectTrakt = () => useMutation({ mutationFn: () => api<{ url: string }>('/trakt/connect', { method: 'POST' }), onSuccess: (data) => window.location.assign(data.url) })
export function useSaveTrakt() {
  const { data: user } = useMe()
  const qc = useQueryClient()
  return useMutation({ mutationFn: (body: TraktSettings) => api<TraktStatus>('/trakt', { method: 'PUT', body }), onSuccess: (data) => qc.setQueryData(key(user?.id), data) })
}
export function useSyncTrakt() {
  const { data: user } = useMe()
  const qc = useQueryClient()
  return useMutation({ mutationFn: () => api<TraktStatus>('/trakt/sync', { method: 'POST' }), onSuccess: (data) => qc.setQueryData(key(user?.id), data) })
}
export function useDisconnectTrakt() {
  const { data: user } = useMe()
  const qc = useQueryClient()
  return useMutation({ mutationFn: () => api<void>('/trakt', { method: 'DELETE' }), onSuccess: () => { qc.removeQueries({ queryKey: listKey(user?.id) }); return qc.invalidateQueries({ queryKey: key(user?.id) }) } })
}
export const useTraktApp = () => useQuery({ queryKey: ['trakt', 'app'], queryFn: () => api<TraktApp>('/trakt/config') })
export function useSaveTraktApp() {
  const qc = useQueryClient()
  return useMutation({ mutationFn: (body: Pick<TraktApp, 'clientId'>) => api<TraktApp>('/trakt/config', { method: 'PUT', body }), onSuccess: (data) => { qc.setQueryData(['trakt', 'app'], data); return qc.invalidateQueries({ queryKey: ['trakt', 'account'] }) } })
}
