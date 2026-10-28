import { QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { ApiError } from './api/client'
import { queryKeys } from './api/hooks'
import { App } from './App'
import './i18n'
import './styles.css'

const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({
    // An expired session on any request signs the user out in the UI.
    onError: (err) => {
      if (err instanceof ApiError && err.status === 401) queryClient.setQueryData(queryKeys.me, null)
    },
  }),
  defaultOptions: {
    queries: {
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
      refetchOnWindowFocus: false,
    },
  },
})

const root = document.getElementById('root')
if (!root) throw new Error('missing #root element')

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
