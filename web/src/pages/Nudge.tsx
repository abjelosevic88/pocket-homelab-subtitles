import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { FileDetail, Nudge as NudgeRow, Subtitle } from '../lib/types'
import { ago, fileTitle, fmtMs } from '../lib/format'
import { Card, Empty, Loading, PageHeader, useAsync, useDebounce } from '../components/ui'
import { Lang, Pin } from '../components/bits'
import { NudgePanel } from '../components/NudgePanel'

/** The quick tool: find a file, move it by some milliseconds. */
export default function Nudge() {
  const [q, setQ] = useState(() => new URLSearchParams(window.location.search).get('q') || '')
  const dq = useDebounce(q, 200)
  const [sel, setSel] = useState<number | null>(null)
  const list = useAsync(() => api.get<{ items: Subtitle[]; total: number }>(`${V1}/library/files?${new URLSearchParams({ q: dq, limit: '60' })}`), [dq])
  const file = useAsync(() => sel ? api.get<FileDetail>(`${V1}/files/${sel}`) : Promise.resolve(null), [sel])
  const recent = useAsync(() => api.get<(NudgeRow & { file: Subtitle | null })[]>(`${V1}/nudges`), [])
  useEffect(() => { if (list.data?.items.length === 1) setSel(list.data.items[0].id) }, [list.data])
  const reload = () => { file.reload(); list.reload(); recent.reload() }
  return (
    <>
      <PageHeader title="Nudge a subtitle" sub="Move every cue of one file by a few milliseconds. Negative = appears earlier, positive = later. The file is pinned so the nightly sweep keeps your timing." />
      <div className="nudge-layout">
        <div className="grid" style={{ gap: 12, alignContent: 'start' }}>
          <div className="search big"><span className="ico">⌕</span><input value={q} onChange={e => setQ(e.target.value)} placeholder="e.g. walking dead s05e01 sr" autoFocus /></div>
          <Card flush>
            {list.loading && !list.data ? <Loading /> : !list.data?.items.length ? <Empty title="No match" /> : (
              <ul className="rows pick">
                {list.data.items.map(f => (
                  <li key={f.id}><button type="button" className={`row-item ${sel === f.id ? 'sel' : ''}`} onClick={() => setSel(f.id)}>
                    <span className="grow"><span className="fname">{fileTitle(f)}</span></span>
                    <Lang lang={f.lang} flags={f.flags} />
                    {f.nudge_ms !== 0 && <span className="badge nud">{fmtMs(f.nudge_ms)}</span>}
                    <Pin on={f.pinned} />
                  </button></li>
                ))}
                {list.data.total > list.data.items.length && <li className="row-item more">{list.data.total - list.data.items.length} more — type to narrow down</li>}
              </ul>
            )}
          </Card>
        </div>
        <div className="grid" style={{ gap: 16, alignContent: 'start' }}>
          {file.data ? (
            <Card title={<span className="row wrap">{fileTitle(file.data)} <Lang lang={file.data.lang} flags={file.data.flags} /></span>} actions={<Link className="btn ghost sm" to={`/files/${file.data.id}`}>Details →</Link>}>
              <p className="mono small muted break" style={{ marginTop: 0 }}>{file.data.rel}</p>
              <NudgePanel file={file.data} onChange={reload} />
              <p className="muted small mt">Jellyfin is told about the change right away; restart playback to load the new timing.</p>
            </Card>
          ) : <Card><Empty title="Pick a file">Search on the left, then shift it here.</Empty></Card>}
          <Card title="Recent nudges" flush>
            {!recent.data?.length ? <div className="empty-row">None yet</div> : (
              <ul className="rows">
                {recent.data.slice(0, 12).map(n => (
                  <li key={n.id} className="row-item clickable" onClick={() => n.file && setSel(n.file.id)}>
                    <span className="grow">{n.file ? fileTitle(n.file) : n.path.split('/').pop()} {n.file && <Lang lang={n.file.lang} />}</span>
                    <span className={`mono small ${n.undone ? 'muted strike' : ''}`}>{fmtMs(n.ms)}</span>
                    <span className="muted small nowrap">{ago(n.created_at)}</span>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      </div>
    </>
  )
}
