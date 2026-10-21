import React, { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, ConnMode, Settings as SettingsT, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime, fmtTime, fmtYMD, type DateFormat, type TimeFormat } from '../lib/format'
import { Confirm, Field, Loading, Modal, Segmented, SettingRow, Switch, Tabs, copyText, useAsync, useIsMobile, useToast } from '../components/ui'
import { Avatar } from '../components/people'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'

type TabId = 'sync' | 'integrations' | 'notifications' | 'general' | 'profile' | 'users' | 'api' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string }[] = [
  { id: 'sync', label: 'Sync & sweep', icon: 'waveform', sub: 'How a file is timed, which downloads are rejected, and the nightly check of the whole library.' },
  { id: 'integrations', label: 'Integrations', icon: 'layers', sub: 'Where the subtitles come from, the command Bazarr runs after every download, and who is told when a file changes.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'Pushes for failures, adjusted files and the sweep summary — through Apprise, ntfy or a webhook.' },
  { id: 'general', label: 'General', icon: 'settings', sub: 'The app title and how dates and times are written.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'users', label: 'Users', icon: 'users', sub: 'Everyone who can sign in to this app.' },
  { id: 'api', label: 'API tokens', icon: 'key', sub: 'Personal tokens for Bazarr\'s hook, a Homepage widget, scripts and the Pocket Homelab app.' },
  { id: 'data', label: 'Backup & data', icon: 'database', sub: 'Download the database; what the app keeps on disk.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, the server and the sync engine.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  // old links: /settings?tab=sync → /settings/sync
  const legacy = new URLSearchParams(window.location.search).get('tab') || ''
  const want = raw || legacy
  const tab: TabId = TABS.find(t => t.id === want)?.id || 'sync'
  const current = TABS.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  useEffect(() => { if (!raw && legacy) navigate(`/settings/${tab}`, { replace: true }) }, [raw, legacy, tab, navigate])

  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])

  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to Subtitles" title="Back to Subtitles (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={TABS.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {TABS.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title">
              <h2>{current.label}</h2>
              <p>{current.sub}</p>
            </div>
            {tab === 'sync' && <Sync />}
            {tab === 'integrations' && <Integrations />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'general' && <General />}
            {tab === 'profile' && <Profile />}
            {tab === 'users' && <Users />}
            {tab === 'api' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

/** A settings card: title, explanation, actions on the right — Pocket Calendar's section. */
function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico"><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

type Draft = SettingsT & { bazarr_api_key?: string | null; jellyfin_api_key?: string | null }

/** Shared form state for the settings sections: edit a draft, save the whole thing. */
function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<Draft | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async () => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, f); setSettings(s); setF(s); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setF(x => x && { ...x, [k]: v })
  /** Saves a change right away (the connection switch, Disconnect). */
  const saveNow = async (patch: Partial<Draft>, msg: string) => {
    if (!f) return
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast(msg) } catch (e) { toast((e as Error).message, 'err') }
  }
  return { f, set, save, saveNow, busy, isAdmin: user?.role === 'admin' }
}

function SaveBar({ save, busy, isAdmin }: { save: () => void; busy: boolean; isAdmin: boolean }) {
  return isAdmin ? <div className="row"><button type="button" className="btn primary" onClick={save} disabled={busy}>{busy ? 'Saving…' : 'Save changes'}</button></div> : null
}

function AdminOnly({ isAdmin }: { isAdmin: boolean }) {
  return isAdmin ? null : <div className="callout info"><Icon name="lock" size={15} /><span>Only an admin can change these.</span></div>
}

function Num({ value, onChange, unit, disabled, min, max, step, width = 96 }: { value: number; onChange: (n: number) => void; unit: string; disabled?: boolean; min?: number; max?: number; step?: number; width?: number }) {
  return <div className="row"><input type="number" className="tnum" style={{ width }} step={step} min={min} max={max} value={value} onChange={e => onChange(Number(e.target.value))} disabled={disabled} /><span className="muted small">{unit}</span></div>
}

function Sync() {
  const { f, set, save, busy, isAdmin } = useSettingsForm()
  const plan = useAsync(() => api.get<Record<string, number>>(`${V1}/sweep/plan`), [])
  if (!f) return <Loading />
  const d = !isAdmin
  return (
    <div className="stack loose">
      <AdminOnly isAdmin={isAdmin} />
      <Section title="Timing" icon="clock">
        <SettingRow title="Reading lead" help="Audio alignment puts a cue exactly on the first syllable, which can read as early. A lead moves every cue this much after the voice (0 = pure audio timing; 300 ms suits many people). Changing it re-times files on their next run.">
          <Num value={f.lead_ms} onChange={v => set('lead_ms', v)} unit="ms" step={50} min={-2000} max={2000} disabled={d} />
        </SettingRow>
        <SettingRow title="Use the centre channel when it hears dialogue better" help="On 5.1 audio the dialogue sits in the centre channel; whispered scenes under music survive there. Chosen per video, only when it beats the full mix.">
          <Switch checked={f.centre_ref} onChange={v => set('centre_ref', v)} disabled={d} label="Use the centre channel" />
        </SettingRow>
        <SettingRow title="Polish short displaced blocks" help="Re-measures each cue with its neighbours and moves a block only when several independent checks agree.">
          <Switch checked={f.polish} onChange={v => set('polish', v)} disabled={d} label="Polish short displaced blocks" />
        </SettingRow>
        <SettingRow title="Re-measure embedded-track results against the audio" help="Off by default: it helps when the release's own track drifts, but on quiet acts it can measure noise.">
          <Switch checked={f.ref_residual} onChange={v => set('ref_residual', v)} disabled={d} label="Re-measure embedded-track results" />
        </SettingRow>
      </Section>
      <Section title="Quality gates" icon="shield">
        <SettingRow title="Reject subtitles whose text belongs to another video" help="After alignment the cues must actually sit on detected speech — a subtitle for the wrong episode can measure perfectly in sync.">
          <Switch checked={f.content_gate} onChange={v => set('content_gate', v)} disabled={d} label="Reject wrong content" />
        </SettingRow>
        <SettingRow title="Reject machine translations of fresh downloads in" help="Language codes, comma separated. The heuristic (no translator credit, many fragment cues, short lines, no dialogue dashes) is tuned on Serbian, Croatian and Bosnian.">
          <input value={f.mt_gate_langs} onChange={e => set('mt_gate_langs', e.target.value)} placeholder="sr,hr,bs" disabled={d} style={{ width: 160 }} />
        </SettingRow>
        <SettingRow stack title="A rejected download is…" help={f.on_reject === 'blacklist' ? 'Bazarr bans that upload and searches for a replacement right away; the file leaves the library (a copy is kept).' : f.on_reject === 'quarantine' ? 'The file leaves the library (a copy is kept); search for another one yourself.' : 'Nothing is removed; the file shows under Issues.'}>
          <Segmented value={f.on_reject} onChange={v => !d && set('on_reject', v)} label="A rejected download is" options={[{ value: 'blacklist', label: 'Blacklisted in Bazarr' }, { value: 'quarantine', label: 'Moved out' }, { value: 'keep', label: 'Kept, just flagged' }]} />
        </SettingRow>
        <SettingRow title="Convert Serbian Cyrillic to Latin for" help="Language codes. Subtitle packs often ship both scripts and Bazarr picks one at random.">
          <input value={f.latinize_langs} onChange={e => set('latinize_langs', e.target.value)} placeholder="sr" disabled={d} style={{ width: 160 }} />
        </SettingRow>
      </Section>
      <Section title="Nightly sweep" icon="moon" sub={plan.data ? <>Right now a sweep would check <b>{plan.data.todo}</b> files ({plan.data.unchanged} unchanged, {plan.data.pinned} pinned{plan.data.no_video ? `, ${plan.data.no_video} without a video` : ''}).</> : undefined}>
        <SettingRow title="Sweep the library every night" help="Checks every file whose subtitle or video changed since its last verdict — including files that did not come through Bazarr.">
          <Switch checked={f.sweep_enabled} onChange={v => set('sweep_enabled', v)} disabled={d} label="Sweep the library every night" />
        </SettingRow>
        <SettingRow title="Start at"><input type="time" className="set-time" value={f.sweep_time} onChange={e => set('sweep_time', e.target.value)} disabled={d} /></SettingRow>
        <SettingRow title="Time budget" help="Leftovers wait for the next night."><Num value={f.sweep_budget} onChange={v => set('sweep_budget', v)} unit="min" min={5} max={1440} disabled={d} /></SettingRow>
        <SettingRow title="Re-check verdicts older than" help="Optional. After an engine improvement, set a date and older verdicts get re-measured, a few hours each night.">
          <input type="date" value={f.reverify_before} onChange={e => set('reverify_before', e.target.value)} disabled={d} style={{ width: 170 }} />
        </SettingRow>
        <SettingRow title="Only manage these languages" help="Empty = every subtitle in the library.">
          <input value={f.languages} onChange={e => set('languages', e.target.value)} placeholder="e.g. en,sr" disabled={d} style={{ width: 160 }} />
        </SettingRow>
        <SettingRow title="Unpin a file when Bazarr downloads a new one in its place" help="A pin protects hand-made timing of one particular file; a new download is a different subtitle.">
          <Switch checked={f.auto_unpin_new} onChange={v => set('auto_unpin_new', v)} disabled={d} label="Unpin on a new download" />
        </SettingRow>
        <SettingRow title="Independent check tolerance" help="Verify passes when the whole-file offset is within this of the reading lead.">
          <Num value={f.verify_tol_ms} onChange={v => set('verify_tol_ms', v)} unit="ms" min={100} max={5000} step={50} disabled={d} />
        </SettingRow>
      </Section>
      <SaveBar save={save} busy={busy} isAdmin={isAdmin} />
    </div>
  )
}

const connBadge = (mode: ConnMode, via?: string) => mode === 'off' ? <span className="badge">off</span>
  : via ? <span className="badge ok">{via === 'core' ? 'Pocket Core' : 'direct'}</span>
  : <span className="badge warn">not connected</span>

/** Pocket Core | Off for Bazarr or Jellyfin, with Direct as an advanced option when the
 * box has a Pocket Core (always shown while it is the current mode, and shown normally
 * without a Core); the switch saves at once. */
function Connection({ app, mode, via, source, detail, as, core, disabled, onMode, onDisconnect, hasKey, test, direct }: {
  app: string; mode: ConnMode; via?: string; source?: string; detail?: string; as?: string; core: boolean; disabled: boolean
  onMode: (m: ConnMode) => void; onDisconnect: () => void; hasKey: boolean; test: React.ReactNode; direct: React.ReactNode
}) {
  const [advanced, setAdvanced] = useState(false)
  const showDirect = !core || mode === 'direct' || advanced
  const ok = !!via
  const whose = source === 'personal' ? " (the admin's own key)" : source === 'box' ? " (the box's key)" : ''
  const line = mode === 'off' ? `Off: Pocket Subtitles does not talk to ${app}.`
    : mode === 'core' ? (ok ? `Connected through Pocket Core${whose}, as the admin ${as || ''}. ${app} is box-wide, so the app always uses that admin's ${app} access from Core; nothing to fill in.` : `Not connected: ${detail}.`)
    : (ok ? `Direct connection with the URL and API key below.` : `Not connected: ${detail || 'add the URL and an API key'}.`)
  return (
    <div className="conn-block">
      <div className="conn-mode"><Segmented size="sm" value={mode} onChange={m => !disabled && onMode(m)} label={`${app} connection`} options={[
        { value: 'core' as const, label: 'Pocket Core', title: core ? `Through Pocket Core, as the admin ${as || ''}` : 'There is no Pocket Core on this box' },
        { value: 'direct' as const, label: 'Direct', title: 'A URL and API key saved here' },
        { value: 'off' as const, label: 'Off' },
      ].filter(o => o.value !== 'direct' || showDirect)} />
        {!showDirect && !disabled && <button type="button" className="conn-advanced" onClick={() => setAdvanced(true)}>Advanced: connect directly with a URL and key</button>}
      </div>
      <div className={`conn-status ${mode === 'off' ? '' : ok ? 'ok' : 'bad'}`}><span className="dot" />{line}</div>
      {mode === 'direct' && <>
        <div className="set-app-direct">{direct}</div>
        <div className="row">{test}{hasKey && !disabled && <button type="button" className="btn ghost sm danger-text" onClick={onDisconnect} title="Remove the saved URL and API key">Disconnect</button>}</div>
      </>}
      {mode === 'core' && <div className="row">{test}</div>}
    </div>
  )
}

function Integrations() {
  const { f, set, save, saveNow, busy, isAdmin } = useSettingsForm()
  const { toast } = useToast()
  const [testing, setTesting] = useState('')
  if (!f) return <Loading />
  const d = !isAdmin
  const test = async (what: string) => {
    setTesting(what)
    try { await save(); const r = await api.post<{ message: string }>(`${V1}/settings/test/${what}`); toast(r.message) } catch (e) { toast((e as Error).message, 'err') } finally { setTesting('') }
  }
  const modeMsg = (app: string, m: ConnMode) => m === 'off' ? `${app} switched off` : m === 'core' ? `${app} through Pocket Core` : `Direct ${app} connection`
  const core = !!f.core_configured
  return (
    <div className="stack loose">
      <AdminOnly isAdmin={isAdmin} />
      <Section title="Bazarr" icon="captions" actions={connBadge(f.bazarr_mode, f.bazarr_via)} sub="Optional, but this is where the subtitles come from. Connected, the app shows what is still missing, blacklists rejected downloads so Bazarr fetches another, and can reset a whole season.">
        <div className="stack">
          <Connection app="Bazarr" mode={f.bazarr_mode} via={f.bazarr_via} source={f.bazarr_source} detail={f.bazarr_detail} as={f.services_as} core={core} disabled={d}
            hasKey={f.bazarr_api_key_set || !!f.bazarr_url}
            onMode={m => saveNow({ bazarr_mode: m }, modeMsg('Bazarr', m))}
            onDisconnect={() => saveNow({ bazarr_url: '', bazarr_api_key: '' }, 'Bazarr URL and API key removed')}
            test={<button type="button" className="btn" onClick={() => test('bazarr')} disabled={d || testing !== ''}>{testing === 'bazarr' ? 'Testing…' : f.bazarr_mode === 'direct' ? 'Save & test' : 'Test connection'}</button>}
            direct={<>
              <Field label="URL" help="As this app reaches it, e.g. http://bazarr:6767"><input value={f.bazarr_url} onChange={e => set('bazarr_url', e.target.value)} placeholder="http://bazarr:6767" disabled={d} /></Field>
              <Field label="API key" help={f.bazarr_api_key_set ? 'A key is saved. Type to replace it.' : 'Bazarr → Settings → General → Security.'}><input type="password" autoComplete="off" value={f.bazarr_api_key ?? ''} onChange={e => set('bazarr_api_key', e.target.value)} placeholder={f.bazarr_api_key_set ? '••••••••' : ''} disabled={d} /></Field>
            </>} />
          {f.bazarr_mode !== 'off' && <Field label="Path mapping" help="Only if Bazarr sees the library under another path: bazarr-path=this-app-path, comma separated."><input value={f.bazarr_path_map} onChange={e => set('bazarr_path_map', e.target.value)} placeholder="/tv=/media/shows,/movies=/media/movies" disabled={d} /></Field>}
          {f.bazarr_mode !== 'off' && <SaveBar save={save} busy={busy} isAdmin={isAdmin} />}
        </div>
      </Section>
      <HookCommand />
      <Section title="Season tools" icon="tv" sub="How Library → a show → Reset… downloads a season again through Bazarr.">
        <SettingRow title="Languages a season reset downloads" help="Empty = the languages the season had before."><input value={f.season_langs} onChange={e => set('season_langs', e.target.value)} placeholder="en" disabled={d} style={{ width: 160 }} /></SettingRow>
        <SettingRow title="Use the manual provider search for" help="The manual search also offers lower-scoring human subtitles that Bazarr's automatic search refuses."><input value={f.season_manual_langs} onChange={e => set('season_manual_langs', e.target.value)} placeholder="e.g. sr" disabled={d} style={{ width: 160 }} /></SettingRow>
        <SettingRow title="Preferred provider" help="Ranked first in manual searches, e.g. titlovi or opensubtitlescom."><input value={f.prefer_provider} onChange={e => set('prefer_provider', e.target.value)} disabled={d} style={{ width: 160 }} /></SettingRow>
        <div className="mt-s"><SaveBar save={save} busy={busy} isAdmin={isAdmin} /></div>
      </Section>
      <Section title="Jellyfin / Emby" icon="video" actions={connBadge(f.jellyfin_mode, f.jellyfin_via)} sub="Optional. After a file changes, the server is told to rescan that folder — network mounts send no change events, so without it the new timing appears only after the next scheduled scan.">
        <div className="stack">
          <Connection app="Jellyfin" mode={f.jellyfin_mode} via={f.jellyfin_via} source={f.jellyfin_source} detail={f.jellyfin_detail} as={f.services_as} core={core} disabled={d}
            hasKey={f.jellyfin_api_key_set || !!f.jellyfin_url}
            onMode={m => saveNow({ jellyfin_mode: m }, modeMsg('Jellyfin', m))}
            onDisconnect={() => saveNow({ jellyfin_url: '', jellyfin_api_key: '' }, 'Jellyfin URL and API key removed')}
            test={<button type="button" className="btn" onClick={() => test('jellyfin')} disabled={d || testing !== ''}>{testing === 'jellyfin' ? 'Testing…' : f.jellyfin_mode === 'direct' ? 'Save & test' : 'Test connection'}</button>}
            direct={<>
              <Field label="URL"><input value={f.jellyfin_url} onChange={e => set('jellyfin_url', e.target.value)} placeholder="http://jellyfin:8096" disabled={d} /></Field>
              <Field label="API key" help={f.jellyfin_api_key_set ? 'A key is saved. Type to replace it.' : 'Dashboard → API keys.'}><input type="password" autoComplete="off" value={f.jellyfin_api_key ?? ''} onChange={e => set('jellyfin_api_key', e.target.value)} placeholder={f.jellyfin_api_key_set ? '••••••••' : ''} disabled={d} /></Field>
            </>} />
          {f.jellyfin_mode !== 'off' && <Field label="Path mapping" help="If Jellyfin sees the library elsewhere: this-app-path=jellyfin-path."><input value={f.jellyfin_path_map} onChange={e => set('jellyfin_path_map', e.target.value)} placeholder="/media=/data/media" disabled={d} /></Field>}
          {f.jellyfin_mode !== 'off' && <SaveBar save={save} busy={busy} isAdmin={isAdmin} />}
        </div>
      </Section>
    </div>
  )
}

/** The one line to paste into Bazarr so every download comes here. */
function HookCommand() {
  const { toast } = useToast()
  const [url, setUrl] = useState(window.location.origin)
  const [token, setToken] = useState('')
  const cmd = `curl -fsS -m 20 -H "Authorization: Bearer ${token || '<token>'}" --data-urlencode video={{episode}} --data-urlencode subtitle={{subtitles}} --data-urlencode language={{subtitles_language_code2}} --data-urlencode provider={{provider}} --data-urlencode subtitle_id={{subtitle_id}} --data-urlencode series_id={{series_id}} --data-urlencode episode_id={{episode_id}} ${url.replace(/\/$/, '')}/api/v1/hooks/bazarr`
  const mint = async () => { try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: 'Bazarr hook' }); setToken(r.token); toast('Token created — it is in the command below') } catch (e) { toast((e as Error).message, 'err') } }
  const copy = async () => toast(await copyText(cmd) ? 'Copied' : 'Select and copy it by hand', 'ok')
  return (
    <Section title="Connect Bazarr's downloads" icon="terminal" sub="Bazarr's custom post-processing hands every download to the queue here — one curl, it returns at once.">
      <div className="stack">
        <ol className="set-steps">
          <li>Bazarr → Settings → Subtitles → <b>Custom post-processing</b>: turn it on.</li>
          <li>Make sure Bazarr's own <b>“Automatic subtitles synchronization”</b> is off (this app does it, better).</li>
          <li>Paste this command. Bazarr waits only a moment; the sync runs here.</li>
        </ol>
        <Field label="URL Bazarr reaches this app at" help="From inside Bazarr's container: the service name on a shared network, or the host's address."><input value={url} onChange={e => setUrl(e.target.value)} /></Field>
        <pre className="cmd">{cmd}</pre>
        <div className="row wrap">{!token && <button type="button" className="btn" onClick={mint}><Icon name="key" size={15} />Create a token for Bazarr</button>}<button type="button" className="btn primary" onClick={copy}><Icon name="copy" size={15} />Copy command</button></div>
      </div>
    </Section>
  )
}

function Notifications() {
  const { f, set, save, busy, isAdmin } = useSettingsForm()
  const { toast } = useToast()
  const [testing, setTesting] = useState(false)
  if (!f) return <Loading />
  const d = !isAdmin
  const test = async () => {
    setTesting(true)
    try { await save(); const r = await api.post<{ message: string }>(`${V1}/settings/test/notify`); toast(r.message) } catch (e) { toast((e as Error).message, 'err') } finally { setTesting(false) }
  }
  return (
    <div className="stack loose">
      <AdminOnly isAdmin={isAdmin} />
      <Section title="Where to send" icon="bell">
        <SettingRow stack title="Send to">
          <Segmented value={f.notify_kind} onChange={v => !d && set('notify_kind', v)} label="Send to" options={[{ value: '', label: 'Off' }, { value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'webhook', label: 'Webhook' }]} />
        </SettingRow>
        {f.notify_kind && <SettingRow stack title="URL" help={f.notify_kind === 'apprise' ? 'Apprise API, e.g. http://apprise:8000/notify/subtitles' : f.notify_kind === 'ntfy' ? 'Topic URL, e.g. https://ntfy.sh/my-topic' : 'Receives JSON {title, body, level}.'}>
          <input value={f.notify_url} onChange={e => set('notify_url', e.target.value)} disabled={d} aria-label="Notification URL" />
        </SettingRow>}
      </Section>
      {f.notify_kind && <Section title="What to send">
        <SettingRow title="Failures and rejections"><Switch checked={f.notify_failures} onChange={v => set('notify_failures', v)} disabled={d} label="Failures and rejections" /></SettingRow>
        <SettingRow title="Every adjusted file"><Switch checked={f.notify_synced} onChange={v => set('notify_synced', v)} disabled={d} label="Every adjusted file" /></SettingRow>
        <SettingRow title="Sweep summary"><Switch checked={f.notify_sweep} onChange={v => set('notify_sweep', v)} disabled={d} label="Sweep summary" /></SettingRow>
      </Section>}
      {isAdmin && <div className="row wrap">
        <button type="button" className="btn primary" onClick={save} disabled={busy}>{busy ? 'Saving…' : 'Save changes'}</button>
        {f.notify_kind && <button type="button" className="btn" onClick={test} disabled={testing}><Icon name="bell" size={15} />{testing ? 'Sending…' : 'Save & send a test'}</button>}
      </div>}
    </div>
  )
}

function General() {
  const { f, set, save, busy, isAdmin } = useSettingsForm()
  if (!f) return <Loading />
  const now = new Date()
  return (
    <div className="stack loose">
      <AdminOnly isAdmin={isAdmin} />
      <Section title="Preferences">
        <SettingRow title="App title" help="Shown in the sidebar and the browser tab."><input value={f.title} onChange={e => set('title', e.target.value)} maxLength={40} disabled={!isAdmin} style={{ width: 220 }} /></SettingRow>
        <SettingRow title="Date format" help={`Today: ${fmtYMD(now.getFullYear(), now.getMonth() + 1, now.getDate(), true, f.date_format as DateFormat)}`}>
          <Select value={f.date_format || 'auto'} onChange={v => set('date_format', v)} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} disabled={!isAdmin} ariaLabel="Date format" />
        </SettingRow>
        <SettingRow title="Time format" help={`Now: ${fmtTime(now, f.time_format as TimeFormat)}`}>
          <Select value={f.time_format || 'auto'} onChange={v => set('time_format', v)} options={TIME_FORMATS.map(t => ({ value: t.id, label: t.label }))} disabled={!isAdmin} ariaLabel="Time format" />
        </SettingRow>
      </Section>
      <SaveBar save={save} busy={busy} isAdmin={isAdmin} />
    </div>
  )
}

function Users() {
  const { user, users, reloadUsers, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); await reloadUsers(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Section title="People" icon="users" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={14} />Add user</button>}
      sub="Members can look around, nudge, pin and sync files. Admins also change settings, reset seasons and manage users.">
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar user={u} size={34} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge accent">admin</span>}</div>
              <div className="muted small ellipsis">{u.email}</div>
            </div>
            {isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit; passwords are not used here.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadUsers() }} />}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name || del.email}?`} message="Their account and tokens are deleted. What they did stays in the history." onCancel={() => setDel(null)} onConfirm={() => { const x = del; setDel(null); run(() => api.del(`${V1}/users/${x.id}`), 'User removed') }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try {
      if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f)
      toast(user.id ? 'User saved' : 'User added'); onSaved()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit user' : 'Add user'} onClose={onClose} size="sm">
      <form onSubmit={save} className="stack">
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} label="Role" options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="submit" className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add user'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={save} className="stack loose">
      <Section title="You" icon="user">
        <div className="set-profile">
          {user && <Avatar user={user} size={56} />}
          <div className="stack grow">
            <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
            <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
          </div>
        </div>
      </Section>
      {status?.auth_mode === 'local' && <Section title="Password" icon="lock">
        <div className="stack">
          <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
          <Field label="New password" help="At least 8 characters. Leave empty to keep the current one."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
        </div>
      </Section>}
      <div className="row"><button type="submit" className="btn primary" disabled={busy}>{busy ? 'Saving…' : 'Save profile'}</button></div>
    </form>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Section title="Personal tokens" icon="key" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you. Bazarr's post-processing hook uses one; so can a Homepage widget (<code>GET /api/v1/widget</code>) or a script queueing a file (<code>POST /api/v1/sync</code>). See <code>docs/api.md</code>.</>}>
      <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. Homepage)" aria-label="Token name" /><button type="submit" className="btn primary"><Icon name="plus" size={15} />Create</button></form>
      {fresh && <div className="set-fresh mb"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><pre className="mono">{fresh}</pre></div>}
      {data && data.length > 0 ? (
        <div className="set-people">
          {data.map(t => (
            <div key={t.id} className="set-person">
              <span className="set-token-ico"><Icon name="key" size={16} /></span>
              <div className="grow">
                <div className="set-person-name">{t.name}<code className="muted small">{t.prefix}…</code></div>
                <div className="muted small">Last used {t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'} · created {fmtDateTime(t.created_at)}</div>
              </div>
              <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button>
            </div>
          ))}
        </div>
      ) : <p className="muted">No tokens yet.</p>}
      {del && <Confirm confirmLabel="Revoke" title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
    </Section>
  )
}

function Data() {
  const { data } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  return (
    <div className="stack loose">
      <Section title="Database" icon="database" sub={<>A consistent copy of the SQLite database (<code>VACUUM INTO</code>): history, verdicts, pins, nudges, users and settings. Restore by stopping the app and replacing <code>pocket-subtitles.db</code>.</>}>
        <a className="btn primary" href={`${V1}/backup`}><Icon name="download" size={15} />Download database</a>
      </Section>
      <Section title="On disk" icon="folder">
        {data ? (
          <dl className="set-dl">
            <div><dt>Originals & nudge backups</dt><dd><code>{String(data.backup_dir)}</code><div className="muted small">{Number(data.backup_files).toLocaleString()} files · {fmtBytes(Number(data.backup_bytes))}</div></dd></div>
            <div><dt>Measurement cache</dt><dd><code>{String(data.cache_dir)}</code><div className="muted small">{Number(data.cache_files).toLocaleString()} files · {fmtBytes(Number(data.cache_bytes))} — rebuildable, skip it in backups if space matters</div></dd></div>
          </dl>
        ) : <Loading />}
      </Section>
    </div>
  )
}

function About() {
  const { data } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const doctor = useAsync(() => api.get<Record<string, unknown>>(`${V1}/doctor`), [])
  const counts = (data?.counts || {}) as Record<string, number>
  const dd = doctor.data
  const tiles: [string, number | undefined, string][] = [['Files', counts.subtitles, 'captions'], ['Runs', counts.runs, 'activity'], ['Nudges', counts.nudges, 'nudge'], ['Users', counts.users, 'users']]
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Subtitles</h3>
          <p className="muted small">Keeps subtitles in sync with the audio: every download from Bazarr is measured and re-timed when it is off, the library is swept each night, and anything can be nudged by hand. Open source (MIT); alignment by ffsubsync and alass — part of the Pocket apps.</p>
        </div>
      </div>
      {data && <div className="set-stats">
        {tiles.map(([label, n, icon]) => (
          <div key={label} className="set-stat"><Icon name={icon} size={16} /><span className="set-stat-n tnum">{(n ?? 0).toLocaleString()}</span><span className="set-stat-l">{label}</span></div>
        ))}
      </div>}
      <Section title="This server">
        {data ? (
          <dl className="set-dl">
            <div><dt>Version</dt><dd><code>{String(data.version)}</code></dd></div>
            <div><dt>Runtime</dt><dd>{String(data.go)} · {String(data.os)}/{String(data.arch)} · {String(data.workers)} worker(s)</dd></div>
            <div><dt>Media folders</dt><dd><code>{(data.media_dirs as string[]).join(', ')}</code></dd></div>
            <div><dt>Auth mode</dt><dd>{String(data.auth_mode)}{data.legacy_api !== 'off' && ` · legacy API ${String(data.legacy_api)}`}</dd></div>
            <div><dt>Database</dt><dd><code>{String(data.db_path)}</code> · {fmtBytes(Number(data.db_bytes))}</dd></div>
            <div><dt>Time zone</dt><dd>{String(data.timezone)}</dd></div>
            <div><dt>Uptime</dt><dd>{String(data.uptime)}</dd></div>
          </dl>
        ) : <Loading />}
      </Section>
      <Section title="Sync engine" icon="waveform" actions={dd && (dd.ok ? <span className="status s-good"><Icon name="check" size={13} />Ready</span> : <span className="status s-crit"><Icon name="x" size={13} />Something is missing</span>)}>
        {!dd ? <Loading /> : (
          <>
            <dl className="set-dl">
              {['python', 'ffsubsync', 'numpy', 'alass', 'ffmpeg'].map(k => <div key={k}><dt>{k}</dt><dd>{dd[k] ? <code>{String(dd[k])}</code> : <span className="danger-text">not found</span>}</dd></div>)}
            </dl>
            {Array.isArray(dd.errors) && <pre className="small mt-s">{(dd.errors as string[]).join('\n')}</pre>}
          </>
        )}
      </Section>
      <div className="row wrap">
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-subtitles" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-subtitles/releases" target="_blank" rel="noreferrer"><Icon name="file" size={15} />Release notes</a>
      </div>
    </div>
  )
}
