import { useTranslation } from 'react-i18next'
import type { DeleteTrigger } from '../api/types'
import { Field, Input, Select } from './ui'

/** Window size and delete trigger of a series. */
export function TriggerFields(props: {
  windowSize: number
  trigger: DeleteTrigger
  value: number
  onWindow: (n: number) => void
  onTrigger: (t: DeleteTrigger) => void
  onValue: (n: number) => void
}) {
  const { t } = useTranslation()
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <Field id="window" label={t('series.windowSize')} hint={t('series.windowSizeHint')}>
        <Input id="window" type="number" min={1} max={50} value={props.windowSize} onChange={(e) => props.onWindow(Number(e.target.value))} />
      </Field>
      <Field id="trigger" label={t('series.trigger')}>
        <Select id="trigger" value={props.trigger} onChange={(e) => props.onTrigger(e.target.value as DeleteTrigger)}>
          {(['progress', 'minutes', 'watched'] as const).map((tr) => (
            <option key={tr} value={tr}>
              {t(`series.triggers.${tr}`)}
            </option>
          ))}
        </Select>
      </Field>
      <Field id="trigger-value" label={t(`series.triggerValue.${props.trigger}`)}>
        <Input id="trigger-value" type="number" min={0} max={props.trigger === 'progress' ? 100 : 10000} value={props.value} onChange={(e) => props.onValue(Number(e.target.value))} />
      </Field>
    </div>
  )
}
