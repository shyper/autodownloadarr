import { useEffect, useRef, useState } from 'react'
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
  const [menuOpen, setMenuOpen] = useState(false)
  const header = useRef<HTMLElement>(null)

  // The phone menu closes on navigation, Escape and taps outside it.
  useEffect(() => {
    if (!menuOpen) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setMenuOpen(false)
    const onPointer = (e: PointerEvent) => {
      if (header.current && !header.current.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('pointerdown', onPointer)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('pointerdown', onPointer)
    }
  }, [menuOpen])

  const links = [
    { to: '/', label: t('nav.dashboard'), end: true },
    { to: '/movies', label: t('nav.movies') },
    { to: '/series', label: t('nav.series') },
    { to: '/downloads', label: t('nav.downloads') },
    ...(user?.role === 'admin' ? [{ to: '/settings', label: t('nav.settings') }] : []),
    { to: '/account', label: t('nav.account') },
  ]
  const navClass = ({ isActive }: { isActive: boolean }) =>
    `rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
      isActive ? 'bg-accent-soft text-accent' : 'text-muted hover:bg-surface-2 hover:text-fg'
    }`
  const mobileNavClass = ({ isActive }: { isActive: boolean }) =>
    `flex min-h-11 items-center rounded-lg px-3 text-base font-medium ${
      isActive ? 'bg-accent-soft text-accent' : 'text-fg hover:bg-surface-2'
    }`
  const doLogout = () => logout.mutate(undefined, { onSettled: () => navigate('/login', { replace: true }) })
  const liveDot = (
    <span
      className={`size-2 shrink-0 rounded-full ${streamColour[stream]}`}
      title={t(`nav.liveUpdates.${stream}`)}
      role="img"
      aria-label={t(`nav.liveUpdates.${stream}`)}
    />
  )

  return (
    <div className="min-h-dvh">
      <header ref={header} className="sticky top-0 z-10 border-b border-line bg-surface/90 backdrop-blur">
        <div className="mx-auto flex max-w-6xl items-center gap-x-4 px-4 py-3 xl:gap-x-6">
          <NavLink to="/" className="flex items-center gap-2 font-semibold text-fg">
            <Logo className="size-7" />
            {t('app.name')}
          </NavLink>
          <nav className="hidden gap-1 lg:flex">
            {links.map((l) => (
              <NavLink key={l.to} to={l.to} end={l.end} className={navClass}>
                {l.label}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto hidden items-center gap-3 lg:flex">
            {liveDot}
            <LanguageSelect />
            <span className="hidden text-sm text-muted xl:inline">{user?.username}</span>
            <Button variant="ghost" loading={logout.isPending} onClick={doLogout}>
              {t('nav.logout')}
            </Button>
          </div>
          <div className="ml-auto flex items-center gap-3 lg:hidden">
            {liveDot}
            <button
              type="button"
              className="flex size-11 items-center justify-center rounded-lg text-fg hover:bg-surface-2 focus-visible:outline-2 focus-visible:outline-accent"
              aria-expanded={menuOpen}
              aria-controls="mobile-menu"
              aria-label={menuOpen ? t('nav.menuClose') : t('nav.menuOpen')}
              onClick={() => setMenuOpen((o) => !o)}
            >
              <svg viewBox="0 0 24 24" className="size-6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                {menuOpen ? <path d="M6 6l12 12M18 6L6 18" /> : <path d="M4 7h16M4 12h16M4 17h16" />}
              </svg>
            </button>
          </div>
        </div>
        {menuOpen && (
          <div id="mobile-menu" className="border-t border-line px-4 pt-2 pb-4 lg:hidden">
            <nav className="flex flex-col gap-1">
              {links.map((l) => (
                <NavLink key={l.to} to={l.to} end={l.end} className={mobileNavClass} onClick={() => setMenuOpen(false)}>
                  {l.label}
                </NavLink>
              ))}
            </nav>
            <div className="mt-3 flex items-center gap-3 border-t border-line pt-3">
              <LanguageSelect />
              <span className="min-w-0 flex-1 truncate text-sm text-muted">{user?.username}</span>
              <Button variant="ghost" loading={logout.isPending} onClick={doLogout}>
                {t('nav.logout')}
              </Button>
            </div>
          </div>
        )}
      </header>
      <main className="mx-auto max-w-6xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  )
}
