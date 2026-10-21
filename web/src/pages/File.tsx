import { useCallback, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { usePoll } from '../lib/app-context'
import type { FileDetail, Task } from '../lib/types'
import { ago, fileTitle, fmtDateTime, METHOD_LABEL, SOURCE_LABEL } from '../lib/format'
import { Card, Confirm, Loading, PageHeader, useAsync, useToast } from '../components/ui'
import { Decision, Lang, Pin, Shift, StatusPill } from '../components/bits'
import { NudgePanel } from '../components/NudgePanel'
import { RunModal } from './History'
import { Icon } from '../components/icons'
import { announceChange } from '../lib/events'

const EVENT_LABEL: Record<string, string> = { nudge: 'Nudged', undo: 'Nudge undone', pin: 'Pinned', unpin: 'Unpinned', restore: 'Restored', verify: 'Verified', reject: 'Rejected', quarantine: 'Quarantined', rename: 'Renamed' }

export default function FilePage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const { data, error, reload, setData } = useAsync(() => api.get<FileDetail>(`${V1}/files/${id}`), [id])
  const [confirm, setConfirm] = useState<'restore' | 'sync-pinned' | null>(null)
  const [run, setRun] = useState<number | null>(null)
  const [preview, setPreview] = useState<string | null>(null)
  const poll = useCallback(() => { if (data?.queued) api.get<FileDetail>(`${V1}/files/${id}`).then(setData).catch(() => {}) }, [data?.queued, id, setData])
  usePoll(poll, 3000, [poll])
  if (error) return <div className="callout danger">{error}</div>
  if (!data) return <Loading />
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast(msg); announceChange(); reload() } catch (e) { toast((e as Error).message, 'err') } }
  const sync = (unpin = false) => act(() => api.post(`${V1}/files/${id}/sync`, { unpin }), 'Queued — it runs next')
  const verify = async () => { try { const t = await api.post<Task>(`${V1}/files/${id}/verify`); toast('Checking against the audio…'); navigate(`/tasks/${t.id}`) } catch (e) { toast((e as Error).message, 'err') } }
  const timeline = [
    ...data.runs.map(r => ({ at: r.created_at, kind: 'run' as const, run: r })),
    ...data.events.map(e => ({ at: e.created_at, kind: 'event' as const, ev: e })),
  ].sort((a, b) => b.at.localeCompare(a.at))
  const v = data.verify
  return (
    <>
      <PageHeader title={<span className="row wrap">{fileTitle(data)} <Lang lang={data.lang} flags={data.flags} /></span>}
        sub={<><Link to={`/library/t/${encodeURIComponent(data.title)}`} className="back-link"><Icon name="chevron-left" size={14} />{data.title}</Link> · <span className="mono small break">{data.rel}</span></>}
        actions={<>
          <button type="button" className="btn" onClick={verify} disabled={!data.exists}><Icon name="check-circle" size={15} />Verify</button>
          <button type="button" className="btn primary" disabled={!data.exists || !!data.queued} onClick={() => data.pinned ? setConfirm('sync-pinned') : sync()}>{data.queued ? (data.queued.state === 'running' ? 'Syncing…' : 'Queued') : 'Sync now'}</button>
        </>} />
      {!data.exists && <div className="callout danger mb"><Icon name="alert" size={15} /><span>This file is no longer in the library{data.last_decision.startsWith('rejected') ? ' — it was rejected; see the run below to put it back' : ''}.</span></div>}
      {data.queued && <div className="callout info mb"><span className="spinner sm" /><span>{data.queued.state === 'running' ? `Syncing: ${data.queued.stage || 'starting'}` : 'Waiting in the queue'}</span></div>}
      <div className="two-col">
        <div className="grid" style={{ gap: 16 }}>
          <Card title={<><Icon name="captions" size={16} />Current state</>}>
            <div className="row wrap mb-sm"><Decision d={data.last_decision} size="lg" /><Shift s={data.last_shift} decision={data.last_decision} /><Pin on={data.pinned} note={data.pin_note} /></div>
            {data.last_reason && <p className="small">{data.last_reason}</p>}
            <table className="table compact kv">
              <tbody>
                {data.last_method && <tr><th>Method</th><td>{METHOD_LABEL[data.last_method] || data.last_method}</td></tr>}
                <tr><th>Last checked</th><td>{data.last_checked_at ? <span title={fmtDateTime(data.last_checked_at)}>{ago(data.last_checked_at)}</span> : <span className="muted">never</span>}</td></tr>
                {data.pinned && <tr><th>Pinned</th><td className="small">{data.pin_note || 'by hand'}{data.pinned_at && <span className="muted"> · {ago(data.pinned_at)}</span>}</td></tr>}
                <tr><th>Video</th><td className="mono small break">{data.video_path ? data.video_path.split('/').pop() : <span className="danger-text">no video found next to it</span>}</td></tr>
              </tbody>
            </table>
            <div className="row wrap mt">
              {data.pinned ? <button type="button" className="btn sm" onClick={() => act(() => api.post(`${V1}/files/${id}/unpin`), 'Unpinned')}>Unpin</button>
                : <button type="button" className="btn sm" onClick={() => act(() => api.post(`${V1}/files/${id}/pin`, { note: 'pinned by hand' }), 'Pinned — no automatic run will touch it')}><Icon name="pushpin" size={14} />Pin as it is</button>}
              {data.has_original && <button type="button" className="btn sm" onClick={() => setConfirm('restore')}><Icon name="undo" size={14} />Restore original download</button>}
              <button type="button" className="btn sm ghost" onClick={async () => setPreview(preview === null ? await api.getText(`${V1}/files/${id}/text`) : null)}>{preview === null ? 'Show text' : 'Hide text'}</button>
            </div>
          </Card>
          {v && (
            <Card title={<><Icon name="shield" size={16} />Independent check</>}>
              <div className="row wrap mb-sm">{v.ok ? <StatusPill cls="good" icon="check">In sync with the audio</StatusPill> : <StatusPill cls="warn" icon="alert">{v.problems.join('; ')}</StatusPill>}{data.verified_at && <span className="muted small">{ago(data.verified_at)}</span>}</div>
              <p className="small muted">Whole-file offset against the audio: <b className="mono">{v.offset === null ? 'unmeasurable' : `${v.offset > 0 ? '+' : ''}${v.offset.toFixed(2)} s`}</b> (target {v.target.toFixed(2)} s ± {v.tolerance.toFixed(1)} s).</p>
              {v.tracks?.length > 0 && <p className="small muted">Embedded tracks: {v.tracks.map(t => `#${t.track} ${t.ok ? 'trusted' : 'rejected'}${t.offset !== null ? ` (${t.offset.toFixed(2)} s)` : ''}`).join(' · ')}</p>}
            </Card>
          )}
          {preview !== null && <Card title={<><Icon name="text" size={16} />Text</>} flush><pre className="srt-preview">{preview.slice(0, 20000)}</pre></Card>}
        </div>
        <div className="grid" style={{ gap: 16 }}>
          <Card title={<><Icon name="nudge" size={16} />Nudge by hand</>}><NudgePanel file={data} onChange={reload} /></Card>
          <Card title={<><Icon name="clock" size={16} />Timeline</>} flush>
            {timeline.length === 0 ? <div className="empty-row">Nothing yet</div> : (
              <ul className="rows">
                {timeline.map(t => t.kind === 'run' ? (
                  <li key={`r${t.run.id}`} className="row-item clickable" onClick={() => setRun(t.run.id)}>
                    <div className="grow"><Decision d={t.run.decision} /> <Shift s={t.run.max_shift} decision={t.run.decision} /><div className="muted small">{SOURCE_LABEL[t.run.source] || t.run.source}{t.run.method && ` · ${METHOD_LABEL[t.run.method] || t.run.method}`}</div></div>
                    <span className="muted small nowrap" title={fmtDateTime(t.at)}>{ago(t.at)}</span>
                  </li>
                ) : (
                  <li key={`e${t.ev.id}`} className="row-item">
                    <div className="grow"><span className="badge">{EVENT_LABEL[t.ev.kind] || t.ev.kind}</span> <span className="small">{t.ev.detail}</span></div>
                    <span className="muted small nowrap" title={fmtDateTime(t.at)}>{ago(t.at)}</span>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      </div>
      {confirm === 'restore' && <Confirm danger={false} title="Restore the original download?" message="The file goes back to exactly what the provider delivered, before any automatic change, and gets pinned so the sweep leaves it alone. The current version is kept as a backup." onCancel={() => setConfirm(null)} onConfirm={() => { setConfirm(null); act(() => api.post(`${V1}/files/${id}/restore-original`), 'Original restored and pinned') }} />}
      {confirm === 'sync-pinned' && <Confirm danger={false} title="Unpin and sync?" message="This file is pinned (its timing was set by hand). Syncing it unpins it, and the automatic result replaces the hand-made timing. Undo is still possible from the nudge backups." onCancel={() => setConfirm(null)} onConfirm={() => { setConfirm(null); sync(true) }} />}
      {run && <RunModal id={run} onClose={() => setRun(null)} />}
    </>
  )
}
