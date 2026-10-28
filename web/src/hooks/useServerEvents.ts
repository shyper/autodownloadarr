import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { movieKeys } from '../api/movies'
import { seriesKeys } from '../api/series'
import type { Download, EpisodeDownload, Movie } from '../api/types'

export type StreamState = 'connecting' | 'open' | 'error'

type ServerEvent<T> = { type: string; time: string; data: T }

/**
 * Keeps the Server-Sent Events stream open, reports its state and feeds
 * movie updates and download progress into the query cache.
 */
export function useServerEvents(): StreamState {
  const [state, setState] = useState<StreamState>('connecting')
  const qc = useQueryClient()

  useEffect(() => {
    const source = new EventSource('/api/v1/events')
    source.onopen = () => {
      setState('open')
      // Events may have been missed while disconnected.
      void qc.invalidateQueries({ queryKey: movieKeys.all })
      void qc.invalidateQueries({ queryKey: movieKeys.downloads })
      void qc.invalidateQueries({ queryKey: seriesKeys.all })
    }
    // EventSource reconnects on its own after an error.
    source.onerror = () => setState(source.readyState === EventSource.CLOSED ? 'error' : 'connecting')

    const onMovie = (e: MessageEvent<string>) => {
      const ev = JSON.parse(e.data) as ServerEvent<Movie>
      qc.setQueryData<Movie[]>(movieKeys.list, (list) =>
        list?.map((m) => (m.id === ev.data.id ? { ...m, ...ev.data, posterUrl: m.posterUrl } : m)),
      )
      void qc.invalidateQueries({ queryKey: movieKeys.detail(ev.data.id) })
      if (ev.data.state !== 'downloading') {
        qc.setQueryData<Download[]>(movieKeys.downloads, (list) => list?.filter((d) => d.movieId !== ev.data.id))
      }
    }
    const onRemoved = () => void qc.invalidateQueries({ queryKey: movieKeys.all })
    const onProgress = (e: MessageEvent<string>) => {
      const raw = JSON.parse(e.data) as ServerEvent<Download | EpisodeDownload>
      if ('episodeId' in raw.data) {
        const d = raw.data
        qc.setQueryData<EpisodeDownload[]>(seriesKeys.downloads, (list = []) => {
          const prev = list.find((x) => x.episodeId === d.episodeId)
          return [...list.filter((x) => x.episodeId !== d.episodeId), { ...d, releaseTitle: d.releaseTitle || prev?.releaseTitle || '' }]
        })
        return
      }
      const ev = raw as ServerEvent<Download>
      qc.setQueryData<Download[]>(movieKeys.downloads, (list = []) => {
        const others = list.filter((d) => d.movieId !== ev.data.movieId)
        const prev = list.find((d) => d.movieId === ev.data.movieId)
        return [...others, { ...ev.data, releaseTitle: ev.data.releaseTitle || prev?.releaseTitle || '' }]
      })
    }
    source.addEventListener('movie.updated', onMovie)
    source.addEventListener('movie.removed', onRemoved)
    source.addEventListener('download.progress', onProgress)
    // Series: refresh the affected views; downloads that ended drop out.
    let pending: number | undefined
    const onSeries = () => {
      window.clearTimeout(pending)
      pending = window.setTimeout(() => {
        void qc.invalidateQueries({ queryKey: seriesKeys.all })
      }, 300)
    }
    source.addEventListener('series.updated', onSeries)
    source.addEventListener('episode.updated', onSeries)
    source.addEventListener('series.removed', onSeries)
    return () => source.close()
  }, [qc])

  return state
}
