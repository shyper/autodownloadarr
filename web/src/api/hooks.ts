import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from './client'
import type { Warning } from './types'

export type Role = 'admin' | 'user'

export type User = {
  id: number
  username: string
  role: Role
  createdAt: string
  lastLoginAt?: string
}

export type SystemStatus = {
  version: string
  setupRequired: boolean
}

export const queryKeys = {
  status: ['system', 'status'] as const,
  me: ['auth', 'me'] as const,
}

export function useSystemStatus() {
  return useQuery({
    queryKey: queryKeys.status,
    queryFn: ({ signal }) => api<SystemStatus>('/system/status', { signal }),
    staleTime: 60_000,
  })
}

/** Problems the user should know about (e.g. failing Plex scans). */
export function useWarnings(enabled = true) {
  return useQuery({
    queryKey: ['system', 'warnings'],
    queryFn: ({ signal }) => api<Warning[]>('/system/warnings', { signal }),
    refetchInterval: 60_000,
    enabled,
  })
}

/** The logged-in user, or null when not logged in. */
export function useMe() {
  return useQuery({
    queryKey: queryKeys.me,
    queryFn: async ({ signal }) => {
      try {
        return (await api<{ user: User }>('/auth/me', { signal })).user
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null
        throw err
      }
    },
    staleTime: 60_000,
  })
}

type Credentials = { username: string; password: string }

export function useLogin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Credentials) => api<{ user: User }>('/auth/login', { method: 'POST', body }),
    onSuccess: ({ user }) => qc.setQueryData(queryKeys.me, user),
  })
}

export function useSetup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Credentials & { token: string }) => api<{ user: User }>('/setup', { method: 'POST', body }),
    onSuccess: ({ user }) => {
      qc.setQueryData(queryKeys.me, user)
      qc.setQueryData<SystemStatus>(queryKeys.status, (s) => (s ? { ...s, setupRequired: false } : s))
    },
  })
}

export function useLogout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api<void>('/auth/logout', { method: 'POST' }),
    onSettled: () => {
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== 'system' })
      qc.setQueryData(queryKeys.me, null)
    },
  })
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (body: { currentPassword: string; newPassword: string }) =>
      api<void>('/auth/password', { method: 'POST', body }),
  })
}
