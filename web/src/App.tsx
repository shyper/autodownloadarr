import { Route, Routes } from 'react-router'
import { DownloadsPage } from './pages/DownloadsPage'
import { RequireAuth, RequireGuest } from './components/guards'
import { Layout } from './components/Layout'
import { AuthorizePage } from './pages/AuthorizePage'
import { AccountPage } from './pages/AccountPage'
import { DashboardPage } from './pages/DashboardPage'
import { LoginPage } from './pages/LoginPage'
import { MovieDetailPage } from './pages/MovieDetailPage'
import { MoviesPage } from './pages/MoviesPage'
import { NotFoundPage } from './pages/NotFoundPage'
import { SeriesDetailPage } from './pages/SeriesDetailPage'
import { SeriesPage } from './pages/SeriesPage'
import { SettingsPage } from './pages/SettingsPage'
import { SetupWizard } from './pages/setup/SetupWizard'

export function App() {
  return (
    <Routes>
      <Route path="/setup" element={<SetupWizard />} />
      <Route
        path="/login"
        element={
          <RequireGuest>
            <LoginPage />
          </RequireGuest>
        }
      />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<DashboardPage />} />
        <Route path="movies" element={<MoviesPage />} />
        <Route path="movies/:id" element={<MovieDetailPage />} />
        <Route path="series" element={<SeriesPage />} />
        <Route path="series/:id" element={<SeriesDetailPage />} />
        <Route path="downloads" element={<DownloadsPage />} />
        <Route path="settings/:tab?" element={<SettingsPage />} />
        <Route path="authorize" element={<AuthorizePage />} />
        <Route path="account" element={<AccountPage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  )
}
