// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { useCallback, useEffect, useRef, useState } from 'react'
import { announceChange, useOnChange } from '../lib/events'
import { Outlet, useLocation, useMatch, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp, usePoll } from '../lib/app-context'
import type { Overview } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { Avatar } from './people'
import { type MenuItem, MenuButton, modalOpen, useIsMobile, useMediaQuery, useToast } from './ui'

const SIDEBAR_KEY = 'ps-sidebar'

export default function Layout() {
  const navigate = useNavigate()
  const loc = useLocation()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)

  // live counts for the sidebar: what is syncing, the queue, what needs a person
  const [live, setLive] = useState<Overview | null>(null)
  const loadLive = useCallback(() => { api.get<Overview>(`${V1}/overview`).then(setLive).catch(() => {}) }, [])
  useEffect(loadLive, [loadLive, loc.pathname])
  usePoll(loadLive, 10000)
  useOnChange(loadLive)

  useEffect(() => {
    const open = () => setShortcuts(true)
    window.addEventListener('ps:shortcuts', open)
    return () => window.removeEventListener('ps:shortcuts', open)
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if ((t instanceof Element && t.closest('input, textarea, select, [contenteditable="true"]')) || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'n': go('/nudge'); break
        case 'o': go('/overview'); break
        case 'h': go('/history'); break
        case 'l': go('/library'); break
        case 'i': go('/issues'); break
        case 't': go('/tasks'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const act = async (fn: () => Promise<string>) => { try { toast(await fn()); loadLive(); announceChange() } catch (e) { toast((e as Error).message, 'err') } }
  const commands: PaletteCommand[] = [
    { id: 'nudge', label: 'Nudge a file', icon: 'nudge', hint: 'N', keywords: 'shift offset ms earlier later', run: () => navigate('/nudge') },
    { id: 'overview', label: 'Overview', icon: 'activity', hint: 'O', keywords: 'home dashboard', run: () => navigate('/overview') },
    { id: 'history', label: 'History', icon: 'clock', hint: 'H', keywords: 'runs', run: () => navigate('/history') },
    { id: 'adjusted', label: 'History — adjusted files', icon: 'clock', keywords: 'synced', run: () => navigate('/history?outcome=adjusted') },
    { id: 'library', label: 'Library', icon: 'layers', hint: 'L', keywords: 'shows movies', run: () => navigate('/library') },
    { id: 'pinned', label: 'Library — pinned files', icon: 'pushpin', run: () => navigate('/library?status=pinned') },
    { id: 'issues', label: 'Issues — needs attention', icon: 'alert', hint: 'I', run: () => navigate('/issues') },
    { id: 'rejected', label: 'Issues — rejected downloads', icon: 'x-circle', keywords: 'wrong content machine translation', run: () => navigate('/issues?tab=rejected') },
    { id: 'missing', label: 'Issues — missing subtitles', icon: 'search', keywords: 'bazarr wanted', run: () => navigate('/issues?tab=missing') },
    { id: 'tasks', label: 'Tasks', icon: 'list-checks', hint: 'T', keywords: 'sweep season', run: () => navigate('/tasks') },
    { id: 'queue', label: 'Queue', icon: 'list', keywords: 'waiting syncing', run: () => navigate('/tasks?tab=queue') },
    { id: 'sweep', label: 'Run the nightly sweep now', icon: 'refresh', keywords: 'sweep check library', run: () => act(async () => { await api.post(`${V1}/sweep`); return 'Sweep started' }) },
    { id: 'scan', label: 'Rescan the library', icon: 'folder', keywords: 'scan files', run: () => act(async () => { const r = await api.post<{ files: number; added: number }>(`${V1}/library/scan`); return `${r.files} files, ${r.added} new` }) },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'sync-settings', label: 'Settings — sync & sweep', icon: 'waveform', keywords: 'reading lead gates', run: () => navigate('/settings/sync') },
    { id: 'integrations', label: 'Settings — integrations (Bazarr, Jellyfin)', icon: 'layers', keywords: 'hook post-processing', run: () => navigate('/settings/integrations') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  return (
    <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
      {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
      <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
        <Sidebar onNavigate={closeDrawer} live={live} />
      </div>
      <div className="main">
        <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} />
        <main className="content"><div className="content-inner"><Outlet /></div></main>
      </div>
      {mobile && loc.pathname !== '/nudge' && <button type="button" className="fab" aria-label="Nudge a file" title="Nudge a file" onClick={() => navigate('/nudge')}><Icon name="nudge" size={24} strokeWidth={2} /></button>}
      {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
      {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
    </div>
  )
}

const SECTION: Record<string, string> = { overview: 'Overview', history: 'History', library: 'Library', files: 'Library', nudge: 'Nudge', issues: 'Issues', tasks: 'Tasks' }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const titleMatch = useMatch('/library/t/:title')
  const fileMatch = useMatch('/files/:id')
  const taskMatch = useMatch('/tasks/:id')
  const section = SECTION[loc.pathname.split('/')[1] || ''] || ''
  const crumb = titleMatch ? { to: '/library', label: 'Library', here: titleMatch.params.title || '' }
    : fileMatch ? { to: '/library', label: 'Library', here: 'File' }
    : taskMatch ? { to: '/tasks', label: 'Tasks', here: 'Task' }
    : null
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('ps:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb ? <><button type="button" className="crumb" onClick={() => navigate(crumb.to)}>{crumb.label}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb.here}</span></> : section}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        {user && <Avatar user={user} size={30} />}
      </MenuButton>
    </header>
  )
}
