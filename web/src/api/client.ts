/** Error returned by the API. `code` is stable and maps to a translated message. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  /** Seconds to wait before retrying (429 responses). */
  readonly retryAfter?: number

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

type RequestOptions = {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: unknown
  signal?: AbortSignal
}

/** Calls an endpoint below /api/v1 and returns the decoded JSON body. */
export async function api<T>(path: string, { method = 'GET', body, signal }: RequestOptions = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(`/api/v1${path}`, {
      method,
      signal,
      credentials: 'same-origin',
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError(0, 'network_error', String(err))
  }

  if (res.status === 204) return undefined as T
  const data: unknown = await res.json().catch(() => null)
  if (!res.ok) {
    const error = (data as { error?: { code?: string; message?: string } } | null)?.error
    const retryAfter = Number(res.headers.get('Retry-After')) || undefined
    throw new ApiError(res.status, error?.code ?? 'unknown', error?.message ?? res.statusText, retryAfter)
  }
  return data as T
}
