import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'

export type Grant = { id: string; clientId: string; clientName: string; scopes: string; createdAt: number }
export function useGrants() {
  return useQuery({ queryKey: ['oauth', 'grants'], queryFn: () => api<{ grants: Grant[]; mcpUrl: string; enabled: boolean }>('/oauth/grants') })
}
export function useRevokeGrant() {
  const qc = useQueryClient()
  return useMutation({ mutationFn: (id: string) => api<void>('/oauth/grants/revoke', { method: 'POST', body: { id } }), onSuccess: () => qc.invalidateQueries({ queryKey: ['oauth', 'grants'] }) })
}
export function useCreateOAuthClient() {
  return useMutation({ mutationFn: (body: { client_name: string; redirect_uris: string[] }) => api<{ client_id: string }>('/oauth/clients', { method: 'POST', body }) })
}
export function useAuthorization(query: string) {
  return useQuery({ queryKey: ['oauth', 'authorization', query], queryFn: () => api<{ clientName: string; clientId: string; redirectUri: string; scopes: string[] }>(`/oauth/authorization${query}`), retry: false })
}
export function useAuthorize(query: string) {
  return useMutation({ mutationFn: (body: { allow: boolean; scope: string }) => api<{ redirect: string }>(`/oauth/authorize${query}`, { method: 'POST', body }), onSuccess: (data) => { window.location.assign(data.redirect) } })
}
