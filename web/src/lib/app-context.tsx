import React, { createContext, useCallback, useContext, useEffect, useState } from 'react'
import { api, V1 } from './api'
import type { Settings, User } from './types'
import { setDateFormats, type DateFormat, type TimeFormat } from './format'

interface AuthStatus { needs_setup: boolean; auth_mode: string; authenticated: boolean; user: User | null; version: string; title: string; oidc?: boolean; oidc_name?: string }

interface AppState {
  status: AuthStatus | null
  user: User | null
  settings: Settings | null
  users: User[]
  theme: 'light' | 'dark'
  toggleTheme: () => void
  refresh: () => Promise<void>
  reloadUsers: () => Promise<void>
  setUser: (u: User | null) => void
  setSettings: (s: Settings) => void
}

const Ctx = createContext<AppState>(null!)
export function useApp() { return useContext(Ctx) }

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [user, setUser] = useState<User | null>(null)
  const [settings, setSettings] = useState<Settings | null>(null)
  const [users, setUsers] = useState<User[]>([])
  const [theme, setTheme] = useState<'light' | 'dark'>(() => (localStorage.getItem('pst-theme') as 'light' | 'dark') || (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'))

  useEffect(() => { document.documentElement.dataset.theme = theme; localStorage.setItem('pst-theme', theme) }, [theme])
  useEffect(() => { if (settings?.title) document.title = settings.title }, [settings?.title])
  useEffect(() => { if (settings) setDateFormats((settings.date_format || 'auto') as DateFormat, (settings.time_format || 'auto') as TimeFormat) }, [settings?.date_format, settings?.time_format]) // eslint-disable-line react-hooks/exhaustive-deps

  const reloadUsers = useCallback(async () => { setUsers(await api.get<User[]>(`${V1}/users`)) }, [])

  const refresh = useCallback(async () => {
    const st = await api.get<AuthStatus>(`${V1}/auth/status`)
    setStatus(st)
    setUser(st.user)
    if (st.authenticated) {
      setSettings(await api.get<Settings>(`${V1}/settings`))
      await reloadUsers()
    }
  }, [reloadUsers])

  useEffect(() => { refresh().catch(() => setStatus({ needs_setup: false, auth_mode: 'local', authenticated: false, user: null, version: '', title: 'Pocket Subtitles' })) }, [refresh])
  useEffect(() => {
    const h = () => { setUser(null); setStatus(s => s ? { ...s, authenticated: false, user: null } : s) }
    window.addEventListener('ps:unauthorized', h)
    return () => window.removeEventListener('ps:unauthorized', h)
  }, [])

  return <Ctx.Provider value={{ status, user, settings, users, theme, toggleTheme: () => setTheme(t => t === 'dark' ? 'light' : 'dark'), refresh, reloadUsers, setUser, setSettings }}>{children}</Ctx.Provider>
}

/** Re-run fn every ms while the tab is visible (live pages). */
export function usePoll(fn: () => void, ms: number, deps: unknown[] = []) {
  useEffect(() => {
    let t: ReturnType<typeof setInterval> | undefined
    const start = () => { stop(); t = setInterval(() => { if (!document.hidden) fn() }, ms) }
    const stop = () => { if (t) clearInterval(t) }
    const vis = () => { if (!document.hidden) fn() }
    start()
    document.addEventListener('visibilitychange', vis)
    return () => { stop(); document.removeEventListener('visibilitychange', vis) }
  }, deps) // eslint-disable-line react-hooks/exhaustive-deps
}
