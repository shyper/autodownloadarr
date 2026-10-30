import { scopeLabel } from '../scopes'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation } from 'react-router'
import { useAuthorization, useAuthorize } from '../api/integrations'
import { Alert, Button, Card, Checkbox, Spinner } from '../components/ui'
import { errorMessage } from '../i18n'

export function AuthorizePage() {
  const { t } = useTranslation()
  const { search } = useLocation()
  const request = useAuthorization(search)
  const authorize = useAuthorize(search)
  const [excluded, setExcluded] = useState<string[]>([])
  if (request.isPending) return <Spinner />
  if (request.isError) return <Alert>{errorMessage(t, request.error)}</Alert>
  return <Card className="mx-auto max-w-xl space-y-4">
    <h1 className="text-xl font-semibold">{t('integrations.authorize')}</h1>
    <p>{t('integrations.consent', { name: request.data.clientName })}</p>
    <p className="break-all text-xs text-muted">{request.data.clientId}</p>
    <p className="text-sm text-muted">{t('integrations.callback')}: {request.data.redirectUri}</p>
    {request.data.scopes.map((scope) => <Checkbox key={scope} label={scopeLabel(t, scope)} checked={!excluded.includes(scope)} onChange={(e) => setExcluded((prev) => e.target.checked ? prev.filter((s) => s !== scope) : [...prev, scope])} />)}
    {authorize.isError && <Alert>{errorMessage(t, authorize.error)}</Alert>}
    <div className="flex gap-2">
      <Button variant="secondary" loading={authorize.isPending} onClick={() => authorize.mutate({ allow: false, scope: '' })}>{t('integrations.deny')}</Button>
      <Button disabled={request.data.scopes.every((s) => excluded.includes(s))} loading={authorize.isPending} onClick={() => authorize.mutate({ allow: true, scope: request.data.scopes.filter((s) => !excluded.includes(s)).join(' ') })}>{t('integrations.allow')}</Button>
    </div>
  </Card>
}
