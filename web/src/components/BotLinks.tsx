import { useTranslation } from 'react-i18next'
import { useBotLinks, useDeleteBotLink, useUpdateBotLink } from '../api/integrations'
import { errorMessage } from '../i18n'
import { Alert, Button, Card, Checkbox, Select } from './ui'

export function BotLinks() {
  const { t } = useTranslation()
  const links = useBotLinks()
  const update = useUpdateBotLink()
  const remove = useDeleteBotLink()
  const error = links.error ?? update.error ?? remove.error
  return <Card className="space-y-4">
    <h2 className="font-semibold">{t('bots.linksTitle')}</h2>
    <p className="text-sm text-muted">{t('bots.linksHint')}</p>
    {error && <Alert>{errorMessage(t, error)}</Alert>}
    {links.data?.length === 0 && <p className="text-sm text-muted">{t('bots.noLinks')}</p>}
    {links.data?.map((link) => <div key={link.id} className="space-y-3 border-t border-line pt-3">
      <p className="font-medium capitalize">{link.provider} <span className="text-sm text-muted">{link.externalId}</span></p>
      <Select aria-label={t('bots.language')} value={link.language} disabled={update.isPending} onChange={(e) => update.mutate({ id: link.id, language: e.target.value, notifications: link.notifications })}><option value="de">Deutsch</option><option value="en">English</option></Select>
      <Checkbox label={t('bots.notifications')} checked={link.notifications} disabled={update.isPending} onChange={(e) => update.mutate({ id: link.id, language: link.language, notifications: e.target.checked })} />
      <Button variant="ghost" loading={remove.isPending} onClick={() => remove.mutate(link.id)}>{t('bots.disconnect')}</Button>
    </div>)}
  </Card>
}
