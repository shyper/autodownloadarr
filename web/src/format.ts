/** Human-readable size in binary units. */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

export function formatDateTime(iso: string | undefined, lang: string | undefined): string {
  if (!iso) return ''
  return new Date(iso).toLocaleString(lang, { dateStyle: 'medium', timeStyle: 'short' })
}

/** Splits a comma or newline separated list. */
export function splitList(s: string): string[] {
  return s
    .split(/[,\n]/)
    .map((x) => x.trim())
    .filter(Boolean)
}
