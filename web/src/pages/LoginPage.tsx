import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useLogin } from '../api/hooks'
import { LanguageSelect } from '../components/LanguageSelect'
import { Alert, Button, Card, Field, Input, Logo } from '../components/ui'
import { errorMessage } from '../i18n'

export function LoginPage() {
  const { t } = useTranslation()
  const login = useLogin()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')

  const submit = (e: FormEvent) => {
    e.preventDefault()
    // RequireGuest redirects once the user is known.
    login.mutate({ username, password })
  }

  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-6 px-4 py-10">
      <div className="flex flex-col items-center gap-3 text-center">
        <Logo className="size-12" />
        <div>
          <h1 className="text-2xl font-semibold">{t('login.title')}</h1>
          <p className="text-sm text-muted">{t('login.subtitle')}</p>
        </div>
      </div>
      <Card className="w-full max-w-sm">
        <form onSubmit={submit} className="space-y-4">
          {login.isError && <Alert>{errorMessage(t, login.error)}</Alert>}
          <Field id="username" label={t('login.username')}>
            <Input
              id="username"
              autoComplete="username"
              autoFocus
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field id="password" label={t('login.password')}>
            <Input
              id="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Button type="submit" className="w-full" loading={login.isPending}>
            {t('login.submit')}
          </Button>
        </form>
      </Card>
      <LanguageSelect />
    </div>
  )
}
