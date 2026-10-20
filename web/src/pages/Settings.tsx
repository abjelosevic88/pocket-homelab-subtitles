import { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, ConnMode, Settings as SettingsT, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime, fmtTime, fmtYMD, type DateFormat, type TimeFormat } from '../lib/format'
import { Card, Confirm, Field, Loading, Modal, PageHeader, Segmented, Tabs, useAsync, useToast } from '../components/ui'
import { Avatar } from '../components/people'
import { Select } from '../components/Select'

type Tab = 'general' | 'sync' | 'integrations' | 'users' | 'profile' | 'api' | 'data' | 'about'

export default function Settings() {
  const [tab, setTab] = useState<Tab>(() => (new URLSearchParams(window.location.search).get('tab') as Tab) || 'sync')
  return (
    <>
      <PageHeader title="Settings" />
      <Tabs tabs={[{ id: 'sync', label: 'Sync' }, { id: 'integrations', label: 'Integrations' }, { id: 'general', label: 'General' }, { id: 'users', label: 'Users' }, { id: 'profile', label: 'Profile' }, { id: 'api', label: 'API tokens' }, { id: 'data', label: 'Backup & data' }, { id: 'about', label: 'About' }]} value={tab} onChange={t => { setTab(t); window.history.replaceState(null, '', `?tab=${t}`) }} />
      <div className="mt">
        {tab === 'general' && <General />}
        {tab === 'sync' && <Sync />}
        {tab === 'integrations' && <Integrations />}
        {tab === 'users' && <Users />}
        {tab === 'profile' && <Profile />}
        {tab === 'api' && <Tokens />}
        {tab === 'data' && <Data />}
        {tab === 'about' && <About />}
      </div>
    </>
  )
}

type Draft = SettingsT & { bazarr_api_key?: string | null; jellyfin_api_key?: string | null }

/** Shared form state for the settings tabs: edit a draft, save the whole thing. */
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
  return isAdmin ? <div><button className="btn primary" onClick={save} disabled={busy}>Save</button></div> : <p className="muted small">Only an admin can change these.</p>
}

function Check({ label, checked, onChange, disabled, help }: { label: React.ReactNode; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; help?: React.ReactNode }) {
  return <label className="check"><input type="checkbox" checked={checked} onChange={e => onChange(e.target.checked)} disabled={disabled} /><span>{label}{help && <span className="help">{help}</span>}</span></label>
}

function General() {
  const { f, set, save, busy, isAdmin } = useSettingsForm()
  if (!f) return <Loading />
  const now = new Date()
  return (
    <Card title="Preferences">
      <div className="grid" style={{ gap: 14, maxWidth: 560 }}>
        <Field label="App title" help="Shown in the sidebar and browser tab."><input value={f.title} onChange={e => set('title', e.target.value)} maxLength={40} disabled={!isAdmin} /></Field>
        <div className="two-col tight">
          <Field group label="Date format" help={`Today: ${fmtYMD(now.getFullYear(), now.getMonth() + 1, now.getDate(), true, f.date_format as DateFormat)}`}>
            <Select value={f.date_format || 'auto'} onChange={v => set('date_format', v)} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} disabled={!isAdmin} ariaLabel="Date format" />
          </Field>
          <Field group label="Time format" help={`Now: ${fmtTime(now, f.time_format as TimeFormat)}`}>
            <Select value={f.time_format || 'auto'} onChange={v => set('time_format', v)} options={TIME_FORMATS.map(t => ({ value: t.id, label: t.label }))} disabled={!isAdmin} ariaLabel="Time format" />
          </Field>
        </div>
        <SaveBar save={save} busy={busy} isAdmin={isAdmin} />
      </div>
    </Card>
  )
}

function Sync() {
  const { f, set, save, busy, isAdmin } = useSettingsForm()
  const plan = useAsync(() => api.get<Record<string, number>>(`${V1}/sweep/plan`), [])
  if (!f) return <Loading />
  const d = !isAdmin
  return (
    <div className="two-col">
      <div className="grid" style={{ gap: 16 }}>
        <Card title="Timing">
          <div className="grid" style={{ gap: 14 }}>
            <Field label="Reading lead" help="Audio alignment puts a cue exactly on the first syllable, which can read as early. A lead moves every cue this much after the voice (0 = pure audio timing; 300 ms suits many people). Changing it re-times files on their next run.">
              <div className="row"><input type="number" className="num-input" step={50} min={-2000} max={2000} value={f.lead_ms} onChange={e => set('lead_ms', Number(e.target.value))} disabled={d} /> <span className="muted">ms</span></div>
            </Field>
            <Check label="Use the centre channel when it hears dialogue better" help="On 5.1 audio the dialogue sits in the centre channel; whispered scenes under music survive there. Chosen per video, only when it beats the full mix." checked={f.centre_ref} onChange={v => set('centre_ref', v)} disabled={d} />
            <Check label="Polish short displaced blocks" help="Re-measures each cue with its neighbours and moves a block only when several independent checks agree." checked={f.polish} onChange={v => set('polish', v)} disabled={d} />
            <Check label="Re-measure embedded-track results against the audio" help="Off by default: it helps when the release's own track drifts, but on quiet acts it can measure noise." checked={f.ref_residual} onChange={v => set('ref_residual', v)} disabled={d} />
          </div>
        </Card>
        <Card title="Quality gates">
          <div className="grid" style={{ gap: 14 }}>
            <Check label="Reject subtitles whose text belongs to another video" help="After alignment the cues must actually sit on detected speech — a subtitle for the wrong episode can measure perfectly in sync." checked={f.content_gate} onChange={v => set('content_gate', v)} disabled={d} />
            <Field label="Reject machine translations of fresh downloads in" help="Language codes, comma separated. The heuristic (no translator credit, many fragment cues, short lines, no dialogue dashes) is tuned on Serbian, Croatian and Bosnian."><input value={f.mt_gate_langs} onChange={e => set('mt_gate_langs', e.target.value)} placeholder="sr,hr,bs" disabled={d} /></Field>
            <Field group label="A rejected download is…">
              <Segmented value={f.on_reject} onChange={v => set('on_reject', v)} options={[{ value: 'blacklist', label: 'Blacklisted in Bazarr' }, { value: 'quarantine', label: 'Moved out' }, { value: 'keep', label: 'Kept, just flagged' }]} />
              <span className="help">{f.on_reject === 'blacklist' ? 'Bazarr bans that upload and searches for a replacement right away; the file leaves the library (a copy is kept).' : f.on_reject === 'quarantine' ? 'The file leaves the library (a copy is kept); search for another one yourself.' : 'Nothing is removed; the file shows under Issues.'}</span>
            </Field>
            <Field label="Convert Serbian Cyrillic to Latin for" help="Language codes. Subtitle packs often ship both scripts and Bazarr picks one at random."><input value={f.latinize_langs} onChange={e => set('latinize_langs', e.target.value)} placeholder="sr" disabled={d} /></Field>
          </div>
        </Card>
      </div>
      <div className="grid" style={{ gap: 16 }}>
        <Card title="Nightly sweep">
          <div className="grid" style={{ gap: 14 }}>
            <Check label="Sweep the library every night" checked={f.sweep_enabled} onChange={v => set('sweep_enabled', v)} disabled={d} help="Checks every file whose subtitle or video changed since its last verdict — including files that did not come through Bazarr." />
            <div className="two-col tight">
              <Field label="Start at"><input type="time" value={f.sweep_time} onChange={e => set('sweep_time', e.target.value)} disabled={d} /></Field>
              <Field label="Time budget" help="Leftovers wait for the next night."><div className="row"><input type="number" min={5} max={1440} value={f.sweep_budget} onChange={e => set('sweep_budget', Number(e.target.value))} disabled={d} /><span className="muted">min</span></div></Field>
            </div>
            <Field label="Re-check verdicts older than" help="Optional. After an engine improvement, set a date and older verdicts get re-measured, a few hours each night."><input type="date" value={f.reverify_before} onChange={e => set('reverify_before', e.target.value)} disabled={d} /></Field>
            <Field label="Only manage these languages" help="Empty = every subtitle in the library."><input value={f.languages} onChange={e => set('languages', e.target.value)} placeholder="e.g. en,sr" disabled={d} /></Field>
            <Check label="Unpin a file when Bazarr downloads a new one in its place" help="A pin protects hand-made timing of one particular file; a new download is a different subtitle." checked={f.auto_unpin_new} onChange={v => set('auto_unpin_new', v)} disabled={d} />
            <Field label="Independent check tolerance" help="Verify passes when the whole-file offset is within this of the reading lead."><div className="row"><input type="number" className="num-input" min={100} max={5000} step={50} value={f.verify_tol_ms} onChange={e => set('verify_tol_ms', Number(e.target.value))} disabled={d} /><span className="muted">ms</span></div></Field>
            {plan.data && <p className="muted small">Right now a sweep would check <b>{plan.data.todo}</b> files ({plan.data.unchanged} unchanged, {plan.data.pinned} pinned{plan.data.no_video ? `, ${plan.data.no_video} without a video` : ''}).</p>}
          </div>
        </Card>
        <SaveBar save={save} busy={busy} isAdmin={isAdmin} />
      </div>
    </div>
  )
}

const connBadge = (mode: ConnMode, via?: string) => mode === 'off' ? <span className="badge">off</span>
  : via ? <span className="badge cat">{via === 'core' ? 'Pocket Core' : 'direct'}</span>
  : <span className="badge warn">not connected</span>

/** Pocket Core | Direct | Off for one service; the switch is saved at once. */
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
      <div><Segmented size="sm" value={mode} onChange={m => !disabled && onMode(m)} options={[
        { value: 'core' as const, label: 'Pocket Core', title: core ? `Through Pocket Core, as the admin ${as || ''}` : 'There is no Pocket Core on this box' },
        { value: 'direct' as const, label: 'Direct', title: 'A URL and API key saved here' },
        { value: 'off' as const, label: 'Off' },
      ].filter(o => o.value !== 'direct' || showDirect)} /></div>
      {!showDirect && !disabled && <div><button type="button" className="link-btn small" onClick={() => setAdvanced(true)}>Advanced: connect directly with a URL and key</button></div>}
      <div className={`conn-status ${mode === 'off' ? '' : ok ? 'ok' : 'bad'}`}><span className="dot" />{line}</div>
      {mode === 'direct' && <>
        {direct}
        <div className="row">{test}{hasKey && !disabled && <button className="btn ghost sm danger-text" onClick={onDisconnect} title="Remove the saved URL and API key">Disconnect</button>}</div>
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
    <div className="two-col">
      <div className="grid" style={{ gap: 16 }}>
        <Card title="Bazarr" actions={connBadge(f.bazarr_mode, f.bazarr_via)}>
          <div className="grid" style={{ gap: 14 }}>
            <p className="muted small" style={{ margin: 0 }}>Optional, but this is where the subtitles come from. Connected, the app shows what is still missing, blacklists rejected downloads so Bazarr fetches another, and can reset a whole season.</p>
            <Connection app="Bazarr" mode={f.bazarr_mode} via={f.bazarr_via} source={f.bazarr_source} detail={f.bazarr_detail} as={f.services_as} core={core} disabled={d}
              hasKey={f.bazarr_api_key_set || !!f.bazarr_url}
              onMode={m => saveNow({ bazarr_mode: m }, modeMsg('Bazarr', m))}
              onDisconnect={() => saveNow({ bazarr_url: '', bazarr_api_key: '' }, 'Bazarr URL and API key removed')}
              test={<button className="btn" onClick={() => test('bazarr')} disabled={d || testing !== ''}>{testing === 'bazarr' ? 'Testing…' : f.bazarr_mode === 'direct' ? 'Save & test' : 'Test connection'}</button>}
              direct={<>
                <Field label="URL" help="As this app reaches it, e.g. http://bazarr:6767"><input value={f.bazarr_url} onChange={e => set('bazarr_url', e.target.value)} placeholder="http://bazarr:6767" disabled={d} /></Field>
                <Field label="API key" help={f.bazarr_api_key_set ? 'A key is saved. Type to replace it.' : 'Bazarr → Settings → General → Security.'}><input type="password" autoComplete="off" value={f.bazarr_api_key ?? ''} onChange={e => set('bazarr_api_key', e.target.value)} placeholder={f.bazarr_api_key_set ? '••••••••' : ''} disabled={d} /></Field>
              </>} />
            {f.bazarr_mode !== 'off' && <Field label="Path mapping" help="Only if Bazarr sees the library under another path: bazarr-path=this-app-path, comma separated."><input value={f.bazarr_path_map} onChange={e => set('bazarr_path_map', e.target.value)} placeholder="/tv=/media/shows,/movies=/media/movies" disabled={d} /></Field>}
            {f.bazarr_mode !== 'off' && <SaveBar save={save} busy={busy} isAdmin={isAdmin} />}
          </div>
        </Card>
        <HookCommand />
        <Card title="Season tools">
          <div className="grid" style={{ gap: 14 }}>
            <Field label="Languages a season reset downloads" help="Empty = the languages the season had before."><input value={f.season_langs} onChange={e => set('season_langs', e.target.value)} placeholder="en" disabled={d} /></Field>
            <Field label="Use the manual provider search for" help="The manual search also offers lower-scoring human subtitles that Bazarr's automatic search refuses."><input value={f.season_manual_langs} onChange={e => set('season_manual_langs', e.target.value)} placeholder="e.g. sr" disabled={d} /></Field>
            <Field label="Preferred provider" help="Ranked first in manual searches, e.g. titlovi or opensubtitlescom."><input value={f.prefer_provider} onChange={e => set('prefer_provider', e.target.value)} disabled={d} /></Field>
            <SaveBar save={save} busy={busy} isAdmin={isAdmin} />
          </div>
        </Card>
      </div>
      <div className="grid" style={{ gap: 16 }}>
        <Card title="Jellyfin / Emby" actions={connBadge(f.jellyfin_mode, f.jellyfin_via)}>
          <div className="grid" style={{ gap: 14 }}>
            <p className="muted small" style={{ margin: 0 }}>Optional. After a file changes, the server is told to rescan that folder — network mounts send no change events, so without it the new timing appears only after the next scheduled scan.</p>
            <Connection app="Jellyfin" mode={f.jellyfin_mode} via={f.jellyfin_via} source={f.jellyfin_source} detail={f.jellyfin_detail} as={f.services_as} core={core} disabled={d}
              hasKey={f.jellyfin_api_key_set || !!f.jellyfin_url}
              onMode={m => saveNow({ jellyfin_mode: m }, modeMsg('Jellyfin', m))}
              onDisconnect={() => saveNow({ jellyfin_url: '', jellyfin_api_key: '' }, 'Jellyfin URL and API key removed')}
              test={<button className="btn" onClick={() => test('jellyfin')} disabled={d || testing !== ''}>{testing === 'jellyfin' ? 'Testing…' : f.jellyfin_mode === 'direct' ? 'Save & test' : 'Test connection'}</button>}
              direct={<>
                <Field label="URL"><input value={f.jellyfin_url} onChange={e => set('jellyfin_url', e.target.value)} placeholder="http://jellyfin:8096" disabled={d} /></Field>
                <Field label="API key" help={f.jellyfin_api_key_set ? 'A key is saved. Type to replace it.' : 'Dashboard → API keys.'}><input type="password" autoComplete="off" value={f.jellyfin_api_key ?? ''} onChange={e => set('jellyfin_api_key', e.target.value)} placeholder={f.jellyfin_api_key_set ? '••••••••' : ''} disabled={d} /></Field>
              </>} />
            {f.jellyfin_mode !== 'off' && <Field label="Path mapping" help="If Jellyfin sees the library elsewhere: this-app-path=jellyfin-path."><input value={f.jellyfin_path_map} onChange={e => set('jellyfin_path_map', e.target.value)} placeholder="/media=/data/media" disabled={d} /></Field>}
            {f.jellyfin_mode !== 'off' && <SaveBar save={save} busy={busy} isAdmin={isAdmin} />}
          </div>
        </Card>
        <Card title="Notifications">
          <div className="grid" style={{ gap: 14 }}>
            <Field group label="Send to"><Segmented value={f.notify_kind} onChange={v => set('notify_kind', v)} options={[{ value: '', label: 'Off' }, { value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'webhook', label: 'Webhook' }]} /></Field>
            {f.notify_kind && <Field label="URL" help={f.notify_kind === 'apprise' ? 'Apprise API, e.g. http://apprise:8000/notify/subtitles' : f.notify_kind === 'ntfy' ? 'Topic URL, e.g. https://ntfy.sh/my-topic' : 'Receives JSON {title, body, level}.'}><input value={f.notify_url} onChange={e => set('notify_url', e.target.value)} disabled={d} /></Field>}
            {f.notify_kind && <>
              <Check label="Failures and rejections" checked={f.notify_failures} onChange={v => set('notify_failures', v)} disabled={d} />
              <Check label="Every adjusted file" checked={f.notify_synced} onChange={v => set('notify_synced', v)} disabled={d} />
              <Check label="Sweep summary" checked={f.notify_sweep} onChange={v => set('notify_sweep', v)} disabled={d} />
              <div className="row"><button className="btn" onClick={() => test('notify')} disabled={d || testing !== ''}>{testing === 'notify' ? 'Sending…' : 'Save & send a test'}</button></div>
            </>}
            <SaveBar save={save} busy={busy} isAdmin={isAdmin} />
          </div>
        </Card>
      </div>
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
  const copy = async () => { try { await navigator.clipboard.writeText(cmd); toast('Copied') } catch { toast('Select and copy it by hand', 'err') } }
  return (
    <Card title="Connect Bazarr's downloads">
      <ol className="small steps">
        <li>Bazarr → Settings → Subtitles → <b>Custom post-processing</b>: turn it on.</li>
        <li>Make sure Bazarr's own <b>“Automatic subtitles synchronization”</b> is off (this app does it, better).</li>
        <li>Paste this command. Bazarr waits only a moment; the sync runs here.</li>
      </ol>
      <Field label="URL Bazarr reaches this app at" help="From inside Bazarr's container: the service name on a shared network, or the host's address."><input value={url} onChange={e => setUrl(e.target.value)} /></Field>
      <pre className="cmd mt-sm">{cmd}</pre>
      <div className="row wrap">{!token && <button className="btn" onClick={mint}>Create a token for Bazarr</button>}<button className="btn primary" onClick={copy}>Copy command</button></div>
    </Card>
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
    <Card title="Users" actions={isAdmin && <button className="btn primary sm" onClick={() => setEdit({ role: 'member' })}>+ Add user</button>}>
      <p className="muted">Members can look around, nudge, pin and sync files. Admins also change settings, reset seasons and manage users.</p>
      <table className="table compact">
        <thead><tr><th /><th>Name</th><th>Email</th><th>Role</th><th /></tr></thead>
        <tbody>
          {users.map(u => (
            <tr key={u.id}>
              <td style={{ width: 32 }}><Avatar user={u} /></td>
              <td>{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge" style={{ marginLeft: 6 }}>you</span>}</td>
              <td className="muted">{u.email}</td>
              <td><span className={`badge ${u.role === 'admin' ? 'cat' : ''}`}>{u.role}</span></td>
              <td className="right">{isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit; passwords are not used here.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadUsers() }} />}
      {del && <Confirm title={`Remove ${del.name || del.email}?`} message="Their account and tokens are deleted. What they did stays in the history." onCancel={() => setDel(null)} onConfirm={() => { const x = del; setDel(null); run(() => api.del(`${V1}/users/${x.id}`), 'User removed') }} />}
    </Card>
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
    <Modal title={user.id ? 'Edit user' : 'Add user'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add user'}</button></div>
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
    <Card title="Profile">
      <form onSubmit={save} className="grid" style={{ gap: 14, maxWidth: 480 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
        <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
        {status?.auth_mode === 'local' && <>
          <hr />
          <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
          <Field label="New password" help="At least 8 characters. Leave empty to keep the current one."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
        </>}
        <div><button className="btn primary" disabled={busy}>Save</button></div>
      </form>
    </Card>
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
    <Card title="API tokens">
      <p className="muted">Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you. Bazarr's post-processing hook uses one; so can a Homepage widget (<code>GET /api/v1/widget</code>) or a script queueing a file (<code>POST /api/v1/sync</code>). See <code>docs/api.md</code>.</p>
      <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. Homepage)" /><button className="btn primary">Create</button></form>
      {fresh && <div className="callout success mb"><b>Copy it now — it is shown once.</b><pre className="mono" style={{ margin: '8px 0 0' }}>{fresh}</pre></div>}
      {data && data.length > 0 ? (
        <table className="table compact">
          <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th>Created</th><th /></tr></thead>
          <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="muted">{fmtDateTime(t.created_at)}</td><td className="right"><button className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
        </table>
      ) : <p className="muted">No tokens yet.</p>}
      {del && <Confirm title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
    </Card>
  )
}

function Data() {
  const { data } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  return (
    <div className="two-col">
      <Card title="Database">
        <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>): history, verdicts, pins, nudges, users and settings. Restore by stopping the app and replacing <code>pocket-subtitles.db</code>.</p>
        <a className="btn primary" href={`${V1}/backup`}>Download database</a>
      </Card>
      <Card title="On disk">
        {data ? (
          <table className="table compact kv"><tbody>
            <tr><th>Originals & nudge backups</th><td className="mono small">{String(data.backup_dir)}<br /><span className="muted">{Number(data.backup_files).toLocaleString()} files · {fmtBytes(Number(data.backup_bytes))}</span></td></tr>
            <tr><th>Measurement cache</th><td className="mono small">{String(data.cache_dir)}<br /><span className="muted">{Number(data.cache_files).toLocaleString()} files · {fmtBytes(Number(data.cache_bytes))} — rebuildable, skip it in backups if space matters</span></td></tr>
          </tbody></table>
        ) : <Loading />}
      </Card>
    </div>
  )
}

function About() {
  const { data } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const doctor = useAsync(() => api.get<Record<string, unknown>>(`${V1}/doctor`), [])
  const counts = (data?.counts || {}) as Record<string, number>
  const dd = doctor.data
  return (
    <div className="two-col">
      <Card title="About">
        <p><b>Pocket Subtitles</b> keeps subtitles in sync with the audio: every download from Bazarr is measured and re-timed when it is off, the library is swept each night, and anything can be nudged by hand.</p>
        <p className="muted small">Open source (MIT) — <a href="https://github.com/abjelosevic88/pocket-homelab-subtitles" target="_blank" rel="noreferrer">github.com/abjelosevic88/pocket-homelab-subtitles</a>. Sibling projects: Pocket Habits, Pocket Inventory, Pocket Bookshelf, Pocket Split and Pocket Invoicing. Alignment by ffsubsync and alass.</p>
        {data && (
          <table className="table compact kv mt"><tbody>
            <tr><th>Version</th><td className="mono">{String(data.version)}</td></tr>
            <tr><th>Runtime</th><td className="mono">{String(data.go)} · {String(data.os)}/{String(data.arch)} · {String(data.workers)} worker(s)</td></tr>
            <tr><th>Media folders</th><td className="mono small">{(data.media_dirs as string[]).join(', ')}</td></tr>
            <tr><th>Auth mode</th><td className="mono">{String(data.auth_mode)}{data.legacy_api !== 'off' && ` · legacy API ${String(data.legacy_api)}`}</td></tr>
            <tr><th>Database</th><td className="mono small">{String(data.db_path)} · {fmtBytes(Number(data.db_bytes))}</td></tr>
            <tr><th>Timezone</th><td className="mono">{String(data.timezone)}</td></tr>
            <tr><th>Uptime</th><td className="mono">{String(data.uptime)}</td></tr>
            <tr><th>Rows</th><td className="mono small">{counts.subtitles?.toLocaleString()} files · {counts.runs?.toLocaleString()} runs · {counts.nudges} nudges · {counts.users} users</td></tr>
          </tbody></table>
        )}
      </Card>
      <Card title="Sync engine">
        {!dd ? <Loading /> : (
          <>
            <div className="mb-sm">{dd.ok ? <span className="status s-good">✓ Ready</span> : <span className="status s-crit">✕ Something is missing</span>}</div>
            <table className="table compact kv"><tbody>
              {['python', 'ffsubsync', 'numpy', 'alass', 'ffmpeg'].map(k => <tr key={k}><th>{k}</th><td className="mono small">{dd[k] ? String(dd[k]) : <span className="danger-text">not found</span>}</td></tr>)}
            </tbody></table>
            {Array.isArray(dd.errors) && <pre className="small">{(dd.errors as string[]).join('\n')}</pre>}
          </>
        )}
      </Card>
    </div>
  )
}
