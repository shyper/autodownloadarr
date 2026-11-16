import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMediaProfiles, usePlex, useRefreshMediaProfiles, useUpdateMediaProfile } from '../../api/config'
import { useUsers, type User } from '../../api/hooks'
import type { MediaProfile } from '../../api/types'
import { ApiError } from '../../api/client'
import { errorMessage } from '../../i18n'
import { Alert, Badge, Button, Checkbox, Input, SectionTitle, Select, Spinner } from '../ui'

/** Assigns the media server's profiles (Plex Home, Jellyfin/Emby users) to users. */
export function MediaProfiles() {
  const { t } = useTranslation()
  const plex = usePlex()
  const configured = plex.data?.configured === true
  const profiles = useMediaProfiles(configured)
  const users = useUsers(configured)
  const refresh = useRefreshMediaProfiles()
  if (!plex.data?.configured) return null
  const isPlex = plex.data.server.kind === 'plex'

  return (
    <div className="space-y-4">
      <SectionTitle
        title={t('settings.mediaProfiles.title')}
        description={t(isPlex ? 'settings.mediaProfiles.descriptionPlex' : 'settings.mediaProfiles.description')}
        actions={
          <Button variant="secondary" loading={refresh.isPending} onClick={() => refresh.mutate()}>
            {t('settings.mediaProfiles.refresh')}
          </Button>
        }
      />
      {refresh.isError && <Alert>{errorMessage(t, refresh.error)}</Alert>}
      {profiles.isPending ? (
        <Spinner />
      ) : profiles.data && profiles.data.length > 0 ? (
        <ul className="divide-y divide-line rounded-xl border border-line">
          {profiles.data.map((p) => (
            <ProfileRow key={`${p.id}-${p.userId}-${p.watchlist}`} profile={p} users={users.data ?? []} all={profiles.data} isPlex={isPlex} />
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted">{t('settings.mediaProfiles.empty')}</p>
      )}
    </div>
  )
}

function ProfileRow({ profile, users, all, isPlex }: { profile: MediaProfile; users: User[]; all: MediaProfile[]; isPlex: boolean }) {
  const { t } = useTranslation()
  const update = useUpdateMediaProfile()
  const [userId, setUserId] = useState(profile.userId)
  const [watchlist, setWatchlist] = useState(profile.watchlist)
  const [pin, setPin] = useState('')
  const taken = new Set(all.filter((p) => p.id !== profile.id && p.userId > 0).map((p) => p.userId))
  const pinRequired = update.error instanceof ApiError && update.error.code === 'pin_required'
  const needsPin = isPlex && profile.protected && !profile.owner && userId > 0 && (!profile.hasToken || pinRequired)
  const dirty = userId !== profile.userId || watchlist !== profile.watchlist || pin !== ''

  const save = () =>
    update.mutate({ id: profile.id, userId, watchlist: userId > 0 && watchlist, pin: pin || undefined }, { onSuccess: () => setPin('') })

  return (
    <li className="space-y-3 px-4 py-3">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-40 flex-1">
          <p className="flex flex-wrap items-center gap-2 font-medium">
            {profile.name}
            {profile.owner && <Badge tone="muted">{t('settings.mediaProfiles.owner')}</Badge>}
            {profile.protected && <Badge tone="muted">{t('settings.mediaProfiles.protected')}</Badge>}
          </p>
          {profile.owner && <p className="text-xs text-muted">{t('settings.mediaProfiles.ownerHint')}</p>}
        </div>
        <div className="w-44">
          <label htmlFor={`media-profile-user-${profile.id}`} className="mb-1 block text-xs text-muted">
            {t('settings.mediaProfiles.user')}
          </label>
          <Select id={`media-profile-user-${profile.id}`} value={userId} onChange={(e) => setUserId(Number(e.target.value))}>
            <option value={0}>{t('settings.mediaProfiles.unassigned')}</option>
            {users.map((u) => (
              <option key={u.id} value={u.id} disabled={taken.has(u.id)}>
                {u.username}
              </option>
            ))}
          </Select>
        </div>
        {needsPin && (
          <div className="w-28">
            <label htmlFor={`media-profile-pin-${profile.id}`} className="mb-1 block text-xs text-muted">
              {t('settings.mediaProfiles.pin')}
            </label>
            <Input
              id={`media-profile-pin-${profile.id}`}
              type="password"
              inputMode="numeric"
              autoComplete="off"
              maxLength={16}
              value={pin}
              onChange={(e) => setPin(e.target.value)}
            />
          </div>
        )}
        <Button variant="secondary" loading={update.isPending} disabled={!dirty || (needsPin && !pin)} onClick={save}>
          {t('common.save')}
        </Button>
      </div>
      {isPlex && !profile.owner && (
        <Checkbox
          label={t('settings.mediaProfiles.watchlist')}
          hint={t('settings.mediaProfiles.watchlistHint')}
          checked={userId > 0 && watchlist}
          disabled={userId === 0}
          onChange={(e) => setWatchlist(e.target.checked)}
        />
      )}
      {needsPin && <p className="text-xs text-muted">{t('settings.mediaProfiles.pinHint')}</p>}
      {update.isError && !pinRequired && <Alert>{errorMessage(t, update.error)}</Alert>}
      {profile.lastError && !update.isError && <Alert>{t('settings.mediaProfiles.lastError', { error: profile.lastError })}</Alert>}
    </li>
  )
}
