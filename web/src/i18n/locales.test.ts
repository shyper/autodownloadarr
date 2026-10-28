import { describe, expect, it } from 'vitest'
import de from './locales/de.json'
import en from './locales/en.json'

/** Flattens nested translation objects to "a.b.c" keys; arrays count as one key. */
function keys(obj: object, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) =>
    v !== null && typeof v === 'object' && !Array.isArray(v) ? keys(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  )
}

describe('locales', () => {
  it('German has exactly the English keys', () => {
    expect(keys(de).sort()).toEqual(keys(en).sort())
  })

  it('has no empty strings', () => {
    for (const [name, locale] of Object.entries({ en, de })) {
      const empty = JSON.stringify(locale).match(/:""/g)
      expect(empty, name).toBeNull()
    }
  })
})
