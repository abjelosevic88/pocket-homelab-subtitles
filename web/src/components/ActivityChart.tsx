import { useState } from 'react'
import type { DayCount } from '../lib/types'
import { fmtDate } from '../lib/format'

const SERIES = [
  { key: 'adjusted', label: 'Adjusted', cls: 'c-adjusted' },
  { key: 'in_sync', label: 'Already in sync', cls: 'c-insync' },
  { key: 'problems', label: 'Needs attention', cls: 'c-problems' },
] as const

/** Runs per day, stacked by outcome. Hover/focus a day for its numbers; the
 *  table view carries the same values without hovering. */
export function ActivityChart({ days }: { days: DayCount[] }) {
  const [table, setTable] = useState(false)
  const [hover, setHover] = useState<number | null>(null)
  const totals = days.map(d => d.adjusted + d.in_sync + d.problems)
  const max = Math.max(4, ...totals)
  const step = niceStep(max)
  const top = Math.ceil(max / step) * step
  const ticks = Array.from({ length: Math.round(top / step) + 1 }, (_, i) => i * step)
  const peak = totals.indexOf(Math.max(...totals))
  return (
    <div className="viz">
      <div className="viz-head">
        <ul className="legend" aria-label="Legend">
          {SERIES.map(s => <li key={s.key}><span className={`key ${s.cls}`} />{s.label}</li>)}
        </ul>
        <button className="btn ghost sm" onClick={() => setTable(t => !t)}>{table ? 'Chart' : 'Table'}</button>
      </div>
      {table ? (
        <table className="table compact">
          <thead><tr><th>Day</th>{SERIES.map(s => <th key={s.key} className="right">{s.label}</th>)}<th className="right">Total</th></tr></thead>
          <tbody>{days.slice().reverse().map((d, i) => <tr key={d.day}><td>{fmtDate(d.day)}</td><td className="right">{d.adjusted}</td><td className="right">{d.in_sync}</td><td className="right">{d.problems}</td><td className="right"><b>{totals[days.length - 1 - i]}</b></td></tr>)}</tbody>
        </table>
      ) : (
        <div className="cols-wrap">
          <div className="cols-axis" aria-hidden>{ticks.slice().reverse().map(t => <span key={t}>{t.toLocaleString()}</span>)}</div>
          <div className="cols" role="list">
            <div className="cols-grid" aria-hidden>{ticks.map(t => <span key={t} style={{ bottom: `${(t / top) * 100}%` }} />)}</div>
            {days.map((d, i) => {
              const total = totals[i]
              const label = `${fmtDate(d.day)}: ${d.adjusted} adjusted, ${d.in_sync} already in sync, ${d.problems} need attention`
              const segs = SERIES.map(s => ({ ...s, v: d[s.key] })).filter(s => s.v > 0)
              return (
                <div key={d.day} className={`col ${hover === i ? 'hl' : ''}`} role="listitem" tabIndex={0} aria-label={label}
                  onPointerEnter={() => setHover(i)} onPointerLeave={() => setHover(h => h === i ? null : h)} onFocus={() => setHover(i)} onBlur={() => setHover(null)}>
                  <div className="stack" style={{ height: `${(total / top) * 100}%` }}>
                    {segs.slice().reverse().map((s, k) => <span key={s.key} className={`seg ${s.cls} ${k === 0 ? 'cap' : ''}`} style={{ flexGrow: s.v }} />)}
                  </div>
                  {i === peak && total > 0 && hover === null && <span className="col-val" style={{ bottom: `calc(${(total / top) * 100}% + 4px)` }}>{total}</span>}
                  <span className="col-x">{i % 2 === (days.length - 1) % 2 ? fmtDate(d.day, false) : ''}</span>
                  {hover === i && (
                    <div className={`tip ${i > days.length / 2 ? 'left' : ''}`} role="tooltip">
                      <div className="tip-h">{fmtDate(d.day)}</div>
                      {SERIES.map(s => <div key={s.key} className="tip-r"><span className={`lk ${s.cls}`} /><b>{d[s.key]}</b><span className="muted">{s.label}</span></div>)}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

function niceStep(max: number): number {
  const raw = max / 4
  const p = Math.pow(10, Math.floor(Math.log10(raw)))
  for (const m of [1, 2, 2.5, 5, 10]) if (raw <= m * p) return Math.max(1, m * p)
  return 10 * p
}
