import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { useSaveTraktApp, useTraktApp, type TraktApp } from '../../api/trakt'
import { errorMessage } from '../../i18n'
import { Alert, Button, Field, Input, SectionTitle } from '../ui'

export function TraktAppSettings() {
  const q = useTraktApp()
  const { t } = useTranslation()
  if (q.error) return <Alert>{errorMessage(t, q.error)}</Alert>
  if (!q.data) return null
  return <AppForm key={q.data.clientId} initial={q.data} />
}
function AppForm({ initial }: { initial: TraktApp }) {
  const { t } = useTranslation()
  const save = useSaveTraktApp()
  const [clientId, setClientId] = useState(initial.clientId)
  const submit = (e: FormEvent) => { e.preventDefault(); save.mutate({ clientId }) }
  return <form onSubmit={submit} className="space-y-5">
    <SectionTitle title={t('trakt.appTitle')} description={t('trakt.appHint')} />
    {!initial.redirectUri && <Alert>{t('trakt.publicUrlRequired')}</Alert>}
    <a href="https://app.trakt.tv/oauth/applications/new" target="_blank" rel="noreferrer" className="text-sm text-accent hover:underline">{t('trakt.createApp')}</a>
    <Field id="trakt-client" label={t('trakt.clientId')} hint={t('trakt.clientHint')}>
      <Input id="trakt-client" maxLength={200} value={clientId} onChange={(e) => setClientId(e.target.value)} autoComplete="off" />
    </Field>
    <Field id="trakt-redirect" label={t('trakt.redirectUri')} hint={t('trakt.redirectHint')}>
      <Input id="trakt-redirect" readOnly value={initial.redirectUri} className="font-mono" />
    </Field>
    <p className="text-sm text-muted">{t('trakt.changeAppHint')}</p>
    <Link to="/account" className="text-sm text-accent hover:underline">{t('trakt.accountLink')}</Link>
    {save.isError && <Alert>{errorMessage(t, save.error)}</Alert>}
    {save.isSuccess && <Alert tone="success">{t('settings.saved')}</Alert>}
    <div className="flex justify-end"><Button type="submit" loading={save.isPending}>{t('common.save')}</Button></div>
  </form>
}
