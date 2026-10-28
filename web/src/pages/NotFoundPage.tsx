import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'

export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <div className="space-y-3 py-16 text-center">
      <h1 className="text-2xl font-semibold">{t('notFound.title')}</h1>
      <Link to="/" className="text-sm font-medium text-accent hover:underline">
        {t('notFound.back')}
      </Link>
    </div>
  )
}
