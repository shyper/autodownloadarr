import type { TFunction } from 'i18next'

/** OAuth scope colons are part of the key, not i18next namespace separators. */
export function scopeLabel(t: TFunction, scope: string) {
  return t(`integrations.scopes.${scope}`, { nsSeparator: false, defaultValue: scope })
}
