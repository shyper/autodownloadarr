import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useAddPassword, useDeletePassword, usePasswords } from '../../api/config'
import { errorMessage } from '../../i18n'
import { Alert, Button, Input, SectionTitle } from '../ui'

export function PasswordSettings() {
  const { t } = useTranslation()
  const list = usePasswords()
  const add = useAddPassword()
  const remove = useDeletePassword()
  const [pw, setPw] = useState('')

  const submit = (e: FormEvent) => {
    e.preventDefault()
    add.mutate(pw, { onSuccess: () => setPw('') })
  }

  return (
    <div className="space-y-4">
      <SectionTitle title={t('settings.passwords.title')} description={t('settings.passwords.description')} />
      {add.isError && <Alert>{errorMessage(t, add.error)}</Alert>}
      <form onSubmit={submit} className="flex gap-2">
        <Input aria-label={t('settings.passwords.new')} placeholder={t('settings.passwords.new')} value={pw} onChange={(e) => setPw(e.target.value)} />
        <Button type="submit" loading={add.isPending} disabled={!pw.trim()}>
          {t('common.add')}
        </Button>
      </form>
      {list.data && list.data.length > 0 && (
        <ul className="divide-y divide-line rounded-xl border border-line">
          {list.data.map((p) => (
            <li key={p.id} className="flex items-center gap-3 px-4 py-2">
              <code className="min-w-0 flex-1 truncate text-sm">{p.password}</code>
              <span className="text-xs text-muted">{t('settings.passwords.hits', { count: p.hits })}</span>
              <Button variant="ghost" onClick={() => remove.mutate(p.id)}>
                {t('common.delete')}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
