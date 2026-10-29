import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createPlexPin,
  plexPinStatus,
  useConnectPlex,
  useDeletePlex,
  useGeneral,
  usePlex,
  usePlexLibraries,
  useSavePlex,
  useSaveGeneral,
} from '../../api/config'
import type { MediaServer, MediaServerKind, PathMapping, PlexServer } from '../../api/types'
import { errorMessage } from '../../i18n'
import { Alert, Badge, Button, Checkbox, Field, Input, SectionTitle, Select, Spinner } from '../ui'

const kinds: { value: MediaServerKind; label: string }[] = [
  { value: 'plex', label: 'Plex' },
  { value: 'jellyfin', label: 'Jellyfin' },
  { value: 'emby', label: 'Emby' },
]

export function PlexSettings({ onSaved }: { onSaved?: () => void }) {
  const { t } = useTranslation()
  const plex = usePlex()
  if (!plex.data) return null
  return (
    <div className="space-y-5">
      <SectionTitle title={t('settings.mediaServer.title')} description={t('settings.mediaServer.description')} />
      {plex.data.configured ? (
        <PlexLibrarySettings key={plex.data.server.id} server={plex.data.server} onSaved={onSaved} />
      ) : (
        <PlexConnect />
      )}
    </div>
  )
}

/** Sign-in with plex.tv (PIN flow) or manual URL and token. */
function PlexConnect() {
  const { t } = useTranslation()
  const connect = useConnectPlex()
  const save = useSavePlex()
  const [kind, setKind] = useState<MediaServerKind>('plex')
  const [pinId, setPinId] = useState<number | null>(null)
  const [servers, setServers] = useState<PlexServer[] | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [manual, setManual] = useState(false)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('http://')
  const [token, setToken] = useState('')
  const timer = useRef<number | undefined>(undefined)

  useEffect(() => () => window.clearInterval(timer.current), [])

  const signIn = async () => {
    setError(null)
    setServers(null)
    // Open the window right away; browsers block pop-ups opened later.
    const popup = window.open('', 'plex-auth', 'width=600,height=720')
    try {
      const pin = await createPlexPin()
      if (popup) popup.location.href = pin.authUrl
      else window.open(pin.authUrl, '_blank')
      setPinId(pin.id)
      const started = Date.now()
      window.clearInterval(timer.current)
      timer.current = window.setInterval(async () => {
        if (Date.now() - started > 5 * 60_000) {
          window.clearInterval(timer.current)
          setPinId(null)
          return
        }
        try {
          const status = await plexPinStatus(pin.id)
          if (status.authorized) {
            window.clearInterval(timer.current)
            popup?.close()
            setServers(status.servers ?? [])
          }
        } catch (err) {
          window.clearInterval(timer.current)
          setError(err)
        }
      }, 2000)
    } catch (err) {
      popup?.close()
      setError(err)
    }
  }

  const submitManual = (e: FormEvent) => {
    e.preventDefault()
    save.mutate({
      kind,
      name: name.trim() || (kind === 'plex' ? 'Plex' : kind === 'jellyfin' ? 'Jellyfin' : 'Emby'),
      url: url.trim(),
      token: token.trim(),
      accountToken: plexKind ? token.trim() : undefined,
      enabled: true,
      mappings: [],
    })
  }

  const err = error ?? connect.error ?? save.error
  const plexKind = kind === 'plex'
  return (
    <div className="space-y-4">
      {err != null && <Alert>{errorMessage(t, err)}</Alert>}
      <Field id="media-server-kind" label={t('settings.mediaServer.kind')} hint={t('settings.mediaServer.kindHint')}>
        <Select
          id="media-server-kind"
          value={kind}
          onChange={(e) => {
            setKind(e.target.value as MediaServerKind)
            setServers(null)
            setManual(false)
            setError(null)
          }}
        >
          {kinds.map((k) => (
            <option key={k.value} value={k.value}>
              {k.label}
            </option>
          ))}
        </Select>
      </Field>
      {plexKind && servers === null ? (
        <div className="flex flex-wrap items-center gap-3">
          <Button onClick={() => void signIn()} loading={pinId !== null}>
            {t('settings.plex.signIn')}
          </Button>
          {pinId !== null && <span className="text-sm text-muted">{t('settings.plex.waiting')}</span>}
          <Button variant="ghost" onClick={() => setManual((m) => !m)}>
            {t('settings.plex.manual')}
          </Button>
        </div>
      ) : null}
      {plexKind && servers !== null && (
        <ServerPicker
          servers={servers}
          busy={connect.isPending}
          onPick={(serverId, uri) => pinId !== null && connect.mutate({ pinId, serverId, uri })}
        />
      )}
      {(manual || !plexKind) && servers === null && (
        <form onSubmit={submitManual} className="grid gap-4 rounded-xl border border-line bg-surface-2 p-4 sm:grid-cols-2">
          <Field id="media-server-name" label={t('settings.mediaServer.name')}>
            <Input id="media-server-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={kind === 'plex' ? 'Plex' : kind === 'jellyfin' ? 'Jellyfin' : 'Emby'} />
          </Field>
          <Field id="plex-url" label={t('settings.plex.url')}>
            <Input id="plex-url" required value={url} onChange={(e) => setUrl(e.target.value)} placeholder={plexKind ? 'http://192.168.1.10:32400' : 'http://192.168.1.10:8096'} />
          </Field>
          <Field
            id="plex-token"
            label={plexKind ? t('settings.plex.token') : t('settings.mediaServer.apiKey')}
            hint={plexKind ? t('settings.plex.tokenHint') : t('settings.mediaServer.apiKeyHint')}
          >
            <Input id="plex-token" type="password" required value={token} onChange={(e) => setToken(e.target.value)} />
          </Field>
          <div className="flex justify-end sm:col-span-2">
            <Button type="submit" loading={save.isPending}>
              {t('common.save')}
            </Button>
          </div>
        </form>
      )}
    </div>
  )
}

function ServerPicker({ servers, busy, onPick }: { servers: PlexServer[]; busy: boolean; onPick: (id: string, uri: string) => void }) {
  const { t } = useTranslation()
  if (servers.length === 0) return <Alert>{t('settings.plex.noServers')}</Alert>
  return (
    <div className="space-y-3">
      <p className="text-sm font-medium">{t('settings.plex.pickServer')}</p>
      {servers.map((s) => (
        <div key={s.id} className="space-y-2 rounded-xl border border-line p-4">
          <p className="font-medium">
            {s.name} {s.owned && <Badge tone="muted">{t('settings.plex.owned')}</Badge>}
          </p>
          <ul className="space-y-2">
            {s.connections.map((c) => (
              <li key={c.uri} className="flex flex-wrap items-center gap-2">
                <code className="min-w-0 flex-1 truncate text-xs">{c.uri}</code>
                {c.local && <Badge tone="ok">{t('settings.plex.local')}</Badge>}
                {c.relay && <Badge tone="muted">{t('settings.plex.relay')}</Badge>}
                <Button variant="secondary" loading={busy} onClick={() => onPick(s.id, c.uri)}>
                  {t('settings.plex.use')}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  )
}

/** Library selection and path mappings of a connected server. */
function PlexLibrarySettings({ server, onSaved }: { server: MediaServer; onSaved?: () => void }) {
  const { t } = useTranslation()
  const libs = usePlexLibraries(true)
  const general = useGeneral()
  const save = useSavePlex()
  const saveGeneral = useSaveGeneral()
  const plexKind = server.kind === 'plex'
  const [watchlist, setWatchlist] = useState<boolean | null>(null)
  const [pollMinutes, setPollMinutes] = useState<number | null>(null)
  const disconnect = useDeletePlex()
  const [library, setLibrary] = useState(server.movieLibrary)
  const [tvLibrary, setTVLibrary] = useState(server.tvLibrary)
  const [mappings, setMappings] = useState<PathMapping[]>(server.mappings)

  const movieLibs = libs.data?.libraries.filter((l) => l.type === 'movie') ?? []
  const showLibs = libs.data?.libraries.filter((l) => l.type === 'show') ?? []
  const setMapping = (i: number, m: PathMapping) => setMappings((all) => all.map((x, j) => (i === j ? m : x)))
  const addMapping = (m: PathMapping = { local: '', remote: '' }) => setMappings((all) => [...all, m])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (plexKind && !general.data) return
    save.reset()
    saveGeneral.reset()
    try {
      await save.mutateAsync({
        kind: server.kind,
        name: server.name,
        url: server.url,
        movieLibrary: library,
        tvLibrary,
        mappings: mappings.filter((m) => m.local && m.remote),
        enabled: true,
      })
      if (plexKind && general.data) {
        await saveGeneral.mutateAsync({
          ...general.data,
          watchlist: watchlist ?? general.data.watchlist,
          watchlistPollMinutes: pollMinutes ?? general.data.watchlistPollMinutes,
        })
      }
      onSaved?.()
    } catch {
      // The mutation errors are displayed in the form; keep edits for retrying.
    }
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-line p-4">
        <div>
          <p className="font-medium">
            {server.name} <Badge tone="muted">{server.kind}</Badge>
          </p>
          <p className="font-mono text-xs text-muted">{server.url}</p>
        </div>
        <Button type="button" variant="ghost" onClick={() => disconnect.mutate()}>
          {t('settings.plex.disconnect')}
        </Button>
      </div>
      {(libs.isError || save.isError || (plexKind && (general.isError || saveGeneral.isError))) && (
        <Alert>{errorMessage(t, libs.error ?? save.error ?? general.error ?? saveGeneral.error)}</Alert>
      )}
      {save.isSuccess && (!plexKind || saveGeneral.isSuccess) && <Alert tone="success">{t('settings.saved')}</Alert>}
      {libs.isPending ? (
        <Spinner />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="plex-lib" label={t('settings.plex.library')} hint={t('settings.plex.libraryHint')}>
            <Select id="plex-lib" value={library} onChange={(e) => setLibrary(e.target.value)}>
              <option value="">{t('settings.plex.chooseLibrary')}</option>
              {movieLibs.map((l) => (
                <option key={l.key} value={l.key}>
                  {l.title} ({l.locations.join(', ')})
                </option>
              ))}
            </Select>
          </Field>
          <Field id="plex-tv-lib" label={t('settings.plex.tvLibrary')} hint={t('settings.plex.tvLibraryHint')}>
            <Select id="plex-tv-lib" value={tvLibrary} onChange={(e) => setTVLibrary(e.target.value)}>
              <option value="">{t('settings.plex.chooseLibrary')}</option>
              {showLibs.map((l) => (
                <option key={l.key} value={l.key}>
                  {l.title} ({l.locations.join(', ')})
                </option>
              ))}
            </Select>
          </Field>
        </div>
      )}

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium">{t('settings.plex.mappings')}</legend>
        <p className="text-xs text-muted">{t('settings.plex.mappingsHint')}</p>
        {mappings.map((m, i) => (
          <div key={i} className="grid items-center gap-2 sm:grid-cols-[1fr_auto_1fr_auto]">
            <Input aria-label={t('settings.plex.local')} placeholder="/movies" className="font-mono" value={m.local} onChange={(e) => setMapping(i, { ...m, local: e.target.value })} />
            <span className="hidden text-muted sm:inline">→</span>
            <Input aria-label={t('settings.plex.remote')} placeholder="/mnt/hdd/movies" className="font-mono" value={m.remote} onChange={(e) => setMapping(i, { ...m, remote: e.target.value })} />
            <Button type="button" variant="ghost" onClick={() => setMappings((all) => all.filter((_, j) => j !== i))}>
              {t('common.delete')}
            </Button>
          </div>
        ))}
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="secondary" onClick={() => addMapping()}>
            {t('settings.plex.addMapping')}
          </Button>
          {libs.data?.suggestions
            .filter((s) => !mappings.some((m) => m.local === s.local && m.remote === s.remote))
            .map((s) => (
              <Button key={s.local + s.remote} type="button" variant="ghost" onClick={() => addMapping(s)}>
                {t('settings.plex.applySuggestion', { local: s.local, remote: s.remote })}
              </Button>
            ))}
        </div>
        {[libs.data?.movies, libs.data?.series].map(
          (p) =>
            p && (
              <p key={p.local} className="text-xs text-muted">
                {t('settings.plex.preview', { local: p.local, remote: p.remote })}{' '}
                {p.inLibrary ? <Badge tone="ok">{t('settings.plex.inLibrary')}</Badge> : <Badge tone="danger">{t('settings.plex.notInLibrary')}</Badge>}
              </p>
            ),
        )}
      </fieldset>
      {plexKind && (general.data ? (
        <>
          <Checkbox
            label={t('settings.mediaServer.watchlist')}
            hint={t('settings.mediaServer.watchlistHint')}
            checked={watchlist ?? general.data.watchlist}
            onChange={(e) => setWatchlist(e.target.checked)}
          />
          <Field id="watchlist-poll" label={t('settings.mediaServer.watchlistPollMinutes')} hint={t('settings.mediaServer.watchlistPollMinutesHint')}>
            <Input id="watchlist-poll" type="number" required min={1} value={pollMinutes ?? general.data.watchlistPollMinutes} onChange={(e) => setPollMinutes(Number(e.target.value))} />
          </Field>
        </>
      ) : general.isPending ? <Spinner /> : null)}
      <div className="flex justify-end">
        <Button type="submit" loading={save.isPending || saveGeneral.isPending} disabled={plexKind && !general.data}>
          {t('common.save')}
        </Button>
      </div>
    </form>
  )
}
