import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { fmtMs } from '../lib/format'
import { useToast } from './ui'

const QUICK = [-1000, -500, -250, -100, 100, 250, 500, 1000]

/** Shift one file by milliseconds. Every nudge pins the file, so the nightly
 *  sweep leaves the hand-made timing alone. */
export function NudgePanel({ file, onChange }: { file: { id: number; nudge_ms: number; pinned: boolean; can_undo?: boolean }; onChange: () => void }) {
  const { toast } = useToast()
  const [ms, setMs] = useState('')
  const [busy, setBusy] = useState(false)
  const [last, setLast] = useState('')
  const run = async (fn: () => Promise<string>) => {
    setBusy(true)
    try { const m = await fn(); setLast(m); toast(m) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false); onChange() }
  }
  const apply = (v: number) => run(async () => {
    const r = await api.post<{ cues: number; total_ms: number; jellyfin: boolean }>(`${V1}/files/${file.id}/nudge`, { ms: v })
    return `Moved ${r.cues} cues ${fmtMs(v)} (net ${fmtMs(r.total_ms)})${r.jellyfin ? ', Jellyfin refreshed' : ''}`
  })
  const custom = Number(ms)
  return (
    <div className="nudge">
      <div className="nudge-net">
        <span className="muted small">Net manual shift</span>
        <b className={file.nudge_ms ? '' : 'muted'}>{file.nudge_ms ? fmtMs(file.nudge_ms) : 'none'}</b>
        <span className="muted small">{file.pinned ? '📌 pinned — the sync leaves it alone' : 'managed by the automatic sync'}</span>
      </div>
      <div className="nudge-row" role="group" aria-label="Quick shifts">
        {QUICK.map(v => <button key={v} type="button" className={`btn ${v < 0 ? 'earlier' : 'later'}`} disabled={busy} onClick={() => apply(v)}>{v > 0 ? '+' : '−'}{Math.abs(v)}</button>)}
      </div>
      <div className="nudge-legend muted small"><span>← subtitles appear earlier</span><span>later →</span></div>
      <form className="row wrap" onSubmit={e => { e.preventDefault(); if (custom) apply(custom) }}>
        <input className="num-input" type="number" step={50} value={ms} onChange={e => setMs(e.target.value)} placeholder="± ms" aria-label="Custom shift in milliseconds" />
        <button className="btn primary" disabled={busy || !custom}>Apply</button>
        <span className="spacer" />
        <button type="button" className="btn" disabled={busy || file.can_undo === false} onClick={() => run(async () => { const r = await api.post<{ restored_ms: number }>(`${V1}/files/${file.id}/undo`); return `Undid the last nudge (${fmtMs(r.restored_ms)}), still pinned` })}>Undo last</button>
        {file.pinned && <button type="button" className="btn" disabled={busy} title="Hand the file back to the automatic sync" onClick={() => run(async () => { await api.post(`${V1}/files/${file.id}/unpin`); return 'Unpinned — the sync manages it again' })}>Unpin</button>}
      </form>
      {last && <p className="muted small mt-sm">{last}. Restart playback in the player to load the new timing.</p>}
    </div>
  )
}
