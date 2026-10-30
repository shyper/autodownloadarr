import { scopeLabel } from '../scopes'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMe } from '../api/hooks'
import { useCreateOAuthClient, useGrants, useRevokeGrant } from '../api/integrations'
import { Alert, Button, Card, Field, Input } from './ui'
import { errorMessage } from '../i18n'

export function ConnectedApps() {
  const { t } = useTranslation()
  const { data: user } = useMe()
  const grants = useGrants()
  const revoke = useRevokeGrant()
  const create = useCreateOAuthClient()
  const [name, setName] = useState('')
  const [redirect, setRedirect] = useState('')
  return <Card className="space-y-4">
    <h2 className="font-semibold">{t('integrations.title')}</h2>
    {grants.isError && <Alert>{errorMessage(t, grants.error)}</Alert>}
    {revoke.isError && <Alert>{errorMessage(t, revoke.error)}</Alert>}
    {grants.data?.enabled ? <p className="break-all font-mono text-sm">{grants.data.mcpUrl}</p> : <Alert>{t('integrations.configure')}</Alert>}
    {grants.data?.grants.length === 0 && <p className="text-sm text-muted">{t('integrations.none')}</p>}
    {grants.data?.grants.map((g) => <div key={g.id} className="flex items-start justify-between gap-3 border-t border-line pt-3">
      <div><p className="font-medium">{g.clientName}</p><p className="text-sm text-muted">{g.scopes.split(' ').map((s) => scopeLabel(t, s)).join(' · ')}</p></div>
      <Button variant="ghost" loading={revoke.isPending} onClick={() => revoke.mutate(g.id)}>{t('integrations.revoke')}</Button>
    </div>)}
    {user?.role === 'admin' && <form className="space-y-3 border-t border-line pt-4" onSubmit={(e) => { e.preventDefault(); create.mutate({ client_name: name, redirect_uris: redirect.split('\n').map((v) => v.trim()).filter(Boolean) }) }}>
      <h3 className="text-sm font-semibold">{t('integrations.register')}</h3>
      <p className="text-sm text-muted">{t('integrations.registerHint')}</p>
      <Field id="oauth-client-name" label={t('integrations.clientName')}><Input id="oauth-client-name" required value={name} onChange={(e) => setName(e.target.value)} /></Field>
      <Field id="oauth-redirect" label={t('integrations.callback')}><Input id="oauth-redirect" type="url" required value={redirect} onChange={(e) => setRedirect(e.target.value)} /></Field>
      {create.isError && <Alert>{errorMessage(t, create.error)}</Alert>}
      {create.data && <p className="break-all font-mono text-sm">{t('integrations.clientId')}: {create.data.client_id}</p>}
      <Button type="submit" loading={create.isPending}>{t('integrations.register')}</Button>
    </form>}
  </Card>
}
