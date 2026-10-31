import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import {
  useCreateUser,
  useDeleteUser,
  useResetUserPassword,
  useUpdateUser,
  useUsers,
  useMe,
  type Role,
  type User,
} from '../../api/hooks'
import { errorMessage } from '../../i18n'
import { Alert, Button, Field, Input, SectionTitle, Select } from '../ui'

const roleLabel = (t: (key: string) => string, role: Role) => t(`account.roles.${role}`)

export function UserSettings() {
  const { t } = useTranslation()
  const list = useUsers()
  const create = useCreateUser()
  const update = useUpdateUser()
  const remove = useDeleteUser()
  const reset = useResetUserPassword()

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>('user')
  const [quotaRequests, setQuotaRequests] = useState(0)
  const [quotaGb, setQuotaGb] = useState(0)

  const error = create.error ?? update.error ?? remove.error ?? reset.error
  const submit = (e: FormEvent) => {
    e.preventDefault()
    create.mutate(
      { username, password, role, quotaRequests, quotaGb },
      {
        onSuccess: () => {
          setUsername('')
          setPassword('')
          setRole('user')
          setQuotaRequests(0)
          setQuotaGb(0)
        },
      },
    )
  }

  return (
    <div className="space-y-6">
      <SectionTitle title={t('settings.users.title')} description={t('settings.users.description')} />
      {error && <Alert>{errorMessage(t, error)}</Alert>}

      <form onSubmit={submit} className="grid gap-4 sm:grid-cols-2">
        <Field id="new-user-name" label={t('settings.users.username')}>
          <Input
            id="new-user-name"
            autoComplete="off"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </Field>
        <Field id="new-user-password" label={t('settings.users.password')} hint={t('settings.users.passwordHint')}>
          <Input
            id="new-user-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        <Field id="new-user-role" label={t('settings.users.role')}>
          <Select id="new-user-role" value={role} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="user">{roleLabel(t, 'user')}</option>
            <option value="admin">{roleLabel(t, 'admin')}</option>
          </Select>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field id="new-user-requests" label={t('settings.users.quotaRequests')} hint={t('settings.users.quotaHint')}>
            <Input
              id="new-user-requests"
              type="number"
              min={0}
              step={1}
              value={quotaRequests}
              onChange={(e) => setQuotaRequests(Math.max(0, Number(e.target.value) || 0))}
            />
          </Field>
          <Field id="new-user-gb" label={t('settings.users.quotaGb')} hint={t('settings.users.quotaHint')}>
            <Input
              id="new-user-gb"
              type="number"
              min={0}
              step={1}
              value={quotaGb}
              onChange={(e) => setQuotaGb(Math.max(0, Number(e.target.value) || 0))}
            />
          </Field>
        </div>
        <div className="sm:col-span-2">
          <Button type="submit" loading={create.isPending} disabled={!username.trim() || !password}>
            {t('settings.users.add')}
          </Button>
        </div>
      </form>

      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-fg">{t('settings.users.existing')}</h3>
        {list.isPending ? (
          <p className="text-sm text-muted">{t('common.loading')}</p>
        ) : (
          <ul className="divide-y divide-line rounded-xl border border-line">
            {list.data?.map((u) => (
              <UserRow key={`${u.id}-${u.role}-${u.quotaRequests}-${u.quotaGb}`} user={u} />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function UserRow({ user }: { user: User }) {
  const { t, i18n } = useTranslation()
  const me = useMe()
  const isSelf = me.data?.id === user.id
  const update = useUpdateUser()
  const remove = useDeleteUser()
  const reset = useResetUserPassword()

  const [role, setRole] = useState<Role>(user.role)
  const [quotaRequests, setQuotaRequests] = useState(user.quotaRequests)
  const [quotaGb, setQuotaGb] = useState(user.quotaGb)
  const [resetting, setResetting] = useState(false)
  const [newPassword, setNewPassword] = useState('')

  const save = () => update.mutate({ id: user.id, role, quotaRequests, quotaGb })
  const doReset = (e: FormEvent) => {
    e.preventDefault()
    reset.mutate(
      { id: user.id, newPassword },
      {
        onSuccess: () => {
          setResetting(false)
          setNewPassword('')
        },
      },
    )
  }
  const doDelete = () => {
    if (!me.data || isSelf) return
    if (!window.confirm(t('settings.users.deleteConfirm', { name: user.username }))) return
    remove.mutate(user.id)
  }
  const error = update.error ?? remove.error ?? reset.error

  return (
    <li className="space-y-3 px-4 py-3">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-40 flex-1">
          <p className="font-medium">{user.username}</p>
          <p className="text-xs text-muted">
            {user.lastLoginAt
              ? t('settings.users.lastLogin', { date: new Date(user.lastLoginAt).toLocaleString(i18n.resolvedLanguage) })
              : t('settings.users.neverSignedIn')}
          </p>
        </div>
        <div className="w-36">
          <label htmlFor={`role-${user.id}`} className="mb-1 block text-xs text-muted">
            {t('settings.users.role')}
          </label>
          <Select id={`role-${user.id}`} value={role} onChange={(e) => setRole(e.target.value as Role)}>
            <option value="user">{roleLabel(t, 'user')}</option>
            <option value="admin">{roleLabel(t, 'admin')}</option>
          </Select>
        </div>
        <div className="w-24">
          <label htmlFor={`requests-${user.id}`} className="mb-1 block text-xs text-muted">
            {t('settings.users.quotaRequests')}
          </label>
          <Input
            id={`requests-${user.id}`}
            type="number"
            min={0}
            step={1}
            value={quotaRequests}
            onChange={(e) => setQuotaRequests(Math.max(0, Number(e.target.value) || 0))}
          />
        </div>
        <div className="w-24">
          <label htmlFor={`gb-${user.id}`} className="mb-1 block text-xs text-muted">
            {t('settings.users.quotaGb')}
          </label>
          <Input
            id={`gb-${user.id}`}
            type="number"
            min={0}
            step={1}
            value={quotaGb}
            onChange={(e) => setQuotaGb(Math.max(0, Number(e.target.value) || 0))}
          />
        </div>
        <Button variant="secondary" loading={update.isPending} onClick={save}>
          {t('common.save')}
        </Button>
        <Button variant="ghost" onClick={() => setResetting((v) => !v)}>
          {t('settings.users.resetPassword')}
        </Button>
        <Button variant="ghost" loading={remove.isPending} disabled={!me.data || isSelf} title={isSelf ? t('settings.users.cannotDeleteSelf') : undefined} onClick={doDelete}>
          {t('common.delete')}
        </Button>
      </div>
      {resetting && (
        <form onSubmit={doReset} className="flex flex-wrap items-end gap-2 rounded-lg bg-surface-2 p-3">
          <Field id={`new-password-${user.id}`} label={t('settings.users.newPassword')}>
            <Input
              id={`new-password-${user.id}`}
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              required
            />
          </Field>
          <Button type="submit" loading={reset.isPending} disabled={!newPassword}>
            {t('settings.users.resetPassword')}
          </Button>
          <Button type="button" variant="ghost" onClick={() => setResetting(false)}>
            {t('common.cancel')}
          </Button>
        </form>
      )}
      {error && <Alert>{errorMessage(t, error)}</Alert>}
      {reset.isSuccess && !resetting && <Alert tone="success">{t('settings.users.resetDone')}</Alert>}
    </li>
  )
}
