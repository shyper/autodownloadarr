import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { useBotLinkCode, useConfirmBotLink } from '../api/integrations'
import { errorMessage } from '../i18n'
import { Alert, Button, Card, Spinner } from '../components/ui'

export function ConnectBotPage() {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const code = params.get('code') ?? ''
  const link = useBotLinkCode(code)
  const confirm = useConfirmBotLink()
  const navigate = useNavigate()
  if (link.isPending) return <Spinner />
  if (link.isError) return <Alert>{t('bots.linkExpired')}</Alert>
  return <Card className="mx-auto max-w-xl space-y-4">
    <h1 className="text-xl font-semibold">{t('bots.connectTitle')}</h1>
    <p>{t('bots.connectHint', { provider: link.data.provider, external: link.data.externalId })}</p>
    {confirm.isError && <Alert>{errorMessage(t, confirm.error)}</Alert>}
    <div className="flex gap-3"><Link to="/account" className="py-2 text-sm text-muted">{t('common.cancel')}</Link><Button loading={confirm.isPending} onClick={() => confirm.mutate(code, { onSuccess: () => navigate('/account', { replace: true }) })}>{t('bots.connect')}</Button></div>
  </Card>
}
