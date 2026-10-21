// ⌘K / "/" — jump to a file or a show, or run a command from the keyboard.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Subtitle, TitleSummary } from '../lib/types'
import { fileTitle, fmtMs } from '../lib/format'
import { Icon } from './icons'
import { useDebounce } from './ui'

export interface PaletteCommand { id: string; label: string; icon: string; hint?: string; keywords?: string; run: () => void }

export function CommandPalette({ onClose, commands }: { onClose: () => void; commands: PaletteCommand[] }) {
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const list = useRef<HTMLDivElement>(null)
  const dq = useDebounce(q.trim(), 160)
  const [found, setFound] = useState<{ q: string; titles: TitleSummary[]; files: Subtitle[] }>({ q: '', titles: [], files: [] })

  // shows and files from the library, once something is typed
  useEffect(() => {
    if (dq.length < 2) { setFound({ q: dq, titles: [], files: [] }); return }
    let live = true
    Promise.all([
      api.get<TitleSummary[]>(`${V1}/library/titles?${new URLSearchParams({ q: dq })}`).catch(() => [] as TitleSummary[]),
      api.get<{ items: Subtitle[] }>(`${V1}/library/files?${new URLSearchParams({ q: dq, limit: '8' })}`).catch(() => ({ items: [] as Subtitle[] })),
    ]).then(([titles, files]) => { if (live) setFound({ q: dq, titles: (titles || []).slice(0, 5), files: files.items || [] }) })
    return () => { live = false }
  }, [dq])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void }
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = []
    const n = q.trim().toLowerCase()
    const words = n.split(/\s+/).filter(Boolean)
    const hit = (text: string) => words.every(w => text.toLowerCase().includes(w))
    for (const f of found.files) {
      out.push({ key: `f${f.id}`, group: 'Files', icon: <Icon name="captions" size={16} />, label: <>{fileTitle(f)} <span className="pal-lang">{f.lang.toUpperCase()}</span></>, sub: [f.pinned && 'pinned', f.nudge_ms ? fmtMs(f.nudge_ms) : '', f.rel].filter(Boolean).join(' · '), run: () => navigate(`/files/${f.id}`) })
    }
    for (const t of found.titles) {
      out.push({ key: `t${t.library}${t.title}`, group: 'Shows & movies', icon: <Icon name={t.kind === 'episode' ? 'tv' : 'film'} size={16} />, label: t.title, sub: `${t.files} file${t.files === 1 ? '' : 's'}${t.attention ? ` · ${t.attention} need attention` : ''}`, run: () => navigate(`/library/t/${encodeURIComponent(t.title)}`) })
    }
    for (const c of commands) {
      if (n && !hit(`${c.label} ${c.keywords || ''}`)) continue
      out.push({ key: c.id, group: 'Commands', icon: <Icon name={c.icon} size={16} />, label: c.label, hint: c.hint, run: c.run })
    }
    // commands first while nothing is typed; library hits first once something is
    return n ? out : out.filter(r => r.group === 'Commands')
  }, [q, found, commands, navigate])
  useEffect(() => setHi(0), [q, found])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined) => { if (!r) return; onClose(); r.run() }
  const searching = dq !== q.trim() || found.q !== dq
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Find a file or a show, or run a command…" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi]) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          {searching && q.trim().length >= 2 ? <span className="spinner sm" /> : <kbd>esc</kbd>}
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">{searching ? 'Searching…' : 'Nothing matches.'}</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow"><span className="pal-label">{r.label}</span>{r.sub && <span className="pal-sub ellipsis">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevron-right" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
      </div>
    </div>, document.body)
}
