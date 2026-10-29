import { describe, expect, it } from 'vitest'
import type { Attempt, Release } from './api/types'
import { releaseMark } from './releases'

const release = (title: string): Release => ({
  id: 1,
  indexerName: 'idx',
  title,
  size: 1,
  grabs: 0,
  passworded: false,
  score: 0,
  rejected: '',
  blocklisted: false,
})
const attempt = (releaseTitle: string, result: Attempt['result'], errorKind = ''): Attempt => ({
  id: 1,
  releaseTitle,
  indexerName: 'idx',
  startedAt: '2026-10-29T10:00:00Z',
  result,
  errorKind,
  message: '',
})

describe('releaseMark', () => {
  it('marks the release on disk and the one downloading', () => {
    expect(releaseMark(release('A'), { releaseTitle: 'A', state: 'available' }, [])).toEqual({ kind: 'loaded' })
    expect(releaseMark(release('A'), { releaseTitle: 'A', state: 'watched' }, [])).toEqual({ kind: 'loaded' })
    expect(releaseMark(release('A'), { releaseTitle: 'A', state: 'downloading' }, [])).toEqual({ kind: 'loading' })
  })

  it('marks releases whose last attempt failed', () => {
    const attempts = [attempt('B', 'failed', 'incomplete'), attempt('C', 'success'), attempt('C', 'failed', 'password')]
    expect(releaseMark(release('B'), { releaseTitle: 'A', state: 'available' }, attempts)).toEqual({ kind: 'failed', errorKind: 'incomplete' })
    // Only the newest attempt counts: C worked on the second try.
    expect(releaseMark(release('C'), { releaseTitle: 'A', state: 'available' }, attempts)).toBeNull()
  })

  it('leaves untouched and deleted releases unmarked', () => {
    expect(releaseMark(release('D'), { releaseTitle: 'A', state: 'available' }, [])).toBeNull()
    expect(releaseMark(release('A'), { releaseTitle: 'A', state: 'deleted' }, [])).toBeNull()
  })
})
