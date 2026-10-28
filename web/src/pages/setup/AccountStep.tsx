import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useSetup } from '../../api/hooks'
import { Alert, Button, Field, Input } from '../../components/ui'
import { errorMessage } from '../../i18n'

const minPasswordLength = 8

export function AccountStep({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation()
  const setup = useSetup()
  const [token, setToken] = useState('')
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (password.length < minPasswordLength) return setLocalError(t('setup.errors.passwordTooShort'))
    if (password !== confirm) return setLocalError(t('setup.errors.passwordMismatch'))
    setLocalError(null)
    setup.mutate({ token: token.trim(), username: username.trim(), password }, { onSuccess: onDone })
  }

  const error = localError ?? (setup.isError ? errorMessage(t, setup.error) : null)

  return (
    <form onSubmit={submit} className="space-y-5">
      <div className="space-y-1">
        <h2 className="text-lg font-semibold">{t('setup.steps.account.title')}</h2>
        <p className="text-sm text-muted">{t('setup.steps.account.description')}</p>
      </div>
      {error && <Alert>{error}</Alert>}
      <Field id="token" label={t('setup.steps.account.token')} hint={t('setup.steps.account.tokenHint')}>
        <Input
          id="token"
          required
          autoFocus
          autoComplete="off"
          spellCheck={false}
          placeholder="XXXX-XXXX-XXXX"
          className="font-mono tracking-wider uppercase"
          aria-describedby="token-hint"
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
      </Field>
      <Field id="username" label={t('setup.steps.account.username')} hint={t('setup.steps.account.usernameHint')}>
        <Input
          id="username"
          required
          autoComplete="username"
          maxLength={64}
          aria-describedby="username-hint"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
      </Field>
      <div className="grid gap-5 sm:grid-cols-2">
        <Field id="password" label={t('setup.steps.account.password')} hint={t('setup.steps.account.passwordHint')}>
          <Input
            id="password"
            type="password"
            required
            autoComplete="new-password"
            aria-describedby="password-hint"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <Field id="confirm" label={t('setup.steps.account.confirm')}>
          <Input
            id="confirm"
            type="password"
            required
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </Field>
      </div>
      <div className="flex justify-end pt-2">
        <Button type="submit" loading={setup.isPending}>
          {t('setup.steps.account.submit')}
        </Button>
      </div>
    </form>
  )
}
