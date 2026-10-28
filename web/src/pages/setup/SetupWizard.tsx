import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, useNavigate } from 'react-router'
import { useGeneral, useIndexers, useOnboarding, usePlex, useSaveOnboarding, useTMDB, useUsenetServers } from '../../api/config'
import { useSession } from '../../api/session'
import { LoadState } from '../../components/guards'
import { LanguageSelect } from '../../components/LanguageSelect'
import { GeneralForm } from '../../components/settings/GeneralForm'
import { IndexerSettings } from '../../components/settings/IndexerSettings'
import { PlexSettings } from '../../components/settings/PlexSettings'
import { TmdbForm } from '../../components/settings/TmdbForm'
import { UsenetSettings } from '../../components/settings/UsenetSettings'
import { Badge, Button, Card, Logo } from '../../components/ui'
import { AccountStep } from './AccountStep'

const steps = ['account', 'usenet', 'indexer', 'library', 'mediaServer', 'done'] as const
type Step = (typeof steps)[number]

/**
 * First-start wizard: account, then the integrations. Progress is stored on
 * the server, so a closed browser continues where it left off.
 */
export function SetupWizard() {
  const session = useSession()
  const onboarding = useOnboarding(!session.setupRequired && session.user?.role === 'admin')

  if (session.pending || session.failed) return <LoadState failed={session.failed} retry={session.retry} />
  if (!session.setupRequired && !session.user) return <Navigate to="/login" replace />
  if (!session.setupRequired && onboarding.isPending) return <LoadState failed={onboarding.isError} retry={() => void onboarding.refetch()} />

  const done = onboarding.data?.done ?? []
  const firstOpen = session.setupRequired ? 0 : Math.max(1, steps.findIndex((s) => s !== 'account' && !done.includes(s)))
  return <Wizard initial={firstOpen < 0 ? steps.length - 1 : firstOpen} />
}

function Wizard({ initial }: { initial: number }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [index, setIndex] = useState(initial)
  const onboarding = useOnboarding(index > 0)
  const saveOnboarding = useSaveOnboarding()
  const step = steps[index] ?? 'done'

  const markDone = (s: Step, finished = false) => {
    const prev = onboarding.data ?? { done: [], finished: false }
    saveOnboarding.mutate({ done: Array.from(new Set([...prev.done, s])), finished: finished || prev.finished })
  }
  const next = () => {
    if (step !== 'account') markDone(step)
    setIndex((i) => Math.min(i + 1, steps.length - 1))
  }
  const back = index > 1 ? () => setIndex((i) => i - 1) : undefined
  const finish = () => {
    markDone('done', true)
    navigate('/', { replace: true })
  }

  return (
    <div className="flex min-h-dvh flex-col items-center px-4 py-10">
      <div className="w-full max-w-3xl space-y-8">
        <header className="flex items-start justify-between gap-4">
          <div className="flex items-center gap-3">
            <Logo className="size-10" />
            <div>
              <h1 className="text-xl font-semibold">{t('setup.title')}</h1>
              <p className="text-sm text-muted">{t('setup.subtitle')}</p>
            </div>
          </div>
          <LanguageSelect />
        </header>

        <Stepper current={index} />

        <Card>
          {step === 'account' && <AccountStep onDone={next} />}
          {step === 'usenet' && (
            <StepFrame step="usenet" onNext={next} onBack={back}>
              <UsenetSettings />
            </StepFrame>
          )}
          {step === 'indexer' && (
            <StepFrame step="indexer" onNext={next} onBack={back}>
              <IndexerSettings />
            </StepFrame>
          )}
          {step === 'library' && (
            <StepFrame step="library" onNext={next} onBack={back}>
              <div className="space-y-8">
                <GeneralForm fields="library" />
                <TmdbForm />
              </div>
            </StepFrame>
          )}
          {step === 'mediaServer' && (
            <StepFrame step="mediaServer" onNext={next} onBack={back}>
              <PlexSettings />
            </StepFrame>
          )}
          {step === 'done' && <DoneStep onBack={back} onFinish={finish} />}
        </Card>
      </div>
    </div>
  )
}

/** Whether a step has the minimum it needs to work. */
function useStepComplete(step: Step): boolean {
  const usenet = useUsenetServers()
  const indexers = useIndexers()
  const general = useGeneral()
  const tmdb = useTMDB()
  const plex = usePlex()
  switch (step) {
    case 'usenet':
      return (usenet.data?.length ?? 0) > 0
    case 'indexer':
      return (indexers.data?.length ?? 0) > 0
    case 'library':
      return !!general.data?.moviesPath && !!tmdb.data?.hasKey
    case 'mediaServer':
      return plex.data?.configured === true && !!plex.data.server.movieLibrary
    default:
      return true
  }
}

function StepFrame({ step, onNext, onBack, children }: { step: Step; onNext: () => void; onBack?: () => void; children: ReactNode }) {
  const { t } = useTranslation()
  const complete = useStepComplete(step)
  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h2 className="text-lg font-semibold">{t(`setup.steps.${step}.title`)}</h2>
        <p className="text-sm text-muted">{t(`setup.steps.${step}.description`)}</p>
      </div>
      {children}
      <div className="flex flex-wrap justify-between gap-3 border-t border-line pt-4">
        {onBack ? (
          <Button variant="ghost" onClick={onBack}>
            {t('common.back')}
          </Button>
        ) : (
          <span />
        )}
        <Button variant={complete ? 'primary' : 'secondary'} onClick={onNext}>
          {complete ? t('common.continue') : t('common.skip')}
        </Button>
      </div>
    </div>
  )
}

function DoneStep({ onBack, onFinish }: { onBack?: () => void; onFinish: () => void }) {
  const { t } = useTranslation()
  const checks: [Step, boolean][] = [
    ['usenet', useStepComplete('usenet')],
    ['indexer', useStepComplete('indexer')],
    ['library', useStepComplete('library')],
    ['mediaServer', useStepComplete('mediaServer')],
  ]
  return (
    <div className="space-y-4">
      <h2 className="text-lg font-semibold">{t('setup.steps.done.title')}</h2>
      <p className="text-sm text-muted">{t('setup.steps.done.description')}</p>
      <ul className="space-y-2">
        {checks.map(([s, ok]) => (
          <li key={s} className="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2 text-sm">
            <span>{t(`setup.steps.${s}.label`)}</span>
            <Badge tone={ok ? 'ok' : 'warn'}>{ok ? t('setup.configured') : t('setup.missing')}</Badge>
          </li>
        ))}
      </ul>
      <div className="flex justify-between gap-3 pt-2">
        {onBack ? (
          <Button variant="ghost" onClick={onBack}>
            {t('common.back')}
          </Button>
        ) : (
          <span />
        )}
        <Button onClick={onFinish}>{t('setup.steps.done.submit')}</Button>
      </div>
    </div>
  )
}

function Stepper({ current }: { current: number }) {
  const { t } = useTranslation()
  return (
    <nav aria-label={t('setup.stepOf', { current: current + 1, total: steps.length })}>
      <p className="mb-3 text-sm text-muted sm:hidden">
        {t('setup.stepOf', { current: current + 1, total: steps.length })} ·{' '}
        <span className="text-fg">{t(`setup.steps.${steps[current]}.label`)}</span>
      </p>
      <ol className="hidden gap-2 sm:grid sm:grid-cols-6">
        {steps.map((s, i) => {
          const state = i < current ? 'done' : i === current ? 'current' : 'todo'
          return (
            <li key={s} aria-current={state === 'current' ? 'step' : undefined} className="space-y-2">
              <div className={`h-1 rounded-full ${state === 'todo' ? 'bg-line' : 'bg-accent'}`} />
              <p className={`text-xs font-medium ${state === 'current' ? 'text-fg' : 'text-muted'}`}>
                {i + 1}. {t(`setup.steps.${s}.label`)}
              </p>
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
