import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { useGrants } from '../../api/integrations'
import { errorMessage } from '../../i18n'
import { scopeLabel } from '../../scopes'
import { ConnectedApps } from '../ConnectedApps'
import { Alert, Button, Card, Spinner } from '../ui'

export function McpSettings() {
  const { t } = useTranslation()
  const config = useGrants()
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const endpoint = config.data?.enabled ? config.data.mcpUrl : ''
  const origin = endpoint ? endpoint.replace(/\/mcp$/, '') : 'https://autodownloadarr.example.com'
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(endpoint)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
  }

  return <div className="space-y-6">
    <Card className="space-y-4">
      <h2 className="text-lg font-semibold">{t('mcp.title')}</h2>
      <p className="text-sm text-muted">{t('mcp.description')}</p>
      {config.isPending && <Spinner />}
      {config.isError && <Alert>{errorMessage(t, config.error)}</Alert>}
      {config.data && !config.data.enabled && <Alert>{t('mcp.disabled')}</Alert>}
      {endpoint && <div className="space-y-2 rounded-xl border border-line p-3">
        <p className="text-sm font-medium">{t('mcp.serverUrl')}</p>
        <code className="block break-all text-sm">{endpoint}</code>
        <Button variant="secondary" onClick={() => { void copy() }}>{t(copyState === 'copied' ? 'mcp.copied' : 'mcp.copy')}</Button>
        {copyState === 'failed' && <Alert>{t('mcp.copyFailed')}</Alert>}
      </div>}
      <h3 className="font-medium">{t('mcp.setupTitle')}</h3>
      <ol className="list-outside list-decimal space-y-4 pl-5 text-sm">
        <li className="space-y-2"><p>{t('mcp.setupServer')}</p><pre className="overflow-x-auto rounded-lg bg-surface-2 p-3 text-xs">{`environment:\n  - AUTODOWNLOADARR_PUBLIC_URL=${origin}`}</pre><p className="text-muted">{t('mcp.restart')}</p><p className="text-muted">{t('mcp.proxy')}</p></li>
        <li>{t('mcp.setupClient')}</li>
        <li>{t('mcp.setupLogin')}</li>
        <li>{t('mcp.setupUse')}</li>
      </ol>
      <p className="text-sm text-muted">{t('mcp.compatibility')}</p>
    </Card>
    <Card className="space-y-3">
      <h3 className="font-semibold">{t('mcp.permissionsTitle')}</h3>
      <ul className="list-inside list-disc space-y-1 text-sm">{['media:read', 'media:request', 'media:manage', 'media:delete', 'offline_access'].map((scope) => <li key={scope}>{scopeLabel(t, scope)}</li>)}</ul>
      <p className="text-sm text-muted">{t('mcp.permissionsHint')}</p>
      <Link className="text-sm text-accent hover:underline" to="/account">{t('mcp.accountLink')}</Link>
    </Card>
    <ConnectedApps showEndpoint={false} />
  </div>
}
