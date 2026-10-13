import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Subtitle, Task, TitleSummary } from '../lib/types'
import { ago, fmtMs } from '../lib/format'
import { Card, Confirm, Empty, Loading, Modal, PageHeader, Segmented, useAsync, useDebounce, useToast } from '../components/ui'
import { Decision, Lang, Pin, Shift } from '../components/bits'

const STATUSES = [
  { value: '', label: 'All' }, { value: 'attention', label: 'Needs attention' }, { value: 'synced', label: 'Adjusted' },
  { value: 'pinned', label: 'Pinned' }, { value: 'nudged', label: 'Nudged' }, { value: 'unchecked', label: 'Not checked' },
]

export default function Library() {
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState(params.get('q') || '')
  const dq = useDebounce(q, 200)
  const status = params.get('status') || ''
  const lib = params.get('library') || ''
  const { toast } = useToast()
  const qs = new URLSearchParams({ q: dq, status, library: lib })
  const { data, loading, reload } = useAsync(() => api.get<TitleSummary[]>(`${V1}/library/titles?${qs}`), [qs.toString()])
  const set = (k: string, v: string) => { const p = new URLSearchParams(params); if (v) p.set(k, v); else p.delete(k); setParams(p, { replace: true }) }
  const libraries = Array.from(new Set((data || []).map(t => t.library))).sort()
  const scan = async () => { try { const r = await api.post<{ files: number; added: number }>(`${V1}/library/scan`); toast(`${r.files} files, ${r.added} new`); reload() } catch (e) { toast((e as Error).message, 'err') } }
  const files = (data || []).reduce((a, t) => a + t.files, 0)
  return (
    <>
      <PageHeader title="Library" sub={data ? `${data.length.toLocaleString()} shows & movies · ${files.toLocaleString()} subtitle files` : ' '} actions={<button className="btn" onClick={scan}>Rescan</button>} />
      <div className="toolbar">
        <div className="search"><span className="ico">⌕</span><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search titles and files…" autoFocus /></div>
        {libraries.length > 1 && <Segmented size="sm" value={lib} onChange={v => set('library', v)} options={[{ value: '', label: 'All' }, ...libraries.map(l => ({ value: l, label: l }))]} />}
      </div>
      <div className="filters"><Segmented size="sm" value={status} onChange={v => set('status', v)} options={STATUSES} /></div>
      {loading && !data ? <Loading /> : !data?.length ? <Card><Empty title="Nothing found">{q || status ? 'No titles match.' : 'No subtitle files in the library folders yet (MEDIA_DIRS).'}</Empty></Card> : (
        <div className="title-grid">
          {data.map(t => (
            <Link key={t.library + t.title} className="card title-card" to={`/library/t/${encodeURIComponent(t.title)}${status ? `?status=${status}` : ''}`}>
              <div className="row"><span className="nm grow">{t.title}</span>{t.attention > 0 && <span className="status s-warn">⚠ {t.attention}</span>}</div>
              <div className="muted small">{t.kind === 'episode' ? `${t.seasons?.length || 0} season${(t.seasons?.length || 0) === 1 ? '' : 's'} · ` : ''}{t.files} file{t.files === 1 ? '' : 's'}{t.pinned ? ` · ${t.pinned} pinned` : ''}</div>
              <div className="row wrap mt-sm">{t.langs.map(l => <Lang key={l} lang={l} />)}<span className="spacer" />{t.last_at && <span className="muted small">checked {ago(t.last_at)}</span>}</div>
            </Link>
          ))}
        </div>
      )}
    </>
  )
}

/** One show or movie: its files grouped by season, with the season tools. */
export function TitlePage() {
  const { title = '' } = useParams()
  const [params] = useSearchParams()
  const status = params.get('status') || ''
  const { user, settings } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const { data, reload } = useAsync(() => api.get<{ items: Subtitle[] }>(`${V1}/library/files?${new URLSearchParams({ title, status, limit: '2000' })}`), [title, status])
  const [tool, setTool] = useState<{ season: number; mode: 'resync' | 'verify' | 'reset' } | null>(null)
  if (!data) return <Loading />
  const seasons = new Map<number, Subtitle[]>()
  for (const f of data.items) {
    const s = f.season ?? 0
    seasons.set(s, [...(seasons.get(s) || []), f])
  }
  const isShow = data.items.some(f => f.kind === 'episode')
  const start = async (season: number, mode: string, fresh = false) => {
    try { const t = await api.post<Task>(`${V1}/seasons`, { title, season, mode, fresh }); toast('Started'); navigate(`/tasks/${t.id}`) } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <>
      <PageHeader title={title} sub={<><Link to="/library">← Library</Link> · {data.items.length} subtitle files</>}
        actions={!isShow && data.items.length > 0 && <><button className="btn" onClick={() => setTool({ season: 0, mode: 'verify' })}>Verify</button><button className="btn" onClick={() => setTool({ season: 0, mode: 'resync' })}>Re-sync all</button></>} />
      {[...seasons.entries()].sort((a, b) => a[0] - b[0]).map(([season, files]) => (
        <Card key={season} className="mb" flush title={isShow ? (season ? `Season ${season}` : 'Other') : 'Files'}
          actions={isShow && season > 0 && <>
            <button className="btn ghost sm" onClick={() => setTool({ season, mode: 'verify' })}>Verify</button>
            <button className="btn ghost sm" onClick={() => setTool({ season, mode: 'resync' })}>Re-sync</button>
            {user?.role === 'admin' && settings?.bazarr_api_key_set && <button className="btn ghost sm" onClick={() => setTool({ season, mode: 'reset' })}>Reset…</button>}
          </>}>
          <table className="table files">
            <tbody>
              {files.map(f => (
                <tr key={f.id} className="clickable" onClick={() => navigate(`/files/${f.id}`)}>
                  <td className="nowrap"><b>{f.kind === 'episode' ? f.label : f.title}</b></td>
                  <td><Lang lang={f.lang} flags={f.flags} /></td>
                  <td><Decision d={f.last_decision} /> <Pin on={f.pinned} note={f.pin_note} />{f.nudge_ms !== 0 && <span className="badge nud">{fmtMs(f.nudge_ms)}</span>}</td>
                  <td className="right hide-sm"><Shift s={f.last_shift} decision={f.last_decision} /></td>
                  <td className="muted small right hide-sm">{f.last_checked_at ? ago(f.last_checked_at) : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      ))}
      {data.items.length === 0 && <Card><Empty title="No files">Nothing matches.</Empty></Card>}
      {tool?.mode === 'verify' && <Confirm danger={false} title={`Verify ${tool.season ? `season ${tool.season}` : title}?`} message="Each file is measured once more against the real audio (never the embedded track), and checked for script and machine-translation smell. Nothing is changed. Files with no cached audio reference take a minute or two each." onCancel={() => setTool(null)} onConfirm={() => { const t = tool; setTool(null); start(t.season, 'verify') }} />}
      {tool?.mode === 'resync' && <ResyncModal season={tool.season} title={title} onClose={() => setTool(null)} onStart={fresh => { const t = tool; setTool(null); start(t.season, 'resync', fresh) }} />}
      {tool?.mode === 'reset' && <ResetModal season={tool.season} title={title} langs={settings?.season_langs || ''} onClose={() => setTool(null)} onStart={() => { const t = tool; setTool(null); start(t.season, 'reset') }} />}
    </>
  )
}

function ResyncModal({ season, title, onClose, onStart }: { season: number; title: string; onClose: () => void; onStart: (fresh: boolean) => void }) {
  const [fresh, setFresh] = useState(false)
  return (
    <Modal title={`Re-sync ${season ? `season ${season}` : title}`} onClose={onClose}>
      <p>Every file is unpinned and synced again from its current state, then verified independently. Use it after changing the reading lead or when a season feels off.</p>
      <label className="row mt-sm"><input type="checkbox" checked={fresh} onChange={e => setFresh(e.target.checked)} style={{ width: 'auto' }} /> Rebuild the audio references first (slower; use when a measurement looks wrong)</label>
      <div className="form-actions"><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={() => onStart(fresh)}>Re-sync</button></div>
    </Modal>
  )
}

function ResetModal({ season, title, langs, onClose, onStart }: { season: number; title: string; langs: string; onClose: () => void; onStart: () => void }) {
  return (
    <Modal title={`Reset ${title} season ${season}`} onClose={onClose}>
      <p>Starts over from the providers:</p>
      <ol className="small">
        <li>backs up every subtitle of the season and unpins them,</li>
        <li>deletes them through Bazarr,</li>
        <li>downloads <b>{langs || 'the same languages'}</b> again (manual provider search where set up in Settings → Integrations), one at a time,</li>
        <li>lets each download sync on arrival, removes HI duplicates,</li>
        <li>verifies every file against the audio.</li>
      </ol>
      <p className="muted small">Anything no provider serves any more is put back from the backup. Takes roughly a minute per file.</p>
      <div className="form-actions"><button className="btn" onClick={onClose}>Cancel</button><button className="btn danger" onClick={onStart}>Reset season</button></div>
    </Modal>
  )
}
