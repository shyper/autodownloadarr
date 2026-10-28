import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useSaveTMDB, useTMDB } from '../../api/config'
import { errorMessage } from '../../i18n'
import { Alert, Badge, Button, Field, Input, SectionTitle } from '../ui'

export function TmdbForm({ onSaved }: { onSaved?: () => void }) {
  const { t } = useTranslation()
  const status = useTMDB()
  const save = useSaveTMDB()
  const [key, setKey] = useState('')

  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(key.trim(), {
      onSuccess: () => {
        setKey('')
        onSaved?.()
      },
    })
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <SectionTitle
        title={t('settings.tmdb.title')}
        description={t('settings.tmdb.description')}
        actions={status.data?.hasKey && <Badge tone="ok">{t('settings.tmdb.configured')}</Badge>}
      />
      {save.isError && <Alert>{errorMessage(t, save.error)}</Alert>}
      {save.isSuccess && <Alert tone="success">{t('settings.tmdb.saved')}</Alert>}
      <Field id="tmdb-key" label={t('settings.tmdb.key')} hint={t('settings.tmdb.keyHint')}>
        <Input
          id="tmdb-key"
          type="password"
          autoComplete="off"
          required
          placeholder={status.data?.hasKey ? t('settings.unchanged') : ''}
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
      </Field>
      <div className="flex justify-end">
        <Button type="submit" loading={save.isPending} disabled={!key.trim()}>
          {t('settings.testAndSave')}
        </Button>
      </div>
    </form>
  )
}
