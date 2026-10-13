// Formatting: dates (format chosen in Settings → General), decisions, shifts.
// ---- dates: the format comes from Settings → General and is set once by the app context.
export type DateFormat = 'auto' | 'dmy-dot' | 'dmy-slash' | 'mdy-slash' | 'ymd' | 'dmmmy' | 'mmmdy'
export type TimeFormat = 'auto' | '24h' | '12h'
let DATE_FMT: DateFormat = 'auto'
let TIME_FMT: TimeFormat = 'auto'
export function setDateFormats(d: DateFormat, t: TimeFormat) { DATE_FMT = d; TIME_FMT = t }

export const DATE_FORMATS: { id: DateFormat; label: string }[] = [
  { id: 'auto', label: 'Browser locale' }, { id: 'dmy-dot', label: '31.12.2026' }, { id: 'dmy-slash', label: '31/12/2026' },
  { id: 'mdy-slash', label: '12/31/2026' }, { id: 'ymd', label: '2026-12-31' }, { id: 'dmmmy', label: '31 Dec 2026' }, { id: 'mmmdy', label: 'Dec 31, 2026' },
]
export const TIME_FORMATS: { id: TimeFormat; label: string }[] = [{ id: 'auto', label: 'Browser locale' }, { id: '24h', label: '17:05' }, { id: '12h', label: '5:05 PM' }]

const MON = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** Format a calendar date (y, m 1-12, d) in the chosen format; withYear=false drops the year for nearby dates. */
export function fmtYMD(y: number, m: number, d: number, withYear = true, fmt: DateFormat = DATE_FMT): string {
  const dd = String(d).padStart(2, '0'), mm = String(m).padStart(2, '0')
  switch (fmt) {
    case 'dmy-dot': return withYear ? `${dd}.${mm}.${y}` : `${dd}.${mm}.`
    case 'dmy-slash': return withYear ? `${dd}/${mm}/${y}` : `${dd}/${mm}`
    case 'mdy-slash': return withYear ? `${mm}/${dd}/${y}` : `${mm}/${dd}`
    case 'ymd': return withYear ? `${y}-${mm}-${dd}` : `${mm}-${dd}`
    case 'dmmmy': return withYear ? `${d} ${MON[m - 1]} ${y}` : `${d} ${MON[m - 1]}`
    case 'mmmdy': return withYear ? `${MON[m - 1]} ${d}, ${y}` : `${MON[m - 1]} ${d}`
    default: return new Date(y, m - 1, d).toLocaleDateString(undefined, withYear ? { day: 'numeric', month: 'short', year: 'numeric' } : { day: 'numeric', month: 'short' })
  }
}

/** "2026-03-14" → "14 Mar 2026" (or the chosen format); "" stays "". */
export function fmtDate(d: string, withYear = true): string {
  if (!d) return ''
  const [y, m, day] = d.slice(0, 10).split('-').map(Number)
  return fmtYMD(y, m, day, withYear)
}

export function fmtTime(t: Date, fmt: TimeFormat = TIME_FMT): string {
  const h = t.getHours(), mi = String(t.getMinutes()).padStart(2, '0')
  switch (fmt) {
    case '24h': return `${String(h).padStart(2, '0')}:${mi}`
    case '12h': return `${h % 12 || 12}:${mi} ${h < 12 ? 'AM' : 'PM'}`
    default: return t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  }
}

/** ISO timestamp → "14 Mar 2026 17:05" in the chosen formats. */
export function fmtDateTime(iso: string): string {
  if (!iso) return ''
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return iso
  return `${fmtYMD(t.getFullYear(), t.getMonth() + 1, t.getDate())} ${fmtTime(t)}`
}

/** Relative for the last week ("3 h ago"), the full date and time after that. */
export function ago(iso: string): string {
  const t = new Date(iso).getTime()
  const s = Math.max(0, (Date.now() - t) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`
  if (s < 86400 * 7) return `${Math.floor(s / 86400)} d ago`
  return fmtDateTime(iso)
}

/** "2026-11" → "November 2026". */
export function fmtMonth(ym: string): string {
  const [y, m] = ym.split('-').map(Number)
  return new Date(y, m - 1, 1).toLocaleDateString(undefined, { month: 'long', year: 'numeric' })
}

export function initials(name: string): string {
  const parts = name.trim().split(/[\s@._-]+/).filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

const PALETTE = ['#2563eb', '#7c3aed', '#0891b2', '#059669', '#d97706', '#dc2626', '#db2777', '#4b5563', '#b45309', '#0d9488']
export function colorFor(id: number): string { return PALETTE[Math.abs(id) % PALETTE.length] }


export interface DecisionInfo { label: string; icon: string; cls: 'good' | 'mut' | 'warn' | 'crit' | 'info' }

/** Human label + status class for an engine decision. Status always ships with an icon and a label. */
export function decisionInfo(d: string): DecisionInfo {
  switch (d) {
    case 'synced': return { label: 'Adjusted', icon: '✓', cls: 'good' }
    case 'already_in_sync': return { label: 'In sync', icon: '✓', cls: 'mut' }
    case 'left_unchanged': return { label: 'Gave up', icon: '⚠', cls: 'warn' }
    case 'rejected_wrong_content': return { label: 'Wrong content', icon: '✕', cls: 'crit' }
    case 'rejected_machine_translation': return { label: 'Machine translation', icon: '✕', cls: 'crit' }
    case 'error': return { label: 'Error', icon: '✕', cls: 'crit' }
    case 'timeout': return { label: 'Timed out', icon: '✕', cls: 'crit' }
    case 'pinned': return { label: 'Pinned', icon: '📌', cls: 'info' }
    case 'skipped': return { label: 'Skipped', icon: '–', cls: 'mut' }
    case 'queued': return { label: 'Queued', icon: '…', cls: 'mut' }
    case 'running': return { label: 'Syncing', icon: '●', cls: 'info' }
    case 'cancelled': return { label: 'Cancelled', icon: '–', cls: 'mut' }
    case 'failed': return { label: 'Failed', icon: '✕', cls: 'crit' }
    case '': return { label: 'Not checked', icon: '○', cls: 'mut' }
  }
  return { label: d.replace(/_/g, ' '), icon: '•', cls: 'mut' }
}

export const METHOD_LABEL: Record<string, string> = {
  alass_embedded_track: 'Aligned to the embedded track',
  audio_piecewise: 'Aligned to the audio, act by act',
}

export const SOURCE_LABEL: Record<string, string> = {
  bazarr: 'Bazarr download', sweep: 'Nightly sweep', manual: 'Requested', season: 'Season tool', api: 'API', import: 'Imported',
}

/** 2.68 → "2.68 s"; null → "". */
export function fmtShift(s: number | null | undefined): string {
  if (s === null || s === undefined) return ''
  return `${s.toFixed(2)} s`
}

/** 250 → "+250 ms". */
export function fmtMs(ms: number): string {
  return `${ms > 0 ? '+' : ms < 0 ? '−' : ''}${Math.abs(ms).toLocaleString()} ms`
}

export function fmtDuration(ms: number): string {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s} s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min ${s % 60 ? `${s % 60} s` : ''}`.trim()
  return `${Math.floor(m / 60)} h ${m % 60} min`
}

export function fmtElapsed(sec: number): string {
  if (sec < 60) return `${sec} s`
  const m = Math.floor(sec / 60)
  return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(0)} KB`
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`
  return `${(n / 1024 ** 3).toFixed(2)} GB`
}

/** "Dark (2017) · S01E02" or the movie title. */
export function fileTitle(f: { title: string; label: string; kind?: string }): string {
  if (f.label && f.label !== f.title) return `${f.title} · ${f.label}`
  return f.title || f.label
}
