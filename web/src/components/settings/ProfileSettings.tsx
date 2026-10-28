import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useProfiles, useSaveProfile } from '../../api/config'
import type { Profile, ProfileConfig, WordScore } from '../../api/types'
import { splitList } from '../../format'
import { errorMessage } from '../../i18n'
import { Alert, Button, Checkbox, Field, Input, SectionTitle, Select } from '../ui'

const resolutions = [0, 480, 576, 720, 1080, 2160]

export function ProfileSettings() {
  const profiles = useProfiles()
  const profile = profiles.data?.find((p) => p.default) ?? profiles.data?.[0]
  if (!profile) return null
  return <ProfileForm key={profile.id} profile={profile} />
}

function wordsToText(ws: WordScore[]) {
  return ws.map((w) => `${w.term}=${w.score}`).join(', ')
}

function textToWords(s: string): WordScore[] {
  return splitList(s).map((part) => {
    const [term = '', score] = part.split('=')
    return { term: term.trim(), score: Number(score) || 10 }
  })
}

function ProfileForm({ profile }: { profile: Profile }) {
  const { t } = useTranslation()
  const save = useSaveProfile()
  const [c, setC] = useState<ProfileConfig>(profile.config)
  const [required, setRequired] = useState(c.requiredWords.join(', '))
  const [forbidden, setForbidden] = useState(c.forbiddenWords.join(', '))
  const [preferred, setPreferred] = useState(wordsToText(c.preferredWords))
  const set = <K extends keyof ProfileConfig>(k: K, v: ProfileConfig[K]) => setC((prev) => ({ ...prev, [k]: v }))
  const resLabel = (r: number) => (r === 0 ? t('settings.profile.any') : `${r}p`)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate({
      ...profile,
      config: { ...c, requiredWords: splitList(required), forbiddenWords: splitList(forbidden), preferredWords: textToWords(preferred) },
    })
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      <SectionTitle title={t('settings.profile.title')} description={t('settings.profile.description')} />
      {save.isError && <Alert>{errorMessage(t, save.error)}</Alert>}
      {save.isSuccess && <Alert tone="success">{t('settings.saved')}</Alert>}
      <div className="grid gap-5 sm:grid-cols-2">
        <Field id="max-res" label={t('settings.profile.maxResolution')}>
          <Select id="max-res" value={c.maxResolution} onChange={(e) => set('maxResolution', Number(e.target.value))}>
            {resolutions.map((r) => (
              <option key={r} value={r}>
                {resLabel(r)}
              </option>
            ))}
          </Select>
        </Field>
        <Field id="min-res" label={t('settings.profile.minResolution')}>
          <Select id="min-res" value={c.minResolution} onChange={(e) => set('minResolution', Number(e.target.value))}>
            {resolutions.map((r) => (
              <option key={r} value={r}>
                {resLabel(r)}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <Field id="required" label={t('settings.profile.requiredWords')} hint={t('settings.profile.requiredWordsHint')}>
        <Input id="required" placeholder="German" value={required} onChange={(e) => setRequired(e.target.value)} />
      </Field>
      <Field id="required-mode" label={t('settings.profile.requiredMode')}>
        <Select id="required-mode" value={c.requiredMode} onChange={(e) => set('requiredMode', e.target.value as ProfileConfig['requiredMode'])}>
          <option value="any">{t('settings.profile.modeAny')}</option>
          <option value="all">{t('settings.profile.modeAll')}</option>
        </Select>
      </Field>
      <Field id="forbidden" label={t('settings.profile.forbiddenWords')} hint={t('settings.profile.forbiddenWordsHint')}>
        <Input id="forbidden" placeholder="LD, MD, Subbed" value={forbidden} onChange={(e) => setForbidden(e.target.value)} />
      </Field>
      <Field id="preferred" label={t('settings.profile.preferredWords')} hint={t('settings.profile.preferredWordsHint')}>
        <Input id="preferred" placeholder="DL=100, x265=20" value={preferred} onChange={(e) => setPreferred(e.target.value)} />
      </Field>
      <div className="grid gap-5 sm:grid-cols-3">
        <Field id="min-mb" label={t('settings.profile.minMb')}>
          <Input id="min-mb" type="number" min={0} step="0.5" value={c.minMbPerMinute} onChange={(e) => set('minMbPerMinute', Number(e.target.value))} />
        </Field>
        <Field id="max-mb" label={t('settings.profile.maxMb')}>
          <Input id="max-mb" type="number" min={0} step="1" value={c.maxMbPerMinute} onChange={(e) => set('maxMbPerMinute', Number(e.target.value))} />
        </Field>
        <Field id="codec" label={t('settings.profile.codec')}>
          <Select id="codec" value={c.preferredCodec} onChange={(e) => set('preferredCodec', e.target.value)}>
            <option value="">{t('settings.profile.any')}</option>
            <option value="h264">H.264</option>
            <option value="h265">H.265 / HEVC</option>
            <option value="av1">AV1</option>
          </Select>
        </Field>
      </div>
      <Field id="pw-policy" label={t('settings.profile.passwordPolicy')}>
        <Select id="pw-policy" value={c.passwordPolicy} onChange={(e) => set('passwordPolicy', e.target.value as ProfileConfig['passwordPolicy'])}>
          {(['allow', 'known', 'avoid', 'reject'] as const).map((p) => (
            <option key={p} value={p}>
              {t(`settings.profile.password.${p}`)}
            </option>
          ))}
        </Select>
      </Field>
      <Checkbox
        label={t('settings.profile.allowLowQuality')}
        hint={t('settings.profile.allowLowQualityHint')}
        checked={c.allowLowQuality}
        onChange={(e) => set('allowLowQuality', e.target.checked)}
      />
      <div className="flex justify-end">
        <Button type="submit" loading={save.isPending}>
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}
