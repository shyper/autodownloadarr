import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, useLocation } from 'react-router'
import { safeNext, useSession } from '../api/session'
import { Button, FullPage, Spinner } from './ui'

export function LoadState({ failed, retry }: { failed: boolean; retry: () => void }) {
  const { t } = useTranslation()
  if (!failed) {
    return (
      <FullPage>
        <Spinner />
        <span className="sr-only">{t('common.loading')}</span>
      </FullPage>
    )
  }
  return (
    <FullPage>
      <div className="space-y-3 text-center">
        <p>{t('errors.loadFailed')}</p>
        <Button variant="secondary" onClick={retry}>
          {t('common.retry')}
        </Button>
      </div>
    </FullPage>
  )
}

/** Renders children for signed-in users; otherwise redirects to setup or login. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const s = useSession()
  const location = useLocation()
  if (s.pending || s.failed) return <LoadState failed={s.failed} retry={s.retry} />
  if (s.setupRequired) return <Navigate to="/setup" replace />
  if (!s.user) {
    const next = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/login?next=${next}`} replace />
  }
  return children
}

/** Renders children for signed-out users; signed-in users go to `next`. */
export function RequireGuest({ children }: { children: ReactNode }) {
  const s = useSession()
  const location = useLocation()
  if (s.pending || s.failed) return <LoadState failed={s.failed} retry={s.retry} />
  if (s.setupRequired) return <Navigate to="/setup" replace />
  if (s.user) return <Navigate to={safeNext(new URLSearchParams(location.search).get('next'))} replace />
  return children
}
