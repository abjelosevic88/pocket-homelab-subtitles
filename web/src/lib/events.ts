import { useEffect, useRef } from 'react'

/** Something changed (a nudge, a sweep, a sync): the sidebar and pages reload. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('ps:changed'))
export function useOnChange(cb: () => void) {
  const ref = useRef(cb)
  ref.current = cb
  useEffect(() => {
    const h = () => ref.current()
    window.addEventListener('ps:changed', h)
    return () => window.removeEventListener('ps:changed', h)
  }, [])
}
