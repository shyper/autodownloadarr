import { useTranslation } from 'react-i18next'
import { NavLink, Outlet, useNavigate } from 'react-router'
import { useLogout, useMe } from '../api/hooks'
import { useServerEvents, type StreamState } from '../hooks/useServerEvents'
import { LanguageSelect } from './LanguageSelect'
import { Button, Logo } from './ui'

const streamColour: Record<StreamState, string> = {
  open: 'bg-ok',
  connecting: 'bg-warn',
  error: 'bg-danger',
}

export function Layout() {
  const { t } = useTranslation()
  const { data: user } = useMe()
  const logout = useLogout()
  const navigate = useNavigate()
  const stream = useServerEvents()

  const navClass = ({ isActive }: { isActive: boolean }) =>
    `rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
      isActive ? 'bg-accent-soft text-accent' : 'text-muted hover:bg-surface-2 hover:text-fg'
    }`

  return (
    <div className="min-h-dvh">
      <header className="sticky top-0 z-10 border-b border-line bg-surface/90 backdrop-blur">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-6 gap-y-2 px-4 py-3">
          <NavLink to="/" className="flex items-center gap-2 font-semibold text-fg">
            <Logo className="size-7" />
            {t('app.name')}
          </NavLink>
          <nav className="flex gap-1 overflow-x-auto">
            <NavLink to="/" end className={navClass}>
              {t('nav.dashboard')}
            </NavLink>
            <NavLink to="/movies" className={navClass}>
              {t('nav.movies')}
            </NavLink>
            <NavLink to="/series" className={navClass}>
              {t('nav.series')}
            </NavLink>
            {user?.role === 'admin' && (
              <NavLink to="/settings" className={navClass}>
                {t('nav.settings')}
              </NavLink>
            )}
            <NavLink to="/account" className={navClass}>
              {t('nav.account')}
            </NavLink>
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <span
              className={`size-2 rounded-full ${streamColour[stream]}`}
              title={t(`nav.liveUpdates.${stream}`)}
              role="img"
              aria-label={t(`nav.liveUpdates.${stream}`)}
            />
            <LanguageSelect />
            <span className="hidden text-sm text-muted sm:inline">{user?.username}</span>
            <Button
              variant="ghost"
              loading={logout.isPending}
              onClick={() => logout.mutate(undefined, { onSettled: () => navigate('/login', { replace: true }) })}
            >
              {t('nav.logout')}
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  )
}
