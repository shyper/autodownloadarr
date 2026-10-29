import { useTranslation } from 'react-i18next'
import type { SeasonInfo } from '../api/types'
import { Checkbox, Spinner } from './ui'

/**
 * Season selection as equally wide tiles. An empty selection means all
 * seasons, including those that appear later; with single seasons picked,
 * future ones can be added on top.
 */
export function SeasonPicker({
  seasons,
  loading,
  selected,
  future,
  onChange,
}: {
  seasons: SeasonInfo[]
  loading?: boolean
  selected: number[]
  future: boolean
  onChange: (selected: number[], future: boolean) => void
}) {
  const { t } = useTranslation()
  const all = selected.length === 0
  const regular = seasons.filter((s) => s.number > 0)
  const specials = seasons.find((s) => s.number === 0)
  const toggle = (n: number) => {
    const next = selected.includes(n) ? selected.filter((x) => x !== n) : [...selected, n].sort((a, b) => a - b)
    onChange(next, next.length === 0 ? false : future)
  }

  return (
    <fieldset className="space-y-3">
      <legend className="text-sm font-medium">{t('series.request.seasons')}</legend>
      {loading ? (
        <Spinner />
      ) : (
        <>
          <Checkbox
            label={t('series.request.allSeasons')}
            hint={t('series.request.allSeasonsHint')}
            checked={all}
            onChange={(e) => onChange(e.target.checked ? [] : regular.slice(0, 1).map((s) => s.number), false)}
          />
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
            {regular.map((s) => (
              <SeasonTile key={s.number} season={s} on={all || selected.includes(s.number)} dimmed={all} onClick={() => toggle(s.number)} />
            ))}
            {specials && (
              <SeasonTile season={specials} on={selected.includes(0)} dimmed={false} onClick={() => toggle(0)} />
            )}
          </div>
          {!all && (
            <Checkbox
              label={t('series.request.futureSeasons')}
              hint={t('series.request.futureSeasonsHint')}
              checked={future}
              onChange={(e) => onChange(selected, e.target.checked)}
            />
          )}
        </>
      )}
    </fieldset>
  )
}

function SeasonTile({ season, on, dimmed, onClick }: { season: SeasonInfo; on: boolean; dimmed: boolean; onClick: () => void }) {
  const { t } = useTranslation()
  const year = season.airDate?.slice(0, 4)
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={`flex min-h-14 items-start justify-between gap-2 rounded-xl border px-3 py-2 text-left transition-colors focus-visible:outline-2 focus-visible:outline-accent ${
        on ? 'border-accent bg-accent-soft' : 'border-line bg-surface hover:border-accent/50'
      } ${dimmed ? 'opacity-60' : ''}`}
    >
      <span className="min-w-0">
        <span className={`block text-sm font-medium ${on ? 'text-accent' : 'text-fg'}`}>
          {season.number === 0 ? t('series.specials') : t('series.season', { number: season.number })}
        </span>
        <span className="block text-xs text-muted">
          {t('series.episodeCount', { count: season.episodeCount })}
          {year && <> · {year}</>}
        </span>
      </span>
      <span aria-hidden="true" className={`text-sm ${on ? 'text-accent' : 'invisible'}`}>
        ✓
      </span>
    </button>
  )
}
