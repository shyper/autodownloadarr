import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useNotification, useSaveNotification, useTestNotification } from '../../api/config'
import type { NotificationSettings as Settings } from '../../api/types'
import { errorMessage } from '../../i18n'
import { Alert, Button, Checkbox, Field, Input, SectionTitle, Select } from '../ui'

type Draft = Settings & { token?: string }

export function NotificationSettings() {
  const q = useNotification()
  if (!q.data) return null
  return <NotificationForm initial={q.data} />
}

function NotificationForm({ initial }: { initial: Settings }) {
  const { t } = useTranslation()
  const save = useSaveNotification()
  const test = useTestNotification()
  const [n, setN] = useState<Draft>(initial)
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setN((prev) => ({ ...prev, [k]: v }))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(n)
  }
  const sendTest = () => test.mutate(n)

  return (
    <form onSubmit={submit} className="space-y-5">
      <SectionTitle title={t('settings.notifications.title')} description={t('settings.notifications.description')} />
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

      <Field id="notification-provider" label={t('settings.notifications.provider')}>
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
      </Field>

      {n.provider === 'telegram' ? (
        <div className="grid gap-5 sm:grid-cols-2">
          <Field id="notification-token" label={t('settings.notifications.token')} hint={t('settings.notifications.tokenHint')}>
            <Input
              id="notification-token"
              type="password"
              autoComplete="off"
              placeholder={n.hasToken ? t('settings.unchanged') : ''}
              value={n.token ?? ''}
              onChange={(e) => set('token', e.target.value)}
            />
          </Field>
          <Field id="notification-chat" label={t('settings.notifications.chatId')} hint={t('settings.notifications.chatIdHint')}>
            <Input id="notification-chat" value={n.chatId} onChange={(e) => set('chatId', e.target.value)} />
          </Field>
        </div>
      ) : (
        <Field
          id="notification-url"
          label={t(`settings.notifications.url.${n.provider}`)}
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
        <Button type="button" variant="secondary" onClick={sendTest} loading={test.isPending}>
          {t('settings.notifications.sendTest')}
        </Button>
        <Button type="submit" loading={save.isPending}>
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}
