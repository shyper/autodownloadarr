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

export type BotConfig = {
  telegramEnabled: boolean; telegramMode: 'polling' | 'webhook'; hasTelegramToken: boolean; telegramToken?: string
  discordEnabled: boolean; hasDiscordToken: boolean; discordToken?: string; discordApplicationId: string; discordPublicKey: string; discordGuilds: string[]
  telegramWebhookUrl: string; discordInteractionsUrl: string
}
export type BotLink = { id: number; provider: string; externalId: string; language: string; notifications: boolean }
export function useBotConfig() { return useQuery({ queryKey: ['bots', 'config'], queryFn: () => api<BotConfig>('/bots') }) }
export function useSaveBotConfig() {
  const qc = useQueryClient()
  return useMutation({ mutationFn: (body: BotConfig) => api<BotConfig>('/bots', { method: 'PUT', body }), onSuccess: (data) => qc.setQueryData(['bots', 'config'], data) })
}
export const useTestBots = () => useMutation({ mutationFn: () => api<void>('/bots/test', { method: 'POST' }) })
export const useSyncBots = () => useMutation({ mutationFn: () => api<void>('/bots/sync', { method: 'POST' }) })
export const useBotLinks = () => useQuery({ queryKey: ['bots', 'links'], queryFn: () => api<BotLink[]>('/bots/links') })
export function useUpdateBotLink() {
  const qc = useQueryClient()
  return useMutation({ mutationFn: (body: Pick<BotLink, 'id' | 'language' | 'notifications'>) => api<void>('/bots/links', { method: 'PUT', body }), onSuccess: () => qc.invalidateQueries({ queryKey: ['bots', 'links'] }) })
}
export function useDeleteBotLink() {
  const qc = useQueryClient()
  return useMutation({ mutationFn: (id: number) => api<void>(`/bots/links/${id}`, { method: 'DELETE' }), onSuccess: () => qc.invalidateQueries({ queryKey: ['bots', 'links'] }) })
}
export const useBotLinkCode = (code: string) => useQuery({ queryKey: ['bots', 'link', code], queryFn: () => api<BotLink>(`/bots/link-code?code=${encodeURIComponent(code)}`), retry: false })
export function useConfirmBotLink() {
  const qc = useQueryClient()
  return useMutation({ mutationFn: (code: string) => api<void>('/bots/links', { method: 'POST', body: { code } }), onSuccess: () => qc.invalidateQueries({ queryKey: ['bots', 'links'] }) })
}
