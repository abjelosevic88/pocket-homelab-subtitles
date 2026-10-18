import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { Avatar } from './people'

const nav = [
  { to: '/overview', label: 'Overview', ico: '◉', tab: true },
  { to: '/history', label: 'History', ico: '☰', tab: true },
  { to: '/library', label: 'Library', ico: '▤', tab: true },
  { to: '/nudge', label: 'Nudge', ico: '±', tab: true },
  { to: '/issues', label: 'Issues', ico: '⚑', tab: false },
  { to: '/tasks', label: 'Tasks', ico: '⟳', tab: false },
  { to: '/settings', label: 'Settings', ico: '⚙', tab: true },
]

export default function Layout() {
  const { user, settings, status, theme, toggleTheme, setUser } = useApp()
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const logout = async () => { await api.post(`${V1}/auth/logout`); setUser(null); navigate('/login') }
  const title = settings?.title || 'Pocket Subtitles'
  return (
    <div className="app">
      <aside className={`sidebar ${open ? 'open' : ''}`} onClick={() => setOpen(false)}>
        <div className="brand"><span className="logo"><img src="/favicon.svg" alt="" /></span><span className="brand-name">{title}</span></div>
        <nav>
          {nav.map(n => <NavLink key={n.to} to={n.to} className={({ isActive }) => isActive ? 'active' : ''}><span className="ico">{n.ico}</span>{n.label}</NavLink>)}
        </nav>
        <div className="foot">
          <span className="row" title={user?.email}>{user && <Avatar user={user} />}<span className="foot-id"><span>{user?.name || user?.email}</span>{status?.version && <a className="ver" href="https://github.com/abjelosevic88/pocket-homelab-subtitles/releases" target="_blank" rel="noreferrer" title="Version — click for release notes">v{status.version.replace(/^v/, '')}</a>}</span></span>
          <span className="row">
            <button className="btn ghost sm" onClick={toggleTheme} title="Toggle theme">{theme === 'dark' ? '☀' : '☾'}</button>
            {status?.auth_mode === 'local' && <button className="btn ghost sm" onClick={logout} title="Sign out">⎋</button>}
          </span>
        </div>
      </aside>
      <div className="main">
        <header className="topbar">
          <button className="btn ghost icon menu-btn" onClick={() => setOpen(o => !o)} aria-label="Menu">☰</button>
          <span className="topbar-title">{title}</span>
          <span className="spacer" />
          <button className="btn ghost sm" onClick={toggleTheme} title="Toggle theme">{theme === 'dark' ? '☀' : '☾'}</button>
        </header>
        <main className="content"><Outlet /></main>
        <nav className="tabbar">
          {nav.filter(n => n.tab).map(n => <NavLink key={n.to} to={n.to} className={({ isActive }) => isActive ? 'active' : ''}><span className="ico">{n.ico}</span><span>{n.label}</span></NavLink>)}
        </nav>
      </div>
    </div>
  )
}

/** A waveform over two subtitle lines: text, in time with the sound. */
export function Logo() {
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden>
      <rect x="5" y="5.5" width="1.6" height="4" rx=".8" opacity=".8" />
      <rect x="8" y="3.5" width="1.6" height="8" rx=".8" />
      <rect x="11" y="4.7" width="1.6" height="5.6" rx=".8" opacity=".8" />
      <rect x="14" y="2.7" width="1.6" height="9.6" rx=".8" />
      <rect x="17" y="6" width="1.6" height="3" rx=".8" opacity=".8" />
      <rect x="3" y="14" width="18" height="2.4" rx="1.2" />
      <rect x="6" y="18.4" width="12" height="2.4" rx="1.2" opacity=".8" />
    </svg>
  )
}
