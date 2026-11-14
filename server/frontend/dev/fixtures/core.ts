import type { Fixture } from '../types'
import { state } from '../state'

const fixture: Fixture = {
  default: [
  ],
  pages: {
    admin: { user: { username: 'admin', role: 'superadmin' }, mounts: state.mounts },
  },
}
export const pages = fixture.pages
export default fixture.default
