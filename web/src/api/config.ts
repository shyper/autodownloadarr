import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'
import type {
  ArchivePassword,
  Caps,
  General,
  Indexer,
  MediaServer,
  Onboarding,
  PlexConfig,
  PlexLibraries,
  PlexPin,
  PlexPinStatus,
  Profile,
  UsenetServer,
} from './types'

export const configKeys = {
  general: ['config', 'general'] as const,
  tmdb: ['config', 'tmdb'] as const,
  usenet: ['config', 'usenet'] as const,
  indexers: ['config', 'indexers'] as const,
  plex: ['config', 'plex'] as const,
  plexLibraries: ['config', 'plex', 'libraries'] as const,
  profiles: ['config', 'profiles'] as const,
  passwords: ['config', 'passwords'] as const,
  onboarding: ['config', 'onboarding'] as const,
}

export function useGeneral() {
  return useQuery({ queryKey: configKeys.general, queryFn: () => api<General>('/settings/general') })
}

export function useSaveGeneral() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (g: General) => api<General>('/settings/general', { method: 'PUT', body: g }),
    onSuccess: (g) => {
      qc.setQueryData(configKeys.general, g)
      void qc.invalidateQueries({ queryKey: configKeys.plexLibraries })
    },
  })
}

export function useTMDB() {
  return useQuery({ queryKey: configKeys.tmdb, queryFn: () => api<{ hasKey: boolean }>('/settings/tmdb') })
}

export function useSaveTMDB() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (key: string) => api<{ hasKey: boolean }>('/settings/tmdb', { method: 'PUT', body: { key } }),
    onSuccess: (r) => qc.setQueryData(configKeys.tmdb, r),
  })
}

export function useUsenetServers() {
  return useQuery({ queryKey: configKeys.usenet, queryFn: () => api<UsenetServer[]>('/usenet-servers') })
}

export function useSaveUsenetServer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (u: Partial<UsenetServer>) =>
      u.id
        ? api<{ id: number }>(`/usenet-servers/${u.id}`, { method: 'PUT', body: u })
        : api<{ id: number }>('/usenet-servers', { method: 'POST', body: u }),
    onSuccess: () => qc.invalidateQueries({ queryKey: configKeys.usenet }),
  })
}

export function useDeleteUsenetServer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api<void>(`/usenet-servers/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: configKeys.usenet }),
  })
}

export function useTestUsenetServer() {
  return useMutation({
    mutationFn: (u: Partial<UsenetServer>) => api<{ ok: boolean }>('/usenet-servers/test', { method: 'POST', body: u }),
  })
}

export function useIndexers() {
  return useQuery({ queryKey: configKeys.indexers, queryFn: () => api<Indexer[]>('/indexers') })
}

export function useSaveIndexer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (i: Partial<Indexer>) =>
      i.id
        ? api<{ id: number }>(`/indexers/${i.id}`, { method: 'PUT', body: i })
        : api<{ id: number }>('/indexers', { method: 'POST', body: i }),
    onSuccess: () => qc.invalidateQueries({ queryKey: configKeys.indexers }),
  })
}

export function useDeleteIndexer() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api<void>(`/indexers/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: configKeys.indexers }),
  })
}

export function useTestIndexer() {
  return useMutation({
    mutationFn: (i: Partial<Indexer>) => api<Caps>('/indexers/test', { method: 'POST', body: i }),
  })
}

export function usePlex() {
  return useQuery({ queryKey: configKeys.plex, queryFn: () => api<PlexConfig>('/plex') })
}

export function useSavePlex() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (m: Partial<MediaServer>) => api<PlexConfig>('/plex', { method: 'PUT', body: m }),
    onSuccess: (r) => {
      qc.setQueryData(configKeys.plex, r)
      void qc.invalidateQueries({ queryKey: configKeys.plexLibraries })
    },
  })
}

export function useDeletePlex() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api<void>('/plex', { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: configKeys.plex }),
  })
}

export const createPlexPin = () => api<PlexPin>('/plex/pin', { method: 'POST' })
export const plexPinStatus = (id: number) => api<PlexPinStatus>(`/plex/pin/${id}`)

export function useConnectPlex() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { pinId: number; serverId: string; uri: string }) =>
      api<PlexConfig>('/plex/connect', { method: 'POST', body }),
    onSuccess: (r) => {
      qc.setQueryData(configKeys.plex, r)
      void qc.invalidateQueries({ queryKey: configKeys.plexLibraries })
    },
  })
}

export function usePlexLibraries(enabled: boolean) {
  return useQuery({
    queryKey: configKeys.plexLibraries,
    queryFn: () => api<PlexLibraries>('/plex/libraries'),
    enabled,
  })
}

export function useProfiles() {
  return useQuery({ queryKey: configKeys.profiles, queryFn: () => api<Profile[]>('/profiles') })
}

export function useSaveProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (p: Profile) => api<Profile>(`/profiles/${p.id}`, { method: 'PUT', body: p }),
    onSuccess: () => qc.invalidateQueries({ queryKey: configKeys.profiles }),
  })
}

export function usePasswords() {
  return useQuery({ queryKey: configKeys.passwords, queryFn: () => api<ArchivePassword[]>('/archive-passwords') })
}

export function useAddPassword() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (password: string) => api<{ id: number }>('/archive-passwords', { method: 'POST', body: { password } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: configKeys.passwords }),
  })
}

export function useDeletePassword() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api<void>(`/archive-passwords/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: configKeys.passwords }),
  })
}

export function useOnboarding(enabled = true) {
  return useQuery({ queryKey: configKeys.onboarding, queryFn: () => api<Onboarding>('/onboarding'), enabled })
}

export function useSaveOnboarding() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (o: Onboarding) => api<Onboarding>('/onboarding', { method: 'PUT', body: o }),
    onSuccess: (o) => qc.setQueryData(configKeys.onboarding, o),
  })
}
