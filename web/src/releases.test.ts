import { describe, expect, it } from 'vitest'
import type { Attempt, Release } from './api/types'
import { releaseMark } from './releases'

const release = (title: string): Release => ({
  id: 1,
  releaseKey: title,
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
  releaseKey: releaseTitle,
  releaseTitle,
  indexerName: 'idx',
  startedAt: '2026-10-29T10:00:00Z',
  result,
  errorKind,
  message: '',
})

describe('releaseMark', () => {
  it('marks the release on disk and the one downloading', () => {
    expect(releaseMark(release('A'), { releaseKey: 'A', state: 'available' }, [])).toEqual({ kind: 'loaded' })
    expect(releaseMark(release('A'), { releaseKey: 'A', state: 'watched' }, [])).toEqual({ kind: 'loaded' })
    expect(releaseMark(release('A'), { releaseKey: 'A', state: 'downloading' }, [])).toEqual({ kind: 'loading' })
  })

  it('marks releases whose last attempt failed', () => {
    const attempts = [attempt('B', 'failed', 'incomplete'), attempt('C', 'success'), attempt('C', 'failed', 'password')]
    expect(releaseMark(release('B'), { releaseKey: 'A', state: 'available' }, attempts)).toEqual({ kind: 'failed', errorKind: 'incomplete' })
    // Only the newest attempt counts: C worked on the second try.
    expect(releaseMark(release('C'), { releaseKey: 'A', state: 'available' }, attempts)).toBeNull()
  })

  it('leaves untouched and deleted releases unmarked', () => {
    expect(releaseMark(release('D'), { releaseKey: 'A', state: 'available' }, [])).toBeNull()
    expect(releaseMark(release('A'), { releaseKey: 'A', state: 'deleted' }, [])).toBeNull()
  })

  it('distinguishes same-named uploads by their source identity', () => {
    const first = { ...release('Same title'), releaseKey: '1:guid:a' }
    const second = { ...release('Same title'), id: 2, releaseKey: '1:guid:b' }
    const current = { releaseKey: first.releaseKey, state: 'available' as const }
    const attempts = [{ ...attempt('Same title', 'failed', 'incomplete'), releaseKey: second.releaseKey }]
    expect(releaseMark(first, current, attempts)).toEqual({ kind: 'loaded' })
    expect(releaseMark(second, current, attempts)).toEqual({ kind: 'failed', errorKind: 'incomplete' })
    expect(releaseMark({ ...first, releaseKey: '' }, { ...current, releaseKey: '' }, [])).toBeNull()
  })
})
