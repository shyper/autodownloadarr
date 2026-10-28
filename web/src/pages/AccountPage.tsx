import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useChangePassword, useMe } from '../api/hooks'
import { Alert, Button, Card, Field, Input } from '../components/ui'
import { errorMessage } from '../i18n'

export function AccountPage() {
  const { t, i18n } = useTranslation()
  const { data: user } = useMe()
  if (!user) return null

  return (
    <div className="max-w-xl space-y-6">
      <h1 className="text-2xl font-semibold">{t('account.title')}</h1>
      <Card>
        <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
          <dt className="text-muted">{t('login.username')}</dt>
          <dd>{user.username}</dd>
          <dt className="text-muted">{t('account.role')}</dt>
          <dd>{t(`account.roles.${user.role}`)}</dd>
          <dt className="text-muted">{t('account.memberSince')}</dt>
          <dd>{new Date(user.createdAt).toLocaleDateString(i18n.resolvedLanguage)}</dd>
        </dl>
      </Card>
      <ChangePasswordCard />
    </div>
  )
}

function ChangePasswordCard() {
  const { t } = useTranslation()
  const change = useChangePassword()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (next.length < 8) return setLocalError(t('setup.errors.passwordTooShort'))
    if (next !== confirm) return setLocalError(t('setup.errors.passwordMismatch'))
    setLocalError(null)
    change.mutate(
      { currentPassword: current, newPassword: next },
      {
        onSuccess: () => {
          setCurrent('')
          setNext('')
          setConfirm('')
        },
      },
    )
  }

  const error = localError ?? (change.isError ? errorMessage(t, change.error) : null)

  return (
    <Card>
      <form onSubmit={submit} className="space-y-4">
        <div className="space-y-1">
          <h2 className="font-semibold">{t('account.changePassword.title')}</h2>
          <p className="text-sm text-muted">{t('account.changePassword.description')}</p>
        </div>
        {error && <Alert>{error}</Alert>}
        {change.isSuccess && !error && <Alert tone="success">{t('account.changePassword.success')}</Alert>}
        <Field id="current" label={t('account.changePassword.current')}>
          <Input
            id="current"
            type="password"
            required
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </Field>
        <Field id="new" label={t('account.changePassword.new')}>
          <Input
            id="new"
            type="password"
            required
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
        </Field>
        <Field id="confirm" label={t('account.changePassword.confirm')}>
          <Input
            id="confirm"
            type="password"
            required
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </Field>
        <div className="flex justify-end">
          <Button type="submit" loading={change.isPending}>
            {t('account.changePassword.submit')}
          </Button>
        </div>
      </form>
    </Card>
  )
}
