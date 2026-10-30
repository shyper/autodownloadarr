import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useGeneral, useSaveGeneral } from '../../api/config'
import type { General } from '../../api/types'
import { errorMessage } from '../../i18n'
import { TriggerFields } from '../TriggerFields'
import { Alert, Button, Field, Input, SectionTitle, Select } from '../ui'

type Props = {
  /** "library" shows only the movies folder (setup wizard). */
  fields?: 'all' | 'library'
  onSaved?: () => void
}

export function GeneralForm({ fields = 'all', onSaved }: Props) {
  const general = useGeneral()
  if (!general.data) return null
  return <GeneralFormInner initial={general.data} fields={fields} onSaved={onSaved} />
}

function GeneralFormInner({ initial, fields, onSaved }: Props & { initial: General }) {
  const { t } = useTranslation()
  const save = useSaveGeneral()
  const [g, setG] = useState(initial)
  const set = <K extends keyof General>(k: K, v: General[K]) => setG((prev) => ({ ...prev, [k]: v }))
  const num = (k: keyof General) => (e: React.ChangeEvent<HTMLInputElement>) => set(k, Number(e.target.value) as never)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(g, { onSuccess: () => onSaved?.() })
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      <SectionTitle title={t('settings.general.title')} description={t('settings.general.description')} />
      {save.isError && <Alert>{errorMessage(t, save.error)}</Alert>}
      {save.isSuccess && <Alert tone="success">{t('settings.saved')}</Alert>}
      <Field id="movies-path" label={t('settings.general.moviesPath')} hint={t('settings.general.moviesPathHint')}>
        <Input
          id="movies-path"
          required
          placeholder="/movies"
          className="font-mono"
          value={g.moviesPath}
          onChange={(e) => set('moviesPath', e.target.value)}
        />
      </Field>
      <Field id="series-path" label={t('settings.general.seriesPath')} hint={t('settings.general.seriesPathHint')}>
        <Input
          id="series-path"
          placeholder="/series"
          className="font-mono"
          value={g.seriesPath}
          onChange={(e) => set('seriesPath', e.target.value)}
        />
      </Field>
      {fields === 'all' && (
        <fieldset className="space-y-3">
          <legend className="text-sm font-medium">{t('settings.general.seriesDefaults')}</legend>
          <TriggerFields
            windowSize={g.seriesWindow}
            trigger={g.seriesTrigger}
            value={g.seriesTriggerValue}
            onWindow={(n) => set('seriesWindow', n)}
            onTrigger={(tr) => set('seriesTrigger', tr)}
            onValue={(n) => set('seriesTriggerValue', n)}
          />
        </fieldset>
      )}
      {fields === 'all' && (
        <>
          <div className="grid gap-5 sm:grid-cols-2">
            <Field id="delete-after" label={t('settings.general.deleteAfterHours')} hint={t('settings.general.deleteAfterHoursHint')}>
              <Input id="delete-after" type="number" min={0} value={g.deleteAfterHours} onChange={num('deleteAfterHours')} />
            </Field>
            <Field id="expiry" label={t('settings.general.unwatchedExpiryDays')} hint={t('settings.general.unwatchedExpiryDaysHint')}>
              <Input id="expiry" type="number" min={0} value={g.unwatchedExpiryDays} onChange={num('unwatchedExpiryDays')} />
            </Field>
            <Field id="started-expiry" label={t('settings.general.startedExpiryDays')} hint={t('settings.general.startedExpiryDaysHint')}>
              <Input id="started-expiry" type="number" min={0} value={g.startedExpiryDays} onChange={num('startedExpiryDays')} />
            </Field>
            <Field id="max-sources" label={t('settings.general.maxSources')} hint={t('settings.general.maxSourcesHint')}>
              <Input id="max-sources" type="number" min={1} value={g.maxSources} onChange={num('maxSources')} />
            </Field>
            <Field id="parallel" label={t('settings.general.parallelDownloads')}>
              <Input id="parallel" type="number" min={1} max={5} value={g.parallelDownloads} onChange={num('parallelDownloads')} />
            </Field>
            <Field id="poll" label={t('settings.general.watchPollMinutes')} hint={t('settings.general.watchPollMinutesHint')}>
              <Input id="poll" type="number" min={1} value={g.watchPollMinutes} onChange={num('watchPollMinutes')} />
            </Field>
            <Field id="max-rate" label={t('settings.general.maxDownloadMBps')} hint={t('settings.general.maxDownloadMBpsHint')}>
              <Input id="max-rate" type="number" min={0} step="1" value={g.maxDownloadMBps} onChange={num('maxDownloadMBps')} />
            </Field>
            <Field id="max-library" label={t('settings.general.maxLibraryGB')} hint={t('settings.general.maxLibraryGBHint')}>
              <Input id="max-library" type="number" min={0} step="1" value={g.maxLibraryGB} onChange={num('maxLibraryGB')} />
            </Field>
            <Field id="min-free" label={t('settings.general.minFreeGB')} hint={t('settings.general.minFreeGBHint')}>
              <Input id="min-free" type="number" min={0} step="1" value={g.minFreeGB} onChange={num('minFreeGB')} />
            </Field>
            <Field id="language" label={t('settings.general.language')} hint={t('settings.general.languageHint')}>
              <Select id="language" value={g.language} onChange={(e) => set('language', e.target.value)}>
                <option value="de-DE">Deutsch</option>
                <option value="en-US">English</option>
              </Select>
            </Field>
          </div>
        </>
      )}
      <div className="flex justify-end">
        <Button type="submit" loading={save.isPending}>
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}
