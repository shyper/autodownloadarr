import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useDeleteUsenetServer, useSaveUsenetServer, useTestUsenetServer, useUsenetServers } from '../../api/config'
import type { UsenetServer } from '../../api/types'
import { errorMessage } from '../../i18n'
import { Alert, Badge, Button, Checkbox, Field, Input, SectionTitle, Select } from '../ui'

const empty: Partial<UsenetServer> = {
  name: '',
  host: '',
  port: 563,
  tls: true,
  username: '',
  password: '',
  connections: 20,
  priority: 0,
  enabled: true,
  fullCheck: false,
}

export function UsenetSettings() {
  const { t } = useTranslation()
  const servers = useUsenetServers()
  const remove = useDeleteUsenetServer()
  const [editing, setEditing] = useState<Partial<UsenetServer> | null>(null)
  const list = servers.data ?? []

  return (
    <div className="space-y-4">
      <SectionTitle
        title={t('settings.usenet.title')}
        description={t('settings.usenet.description')}
        actions={
          !editing && (
            <Button variant="secondary" onClick={() => setEditing({ ...empty, priority: list.length > 0 ? 10 : 0 })}>
              {t('settings.usenet.add')}
            </Button>
          )
        }
      />
      {list.length === 0 && !editing && <p className="text-sm text-muted">{t('settings.usenet.empty')}</p>}
      {list.length > 0 && (
      <ul className="divide-y divide-line rounded-xl border border-line">
        {list.map((s) => (
          <li key={s.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
            <div className="min-w-0 flex-1">
              <p className="font-medium">{s.name}</p>
              <p className="truncate text-sm text-muted">
                {s.host}:{s.port} · {t('settings.usenet.connectionsCount', { count: s.connections })}
              </p>
            </div>
            <Badge tone={s.priority === 0 ? 'accent' : 'muted'}>
              {s.priority === 0 ? t('settings.usenet.primary') : t('settings.usenet.backup')}
            </Badge>
            {!s.enabled && <Badge tone="muted">{t('settings.disabled')}</Badge>}
            <Button variant="ghost" onClick={() => setEditing({ ...s, password: '' })}>
              {t('common.edit')}
            </Button>
            <Button variant="ghost" onClick={() => remove.mutate(s.id)}>
              {t('common.delete')}
            </Button>
          </li>
        ))}
      </ul>
      )}
      {editing && <UsenetServerForm initial={editing} onDone={() => setEditing(null)} />}
    </div>
  )
}

function UsenetServerForm({ initial, onDone }: { initial: Partial<UsenetServer>; onDone: () => void }) {
  const { t } = useTranslation()
  const save = useSaveUsenetServer()
  const test = useTestUsenetServer()
  const [u, setU] = useState(initial)
  const set = <K extends keyof UsenetServer>(k: K, v: UsenetServer[K]) => {
    test.reset()
    setU((prev) => ({ ...prev, [k]: v }))
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(u, { onSuccess: onDone })
  }

  return (
    <form onSubmit={submit} className="space-y-4 rounded-xl border border-line bg-surface-2 p-4">
      {(save.isError || test.isError) && <Alert>{errorMessage(t, save.error ?? test.error)}</Alert>}
      {test.isSuccess && <Alert tone="success">{t('settings.usenet.testOk')}</Alert>}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="us-host" label={t('settings.usenet.host')}>
          <Input id="us-host" required placeholder="news.example.com" value={u.host} onChange={(e) => set('host', e.target.value)} />
        </Field>
        <Field id="us-port" label={t('settings.usenet.port')}>
          <Input id="us-port" type="number" min={1} max={65535} value={u.port} onChange={(e) => set('port', Number(e.target.value))} />
        </Field>
        <Field id="us-user" label={t('settings.usenet.username')}>
          <Input id="us-user" autoComplete="off" value={u.username} onChange={(e) => set('username', e.target.value)} />
        </Field>
        <Field id="us-pass" label={t('settings.usenet.password')}>
          <Input
            id="us-pass"
            type="password"
            autoComplete="new-password"
            placeholder={u.hasPassword ? t('settings.unchanged') : ''}
            value={u.password}
            onChange={(e) => set('password', e.target.value)}
          />
        </Field>
        <Field id="us-conns" label={t('settings.usenet.connections')} hint={t('settings.usenet.connectionsHint')}>
          <Input id="us-conns" type="number" min={1} max={100} value={u.connections} onChange={(e) => set('connections', Number(e.target.value))} />
        </Field>
        <Field id="us-prio" label={t('settings.usenet.role')}>
          <Select id="us-prio" value={u.priority === 0 ? '0' : '10'} onChange={(e) => set('priority', Number(e.target.value))}>
            <option value="0">{t('settings.usenet.primary')}</option>
            <option value="10">{t('settings.usenet.backup')}</option>
          </Select>
        </Field>
        <Field id="us-name" label={t('settings.usenet.name')}>
          <Input id="us-name" placeholder={u.host} value={u.name} onChange={(e) => set('name', e.target.value)} />
        </Field>
      </div>
      <div className="flex flex-wrap gap-x-6 gap-y-2">
        <Checkbox
          label={t('settings.usenet.tls')}
          checked={u.tls}
          onChange={(e) => {
            set('tls', e.target.checked)
            if (u.port === 563 || u.port === 119) set('port', e.target.checked ? 563 : 119)
          }}
        />
        <Checkbox label={t('settings.enabled')} checked={u.enabled} onChange={(e) => set('enabled', e.target.checked)} />
        <Checkbox
          label={t('settings.usenet.fullCheck')}
          hint={t('settings.usenet.fullCheckHint')}
          checked={u.fullCheck ?? false}
          onChange={(e) => set('fullCheck', e.target.checked)}
        />
      </div>
      <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onDone}>
          {t('common.cancel')}
        </Button>
        <Button type="button" variant="secondary" loading={test.isPending} onClick={() => test.mutate(u)}>
          {t('settings.test')}
        </Button>
        <Button type="submit" loading={save.isPending}>
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}
