import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Run, Subtitle, WantedItem } from '../lib/types'
import { ago, fmtDateTime } from '../lib/format'
import { Card, Empty, Loading, PageHeader, Tabs, useAsync, useToast } from '../components/ui'
import { Decision, FileName, Lang } from '../components/bits'
import { RunModal } from './History'
import { Icon } from '../components/icons'

type Tab = 'attention' | 'rejected' | 'missing'

export default function Issues() {
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) || 'attention'
  return (
    <>
      <PageHeader title="Issues" sub="What the automatic sync could not settle on its own." />
      <Tabs tabs={[{ id: 'attention', label: 'Needs attention' }, { id: 'rejected', label: 'Rejected downloads' }, { id: 'missing', label: 'Missing subtitles' }]} value={tab} onChange={t => setParams({ tab: t }, { replace: true })} />
      <div className="mt">
        {tab === 'attention' && <Attention />}
        {tab === 'rejected' && <Rejected />}
        {tab === 'missing' && <Missing />}
      </div>
    </>
  )
}

function Attention() {
  const navigate = useNavigate()
  const { data } = useAsync(() => api.get<{ items: Subtitle[]; total: number }>(`${V1}/library/files?status=attention&limit=1000`), [])
  if (!data) return <Loading />
  if (!data.items.length) return <Card><Empty icon="check-circle" title="All clear">Every file's last run either synced it or confirmed it was already in sync.</Empty></Card>
  return (
    <Card flush>
      <p className="muted small pad" style={{ paddingBottom: 0 }}>The last run on these files did not end in sync. Open one to verify it, sync it again, pin it as it is, or nudge it by hand. Pinned files are not listed.</p>
      <table className="table">
        <thead><tr><th>File</th><th>Outcome</th><th className="hide-sm">Why</th><th className="right">When</th></tr></thead>
        <tbody>
          {data.items.map(f => (
            <tr key={f.id} className="clickable" onClick={() => navigate(`/files/${f.id}`)}>
              <td><FileName f={f} /></td>
              <td><Decision d={f.last_decision} /></td>
              <td className="small muted hide-sm">{f.last_reason}</td>
              <td className="right muted small nowrap">{f.last_checked_at && ago(f.last_checked_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  )
}

function Rejected() {
  const [open, setOpen] = useState<number | null>(null)
  const { data, reload } = useAsync(() => api.get<{ items: Run[]; total: number }>(`${V1}/runs?outcome=rejected&limit=200`), [])
  if (!data) return <Loading />
  return (
    <>
      <Card flush>
        <p className="muted small pad" style={{ paddingBottom: 0 }}>Downloads whose text did not belong to the video (wrong episode) or read like a machine translation. They were taken out of the library — with Bazarr connected, the upload is blacklisted and a replacement is searched for. A copy is kept: open one to put it back.</p>
        {!data.items.length ? <Empty title="Nothing rejected" /> : (
          <table className="table">
            <thead><tr><th>File</th><th>Reason</th><th className="right">When</th></tr></thead>
            <tbody>
              {data.items.map(r => (
                <tr key={r.id} className="clickable" onClick={() => setOpen(r.id)}>
                  <td><FileName f={r} /><div><Decision d={r.decision} /></div></td>
                  <td className="small muted">{r.reason}</td>
                  <td className="right muted small nowrap" title={fmtDateTime(r.created_at)}>{ago(r.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      {open && <RunModal id={open} onClose={() => { setOpen(null); reload() }} />}
    </>
  )
}

function Missing() {
  const { toast } = useToast()
  const { data, error, reload } = useAsync(() => api.get<{ configured: boolean; items: WantedItem[]; episodes: number; movies: number; bazarr_url: string }>(`${V1}/issues/missing?limit=300`), [])
  const [busy, setBusy] = useState('')
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  if (!data.configured) return <Card><Empty title="Bazarr is not connected">Add its URL and API key under Settings → Integrations to see which episodes and movies still have no subtitles.</Empty></Card>
  const search = async (it: WantedItem, lang: string) => {
    const k = `${it.kind}-${it.episode_id || it.movie_id}-${lang}`
    setBusy(k)
    try { await api.post(`${V1}/issues/missing/search`, { kind: it.kind, series_id: it.series_id, episode_id: it.episode_id, movie_id: it.movie_id, language: lang }); toast('Search started in Bazarr — a download syncs here on arrival'); setTimeout(reload, 4000) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy('') }
  }
  return (
    <Card flush title={`${(data.episodes + data.movies).toLocaleString()} without subtitles`} actions={<a className="btn ghost sm" href={`${data.bazarr_url}/wanted/series`} target="_blank" rel="noreferrer">Open Bazarr<Icon name="external" size={13} /></a>}>
      {!data.items.length ? <Empty title="Nothing missing" /> : (
        <table className="table">
          <thead><tr><th>Title</th><th className="hide-sm">Episode</th><th>Missing</th></tr></thead>
          <tbody>
            {data.items.map(it => (
              <tr key={`${it.kind}-${it.episode_id || it.movie_id}`}>
                <td><b>{it.title}</b><div className="muted small show-sm">{it.detail}</div></td>
                <td className="muted small hide-sm">{it.detail}</td>
                <td><span className="row wrap">{(it.languages || []).map(l => (
                  <button type="button" key={l} className="btn sm" disabled={busy !== ''} title="Search now" onClick={() => search(it, l)}><Lang lang={l} /><Icon name="search" size={13} /></button>
                ))}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  )
}
