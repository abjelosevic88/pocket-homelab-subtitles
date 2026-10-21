import type { User } from '../lib/types'
import { colorFor } from '../lib/format'
import { Avatar as Initials } from './ui'

export function Avatar({ user, size = 24 }: { user: User; size?: number }) {
  return <span title={user.name || user.email} className="avatar-wrap"><Initials name={user.name} email={user.email} size={size} color={colorFor(user.id)} /></span>
}
