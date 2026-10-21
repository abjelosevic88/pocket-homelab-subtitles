// Left column: the nudge button, sections, what is syncing, recent nudges — the Pocket Calendar sidebar.
import { useCallback, useEffect, useState } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp, usePoll } from '../lib/app-context'
import type { Nudge, Overview, Subtitle } from '../lib/types'
import { fileTitle, fmtMs } from '../lib/format'
import { Icon } from './icons'
import { useOnChange } from '../lib/events'

export function Sidebar({ onNavigate, live }: { onNavigate: () => void; live: Overview | null }) {
  const { status, settings } = useApp()
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const title = settings?.title || status?.title || 'Pocket Subtitles'
  const s = live?.stats
  const queued = s ? s.queued + s.running : 0
  const nav = [
    { to: '/overview', label: 'Overview', icon: 'activity' },
    { to: '/history', label: 'History', icon: 'clock' },
    { to: '/library', label: 'Library', icon: 'layers' },
    { to: '/issues', label: 'Issues', icon: 'alert', badge: s?.attention || 0, tone: 'warn', tip: 'files whose last run needs a person' },
    { to: '/tasks', label: 'Tasks', icon: 'list-checks', badge: queued, tone: '', tip: 'in the queue' },
  ]
  return (
    <aside className="sidebar" aria-label="Subtitles">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Subtitles' ? title : <>Pocket <b>Subtitles</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^\d/.test(status.version) ? 'v' + status.version : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" onClick={() => { onNavigate(); navigate('/nudge') }}>
        <Icon name="nudge" size={17} strokeWidth={2.2} />Nudge a file<kbd className="kbd-on-primary">N</kbd>
      </button>

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive || (n.to === '/library' && pathname.startsWith('/files/')) ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}{!!n.badge && <span className={`badge ${n.tone}`} title={`${n.badge.toLocaleString()} ${n.tip}`}>{n.badge > 999 ? '999+' : n.badge}</span>}
          </NavLink>
        ))}
      </nav>

      <Group title="Now syncing">
        {!live ? <p className="sb-empty">…</p> : live.running.length === 0
          ? <Link to="/tasks?tab=queue" onClick={onNavigate} className="sb-live idle"><span className="sb-live-dot" /><span className="grow">Idle</span>{live.queued_total > 0 && <span className="sb-meta">{live.queued_total.toLocaleString()} waiting</span>}</Link>
          : live.running.map(j => (
            <Link key={j.id} to="/tasks?tab=queue" onClick={onNavigate} className="sb-live" title={`${fileTitle(j)} — ${j.stage || 'starting'}`}>
              <span className="spinner sm" />
              <span className="grow"><span className="sb-live-name ellipsis">{fileTitle(j)}</span><span className="sb-live-stage ellipsis">{j.stage || 'starting'}{live.queued_total > 0 ? ` · ${live.queued_total.toLocaleString()} waiting` : ''}</span></span>
            </Link>
          ))}
        {live && <p className="sb-note"><Icon name="moon" size={13} />{live.sweep.enabled ? <>Nightly sweep at {live.sweep.time}</> : 'Nightly sweep off'}{live.sweep.running && <span className="badge accent">running</span>}</p>}
      </Group>

      <RecentNudges onNavigate={onNavigate} />

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('ps:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

function Group({ title, children, closed, add }: { title: string; children: React.ReactNode; closed?: boolean; add?: React.ReactNode }) {
  const [open, setOpen] = useState(!closed)
  return (
    <section className="sb-sec">
      <h4 className="sb-h">
        <button type="button" className="sb-h-btn" aria-expanded={open} onClick={() => setOpen(o => !o)}><Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} />{title}</button>
        {add}
      </h4>
      {open && children}
    </section>
  )
}

function RecentNudges({ onNavigate }: { onNavigate: () => void }) {
  const [list, setList] = useState<(Nudge & { file: Subtitle | null })[] | null>(null)
  const load = useCallback(() => { api.get<(Nudge & { file: Subtitle | null })[]>(`${V1}/nudges`).then(setList).catch(() => setList([])) }, [])
  useEffect(load, [load])
  useOnChange(load)
  usePoll(load, 60000)
  const seen = new Set<number>()
  const rows = (list || []).filter(n => !n.undone && n.file && !seen.has(n.file.id) && !!seen.add(n.file.id)).slice(0, 6)
  return (
    <Group title="Recent nudges">
      {list && rows.length === 0 ? <p className="sb-empty">None yet. A nudge pins the file so the sweep keeps your timing.</p> : (
        <ul className="cal-list">
          {rows.map(n => (
            <li key={n.id} className="cal-row">
              <NavLink to={`/files/${n.file!.id}`} onClick={onNavigate} className={({ isActive }) => `cal-check ${isActive ? 'is-active' : ''}`} title={n.file!.rel}>
                <Icon name="pushpin" size={14} className="sb-pin" />
                <span className="cal-name ellipsis">{fileTitle(n.file!)}</span>
                <span className="sb-meta mono" title="Net manual shift">{fmtMs(n.file!.nudge_ms || n.ms)}</span>
              </NavLink>
            </li>
          ))}
        </ul>
      )}
    </Group>
  )
}
