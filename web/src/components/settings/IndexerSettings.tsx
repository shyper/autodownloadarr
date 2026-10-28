import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useDeleteIndexer, useIndexers, useSaveIndexer, useTestIndexer } from '../../api/config'
import type { Category, Indexer } from '../../api/types'
import { errorMessage } from '../../i18n'
import { Alert, Badge, Button, Checkbox, Field, Input, SectionTitle } from '../ui'

const empty: Partial<Indexer> = { name: '', url: '', apiKey: '', movieCategories: [2000], tvCategories: [5000], priority: 0, enabled: true }

export function IndexerSettings() {
  const { t } = useTranslation()
  const indexers = useIndexers()
  const remove = useDeleteIndexer()
  const [editing, setEditing] = useState<Partial<Indexer> | null>(null)
  const list = indexers.data ?? []

  return (
    <div className="space-y-4">
      <SectionTitle
        title={t('settings.indexers.title')}
        description={t('settings.indexers.description')}
        actions={
          !editing && (
            <Button variant="secondary" onClick={() => setEditing({ ...empty })}>
              {t('settings.indexers.add')}
            </Button>
          )
        }
      />
      {list.length === 0 && !editing && <p className="text-sm text-muted">{t('settings.indexers.empty')}</p>}
      {list.length > 0 && (
      <ul className="divide-y divide-line rounded-xl border border-line">
        {list.map((ix) => (
          <li key={ix.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
            <div className="min-w-0 flex-1">
              <p className="font-medium">{ix.name}</p>
              <p className="truncate font-mono text-xs text-muted">{ix.url}</p>
            </div>
            {!ix.enabled && <Badge tone="muted">{t('settings.disabled')}</Badge>}
            <Button variant="ghost" onClick={() => setEditing({ ...ix, apiKey: '' })}>
              {t('common.edit')}
            </Button>
            <Button variant="ghost" onClick={() => remove.mutate(ix.id)}>
              {t('common.delete')}
            </Button>
          </li>
        ))}
      </ul>
      )}
      {editing && <IndexerForm initial={editing} onDone={() => setEditing(null)} />}
    </div>
  )
}

function IndexerForm({ initial, onDone }: { initial: Partial<Indexer>; onDone: () => void }) {
  const { t } = useTranslation()
  const save = useSaveIndexer()
  const test = useTestIndexer()
  const [ix, setIx] = useState(initial)
  const set = <K extends keyof Indexer>(k: K, v: Indexer[K]) => setIx((prev) => ({ ...prev, [k]: v }))

  const movieCategories: Category[] = (test.data?.categories ?? []).filter((c) => c.id >= 2000 && c.id < 3000)
  const tvCategories: Category[] = (test.data?.categories ?? []).filter((c) => c.id >= 5000 && c.id < 6000)
  const toggle = (key: 'movieCategories' | 'tvCategories', id: number, on: boolean) => {
    const cur = ix[key] ?? []
    set(key, on ? [...cur, id] : cur.filter((c) => c !== id))
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(ix, { onSuccess: onDone })
  }

  return (
    <form onSubmit={submit} className="space-y-4 rounded-xl border border-line bg-surface-2 p-4">
      {(save.isError || test.isError) && <Alert>{errorMessage(t, save.error ?? test.error)}</Alert>}
      {test.isSuccess && (
        <Alert tone="success">
          {test.data.movieSearch
            ? t('settings.indexers.testOk', { params: test.data.movieParams.join(', ') })
            : t('settings.indexers.testOkTextOnly')}
        </Alert>
      )}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="ix-url" label={t('settings.indexers.url')} hint={t('settings.indexers.urlHint')}>
          <Input id="ix-url" required placeholder="https://api.example.com" value={ix.url} onChange={(e) => set('url', e.target.value)} />
        </Field>
        <Field id="ix-key" label={t('settings.indexers.apiKey')}>
          <Input
            id="ix-key"
            type="password"
            autoComplete="off"
            placeholder={ix.hasApiKey ? t('settings.unchanged') : ''}
            value={ix.apiKey}
            onChange={(e) => set('apiKey', e.target.value)}
          />
        </Field>
        <Field id="ix-name" label={t('settings.indexers.name')}>
          <Input id="ix-name" value={ix.name} onChange={(e) => set('name', e.target.value)} />
        </Field>
      </div>
      {([
        ['movieCategories', 'settings.indexers.categories', movieCategories],
        ['tvCategories', 'settings.indexers.tvCategories', tvCategories],
      ] as const).map(([key, label, cats]) =>
        cats.length > 0 ? (
          <fieldset key={key} className="space-y-2">
            <legend className="text-sm font-medium">{t(label)}</legend>
            <div className="grid gap-2 sm:grid-cols-2">
              {cats.flatMap((c) => [c, ...(c.subs ?? [])]).map((c) => (
                <Checkbox
                  key={c.id}
                  label={`${c.name} (${c.id})`}
                  checked={ix[key]?.includes(c.id) ?? false}
                  onChange={(e) => toggle(key, c.id, e.target.checked)}
                />
              ))}
            </div>
          </fieldset>
        ) : null,
      )}
      <Checkbox label={t('settings.enabled')} checked={ix.enabled} onChange={(e) => set('enabled', e.target.checked)} />
      <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onDone}>
          {t('common.cancel')}
        </Button>
        <Button type="button" variant="secondary" loading={test.isPending} onClick={() => test.mutate(ix)}>
          {t('settings.test')}
        </Button>
        <Button type="submit" loading={save.isPending}>
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}
