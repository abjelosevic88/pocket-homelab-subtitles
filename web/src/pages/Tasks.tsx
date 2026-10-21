import { useCallback } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { usePoll } from '../lib/app-context'
import type { Job, RunningJob, Task } from '../lib/types'
import { ago, fmtDateTime, fmtElapsed, SOURCE_LABEL } from '../lib/format'
import { Card, Empty, Loading, PageHeader, Tabs, useAsync, useToast } from '../components/ui'
import { Decision, FileName, Progress, StatusPill } from '../components/bits'
import { TaskSummary } from './Overview'
import { Icon } from '../components/icons'

const KIND: Record<string, string> = { sweep: 'Sweep', season_resync: 'Re-sync', season_verify: 'Verify', season_reset: 'Season reset', verify: 'Verify' }

export default function Tasks() {
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') === 'queue' ? 'queue' : 'tasks'
  return (
    <>
      <PageHeader title="Tasks" sub="The queue, the nightly sweeps and the season tools." />
      <Tabs tabs={[{ id: 'tasks', label: 'Tasks' }, { id: 'queue', label: 'Queue' }]} value={tab} onChange={t => setParams({ tab: t }, { replace: true })} />
      <div className="mt">{tab === 'queue' ? <Queue /> : <TaskList />}</div>
    </>
  )
}

function TaskList() {
  const { data, setData } = useAsync(() => api.get<Task[]>(`${V1}/tasks?limit=100`), [])
  usePoll(() => { api.get<Task[]>(`${V1}/tasks?limit=100`).then(setData).catch(() => {}) }, 5000)
  if (!data) return <Loading />
  if (!data.length) return <Card><Empty title="No tasks yet">Sweeps run nightly; season tools start from a show's page in the Library.</Empty></Card>
  return (
    <Card flush>
      <table className="table">
        <thead><tr><th>Started</th><th>Task</th><th>Result</th></tr></thead>
        <tbody>
          {data.map(t => (
            <tr key={t.id}>
              <td className="muted small nowrap">{fmtDateTime(t.created_at)}</td>
              <td><Link to={`/tasks/${t.id}`}><b>{t.title}</b></Link> <span className="badge">{KIND[t.kind] || t.kind}</span></td>
              <td className="small">{t.state === 'running' ? <div style={{ minWidth: 160 }}><Progress done={t.done} total={t.total} /><span className="muted">{t.done}/{t.total}</span></div> : <><StateBadge s={t.state} /> <TaskSummary t={t} /></>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  )
}

function StateBadge({ s }: { s: string }) {
  const m: Record<string, [string, 'good' | 'crit' | 'mut' | 'info', string]> = { done: ['done', 'good', 'check'], failed: ['failed', 'crit', 'x'], cancelled: ['cancelled', 'mut', 'minus'], running: ['running', 'info', 'dot'] }
  const [l, c, i] = m[s] || [s, 'mut', 'dot']
  return <StatusPill cls={c} icon={i}>{l}</StatusPill>
}

function Queue() {
  const { toast } = useToast()
  const { data, reload, setData } = useAsync(() => api.get<{ running: RunningJob[]; queued: Job[]; total: number }>(`${V1}/queue?limit=500`), [])
  usePoll(() => { api.get<{ running: RunningJob[]; queued: Job[]; total: number }>(`${V1}/queue?limit=500`).then(setData).catch(() => {}) }, 3000)
  if (!data) return <Loading />
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast(msg); reload() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <div className="grid" style={{ gap: 16 }}>
      <Card title="Syncing now" flush>
        {!data.running.length ? <div className="empty-row">Nothing syncing right now</div> : (
          <ul className="rows">{data.running.map(j => (
            <li key={j.id} className="row-item"><div className="grow"><FileName f={j} /><div className="muted small"><span className="spinner sm" /> {j.stage} · {fmtElapsed(j.elapsed_s)}</div></div>
              <button type="button" className="btn ghost sm" onClick={() => act(() => api.post(`${V1}/queue/${j.id}/cancel`), 'Cancelled')}>Cancel</button></li>
          ))}</ul>
        )}
      </Card>
      <Card title={`Waiting (${data.total.toLocaleString()})`} flush actions={data.total > 0 && <button type="button" className="btn ghost sm danger-text" onClick={() => act(() => api.post(`${V1}/queue/clear`), 'Queue cleared')}>Clear all</button>}>
        {!data.queued.length ? <div className="empty-row">Queue is empty</div> : (
          <ol className="rows numbered">{data.queued.map((j, i) => (
            <li key={j.id} className="row-item"><span className="num">{i + 1}</span><div className="grow"><FileName f={j} /></div><span className="badge">{SOURCE_LABEL[j.source] || j.source}</span>
              <button type="button" className="btn ghost icon-only sm" title="Run next" aria-label="Run next" onClick={() => act(() => api.post(`${V1}/queue/${j.id}/bump`), 'Moved to the front')}><Icon name="arrow-up" size={15} /></button>
              <button type="button" className="btn ghost icon-only sm" title="Remove" aria-label="Remove" onClick={() => act(() => api.post(`${V1}/queue/${j.id}/cancel`), 'Removed')}><Icon name="x" size={15} /></button></li>
          ))}</ol>
        )}
      </Card>
    </div>
  )
}

export function TaskPage() {
  const { id } = useParams()
  const { toast } = useToast()
  const { data, setData, reload } = useAsync(() => api.get<{ task: Task; jobs: Job[] }>(`${V1}/tasks/${id}`), [id])
  const poll = useCallback(() => { if (data?.task.state === 'running') api.get<{ task: Task; jobs: Job[] }>(`${V1}/tasks/${id}`).then(setData).catch(() => {}) }, [data?.task.state, id, setData])
  usePoll(poll, 2500, [poll])
  if (!data) return <Loading />
  const t = data.task
  const cancel = async () => { try { await api.post(`${V1}/tasks/${t.id}/cancel`); toast('Stopping…'); reload() } catch (e) { toast((e as Error).message, 'err') } }
  const done = data.jobs.filter(j => j.state !== 'queued' && j.state !== 'running')
  return (
    <>
      <PageHeader title={t.title} sub={<><Link to="/tasks" className="back-link"><Icon name="chevron-left" size={14} />Tasks</Link> · started {ago(t.created_at)}{t.finished_at && ` · finished ${ago(t.finished_at)}`}</>}
        actions={t.state === 'running' && <button type="button" className="btn" onClick={cancel}><Icon name="stop" size={14} />Stop</button>} />
      <div className="row wrap mb"><StateBadge s={t.state} />{t.state !== 'running' && <TaskSummary t={t} />}</div>
      {t.state === 'running' && t.total > 0 && <div className="mb"><Progress done={t.done} total={t.total} /><span className="muted small">{t.done} of {t.total}</span></div>}
      <div className="two-col">
        <Card title={<><Icon name="terminal" size={16} />Log</>} flush><pre className="tasklog">{t.log || '…'}</pre></Card>
        <Card title={`Files (${data.jobs.length})`} flush>
          {!data.jobs.length ? <div className="empty-row">This task queues no syncs.</div> : (
            <ul className="rows">{[...data.jobs.filter(j => j.state === 'running' || j.state === 'queued'), ...done].slice(0, 400).map(j => (
              <li key={j.id} className="row-item"><div className="grow"><FileName f={j} /></div>{j.decision ? <Decision d={j.decision} /> : <Decision d={j.state} />}</li>
            ))}</ul>
          )}
        </Card>
      </div>
    </>
  )
}
