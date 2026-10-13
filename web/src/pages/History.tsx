import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Run } from '../lib/types'
import { fmtDateTime, fmtDuration, METHOD_LABEL, SOURCE_LABEL } from '../lib/format'
import { Card, Empty, Loading, Modal, PageHeader, Segmented, useAsync, useDebounce, useToast } from '../components/ui'
import { Decision, FileName, Shift } from '../components/bits'
import { Select } from '../components/Select'

const OUTCOMES = [
  { value: '', label: 'All' }, { value: 'adjusted', label: 'Adjusted' }, { value: 'in_sync', label: 'In sync' },
  { value: 'attention', label: 'Gave up / errors' }, { value: 'rejected', label: 'Rejected' },
]
const PAGE = 100

export default function History() {
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState(params.get('q') || '')
  const dq = useDebounce(q, 250)
  const outcome = params.get('outcome') || ''
  const source = params.get('source') || ''
  const lang = params.get('lang') || ''
  const [page, setPage] = useState(0)
  const [open, setOpen] = useState<number | null>(null)
  const set = (k: string, v: string) => { const p = new URLSearchParams(params); if (v) p.set(k, v); else p.delete(k); setParams(p, { replace: true }); setPage(0) }
  useEffect(() => { setPage(0) }, [dq])
  const langs = useAsync(() => api.get<string[]>(`${V1}/library/langs`), [])
  const qs = new URLSearchParams({ q: dq, outcome, source, lang, limit: String(PAGE), offset: String(page * PAGE) })
  const { data, loading } = useAsync(() => api.get<{ items: Run[]; total: number }>(`${V1}/runs?${qs}`), [qs.toString()])
  return (
    <>
      <PageHeader title="History" sub={data ? `${data.total.toLocaleString()} runs` : ' '} />
      <div className="toolbar">
        <div className="search"><span className="ico">⌕</span><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search: show, movie, S01E02, file name…" /></div>
        <Select value={source} onChange={v => set('source', v)} ariaLabel="Source" options={[{ value: '', label: 'Every source' }, ...Object.entries(SOURCE_LABEL).map(([value, label]) => ({ value, label }))]} />
        <Select value={lang} onChange={v => set('lang', v)} ariaLabel="Language" options={[{ value: '', label: 'Every language' }, ...(langs.data || []).map(l => ({ value: l, label: l.toUpperCase() }))]} />
      </div>
      <div className="filters"><Segmented size="sm" value={outcome} onChange={v => set('outcome', v)} options={OUTCOMES} /></div>
      <Card flush>
        {loading && !data ? <Loading /> : !data?.items.length ? <Empty title="Nothing here">No runs match these filters.</Empty> : (
          <table className="table history">
            <thead><tr><th>When</th><th>File</th><th>Outcome</th><th className="right hide-sm">Moved</th><th className="hide-sm">How</th></tr></thead>
            <tbody>
              {data.items.map(r => (
                <tr key={r.id} className="clickable" onClick={() => setOpen(r.id)}>
                  <td className="muted nowrap small">{fmtDateTime(r.created_at)}</td>
                  <td><FileName f={r} /></td>
                  <td><Decision d={r.decision} /></td>
                  <td className="right hide-sm"><Shift s={r.max_shift} decision={r.decision} /></td>
                  <td className="muted small hide-sm">{SOURCE_LABEL[r.source] || r.source}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      {data && data.total > PAGE && (
        <div className="pager">
          <button className="btn sm" disabled={page === 0} onClick={() => setPage(p => p - 1)}>← Newer</button>
          <span className="muted small">{page * PAGE + 1}–{Math.min(data.total, (page + 1) * PAGE)} of {data.total.toLocaleString()}</span>
          <button className="btn sm" disabled={(page + 1) * PAGE >= data.total} onClick={() => setPage(p => p + 1)}>Older →</button>
        </div>
      )}
      {open && <RunModal id={open} onClose={() => setOpen(null)} />}
    </>
  )
}

/** One run with everything the engine reported. */
export function RunModal({ id, onClose }: { id: number; onClose: () => void }) {
  const { toast } = useToast()
  const { data } = useAsync(() => api.get<Run>(`${V1}/runs/${id}`), [id])
  if (!data) return <Modal title="Run" onClose={onClose}><Loading /></Modal>
  const d = (data.details || {}) as Record<string, unknown>
  const segs = Array.isArray(d.segments) ? d.segments as { cues: number; offset: number; outlier?: boolean }[] : []
  const restore = async () => { try { await api.post(`${V1}/runs/${id}/restore`); toast('Put back and pinned'); onClose() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Modal title={<FileName f={data} id={data.subtitle_id} />} onClose={onClose} size="lg">
      <div className="row wrap mb-sm"><Decision d={data.decision} size="lg" /><Shift s={data.max_shift} decision={data.decision} /><span className="muted small">{fmtDateTime(data.created_at)} · {SOURCE_LABEL[data.source] || data.source}{data.duration_ms ? ` · took ${fmtDuration(data.duration_ms)}` : ''}</span></div>
      {data.reason && <div className={`callout ${data.decision.startsWith('rejected') || data.decision === 'error' ? 'danger' : ''} mb-sm`}>{data.reason}</div>}
      {typeof d.outcome === 'string' && <p className="small"><b>What happened to the file:</b> {d.outcome}</p>}
      <table className="table compact kv">
        <tbody>
          {data.method && <tr><th>Method</th><td>{METHOD_LABEL[data.method] || data.method}{typeof d.embedded_track === 'number' && ` (track ${d.embedded_track})`}</td></tr>}
          {typeof d.embedded_track_rejected === 'string' && <tr><th>Tracks not trusted</th><td className="small">{d.embedded_track_rejected}</td></tr>}
          {typeof d.speech_reference === 'string' && <tr><th>Speech reference</th><td>{d.speech_reference === 'centre' ? 'centre channel (dialogue)' : 'full mix'}{typeof d.speech_density === 'number' && <span className="muted"> · speech in {Math.round(d.speech_density * 100)}% of the runtime</span>}</td></tr>}
          {data.content_fit !== null && <tr><th>Text fits the audio</th><td>{Math.round(data.content_fit * 100)}% of cues land on speech{data.ref_fit !== null && <span className="muted"> (the video's own track: {Math.round(data.ref_fit * 100)}%)</span>}</td></tr>}
          {typeof d.global_offset === 'number' && <tr><th>Whole-file offset</th><td className="mono">{d.global_offset.toFixed(2)} s</td></tr>}
          {typeof d.lead_out === 'number' && <tr><th>Reading lead</th><td className="mono">{Math.round(d.lead_out * 1000)} ms after the voice</td></tr>}
          {d.stepped_cut === true && <tr><th>Cut</th><td>different cut — offsets step between acts</td></tr>}
          {typeof d.latinized === 'boolean' && d.latinized && <tr><th>Script</th><td>converted from Cyrillic to Latin</td></tr>}
        </tbody>
      </table>
      {segs.length > 0 && <SegmentStrip segs={segs} />}
      {typeof d.quarantine === 'string' && <div className="form-actions"><button className="btn" onClick={restore}>Put this file back (and pin it)</button></div>}
      <details className="mt"><summary className="muted small">Raw engine output</summary><pre>{JSON.stringify(d, null, 2)}</pre></details>
    </Modal>
  )
}

/** Act-by-act offsets: how far each part of the file had to move. */
function SegmentStrip({ segs }: { segs: { cues: number; offset: number; outlier?: boolean }[] }) {
  const max = Math.max(1, ...segs.map(s => Math.abs(s.offset)))
  const total = segs.reduce((a, s) => a + s.cues, 0)
  return (
    <div className="segstrip mt-sm">
      <div className="label muted small">Offset per act (width = share of cues)</div>
      <div className="segs">
        {segs.map((s, i) => (
          <div key={i} className={`segbar ${s.outlier ? 'outlier' : ''}`} style={{ flexGrow: s.cues / total }} title={`Act ${i + 1}: ${s.cues} cues, ${s.offset.toFixed(2)} s${s.outlier ? ' (corrected outlier)' : ''}`}>
            <span className="fill" style={{ height: `${Math.max(4, (Math.abs(s.offset) / max) * 100)}%` }} />
            <span className="v mono">{s.offset > 0 ? '+' : ''}{s.offset.toFixed(1)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
