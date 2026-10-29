import { useTranslation } from 'react-i18next'
import { Navigate, NavLink, useParams } from 'react-router'
import { GeneralForm } from '../components/settings/GeneralForm'
import { IndexerSettings } from '../components/settings/IndexerSettings'
import { PasswordSettings } from '../components/settings/PasswordSettings'
import { PlexSettings } from '../components/settings/PlexSettings'
import { ProfileSettings } from '../components/settings/ProfileSettings'
import { TmdbForm } from '../components/settings/TmdbForm'
import { UsenetSettings } from '../components/settings/UsenetSettings'
import { useSystemStatus } from '../api/hooks'
import { Card, SectionTitle } from '../components/ui'

const tabs = ['general', 'usenet', 'indexers', 'plex', 'profile', 'passwords'] as const
type Tab = (typeof tabs)[number]

export function SettingsPage() {
  const { t } = useTranslation()
  const { tab } = useParams()
  if (!tabs.includes(tab as Tab)) return <Navigate to="/settings/general" replace />

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">{t('nav.settings')}</h1>
      <div className="grid gap-6 md:grid-cols-[180px_1fr]">
        <nav className="flex gap-1 overflow-x-auto md:flex-col" aria-label={t('nav.settings')}>
          {tabs.map((tb) => (
            <NavLink
              key={tb}
              to={`/settings/${tb}`}
              className={({ isActive }) =>
                `rounded-lg px-3 py-2 text-sm font-medium whitespace-nowrap ${isActive ? 'bg-accent-soft text-accent' : 'text-muted hover:bg-surface-2 hover:text-fg'}`
              }
            >
              {t(`settings.tabs.${tb}`)}
            </NavLink>
          ))}
        </nav>
        <div className="min-w-0 space-y-6">
          {tab === 'general' && (
            <>
              <Card>
                <GeneralForm />
              </Card>
              <Card>
                <TmdbForm />
              </Card>
              <Card>
                <SystemInfo />
              </Card>
            </>
          )}
          {tab === 'usenet' && (
            <Card>
              <UsenetSettings />
            </Card>
          )}
          {tab === 'indexers' && (
            <Card>
              <IndexerSettings />
            </Card>
          )}
          {tab === 'plex' && (
            <Card>
              <PlexSettings />
            </Card>
          )}
          {tab === 'profile' && (
            <Card>
              <ProfileSettings />
            </Card>
          )}
          {tab === 'passwords' && (
            <Card>
              <PasswordSettings />
            </Card>
          )}
        </div>
      </div>
    </div>
  )
}

function SystemInfo() {
  const { t } = useTranslation()
  const { data: status } = useSystemStatus()
  return (
    <div className="space-y-3">
      <SectionTitle title={t('settings.system.title')} />
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
        <dt className="text-muted">{t('settings.system.version')}</dt>
        <dd className="font-mono">{status?.version}</dd>
      </dl>
    </div>
  )
}
