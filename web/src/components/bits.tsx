import type React from 'react'
import { Link } from 'react-router-dom'
import { decisionInfo, fileTitle, fmtShift } from '../lib/format'
import { Icon } from './icons'

// the engine's verdict glyphs as stroke icons
const GLYPH_ICON: Record<string, string> = { '✓': 'check', '⚠': 'alert', '✕': 'x', '📌': 'pushpin', '–': 'minus', '…': 'more', '●': 'dot', '○': 'circle', '•': 'dot' }

/** Status pill: icon + label (never colour alone). */
export function Decision({ d, size = '' }: { d: string; size?: '' | 'lg' }) {
  const i = decisionInfo(d)
  return <span className={`status s-${i.cls} ${size}`}><Icon name={GLYPH_ICON[i.icon] || 'dot'} size={size === 'lg' ? 14 : 12} strokeWidth={2.2} />{i.label}</span>
}

/** A status pill with its own icon and label. */
export function StatusPill({ cls, icon, children }: { cls: 'good' | 'mut' | 'warn' | 'crit' | 'info'; icon: string; children: React.ReactNode }) {
  return <span className={`status s-${cls}`}><Icon name={icon} size={12} strokeWidth={2.2} />{children}</span>
}

export function Lang({ lang, flags }: { lang: string; flags?: string }) {
  if (!lang) return null
  return <span className="lang" title={flags ? `${lang} (${flags})` : lang}>{lang.toUpperCase()}{flags ? <small>·{flags.toUpperCase()}</small> : null}</span>
}

export function Shift({ s, decision }: { s: number | null | undefined; decision?: string }) {
  if (s === null || s === undefined || s === 0 || decision === 'already_in_sync') return null
  return <span className="mono muted small">{fmtShift(s)}</span>
}

/** "Dark (2017) · S01E02 [EN]" linking to the file page when there is one. */
export function FileName({ f, id }: { f: { title: string; label: string; lang?: string; flags?: string; name?: string }; id?: number | null }) {
  const text = fileTitle(f) || f.name || '?'
  const inner = <><span className="fname">{text}</span><Lang lang={f.lang || ''} flags={f.flags} /></>
  return id ? <Link className="fname-link" to={`/files/${id}`}>{inner}</Link> : <span className="fname-link">{inner}</span>
}

export function Pin({ on, note }: { on: boolean; note?: string }) {
  if (!on) return null
  return <span className="badge pin" title={note || 'Pinned: no automatic sync touches it'}><Icon name="pushpin" size={11} strokeWidth={2.2} />pinned</span>
}

export function Progress({ done, total }: { done: number; total: number }) {
  const pct = total ? Math.min(100, Math.round((done / total) * 100)) : 0
  return <div className="bar lg" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}><span className="fill" style={{ width: `${pct}%` }} /></div>
}
