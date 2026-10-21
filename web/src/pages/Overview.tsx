import { useCallback, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { usePoll } from '../lib/app-context'
import type { Overview as OverviewT } from '../lib/types'
import { ago, fmtDateTime, fmtElapsed, SOURCE_LABEL } from '../lib/format'
import { Card, Empty, Loading, PageHeader, useAsync, useToast } from '../components/ui'
import { Decision, FileName, Shift } from '../components/bits'
import { ActivityChart } from '../components/ActivityChart'
import { Icon } from '../components/icons'
import { announceChange } from '../lib/events'

export default function Overview() {
  const { toast } = useToast()
  const { data, error, reload, setData } = useAsync(() => api.get<OverviewT>(`${V1}/overview`), [])
  const [busy, setBusy] = useState(false)
  const refresh = useCallback(() => { api.get<OverviewT>(`${V1}/overview`).then(setData).catch(() => {}) }, [setData])
  usePoll(refresh, 4000)
  if (error && !data) return <div className="callout danger">{error}</div>
  if (!data) return <Loading />
  const s = data.stats
  const act = async (fn: () => Promise<unknown>, msg: string) => { setBusy(true); try { await fn(); toast(msg); announceChange(); await reload() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) } }
  const sweep = data.sweep
  const last = sweep.last
  return (
    <>
      <PageHeader title="Overview" sub={<>Every subtitle Bazarr downloads is measured against the audio and re-timed when it is off. {data.last_scan && <span className="muted">Library scanned {ago(data.last_scan)}.</span>}</>}
        actions={<><Link className="btn" to="/nudge"><Icon name="nudge" size={15} />Nudge a file</Link><button type="button" className="btn primary" disabled={busy || sweep.running} onClick={() => act(() => api.post(`${V1}/sweep`), 'Sweep started')}><Icon name="refresh" size={15} />{sweep.running ? 'Sweep running…' : 'Run sweep now'}</button></>} />

      <div className="stats">
        <Stat label="Processed" value={s.processed} hint={`${s.today.toLocaleString()} today`} to="/history" />
        <Stat label="Adjusted" value={s.adjusted} hint={s.processed ? `${Math.round((s.adjusted / s.processed) * 100)}% of runs` : ''} to="/history?outcome=adjusted" />
        <Stat label="In queue" value={s.queued + s.running} hint={s.running ? `${s.running} syncing now` : 'idle'} to="/tasks?tab=queue" />
        <Stat label="Needs attention" value={s.attention} hint="files whose last run failed" to="/issues" tone={s.attention ? 'warn' : ''} />
        <Stat label="Missing downloads" value={s.missing_downloads} hint={s.missing_downloads === null ? 'connect Bazarr in Settings' : `${s.missing_episodes} episodes · ${s.missing_movies} movies`} to="/issues?tab=missing" />
      </div>

      <div className="ov-grid">
        <div className="grid" style={{ gap: 16 }}>
          <Card title={<><Icon name="waveform" size={16} />Now syncing {data.running.length > 0 && <span className="live-dot" aria-label="live" />}</>} flush>
            {data.running.length === 0 ? <div className="empty-row">Nothing syncing right now</div> : (
              <ul className="rows">
                {data.running.map(j => (
                  <li key={j.id} className="row-item">
                    <div className="grow">
                      <FileName f={j} />
                      <div className="muted small"><span className="spinner sm" /> {j.stage || 'starting'} · {fmtElapsed(j.elapsed_s)} · {SOURCE_LABEL[j.source] || j.source}</div>
                    </div>
                    <button type="button" className="btn ghost sm" onClick={() => act(() => api.post(`${V1}/queue/${j.id}/cancel`), 'Cancelled')}>Cancel</button>
                  </li>
                ))}
              </ul>
            )}
          </Card>

          <Card title={<><Icon name="list" size={16} />Up next {data.queued_total > 0 && <span className="muted small">{data.queued_total.toLocaleString()} queued</span>}</>} flush
            actions={data.queued_total > 0 && <Link className="btn ghost sm" to="/tasks?tab=queue">Manage</Link>}>
            {data.next.length === 0 ? <div className="empty-row">Queue is empty</div> : (
              <ol className="rows numbered">
                {data.next.slice(0, 10).map((j, i) => (
                  <li key={j.id} className="row-item">
                    <span className="num">{i + 1}</span>
                    <div className="grow"><FileName f={j} /></div>
                    <span className="badge">{SOURCE_LABEL[j.source] || j.source}</span>
                  </li>
                ))}
                {data.queued_total > 10 && <li className="row-item more">… and {(data.queued_total - 10).toLocaleString()} more</li>}
              </ol>
            )}
          </Card>

          <Card title={<><Icon name="chart" size={16} />Last 14 days</>}><ActivityChart days={data.days} /></Card>
        </div>

        <div className="grid" style={{ gap: 16 }}>
          <Card title={<><Icon name="clock" size={16} />Recent</>} flush actions={<Link className="btn ghost sm" to="/history">All history<Icon name="arrow-right" size={14} /></Link>}>
            {data.recent.length === 0 ? <Empty title="No runs yet">Downloads from Bazarr show up here as they are synced.</Empty> : (
              <ul className="rows">
                {data.recent.map(r => (
                  <li key={r.id} className="row-item">
                    <div className="grow">
                      <FileName f={r} id={r.subtitle_id} />
                      <div className="muted small" title={fmtDateTime(r.created_at)}>{ago(r.created_at)} · {SOURCE_LABEL[r.source] || r.source}</div>
                    </div>
                    <div className="right"><Decision d={r.decision} /><div><Shift s={r.max_shift} decision={r.decision} /></div></div>
                  </li>
                ))}
              </ul>
            )}
          </Card>

          <Card title={<><Icon name="moon" size={16} />Nightly sweep</>} actions={<Link className="btn ghost sm" to="/settings/sync">Schedule</Link>}>
            <p className="muted small" style={{ marginTop: 0 }}>Re-checks every file whose subtitle or video changed since its last verdict (new files included), within a {sweep.budget}-minute budget.</p>
            <table className="table compact kv">
              <tbody>
                <tr><th>Schedule</th><td>{sweep.enabled ? <>daily at <b>{sweep.time}</b>{sweep.next_at && <span className="muted"> · next {fmtDateTime(sweep.next_at)}</span>}</> : <span className="muted">off</span>}</td></tr>
                {last && <tr><th>Last run</th><td><Link to={`/tasks/${last.id}`}>{fmtDateTime(last.created_at)}</Link> · <TaskSummary t={last} /></td></tr>}
              </tbody>
            </table>
          </Card>

          <Card title={<><Icon name="layers" size={16} />Library</>}>
            <div className="mini-stats">
              <Link to="/library"><b>{s.files.toLocaleString()}</b><span>subtitle files</span></Link>
              <Link to="/library"><b>{s.titles.toLocaleString()}</b><span>shows & movies</span></Link>
              <Link to="/library?status=pinned"><b>{s.pinned}</b><span>pinned</span></Link>
              <Link to="/library?status=nudged"><b>{s.nudged}</b><span>nudged</span></Link>
              <Link to="/library?status=unchecked"><b>{s.unchecked}</b><span>not checked yet</span></Link>
            </div>
          </Card>
        </div>
      </div>
    </>
  )
}

function Stat({ label, value, hint, to, tone = '' }: { label: string; value: number | null; hint?: string; to: string; tone?: '' | 'warn' }) {
  return (
    <Link to={to} className={`stat ${tone}`}>
      <div className="l">{label}</div>
      <div className="n">{value === null ? '—' : value.toLocaleString()}</div>
      {hint && <div className="h">{hint}</div>}
    </Link>
  )
}

export function TaskSummary({ t }: { t: { state: string; summary: Record<string, unknown>; done: number; total: number } }) {
  if (t.state === 'running') return <span>running · {t.done}/{t.total}</span>
  const sm = t.summary || {}
  if (sm.error) return <span className="danger-text">{String(sm.error)}</span>
  if (typeof sm.processed === 'number') {
    const d = (sm.decisions || {}) as Record<string, number>
    return <span>{sm.processed} checked{d.synced ? `, ${d.synced} adjusted` : ''}{sm.left ? `, ${sm.left} left over` : ''}{typeof sm.minutes === 'number' ? ` in ${sm.minutes} min` : ''}</span>
  }
  if (typeof sm.verdict === 'string') return <span>{sm.verdict.split('\n')[0]}</span>
  return <span>{t.state}</span>
}
