export type DeleteTrigger = 'progress' | 'minutes' | 'watched'

export type General = {
  moviesPath: string
  seriesPath: string
  seriesWindow: number
  seriesTrigger: DeleteTrigger
  seriesTriggerValue: number
  deleteAfterHours: number
  unwatchedExpiryDays: number
  maxSources: number
  parallelDownloads: number
  watchPollMinutes: number
  language: string
  /** MiB/s, 0 = unlimited */
  maxDownloadMBps: number
}

export type UsenetServer = {
  id: number
  name: string
  host: string
  port: number
  tls: boolean
  insecureSkipVerify: boolean
  username: string
  password?: string
  hasPassword: boolean
  connections: number
  priority: number
  enabled: boolean
  /** Check every article before downloading instead of a sample. */
  fullCheck: boolean
}

export type Indexer = {
  id: number
  name: string
  url: string
  apiKey?: string
  hasApiKey: boolean
  movieCategories: number[]
  tvCategories: number[]
  priority: number
  enabled: boolean
}

export type Category = { id: number; name: string; subs?: Category[] }
export type Caps = {
  movieSearch: boolean
  movieParams: string[]
  tvSearch: boolean
  tvParams: string[]
  categories: Category[]
}

export type PathMapping = { local: string; remote: string }

export type MediaServer = {
  id: number
  name: string
  url: string
  token?: string
  hasToken: boolean
  movieLibrary: string
  tvLibrary: string
  mappings: PathMapping[]
  enabled: boolean
}

export type PlexConfig = { configured: false } | { configured: true; server: MediaServer }

export type PlexPin = { id: number; code: string; authUrl: string }
export type PlexConnection = { uri: string; local: boolean; relay: boolean }
export type PlexServer = { name: string; id: string; owned: boolean; connections: PlexConnection[] }
export type PlexPinStatus = { authorized: boolean; servers?: PlexServer[] }
export type PlexLibrary = { key: string; title: string; type: string; locations: string[] }
export type PathPreview = { local: string; remote: string; inLibrary: boolean }
export type PlexLibraries = {
  libraries: PlexLibrary[]
  suggestions: PathMapping[]
  movies?: PathPreview
  series?: PathPreview
}

export type WordScore = { term: string; score: number }
export type ProfileConfig = {
  maxResolution: number
  minResolution: number
  requiredWords: string[]
  requiredMode: 'any' | 'all'
  forbiddenWords: string[]
  preferredWords: WordScore[]
  allowLowQuality: boolean
  minMbPerMinute: number
  maxMbPerMinute: number
  preferredCodec: string
  passwordPolicy: 'allow' | 'known' | 'avoid' | 'reject'
}
export type Profile = { id: number; name: string; config: ProfileConfig; default: boolean }

export type ArchivePassword = { id: number; password: string; hits: number }
export type Onboarding = { done: string[]; finished: boolean }

export type MovieState =
  | 'searching'
  | 'waiting'
  | 'queued'
  | 'downloading'
  | 'available'
  | 'watched'
  | 'deleted'
  | 'failed'

export type Movie = {
  id: number
  tmdbId: number
  imdbId: string
  title: string
  originalTitle: string
  year: number
  overview: string
  posterUrl?: string
  runtime: number
  state: MovieState
  keep: boolean
  attempts: number
  lastError: string
  nextSearchAt?: string
  releaseTitle: string
  folder: string
  availableAt?: string
  watchedAt?: string
  deleteAt?: string
  /** 0..1, watched since the file is there */
  progress: number
  createdAt: string
}

export type Release = {
  id: number
  indexerName: string
  title: string
  size: number
  publishedAt?: string
  grabs: number
  passworded: boolean
  score: number
  rejected: string
  blocklisted: boolean
}

export type Attempt = {
  id: number
  releaseTitle: string
  indexerName: string
  startedAt: string
  finishedAt?: string
  result: 'running' | 'success' | 'failed' | 'cancelled'
  errorKind: string
  message: string
}

export type BlocklistEntry = { id: number; title: string; reason: string; message: string; createdAt: string }

export type MovieDetail = Movie & { releases: Release[]; attempts: Attempt[]; blocklist: BlocklistEntry[] }

export type SearchResult = {
  tmdbId: number
  title: string
  originalTitle?: string
  year?: number
  overview?: string
  posterUrl?: string
  movieId?: number
  state?: MovieState
}

export type Progress = {
  totalBytes: number
  doneBytes: number
  totalSegments: number
  doneSegments: number
  failedSegments: number
  failedBytes: number
  /** nanoseconds */
  elapsed: number
  /** While the availability of the articles is checked before the download. */
  checking?: boolean
  checked?: number
  checkTotal?: number
}

export type Warning = { code: string; detail: string; at: string }

export type HistoryEntry = Attempt & {
  movieId?: number
  episodeId?: number
  seriesId?: number
  kind: 'movie' | 'episode'
  label: string
  size: number
}

export type HistoryPage = { items: HistoryEntry[]; next?: number }

export type QueueEntry = { kind: 'movie' | 'episode'; movieId?: number; episodeId?: number; seriesId?: number; label: string }

export type Download = { movieId: number; releaseTitle: string; progress: Progress }

export type SeasonInfo = { number: number; name: string; episodeCount: number; airDate?: string }

export type ShowResult = {
  tmdbId: number
  title: string
  year?: number
  overview?: string
  posterUrl?: string
  status?: string
  seasons?: SeasonInfo[]
  seriesId?: number
}

export type EpisodeState = MovieState | 'pending' | 'skipped'

export type Series = {
  id: number
  tmdbId: number
  title: string
  year: number
  overview: string
  posterUrl?: string
  status: string
  seasons: number[]
  futureSeasons: boolean
  knownSeasons: number
  windowSize: number
  deleteTrigger: DeleteTrigger
  triggerValue: number
  keep: boolean
  paused: boolean
  counts: Partial<Record<EpisodeState, number>>
  /** The episode being watched or next up. */
  current?: EpisodeRef
  /** The next episode still to be broadcast. */
  upcoming?: EpisodeRef
}

export type EpisodeRef = { id: number; season: number; episode: number; progress: number; airDate?: string }

export type Episode = {
  id: number
  seriesId: number
  season: number
  episode: number
  title: string
  airDate: string
  /** minutes */
  runtime: number
  state: EpisodeState
  lastError: string
  nextSearchAt?: string
  releaseTitle: string
  progress: number
  startedAt?: string
  watchedAt?: string
  deleteAt?: string
}

export type SeriesDetail = Series & { episodes: Episode[] }
export type EpisodeDetail = Episode & { releases: Release[]; attempts: Attempt[]; blocklist: BlocklistEntry[] }
export type EpisodeDownload = {
  episodeId: number
  seriesId: number
  season: number
  episode: number
  releaseTitle: string
  progress: Progress
}

export type SeriesSettings = {
  seasons: number[]
  futureSeasons: boolean
  windowSize: number
  deleteTrigger: DeleteTrigger
  triggerValue: number
  keep: boolean
  paused: boolean
}
