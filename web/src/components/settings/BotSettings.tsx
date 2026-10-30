import { NotificationSettings } from './NotificationSettings'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useBotConfig, useSaveBotConfig, useSyncBots, useTestBots, type BotConfig } from '../../api/integrations'
import { errorMessage } from '../../i18n'
import { Alert, Button, Checkbox, Field, Input, Select, Spinner } from '../ui'

export function BotSettings() {
  const config = useBotConfig()
  const { t } = useTranslation()
  if (config.isPending) return <Spinner />
  if (config.isError) return <Alert>{errorMessage(t, config.error)}</Alert>
  return <div className="space-y-8"><BotForm initial={config.data} /><div className="border-t border-line pt-6"><NotificationSettings provider="telegram" /></div></div>
}
function BotForm({ initial }: { initial: BotConfig }) {
  const { t } = useTranslation()
  const [value, setValue] = useState(initial)
  const save = useSaveBotConfig()
  const test = useTestBots()
  const sync = useSyncBots()
  const set = <K extends keyof BotConfig>(key: K, v: BotConfig[K]) => setValue((prev) => ({ ...prev, [key]: v }))
  const error = save.error ?? test.error ?? sync.error
  return <form className="space-y-6" onSubmit={(e) => { e.preventDefault(); save.mutate(value, { onSuccess: (data) => setValue(data) }) }}>
    <div><h2 className="font-semibold">{t('bots.settingsTitle')}</h2><p className="text-sm text-muted">{t('bots.settingsHint')}</p></div>
    {error && <Alert>{errorMessage(t, error)}</Alert>}
    {save.isSuccess && <Alert tone="success">{t('settings.saved')}</Alert>}
    {test.isSuccess && <Alert tone="success">{t('bots.testSuccess')}</Alert>}
    {sync.isSuccess && <Alert tone="success">{t('bots.syncSuccess')}</Alert>}
    <fieldset className="space-y-3 rounded-xl border border-line p-4">
      <legend className="px-2 font-medium">Telegram</legend>
      <Checkbox label={t('bots.telegramEnabled')} checked={value.telegramEnabled} onChange={(e) => set('telegramEnabled', e.target.checked)} />
      <Field id="telegram-bot-token" label={t('bots.token')} hint={t('bots.telegramHint')}><Input id="telegram-bot-token" type="password" autoComplete="off" placeholder={value.hasTelegramToken ? t('settings.unchanged') : ''} value={value.telegramToken ?? ''} onChange={(e) => set('telegramToken', e.target.value)} /></Field>
      <Field id="telegram-mode" label={t('bots.mode')}><Select id="telegram-mode" value={value.telegramMode} onChange={(e) => set('telegramMode', e.target.value as BotConfig['telegramMode'])}><option value="polling">{t('bots.polling')}</option><option value="webhook">{t('bots.webhook')}</option></Select></Field>
      {value.telegramWebhookUrl && value.telegramMode === 'webhook' && <p className="break-all font-mono text-xs">{value.telegramWebhookUrl}</p>}
    </fieldset>
    <fieldset className="space-y-3 rounded-xl border border-line p-4">
      <legend className="px-2 font-medium">Discord</legend>
      <Checkbox label={t('bots.discordEnabled')} checked={value.discordEnabled} onChange={(e) => set('discordEnabled', e.target.checked)} />
      <p className="text-sm text-muted">{t('bots.discordHint')}</p>
      <Field id="discord-app-id" label={t('bots.applicationId')}><Input id="discord-app-id" value={value.discordApplicationId} onChange={(e) => set('discordApplicationId', e.target.value)} /></Field>
      <Field id="discord-public-key" label={t('bots.publicKey')}><Input id="discord-public-key" value={value.discordPublicKey} onChange={(e) => set('discordPublicKey', e.target.value)} /></Field>
      <Field id="discord-bot-token" label={t('bots.token')}><Input id="discord-bot-token" type="password" autoComplete="off" placeholder={value.hasDiscordToken ? t('settings.unchanged') : ''} value={value.discordToken ?? ''} onChange={(e) => set('discordToken', e.target.value)} /></Field>
      <Field id="discord-guilds" label={t('bots.guilds')} hint={t('bots.guildsHint')}><Input id="discord-guilds" value={value.discordGuilds.join(', ')} onChange={(e) => set('discordGuilds', e.target.value.split(',').map((v) => v.trim()).filter(Boolean))} /></Field>
      {value.discordInteractionsUrl && <div className="text-sm"><p>{t('bots.interactionsUrl')}</p><p className="break-all font-mono text-xs">{value.discordInteractionsUrl}</p></div>}
      {value.discordApplicationId && <a className="text-sm text-accent hover:underline" href={`https://discord.com/oauth2/authorize?client_id=${encodeURIComponent(value.discordApplicationId)}&scope=bot%20applications.commands&permissions=0`}>{t('bots.installDiscord')}</a>}
    </fieldset>
    <p className="text-sm text-muted">{t('bots.syncHint')}</p>
    <div className="flex flex-wrap justify-end gap-2">
      <Button variant="secondary" type="button" loading={test.isPending} onClick={() => test.mutate()}>{t('bots.test')}</Button>
      <Button variant="secondary" type="button" loading={sync.isPending} onClick={() => sync.mutate()}>{t('bots.sync')}</Button>
      <Button type="submit" loading={save.isPending}>{t('common.save')}</Button>
    </div>
  </form>
}
