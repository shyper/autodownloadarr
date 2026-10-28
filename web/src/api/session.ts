import { useMe, useSystemStatus } from './hooks'

/** Only same-site paths are accepted as redirect targets. */
export function safeNext(next: string | null): string {
  return next && next.startsWith('/') && !next.startsWith('//') ? next : '/'
}

/** Setup state and signed-in user, as needed by the route guards. */
export function useSession() {
  const status = useSystemStatus()
  const me = useMe()
  return {
    pending: status.isPending || me.isPending,
    failed: status.isError || me.isError,
    retry: () => void Promise.all([status.refetch(), me.refetch()]),
    setupRequired: status.data?.setupRequired ?? false,
    user: me.data ?? null,
  }
}
