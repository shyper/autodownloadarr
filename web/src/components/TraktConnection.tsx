import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router'
import { useConnectTrakt, useDisconnectTrakt, useSaveTrakt, useSyncTrakt, useTrakt, useTraktLists, type TraktSettings, type TraktStatus } from '../api/trakt'
import { errorMessage } from '../i18n'
import { Alert, Button, Card, Checkbox, Field, Input } from './ui'

export function TraktConnection() {
  const { t } = useTranslation()
  const q = useTrakt()
  const connect = useConnectTrakt()
  const disconnect = useDisconnectTrakt()
  const [search] = useSearchParams()
  const error = q.error ?? connect.error ?? disconnect.error
  return <Card className="space-y-4">
    <h2 className="font-semibold">{t('trakt.title')}</h2>
    <p className="text-sm text-muted">{t('trakt.hint')}</p>
    {error && <Alert>{errorMessage(t, error)}</Alert>}
    {search.get('trakt') === 'failed' && <Alert>{t('trakt.connectFailed')}</Alert>}
    {search.get('trakt') === 'connected' && q.data?.connected && <Alert tone="success">{t('trakt.connectSuccess')}</Alert>}
    {q.data && !q.data.configured && <Alert>{t('trakt.notConfigured')}</Alert>}
    {q.data?.connected ? <>
      <p className="text-sm font-medium">{t('trakt.connectedAs', { name: q.data.username })}</p>
      <ImportForm key={q.data.username} initial={q.data} />
    </> : q.data?.configured && <>
      {q.data.linked && <Alert>{t('trakt.reconnectHint')}</Alert>}
      <Button loading={connect.isPending} onClick={() => connect.mutate()}>{t(q.data.linked ? 'trakt.reconnect' : 'trakt.connect')}</Button>
    </>}
    {q.data?.connected && <Button variant="ghost" loading={connect.isPending} onClick={() => connect.mutate()}>{t('trakt.reconnect')}</Button>}
    {q.data?.linked && <Button variant="ghost" loading={disconnect.isPending} onClick={() => disconnect.mutate()}>{t('trakt.disconnect')}</Button>}
  </Card>
}
function ImportForm({ initial }: { initial: TraktStatus }) {
  const { t, i18n } = useTranslation()
  const [n, setN] = useState<TraktSettings>(() => ({ enabled: initial.enabled, watchlist: initial.watchlist, listIds: initial.listIds, pollMinutes: initial.pollMinutes }))
  const lists = useTraktLists(true)
  const save = useSaveTrakt()
  const sync = useSyncTrakt()
  const set = <K extends keyof TraktSettings>(key: K, value: TraktSettings[K]) => setN((prev) => ({ ...prev, [key]: value }))
  const toggle = (id: number, enabled: boolean) => setN((prev) => ({ ...prev, listIds: enabled ? [...prev.listIds.filter((x) => x !== id), id] : prev.listIds.filter((x) => x !== id) }))
  const dirty = n.enabled !== initial.enabled || n.watchlist !== initial.watchlist || n.pollMinutes !== initial.pollMinutes || [...n.listIds].sort().join(',') !== [...initial.listIds].sort().join(',')
  const submit = (e: FormEvent) => { e.preventDefault(); save.mutate(n) }
  const error = lists.error ?? save.error ?? sync.error
  const missing = n.listIds.filter((id) => lists.data && !lists.data.some((list) => list.ids.trakt === id))
  return <form onSubmit={submit} className="space-y-4">
    {error && <Alert>{errorMessage(t, error)}</Alert>}
    {save.isSuccess && !dirty && <Alert tone="success">{t('settings.saved')}</Alert>}
    <Checkbox label={t('trakt.enabled')} checked={n.enabled} onChange={(e) => set('enabled', e.target.checked)} />
    <Checkbox label={t('trakt.watchlist')} checked={n.watchlist} onChange={(e) => set('watchlist', e.target.checked)} />
    <fieldset className="space-y-3">
      <legend className="text-sm font-medium">{t('trakt.lists')}</legend>
      <p className="text-xs text-muted">{t('trakt.listsHint')}</p>
      {lists.data?.length === 0 && <p className="text-sm text-muted">{t('trakt.noLists')}</p>}
      {lists.data?.map((list) => <Checkbox key={list.ids.trakt} label={list.name} hint={t('trakt.itemCount', { count: list.item_count })} checked={n.listIds.includes(list.ids.trakt)} onChange={(e) => toggle(list.ids.trakt, e.target.checked)} />)}
      {missing.map((id) => <Checkbox key={id} label={t('trakt.missingList', { id })} checked onChange={(e) => toggle(id, e.target.checked)} />)}
      <Button type="button" variant="ghost" loading={lists.isFetching} onClick={() => { void lists.refetch() }}>{t('trakt.refreshLists')}</Button>
    </fieldset>
    <Field id="trakt-interval" label={t('trakt.interval')} hint={t('trakt.intervalHint')}>
      <Input id="trakt-interval" type="number" min={1} max={10080} required value={n.pollMinutes} onChange={(e) => set('pollMinutes', Number(e.target.value))} />
    </Field>
    <p className="text-xs text-muted">{t('trakt.importHint')}</p>
    {initial.lastSyncAt > 0 && <div className="space-y-1 text-sm text-muted">
      <p>{t('trakt.lastSync', { date: new Date(initial.lastSyncAt).toLocaleString(i18n.resolvedLanguage) })}</p>
      <p>{t('trakt.summary', { requested: initial.requested, skipped: initial.skipped, failed: initial.failed })}</p>
      {initial.lastError && <Alert>{t('trakt.syncFailed', { reason: t(`errors.${initial.lastError}`) })}</Alert>}
    </div>}
    {initial.syncing && <p className="text-sm text-muted" role="status">{t('trakt.syncing')}</p>}
    {dirty && <p className="text-xs text-muted">{t('trakt.saveFirst')}</p>}
    <div className="flex flex-wrap justify-end gap-2">
      <Button type="button" variant="secondary" loading={sync.isPending} disabled={initial.syncing || dirty || !initial.enabled || (!initial.watchlist && initial.listIds.length === 0)} onClick={() => sync.mutate()}>{t('trakt.syncNow')}</Button>
      <Button type="submit" loading={save.isPending}>{t('common.save')}</Button>
    </div>
  </form>
}
