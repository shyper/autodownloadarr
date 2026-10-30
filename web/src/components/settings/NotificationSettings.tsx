import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useNotification, useSaveNotification, useTestNotification } from '../../api/config'
import type { NotificationSettings as Settings } from '../../api/types'
import { notificationDraft, notificationUrlLabel } from '../../notifications'
import { errorMessage } from '../../i18n'
import { Alert, Button, Checkbox, Field, Input, SectionTitle, Select } from '../ui'

type Draft = Settings & { token?: string }

export function NotificationSettings({ provider }: { provider?: 'telegram' }) {
  const q = useNotification()
  if (!q.data) return null
  return <NotificationForm key={`${provider ?? 'all'}:${q.data.provider}`} initial={q.data} provider={provider} />
}

function NotificationForm({ initial, provider }: { initial: Settings; provider?: 'telegram' }) {
  const { t } = useTranslation()
  const save = useSaveNotification()
  const test = useTestNotification()
  const [n, setN] = useState<Draft>(() => notificationDraft(initial, provider))
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setN((prev) => ({ ...prev, [k]: v }))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(n)
  }
  const sendTest = () => test.mutate(n)
  const replacingChannel = provider === 'telegram' && initial.provider !== '' && initial.provider !== 'telegram'
  const canSave = !replacingChannel || n.enabled

  return (
    <form onSubmit={submit} className="space-y-5">
      <SectionTitle title={t(provider ? 'bots.telegramNotificationsTitle' : 'settings.notifications.title')} description={t(provider ? 'bots.telegramNotificationsHint' : 'settings.notifications.description')} />
      {replacingChannel && <Alert>{t('bots.replacesNotificationChannel', { channel: t(`settings.notifications.providers.${initial.provider}`) })}</Alert>}
      {save.isError && <Alert>{errorMessage(t, save.error)}</Alert>}
      {save.isSuccess && <Alert tone="success">{t('settings.saved')}</Alert>}
      {test.isError && <Alert>{errorMessage(t, test.error)}</Alert>}
      {test.isSuccess && <Alert tone="success">{t('settings.notifications.testSent')}</Alert>}

      <Checkbox
        label={t('settings.notifications.enabled')}
        hint={t('settings.notifications.enabledHint')}
        checked={n.enabled}
        onChange={(e) => set('enabled', e.target.checked)}
      />

      {!provider && <Field id="notification-provider" label={t('settings.notifications.provider')}>
        <Select
          id="notification-provider"
          value={n.provider}
          onChange={(e) => set('provider', e.target.value as Settings['provider'])}
        >
          <option value="webhook">{t('settings.notifications.providers.webhook')}</option>
          <option value="discord">{t('settings.notifications.providers.discord')}</option>
          <option value="ntfy">{t('settings.notifications.providers.ntfy')}</option>
          <option value="telegram">{t('settings.notifications.providers.telegram')}</option>
        </Select>
      </Field>}

      {n.provider === 'telegram' ? (
        <div className="grid gap-5 sm:grid-cols-2">
          <div className="space-y-3">
          <Checkbox label={t('bots.useBotToken')} hint={t('bots.useBotTokenHint')} checked={n.useBotToken} onChange={(e) => set('useBotToken', e.target.checked)} />
          {!n.useBotToken && <Field id="notification-token" label={t('settings.notifications.token')} hint={t('settings.notifications.tokenHint')}>
            <Input
              id="notification-token"
              type="password"
              autoComplete="off"
              placeholder={n.hasToken && !initial.useBotToken ? t('settings.unchanged') : ''}
              value={n.token ?? ''}
              onChange={(e) => set('token', e.target.value)}
            />
          </Field>}
          </div>
          <Field id="notification-chat" label={t('settings.notifications.chatId')} hint={t('settings.notifications.chatIdHint')}>
            <Input id="notification-chat" required={n.enabled} value={n.chatId} onChange={(e) => set('chatId', e.target.value)} />
          </Field>
        </div>
      ) : (
        <Field
          id="notification-url"
          label={notificationUrlLabel(t, n.provider)}
          hint={t(`settings.notifications.urlHint.${n.provider}`)}
        >
          <Input
            id="notification-url"
            type="url"
            className="font-mono"
            value={n.url}
            onChange={(e) => set('url', e.target.value)}
          />
        </Field>
      )}

      <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" variant="secondary" onClick={sendTest} disabled={!canSave} loading={test.isPending}>
          {t('settings.notifications.sendTest')}
        </Button>
        <Button type="submit" disabled={!canSave} loading={save.isPending}>
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}
